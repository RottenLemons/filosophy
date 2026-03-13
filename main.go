// This code is written by Mahir Shah

package main

import (
	"bufio"
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
	// Initialize Engine with model and DB paths
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal("cannot get working directory:", err)
	}
	dbPath := filepath.Join(cwd, "filosophy.db")
	textModelPath := filepath.Join(cwd, "text")
	imageModelPath := filepath.Join(cwd, "image")

	sc, err := shared.New(dbPath, textModelPath, imageModelPath)
	if err != nil {
		log.Fatal("failed to initialize Engine:", err)
	}
	defer sc.Close()

	index := len(os.Args) > 1 && os.Args[1] == "--index"
	if index {
		start := time.Now()
		cfg := shared.NewProcessorConfig(8096, 4000, 100, sc)

		// Semaphore to limit concurrent CGO calls (extractous and vips are not thread-safe)
		maxConcurrency := 10
		sem := make(chan struct{}, maxConcurrency)

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

		shared.DrainRemaining(cfg.Chunks, "text", sc)
		shared.DrainRemaining(cfg.Images, "image", sc)
		cfg.CleanupTempDir()
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
		results, err := sc.Search(query)
		if err != nil {
			log.Println("search error:", err)
			continue
		}
		for _, r := range results {
			fmt.Printf("  %.4f  %s\n", r.Score, r.Path)
		}
	}
}
