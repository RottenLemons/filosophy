package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"
	vips "github.com/cshum/vipsgen/vips817"
	kreuzberg "github.com/kreuzberg-dev/kreuzberg/packages/go/v4"

	"github.com/tmc/langchaingo/textsplitter"
)

var parentDir string = "C:/Users/Mahir/Downloads/test/"

func send(data interface{}, task string) string {
	fmt.Println("enter" + task)
	pipePath := `\\.\pipe\test`
	f, err := winio.DialPipe(pipePath, nil)
	if err != nil {
		log.Fatalf("error opening pipe: %v", err)
	}
	defer f.Close()
	msg := fmt.Sprintf(`{"task": %q,"data": %s}`, task, data)
	_, err = f.Write([]byte(msg))
	if err != nil {
		log.Fatalf("write error: %v", err)

	}
	chunk := make([]byte, 1024)
	_, err = f.Read(chunk)
	if err != nil {
		log.Fatalf("write error: %v", err)
	}
	return string(chunk)
}

type metadata struct {
	content, path string
}

type dataType struct {
	data []byte
	mode string
}

var imageExtensions = map[string]struct{}{
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".gif":  {},
	".bmp":  {},
	".tiff": {},
	".tif":  {},
	// ".webp": {}, Not supported for now
	".heic": {},
	".heif": {},
	// ".svg":  {}, Not supported for now
	".ico":   {},
	".avif":  {},
	".jfif":  {},
	".pjpeg": {},
	".pjp":   {},
}

func IsImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	_, ok := imageExtensions[ext]
	return ok
}

func handleChunk(chunks chan metadata, sendQueue chan dataType, mode, content, path string) {
	select {
	case chunks <- metadata{content, path}:
	default:
		dataMap := make(map[string][]string)

		for j := 0; j < cap(chunks); j++ {
			select {
			case tmp := <-chunks:
				dataMap["content"] = append(dataMap["content"], tmp.content)
				dataMap["path"] = append(dataMap["path"], tmp.path)
			default:
			}
		}
		chunks <- metadata{content, path}
		if len(dataMap["content"]) > 0 {
			data, _ := json.Marshal(dataMap)
			sendQueue <- dataType{data, mode}
		}
	}
}

func drainRemain(chunks chan metadata, mode string, sendQueue chan dataType) {
	fmt.Print(len(chunks))
	if len(chunks) > 0 {
		dataMap := make(map[string][]string)
		for len(chunks) > 0 {
			tmp := <-chunks
			dataMap["content"] = append(dataMap["content"], tmp.content)
			dataMap["path"] = append(dataMap["path"], tmp.path)
		}
		data, _ := json.Marshal(dataMap)
		sendQueue <- dataType{data, mode}
	}
}

func main() {
	index := flag.Bool("index", false, "index files for search")
	flag.Parse()
	if *index {
		start := time.Now()
		splitter := textsplitter.NewRecursiveCharacter(func(o *textsplitter.Options) {
			o.ChunkSize = 8096
		})
		chunks := make(chan metadata, 1000)
		images := make(chan metadata, 100)
		sendQueue := make(chan dataType, 10) // Queue for data to send
		sendDone := make(chan struct{})

		// Semaphore to limit concurrent CGO calls (extractous and vips are not thread-safe)
		maxConcurrency := 10 // Limit concurrent workers
		sem := make(chan struct{}, maxConcurrency)

		// Dedicated sender goroutine - ensures sequential sends
		go func() {
			for data := range sendQueue {
				send(data.data, data.mode)
			}
			sendDone <- struct{}{}
		}()

		filepath.Walk(parentDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}

			if info.IsDir() {
				handleChunk(chunks, sendQueue, "text", "", path)
				return nil
			}

			// Acquire semaphore before spawning goroutine
			sem <- struct{}{}

			go func(path string) {
				defer func() { <-sem }()

				if IsImageFile(path) {
					im_bytes, err := os.ReadFile(path)
					if err != nil {
						log.Println("Failed to read image:", err)
						return
					}
					vImg, err := vips.NewThumbnailBuffer(im_bytes, 256, &vips.ThumbnailBufferOptions{
						Height: 256,
						FailOn: vips.FailOnError, // Fail on first error
					})
					im_bytes = nil // Free original image bytes
					if err != nil {
						log.Println("Failed to load image:", err)
						return
					}
					pngBytes, err := vImg.PngsaveBuffer(nil)
					defer vImg.Close() // Close immediately after use
					if err != nil {
						log.Println("Failed to save image as buffer:", err)
						return
					}
					im_string := base64.StdEncoding.EncodeToString(pngBytes)
					pngBytes = nil // Free PNG bytes after encoding
					handleChunk(images, sendQueue, "image", im_string, path)
					return
				}

				result, err := kreuzberg.ExtractFileSync(path, nil)

				if err != nil {
					handleChunk(chunks, sendQueue, "text", "", path)
					return
				}

				splits, err := splitter.SplitText(result.Content)

				if err == nil {
					for _, i := range splits {
						handleChunk(chunks, sendQueue, "text", i, path)
					}
				} else {
					fmt.Println(err)
				}
			}(path)

			return nil
		})

		// Wait for all goroutines to finish by filling the semaphore
		for i := 0; i < maxConcurrency; i++ {
			sem <- struct{}{}
		}

		drainRemain(chunks, "text", sendQueue)  // Last Text
		drainRemain(images, "image", sendQueue) // Last image
		close(sendQueue)
		<-sendDone
		end := time.Now()
		fmt.Println(end.Sub(start))
	}
	for {
		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Search: ")
		query, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Error:", err)
			return
		}
		query = strings.TrimSpace(query)
		fmt.Println(send(fmt.Sprintf("\"%s\"", query), "search"))
	}
}
