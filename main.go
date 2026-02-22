// This code is written by Mahir Shah

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filosophy/shared"
)

var parentDir string = "C:/Users/Mahir/Downloads/test/"

func main() {
	index := len(os.Args) > 1 && os.Args[1] == "--index"
	if index {
		start := time.Now()
		cfg := shared.NewProcessorConfig(8096, 1000, 100, 10)
		sendDone := make(chan struct{})

		// Semaphore to limit concurrent CGO calls (extractous and vips are not thread-safe)
		maxConcurrency := 10
		sem := make(chan struct{}, maxConcurrency)

		// Dedicated sender goroutine - ensures sequential sends
		go func() {
			for data := range cfg.SendQueue {
				if _, err := shared.Send(data.Data, data.Mode); err != nil {
					log.Fatal(err)
				}
			}
			sendDone <- struct{}{}
		}()

		filepath.Walk(parentDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}

			if info.IsDir() {
				shared.ProcessDirectory(path, cfg)
				return nil
			}

			// Acquire semaphore before spawning goroutine
			sem <- struct{}{}

			go func(path string) {
				defer func() { <-sem }()
				shared.ProcessFile(path, cfg)
			}(path)

			return nil
		})

		// Wait for all goroutines to finish by filling the semaphore
		for i := 0; i < maxConcurrency; i++ {
			sem <- struct{}{}
		}

		shared.DrainRemaining(cfg.Chunks, "text", cfg.SendQueue)
		shared.DrainRemaining(cfg.Images, "image", cfg.SendQueue)
		close(cfg.SendQueue)
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
		queryJSON, err := json.Marshal(query)
		if err != nil {
			log.Fatal(err)
		}
		resp, err := shared.Send(string(queryJSON), "search")
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(resp)
	}
}
