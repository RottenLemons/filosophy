// Copyright (C) 2025 Filosophy

package main

import (
	"context"
	"embed"
	"encoding/json"
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

// App struct holds the application state
type App struct {
	ctx        context.Context
	sidecarCmd *exec.Cmd
	daemon     *daemon.Daemon
	baseDir    string
}

// NewApp creates a new App instance
func NewApp() *App {
	return &App{}
}

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
	}
}
