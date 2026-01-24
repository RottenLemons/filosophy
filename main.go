package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	vips "github.com/cshum/vipsgen/vips817"
	"github.com/rahulpoonia29/extractous-go"
	"github.com/tmc/langchaingo/textsplitter"
)

var parentDir string = "C:/Users/Mahir/Downloads/test/"
var c int = 1000

// func main2() {
//  pipePath := `\\.\pipe\Foo`
//  for i := 0; i < 5; i++ {
//      f, err := winio.DialPipe(pipePath, nil)
//      if err != nil {
//          log.Fatalf("error opening pipe: %v", err)
//      }
//      defer f.Close()
//      _, err = f.Write([]byte(`{"type": "text","data":["HELLO, this is my text"]}`))
//      chunk := make([]byte, 1024)
//      _, err = f.Read(chunk)
//      if err != nil {
//          log.Fatalf("write error: %v", err)
//      }
//      fmt.Println("read:", string(chunk))
//  }
// }

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
	// _, err := v4.ExtractFileSync("document.pdf", nil)
	// if err == nil {
	//  return
	// }
	index := flag.Bool("index", false, "index files for search")
	flag.Parse()
	if *index {
		start := time.Now()
		// db, _ = sqlx.Open("sqlite3", "test.db")
		file, _ := os.Open(parentDir)
		names, errRead := file.Readdirnames(c)
		extractor := extractous.New()
		splitter := textsplitter.NewRecursiveCharacter(func(o *textsplitter.Options) {
			o.ChunkSize = 8096
		})
		if extractor == nil {
			log.Fatal("Failed to create extractor")
		}
		defer extractor.Close()
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

		for errRead != io.EOF {
			var wg sync.WaitGroup
			for _, name := range names {
				wg.Add(1)
				go func(name string) {
					defer wg.Done()
					// Acquire semaphore before CGO operations
					sem <- struct{}{}
					defer func() { <-sem }()

					path := parentDir + name

					if IsImageFile(name) {
						im_bytes, err := os.ReadFile(path)
						if err != nil {
							log.Fatal(err)
						}
						vImg, err := vips.NewThumbnailBuffer(im_bytes, 256, &vips.ThumbnailBufferOptions{
							Height: 256,
							FailOn: vips.FailOnError, // Fail on first error
						})
						if err != nil {
							log.Fatalf("Failed to load image: %v", err)
						}
						defer vImg.Close()
						im_bytes, err = vImg.PngsaveBuffer(nil)
						if err != nil {
							log.Fatalf("Failed to save image as buffer: %v", err)
						}
						im_string := base64.StdEncoding.EncodeToString(im_bytes)
						handleChunk(images, sendQueue, "image", im_string, path)
						return
					}

					var b strings.Builder

					reader, _, err := extractor.ExtractFile(path)
					if err != nil {
						handleChunk(chunks, sendQueue, "text", "", path)
					}
					defer reader.Close()

					// Process the document in chunks
					buffer := make([]byte, 16000)
					for {
						n, err := reader.Read(buffer)
						if err == io.EOF {
							splits, err := splitter.SplitText(b.String())
							if err == nil {
								for _, i := range splits {
									handleChunk(chunks, sendQueue, "text", i, path)
								}
							} else {
								fmt.Println(err)
							}
							return
						}
						if err != nil {
						}
						b.Write(buffer[:n])
					}
				}(name)
			}
			wg.Wait()

			names, errRead = file.Readdirnames(c)
		}
		drainRemain(chunks, "text", sendQueue)  // Last Text
		drainRemain(images, "image", sendQueue) // Last image
		close(sendQueue)
		// close(imgQueue)
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
