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

// maxConcurrency limits background indexing goroutines. Keeping this low ensures
// search goroutines can acquire the HNSW read lock between consecutive batch inserts.
// With 10+ goroutines all queueing for HNSW.Lock(), search RLock starves indefinitely.
const maxConcurrency = 3

func runPass1(sc *shared.Engine, pruneStale bool) {
	cfg := shared.NewProcessorConfig(8096, 4000, 100, sc)
	defer cfg.CleanupTempDir()

	var paths []string
	var mtimes []int64
	var sizes []int64

	filepath.Walk(parentDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		paths = append(paths, path)
		mtimes = append(mtimes, info.ModTime().UnixNano())
		sizes = append(sizes, info.Size())
		return nil
	})

	if pruneStale {
		if err := sc.PruneStale(paths); err != nil {
			log.Println("pass1 prune error:", err)
		}
	}

	if err := sc.IndexMetadata(paths, mtimes, sizes); err != nil {
		log.Println("pass1 metadata index error:", err)
	}

	// Also index directory paths for path-based search
	filepath.Walk(parentDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		shared.ProcessDirectory(path, cfg)
		return nil
	})
	shared.DrainRemaining(cfg.Chunks, "text", sc)
}

func runPass2(sc *shared.Engine) {
	paths, _, err := sc.UnindexedFiles()
	if err != nil {
		log.Println("pass2 query error:", err)
		return
	}
	if len(paths) == 0 {
		return
	}

	start := time.Now()
	cfg := shared.NewProcessorConfig(8096, 200, 50, sc)
	defer cfg.CleanupTempDir()

	sem := make(chan struct{}, maxConcurrency)
	for _, path := range paths {
		sem <- struct{}{}
		go func(p string) {
			defer func() { <-sem }()
			shared.ProcessFile(p, cfg)
		}(path)
	}
	for i := 0; i < maxConcurrency; i++ {
		sem <- struct{}{}
	}

	shared.DrainRemaining(cfg.Chunks, "text", sc)
	shared.DrainRemaining(cfg.Images, "image", sc)
	fmt.Printf("indexing complete (%v)\n", time.Since(start))
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal("cannot get working directory:", err)
	}
	dbPath := filepath.Join(cwd, "filosophy.db")
	textModelPath := filepath.Join(cwd, "text")
	imageModelPath := filepath.Join(cwd, "image")

	// Redirect logs to file so background indexing doesn't pollute the search prompt.
	if lf, err := os.OpenFile(filepath.Join(cwd, "filosophy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(lf)
		defer lf.Close()
	}

	_, dbMissing := os.Stat(dbPath)
	forceIndex := len(os.Args) > 1 && os.Args[1] == "--index"

	sc, err := shared.New(dbPath, textModelPath, imageModelPath)
	if err != nil {
		log.Fatal("failed to initialize Engine:", err)
	}
	defer sc.Close()

	if forceIndex {
		if err := sc.ResetContentIndex(); err != nil {
			log.Println("reset index error:", err)
		}
	}

	if forceIndex || os.IsNotExist(dbMissing) {
		// First run or explicit re-index: Pass 1 (fast, makes search usable) then Pass 2 in background.
		runPass1(sc, forceIndex)
		go runPass2(sc)
	} else {
		// DB exists: resume any incomplete Pass 2 work in background.
		go runPass2(sc)
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
