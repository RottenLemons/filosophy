package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	// "github.com/jmoiron/sqlx"
	"github.com/rahulpoonia29/extractous-go"
	"github.com/tmc/langchaingo/textsplitter"

	"github.com/Microsoft/go-winio"
	// "encoding/json"
	// _ "github.com/mattn/go-sqlite3"
	// sqlite_vec "github.com/vlasky/sqlite-vec/bindings/go/cgo"
)

var parentDir string = "C:/Users/Mahir/Downloads/test/"
var c int = 1000

// func main2() {
// 	pipePath := `\\.\pipe\Foo`
// 	for i := 0; i < 5; i++ {
// 		f, err := winio.DialPipe(pipePath, nil)
// 		if err != nil {
// 			log.Fatalf("error opening pipe: %v", err)
// 		}
// 		defer f.Close()
// 		_, err = f.Write([]byte(`{"type": "text","data":["HELLO, this is my text"]}`))
// 		chunk := make([]byte, 1024)
// 		_, err = f.Read(chunk)
// 		if err != nil {
// 			log.Fatalf("write error: %v", err)
// 		}
// 		fmt.Println("read:", string(chunk))
// 	}
// }

func send(data interface{}, task string) string {
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

func main() {
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
		sendQueue := make(chan []byte, 10) // Queue for data to send
		sendDone := make(chan struct{})
		// Dedicated sender goroutine - ensures sequential sends
		go func() {
			for data := range sendQueue {
				send(data, "text")
			}
			sendDone <- struct{}{}
		}()

		for errRead != io.EOF {
			var wg sync.WaitGroup
			for _, name := range names {
				wg.Add(1)
				go func(name string) {
					defer wg.Done()
					var b strings.Builder
					reader, _, err := extractor.ExtractFile(parentDir + name)
					if err != nil {
						return
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
									select {
									case chunks <- metadata{i, parentDir + name}:
									default:
										dataMap := make(map[string][]string)
										for j := 0; j < len(chunks); j++ {
											tmp := <-chunks
											dataMap["content"] = append(dataMap["content"], tmp.content)
											dataMap["path"] = append(dataMap["path"], tmp.path)
										}
										chunks <- metadata{i, parentDir + name}
										data, _ := json.Marshal(dataMap)
										sendQueue <- data
									}
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
		print("DONE YALL")
		if len(chunks) > 0 {
			dataMap := make(map[string][]string)
			for len(chunks) > 0 {
				tmp := <-chunks
				dataMap["content"] = append(dataMap["content"], tmp.content)
				dataMap["path"] = append(dataMap["path"], tmp.path)
			}
			data, _ := json.Marshal(dataMap)
			sendQueue <- data
		}

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
