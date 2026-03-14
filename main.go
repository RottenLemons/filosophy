// Copyright (C) 2025 Filosophy

package main

import (
<<<<<<< HEAD
	"context"
	"embed"
	"encoding/json"
=======
	"bufio"
	"fmt"
>>>>>>> main
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"filosophy/daemon"
	"filosophy/shared"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/build
var assets embed.FS

<<<<<<< HEAD
// App struct holds the application state
type App struct {
	ctx        context.Context
	sidecarCmd *exec.Cmd
	daemon     *daemon.Daemon
	baseDir    string
}
=======
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
>>>>>>> main

// NewApp creates a new App instance
func NewApp() *App {
	return &App{}
}

<<<<<<< HEAD
// startup is called when the Wails app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Determine base directory for watching (home/Downloads/test)
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Failed to get home directory: %v", err)
		home = "C:/Users/Mahir"
	}
	a.baseDir = filepath.Join(home, "Downloads", "test")
	// Ensure the directory exists
	os.MkdirAll(a.baseDir, 0755)

	// Start Python sidecar
	sidecarPath := "sidecar.py"
	// Try to find sidecar in the current working directory
	if _, err := os.Stat(sidecarPath); os.IsNotExist(err) {
		// Try to find sidecar in project root
		exeDir, _ := os.Getwd()
		sidecarPath = filepath.Join(exeDir, "sidecar.py")
	}
	a.sidecarCmd = exec.Command("python", sidecarPath)
	a.sidecarCmd.Stdout = os.Stdout
	a.sidecarCmd.Stderr = os.Stderr
	if err := a.sidecarCmd.Start(); err != nil {
		log.Printf("Failed to start sidecar: %v", err)
	} else {
		log.Println("Sidecar started")
	}

	// Start file-watching daemon
	a.daemon = daemon.NewDaemon(a.baseDir)
	if err := a.daemon.Start(); err != nil {
		log.Printf("Failed to start daemon: %v", err)
	} else {
		log.Println("Daemon started")
	}
}

// shutdown is called when the Wails app stops
func (a *App) shutdown(ctx context.Context) {
	if a.daemon != nil {
		a.daemon.Stop()
	}
	if a.sidecarCmd != nil {
		a.sidecarCmd.Process.Kill()
		a.sidecarCmd.Wait()
	}
}

// Search performs a search query via the Python sidecar and returns the list of file paths.
func (a *App) Search(query string) []string {
	queryJSON, err := json.Marshal(query)
	if err != nil {
		log.Println("Search marshal error:", err)
		return nil
	}
	resp, err := shared.Send(string(queryJSON), "search")
	if err != nil {
		log.Println("Search send error:", err)
		return nil
	}
	// Parse response: sidecar returns a JSON list of strings.
	var paths []string
	if err := json.Unmarshal([]byte(resp), &paths); err != nil {
		// fallback: split lines
		lines := strings.Split(strings.TrimSpace(resp), "\n")
		for _, line := range lines {
			line = strings.Trim(line, `[]"'`)
			if line != "" {
				paths = append(paths, line)
			}
		}
	}
	return paths
}

func main() {
	// Create an instance of the App
	app := NewApp()

	// Application options
	err := wails.Run(&options.App{
		Title:  "Filosophy",
		Width:  1024,
		Height: 768,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
=======
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
>>>>>>> main
	}
}
