// Copyright (C) 2025 Filosophy

package main

import (
	"context"
	"embed"
	"log"
	"os"
	"path/filepath"
	"time"

	"filosophy/daemon"
	"filosophy/shared"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/build
var assets embed.FS

var parentDir string = "C:/Users/Mahir/Downloads/test/"

// maxConcurrency limits background indexing goroutines. Keeping this low ensures
// search goroutines can acquire the HNSW read lock between consecutive batch inserts.
// With 10+ goroutines all queueing for HNSW.Lock(), search RLock starves indefinitely.
const maxConcurrency = 3

// App holds application state exposed to the Wails frontend.
type App struct {
	ctx    context.Context
	sc     *shared.Engine
	daemon *daemon.Daemon
}

func NewApp() *App {
	return &App{}
}

// startup is called by Wails when the app starts. It initialises the engine,
// kicks off background indexing, and starts the file-watcher daemon.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal("cannot get working directory:", err)
	}

	// Redirect logs to file so background work does not pollute anything.
	if lf, err := os.OpenFile(filepath.Join(cwd, "filosophy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(lf)
	}

	dbPath := filepath.Join(cwd, "filosophy.db")
	textModelPath := filepath.Join(cwd, "text")
	imageModelPath := filepath.Join(cwd, "image")

	sc, err := shared.New(dbPath, textModelPath, imageModelPath)
	if err != nil {
		log.Fatal("failed to initialize Engine:", err)
	}
	a.sc = sc

	_, dbMissing := os.Stat(dbPath)
	if os.IsNotExist(dbMissing) {
		runPass1(sc, false)
		go runPass2(sc)
	} else {
		go runPass2(sc)
	}

	a.daemon = daemon.NewDaemon(parentDir)
	if err := a.daemon.Start(); err != nil {
		log.Println("daemon start error:", err)
	}
}

// shutdown is called by Wails when the app closes.
func (a *App) shutdown(ctx context.Context) {
	if a.daemon != nil {
		a.daemon.Stop()
	}
	if a.sc != nil {
		a.sc.Close()
	}
}

// Search is exposed to the frontend via window.go.main.App.Search.
// Returns a slice of matching file paths ordered by relevance.
func (a *App) Search(query string) []string {
	if a.sc == nil || query == "" {
		return nil
	}
	results, err := a.sc.Search(query)
	if err != nil {
		log.Println("search error:", err)
		return nil
	}
	paths := make([]string, len(results))
	for i, r := range results {
		paths[i] = r.Path
	}
	return paths
}

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
	log.Printf("indexing complete (%v)\n", time.Since(start))
}

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "Filosophy",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:  app.startup,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
