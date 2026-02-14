package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Microsoft/go-winio"

	"filosophy/shared"
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
				send(data.Data, data.Mode)
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
		fmt.Println(send(fmt.Sprintf("\"%s\"", query), "search"))
	}
}
