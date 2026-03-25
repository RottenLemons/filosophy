// Copyright (C) 2025 Filosophy

package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"net/http"
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
	ctx    context.Context
	engine *shared.Engine
	daemon *daemon.Daemon
}

// NewApp creates a new App instance
func NewApp() *App {
	return &App{}
}

// startup is called when the Wails app starts
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Determine paths for Engine initialization
	cwd, err := os.Getwd()
	if err != nil {
		log.Printf("Failed to get working directory: %v", err)
		cwd = "."
	}
	dbPath := filepath.Join(cwd, "filosophy.db")
	textModelPath := filepath.Join(cwd, "text")
	imageModelPath := filepath.Join(cwd, "image")

	// Initialize the native Go Engine (replaces Python sidecar)
	engine, err := shared.New(dbPath, textModelPath, imageModelPath)
	if err != nil {
		log.Printf("Failed to initialize Engine: %v", err)
		return
	}
	a.engine = engine
	log.Println("Engine initialized")

	// Determine base directory for file-watching daemon
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Failed to get home directory: %v", err)
		home = cwd
	}
	baseDir := filepath.Join(home, "Downloads", "test")
	os.MkdirAll(baseDir, 0755)

	// Start file-watching daemon (shares the Engine, runs in background)
	a.daemon = daemon.NewDaemon(baseDir, a.engine)
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
	if a.engine != nil {
		a.engine.Close()
	}
}

// Search performs a search query via the native Go Engine.
func (a *App) Search(query string) ([]shared.SearchResult, error) {
	log.Printf("App.Search called with query: %s", query)
	if a.engine == nil {
		log.Printf("App.Search error: engine not initialized")
		return nil, fmt.Errorf("engine not initialized")
	}
	res, err := a.engine.Search(query)
	log.Printf("App.Search returned %d results, err: %v", len(res), err)
	return res, err
}

// OpenFileNative opens a file using the system's default application.
func (a *App) OpenFileNative(path string) error {
	log.Printf("App.OpenFileNative called with path: %s", path)
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", "", path}
	case "darwin":
		cmd = "open"
		args = []string{path}
	default: // linux
		cmd = "xdg-open"
		args = []string{path}
	}
	return exec.Command(cmd, args...).Start()
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
			Handler: &LocalFileHandler{},
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

type LocalFileHandler struct {
	http.Handler
}

func (h *LocalFileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Only intercept requests prefixed with /loadfile/
	if strings.HasPrefix(r.URL.Path, "/loadfile/") {
		// Extract the actual file path and decode it
		filePath := strings.TrimPrefix(r.URL.Path, "/loadfile/")
		
		// Go's net/http automatically unescapes r.URL.Path.
		// If the frontend used encodeURIComponent, the path here is already perfectly decoded.
		// Calling url.PathUnescape again would erroneously double-decode characters (e.g. converting a literal "%20" in a filename into a space).
		decodedPath := filePath

		// Enforce MIME types to prevent iframe/browser rendering bugs
		ext := strings.ToLower(filepath.Ext(decodedPath))
		switch ext {
		case ".jpg", ".jpeg":
			w.Header().Set("Content-Type", "image/jpeg")
		case ".png":
			w.Header().Set("Content-Type", "image/png")
		case ".gif":
			w.Header().Set("Content-Type", "image/gif")
		case ".webp":
			w.Header().Set("Content-Type", "image/webp")
		case ".pdf":
			w.Header().Set("Content-Type", "application/pdf")
		case ".txt", ".md", ".csv":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		}

		// Serve the local file securely
		http.ServeFile(w, r, decodedPath)
		return
	}
	// Fallback for normal frontend assets
	if h.Handler != nil {
		h.Handler.ServeHTTP(w, r)
	}
}
