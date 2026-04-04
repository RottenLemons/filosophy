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
	"sync/atomic"
	"time"

	"filosophy/daemon"
	"filosophy/shared"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/build
var assets embed.FS

// maxConcurrency limits background indexing goroutines. Keeping this low ensures
// search goroutines can acquire the HNSW read lock between consecutive batch inserts.
// With 10+ goroutines all queueing for HNSW.Lock(), search RLock starves indefinitely.
var maxConcurrency = runtime.NumCPU()

func runPass1(ctx context.Context, sc *shared.Engine, dir string, pruneStale bool) {
	if ctx != nil {
		wailsruntime.EventsEmit(ctx, "indexing_status", "Scanning directory...")
		wailsruntime.EventsEmit(ctx, "indexing_progress", 0)
	}

	cfg := shared.NewProcessorConfig(8096, 4000, 100, sc)
	defer cfg.CleanupTempDir()

	var paths []string
	var mtimes []int64
	var sizes []int64

	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		select {
		case <-ctx.Done():
			return filepath.SkipDir
		default:
		}
		if err != nil || info.IsDir() {
			return nil
		}
		paths = append(paths, path)
		mtimes = append(mtimes, info.ModTime().UnixNano())
		sizes = append(sizes, info.Size())
		return nil
	})
	
	if ctx.Err() != nil {
		return
	}

	if pruneStale {
		if ctx != nil {
			wailsruntime.EventsEmit(ctx, "indexing_status", "Pruning stale files...")
		}
		if err := sc.PruneStale(paths); err != nil {
			log.Println("pass1 prune error:", err)
		}
	}

	if ctx != nil {
		wailsruntime.EventsEmit(ctx, "indexing_status", "Indexing metadata...")
	}
	if err := sc.IndexMetadata(paths, mtimes, sizes); err != nil {
		log.Println("pass1 metadata index error:", err)
	}

	if ctx != nil {
		wailsruntime.EventsEmit(ctx, "indexing_status", "Processing directories...")
	}
	// Also index directory paths for path-based search
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		select {
		case <-ctx.Done():
			return filepath.SkipDir
		default:
		}
		if err != nil || !info.IsDir() {
			return nil
		}
		shared.ProcessDirectory(path, cfg)
		return nil
	})
	
	if ctx.Err() != nil {
		return
	}
	shared.DrainRemaining(cfg.Chunks, "text", sc)
}

func runPass2(ctx context.Context, sc *shared.Engine) {
	paths, _, err := sc.UnindexedFiles()
	if err != nil {
		log.Println("pass2 query error:", err)
		if ctx != nil {
			wailsruntime.EventsEmit(ctx, "indexing_status", "Error querying unindexed files")
		}
		return
	}
	
	total := len(paths)
	if total == 0 {
		if ctx != nil {
			wailsruntime.EventsEmit(ctx, "indexing_status", "Indexing complete.")
			wailsruntime.EventsEmit(ctx, "indexing_progress", 100)
		}
		return
	}

	if ctx != nil {
		wailsruntime.EventsEmit(ctx, "indexing_status", "CALCULATING ETA...")
		wailsruntime.EventsEmit(ctx, "indexing_progress", 0)
	}

	start := time.Now()
	cfg := shared.NewProcessorConfig(8096, 4000, 100, sc)
	defer cfg.CleanupTempDir()

	var processed int32

	doneChan := make(chan struct{})
	if ctx != nil {
		go func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()

			var lastETA string
			var lastETATime time.Time

			for {
				select {
				case <-doneChan:
					return
				case <-ticker.C:
					done := atomic.LoadInt32(&processed)
					progressPct := int((float64(done) / float64(total)) * 100)
					
					var statusMsg string
					elapsed := time.Since(start).Seconds()

					// Calculate a stable ETA after processing a few files
					if done > 5 && elapsed > 0 {
						// Only recalculate the displayed ETA string every 2 seconds to prevent micro-flickers
						if time.Since(lastETATime) > 2*time.Second || lastETA == "" {
							itemsPerSec := float64(done) / elapsed
							remaining := float64(total - int(done)) / itemsPerSec
							
							if remaining > 0 {
								etaDur := time.Duration(remaining) * time.Second
								lastETA = fmt.Sprintf("ETA %s", etaDur.Round(time.Second).String())
							} else {
								lastETA = ""
							}
							lastETATime = time.Now()
						}
						
						if lastETA != "" {
							statusMsg = fmt.Sprintf("%d%% - %s", progressPct, lastETA)
						} else {
							statusMsg = fmt.Sprintf("%d%%", progressPct)
						}
					} else {
						statusMsg = fmt.Sprintf("%d%%", progressPct)
					}

					wailsruntime.EventsEmit(ctx, "indexing_progress", progressPct)
					wailsruntime.EventsEmit(ctx, "indexing_status", statusMsg)
				}
			}
		}()
	}

	sem := make(chan struct{}, maxConcurrency)
	for _, path := range paths {
		select {
		case <-ctx.Done():
			if ctx != nil {
				close(doneChan)
			}
			return
		default:
		}
		sem <- struct{}{}
		go func(p string) {
			defer func() { <-sem }()
			shared.ProcessFile(p, cfg)
			atomic.AddInt32(&processed, 1)
		}(path)
	}
	for i := 0; i < maxConcurrency; i++ {
		sem <- struct{}{}
	}

	if ctx != nil {
		close(doneChan)
	}

	shared.DrainRemaining(cfg.Chunks, "text", sc)
	shared.DrainRemaining(cfg.Images, "image", sc)
	fmt.Printf("indexing complete (%v)\n", time.Since(start))
	
	if ctx != nil {
		wailsruntime.EventsEmit(ctx, "indexing_progress", 100)
		wailsruntime.EventsEmit(ctx, "indexing_status", "Indexing complete.")
	}
}

// App struct holds the application state
type App struct {
	ctx           context.Context
	engine        *shared.Engine
	daemon        *daemon.Daemon
	isIndexing    atomic.Bool
	indexerCancel context.CancelFunc
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

	// Redirect logs to file so background indexing doesn't pollute the search prompt.
	if lf, err := os.OpenFile(filepath.Join(cwd, "filosophy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(lf)
		// Leave the log file open for the lifetime of the app
	}

	_, dbMissing := os.Stat(dbPath)
	forceIndex := len(os.Args) > 1 && os.Args[1] == "--index"

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
	
	lastPathFile := filepath.Join(cwd, "filosophy_path.txt")
	baseDir := home
	if content, err := os.ReadFile(lastPathFile); err == nil && len(content) > 0 {
		baseDir = strings.TrimSpace(string(content))
	} else {
		// If no prior path, attempt a logical default
		baseDir = filepath.Join(home, "Downloads")
	}
	
	os.MkdirAll(baseDir, 0755)

	if forceIndex {
		if err := a.engine.ResetContentIndex(); err != nil {
			log.Println("reset index error:", err)
		}
	}

	idxCtx, cancel := context.WithCancel(a.ctx)
	a.indexerCancel = cancel

	if forceIndex || os.IsNotExist(dbMissing) {
		// First-ever run or --index: do a full blocking Pass1 then background Pass2
		// without emitting to the UI (no progress bar interference).
		runPass1(idxCtx, a.engine, baseDir, forceIndex)
		go runPass2(idxCtx, a.engine)
	} else if _, err := os.ReadFile(lastPathFile); err == nil {
		// Normal startup with a known directory: wait 5s for the app to fully
		// initialise and the user to start interacting, THEN silently catch up
		// any files that were added or modified while the app was closed.
		go func() {
			select {
			case <-time.After(5 * time.Second):
			case <-idxCtx.Done():
				return
			}
			log.Println("[startup] Running silent catch-up scan for new files...")
			runPass1(idxCtx, a.engine, baseDir, false)
			runPass2(idxCtx, a.engine)
			log.Println("[startup] Silent catch-up complete.")
		}()
	}

	// Only auto-start the daemon if we have a confirmed saved path from a prior session.
	if _, err := os.ReadFile(lastPathFile); err == nil {
		a.daemon = daemon.NewDaemon(baseDir, a.engine)
		if err := a.daemon.Start(); err != nil {
			log.Printf("Failed to start daemon: %v", err)
		} else {
			log.Println("Daemon started")
		}
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

// SelectDirectory opens the native OS folder picker and returns the selected path.
func (a *App) SelectDirectory() string {
	log.Println("SelectDirectory called")
	dir, err := wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "Select Folder to Index",
	})
	if err != nil {
		log.Printf("SelectDirectory error: %v", err)
		return ""
	}
	return dir
}

// GetLastDirectory returns the persistently stored directory path, or the default user home if empty.
func (a *App) GetLastDirectory() string {
	cwd, _ := os.Getwd()
	content, err := os.ReadFile(filepath.Join(cwd, "filosophy_path.txt"))
	if err == nil && len(content) > 0 {
		return strings.TrimSpace(string(content))
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

// StartIndexing simulates an indexing loop and emits progress to the frontend.
func (a *App) StartIndexing(directoryPath string) {
	if a.engine == nil {
		log.Printf("StartIndexing ignored: engine is still initializing")
		if a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, "indexing_status", "System is still warming up. Please wait a few seconds and try again.")
		}
		return
	}

	if !a.isIndexing.CompareAndSwap(false, true) {
		log.Printf("StartIndexing ignored: indexing already in progress")
		return
	}

	// Cancel any previously running unindexed/background passes!
	if a.indexerCancel != nil {
		a.indexerCancel()
	}
	
	// Create a new context scoped entirely to this manual run
	idxCtx, cancel := context.WithCancel(a.ctx)
	a.indexerCancel = cancel

	// Save the selected path persistently
	cwd, _ := os.Getwd()
	os.WriteFile(filepath.Join(cwd, "filosophy_path.txt"), []byte(directoryPath), 0644)

	log.Printf("StartIndexing called for: %s", directoryPath)
	go func() {
		defer a.isIndexing.Store(false)

		wailsruntime.EventsEmit(a.ctx, "indexing_status", "Stopping old daemon...")
		if a.daemon != nil {
			a.daemon.Stop()
			// Wait briefly to allow daemon gorgeoutines to gracefully exit
			time.Sleep(500 * time.Millisecond)
		}

		wailsruntime.EventsEmit(a.ctx, "indexing_status", "Resetting index...")
		wailsruntime.EventsEmit(a.ctx, "indexing_progress", 0)
		if err := a.engine.ResetContentIndex(); err != nil {
			log.Println("reset index error:", err)
		}
		
		wailsruntime.EventsEmit(a.ctx, "indexing_status", "Phase 1: Metadata Fast-Pass...")
		// Use the UI context here because we want manual progress tracked
		runPass1(idxCtx, a.engine, directoryPath, true)
		
		wailsruntime.EventsEmit(a.ctx, "indexing_status", "Phase 2: Deep Vector Extraction...")
		runPass2(idxCtx, a.engine)

		// Start file-watching daemon on new directory
		a.daemon = daemon.NewDaemon(directoryPath, a.engine)
		if err := a.daemon.Start(); err != nil {
			log.Printf("Failed to restart daemon: %v", err)
		} else {
			log.Printf("Daemon successfully restarted for path: %s", directoryPath)
		}

		wailsruntime.EventsEmit(a.ctx, "indexing_progress", 100)
		wailsruntime.EventsEmit(a.ctx, "indexing_status", "Indexing complete.")
	}()
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
