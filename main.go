// Copyright (C) 2025 Filosophy

package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"filosophy/shared"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows"
)

//go:embed all:frontend/build
var assets embed.FS

var maxConcurrency = runtime.NumCPU()

const daemonPidFile = "filosophy-daemon.pid"

// isDaemonRunning checks whether a previously launched daemon process is still alive.
func isDaemonRunning(cwd string) bool {
	data, err := os.ReadFile(filepath.Join(cwd, daemonPidFile))
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// stopDaemonProcess kills the daemon process recorded in the PID file.
func stopDaemonProcess(cwd string) {
	data, err := os.ReadFile(filepath.Join(cwd, daemonPidFile))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	if proc, err := os.FindProcess(pid); err == nil {
		proc.Kill()
	}
	os.Remove(filepath.Join(cwd, daemonPidFile))
}

// launchDaemon starts filosophy-daemon.exe as a detached process.
// The daemon keeps running after the UI closes.
func launchDaemon(cwd string) {
	if isDaemonRunning(cwd) {
		log.Println("daemon already running, skipping launch")
		return
	}

	// Prefer a pre-built binary next to our own executable.
	exe, _ := os.Executable()
	daemonExe := filepath.Join(filepath.Dir(exe), "filosophy-daemon.exe")
	if _, err := os.Stat(daemonExe); err != nil {
		// Dev fallback: go run (blocks briefly, but detaches immediately below).
		daemonExe = ""
	}

	var cmd *exec.Cmd
	if daemonExe != "" {
		cmd = exec.Command(daemonExe)
	} else {
		cmd = exec.Command("go", "run", "./cmd/daemon")
	}
	cmd.Dir = cwd
	// CREATE_NEW_PROCESS_GROUP ensures the daemon is not in our job object
	// so it survives after the Wails app exits.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		log.Printf("failed to launch daemon: %v", err)
		return
	}
	log.Printf("daemon launched (pid %d)", cmd.Process.Pid)
	cmd.Process.Release() // detach: we no longer own this process
}

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
		if isJunkFile(info.Name()) {
			return nil
		}
		// AppData, Program Files etc. are path-indexed by runSystemPathIndex — skip content indexing.
		if isContentSkipped(path) {
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

					if done > 5 && elapsed > 0 {
						if time.Since(lastETATime) > 2*time.Second || lastETA == "" {
							itemsPerSec := float64(done) / elapsed
							remaining := float64(total-int(done)) / itemsPerSec
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

// systemPathSkipDirs are directories skipped even for path-only indexing —
// either unreadable, transient, or contain no useful paths whatsoever.
var systemPathSkipDirs = map[string]bool{
	"$recycle.bin":              true,
	"system volume information": true,
	"$windows.~bt":              true, // Windows upgrade leftovers
	"$windows.~ws":              true,
	"recovery":                  true,
	"perflogs":                  true,
}

// junkExtensions are file extensions excluded from both path and content indexing.
var junkExtensions = map[string]bool{
	".tmp": true, ".temp": true, ".log": true, ".etl": true,
	".dmp": true, ".mdmp": true, // crash dumps
	".pf":  true, ".sdf": true, // prefetch / SQL CE
	".db-wal": true, ".db-shm": true, // SQLite write-ahead logs
}

// junkFileNames are exact filenames (lowercased) excluded from both path and content indexing.
var junkFileNames = map[string]bool{
	"ntuser.dat":     true,
	"ntuser.dat.log": true,
	"ntuser.ini":     true,
	"desktop.ini":    true,
	"thumbs.db":      true,
	"hiberfil.sys":   true,
	"pagefile.sys":   true,
	"swapfile.sys":   true,
	"usrclass.dat":   true,
}

// isJunkFile returns true if the file should be excluded everywhere (path and content).
func isJunkFile(name string) bool {
	lower := strings.ToLower(name)
	if junkFileNames[lower] {
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	return junkExtensions[ext]
}

// contentSkipPrefixes are path substrings that should be path-indexed only,
// never content-indexed. Files under these dirs appear in path search but
// pass 2 (vector/text extraction) never touches them.
var contentSkipPrefixes = []string{
	`\appdata\`,
	`/appdata/`,
	`\program files\`,
	`/program files/`,
	`\program files (x86)\`,
	`/program files (x86)/`,
	`\programdata\`,
	`/programdata/`,
}

// isContentSkipped returns true if the path should be path-indexed only (no content extraction).
func isContentSkipped(path string) bool {
	lower := strings.ToLower(path)
	for _, prefix := range contentSkipPrefixes {
		if strings.Contains(lower, prefix) {
			return true
		}
	}
	return false
}

// availableDrives returns root paths for all drive letters present on the system.
func availableDrives() []string {
	var drives []string
	for _, letter := range "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		root := string(letter) + ":\\"
		if _, err := os.Stat(root); err == nil {
			drives = append(drives, root)
		}
	}
	return drives
}

// runSystemPathIndex walks all drives and adds every file/directory path into
// paths_fts only (not the files table). This enables path-based search across
// the whole system without triggering content indexing in pass 2.
func runSystemPathIndex(ctx context.Context, sc *shared.Engine) {
	log.Println("[sysindex] Starting system-wide path index...")
	const batchSize = 2000
	batch := make([]string, 0, batchSize)

	flush := func() {
		if len(batch) > 0 {
			sc.IndexPathsFTS(batch)
			batch = batch[:0]
		}
	}

	for _, root := range availableDrives() {
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return filepath.SkipAll
			default:
			}
			if info.IsDir() {
				if systemPathSkipDirs[strings.ToLower(info.Name())] {
					return filepath.SkipDir
				}
				return nil
			}
			if isJunkFile(info.Name()) {
				return nil
			}
			batch = append(batch, path)
			if len(batch) >= batchSize {
				flush()
			}
			return nil
		})
	}
	flush()
	log.Println("[sysindex] System-wide path index complete.")
}

// App struct holds the application state
type App struct {
	ctx           context.Context
	engine        *shared.Engine
	isIndexing    atomic.Bool
	indexerCancel context.CancelFunc
}

func NewApp() *App {
	return &App{}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	cwd, err := os.Getwd()
	if err != nil {
		log.Printf("Failed to get working directory: %v", err)
		cwd = "."
	}
	dbPath := filepath.Join(cwd, "filosophy.db")
	textModelPath := filepath.Join(cwd, "text")
	imageModelPath := filepath.Join(cwd, "image")

	if lf, err := os.OpenFile(filepath.Join(cwd, "filosophy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(lf)
	}

	_, dbMissing := os.Stat(dbPath)
	forceIndex := len(os.Args) > 1 && os.Args[1] == "--index"

	engine, err := shared.New(dbPath, textModelPath, imageModelPath)
	if err != nil {
		log.Printf("Failed to initialize Engine: %v", err)
		return
	}
	a.engine = engine
	log.Println("Engine initialized")

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
		// First run or explicit re-index:
		// - Pass 1: content metadata for baseDir only
		// - Pass 2: content indexing for baseDir only (background)
		// - System path index: all drives, path-only, in background (does not block search)
		runPass1(idxCtx, a.engine, baseDir, forceIndex)
		go func() {
			runPass2(idxCtx, a.engine)
			launchDaemon(cwd)
		}()
		go runSystemPathIndex(idxCtx, a.engine)
	} else {
		// Normal startup: engine is ready, daemon should already be running.
		// Do NOT scan again — the daemon tracked changes while we were closed.
		launchDaemon(cwd)
	}
}

// shutdown is called when the Wails app closes.
// We deliberately do NOT stop the daemon — it keeps running in the background.
func (a *App) shutdown(ctx context.Context) {
	if a.indexerCancel != nil {
		a.indexerCancel()
	}
	if a.engine != nil {
		a.engine.Close()
	}
}

func (a *App) Search(query string) ([]shared.SearchResult, error) {
	log.Printf("App.Search called with query: %s", query)
	if a.engine == nil {
		return nil, fmt.Errorf("engine not initialized")
	}
	res, err := a.engine.Search(query)
	log.Printf("App.Search returned %d results, err: %v", len(res), err)
	return res, err
}

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
	default:
		cmd = "xdg-open"
		args = []string{path}
	}
	return exec.Command(cmd, args...).Start()
}

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

func (a *App) GetLastDirectory() string {
	cwd, _ := os.Getwd()
	content, err := os.ReadFile(filepath.Join(cwd, "filosophy_path.txt"))
	if err == nil && len(content) > 0 {
		return strings.TrimSpace(string(content))
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

// StartIndexing re-indexes a new directory, then relaunches the daemon on it.
func (a *App) StartIndexing(directoryPath string) {
	if a.engine == nil {
		if a.ctx != nil {
			wailsruntime.EventsEmit(a.ctx, "indexing_status", "System is still warming up. Please wait a few seconds and try again.")
		}
		return
	}

	if !a.isIndexing.CompareAndSwap(false, true) {
		return
	}

	if a.indexerCancel != nil {
		a.indexerCancel()
	}

	idxCtx, cancel := context.WithCancel(a.ctx)
	a.indexerCancel = cancel

	cwd, _ := os.Getwd()
	os.WriteFile(filepath.Join(cwd, "filosophy_path.txt"), []byte(directoryPath), 0644)

	log.Printf("StartIndexing called for: %s", directoryPath)
	go func() {
		defer a.isIndexing.Store(false)

		wailsruntime.EventsEmit(a.ctx, "indexing_status", "Stopping daemon...")
		stopDaemonProcess(cwd)
		time.Sleep(300 * time.Millisecond)

		wailsruntime.EventsEmit(a.ctx, "indexing_status", "Resetting index...")
		wailsruntime.EventsEmit(a.ctx, "indexing_progress", 0)
		if err := a.engine.ResetContentIndex(); err != nil {
			log.Println("reset index error:", err)
		}

		wailsruntime.EventsEmit(a.ctx, "indexing_status", "Phase 1: Metadata Fast-Pass...")
		runPass1(idxCtx, a.engine, directoryPath, true)

		wailsruntime.EventsEmit(a.ctx, "indexing_status", "Phase 2: Deep Vector Extraction...")
		runPass2(idxCtx, a.engine)

		wailsruntime.EventsEmit(a.ctx, "indexing_progress", 100)
		wailsruntime.EventsEmit(a.ctx, "indexing_status", "Indexing complete.")

		go runSystemPathIndex(idxCtx, a.engine)
		launchDaemon(cwd)
	}()
}

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:  "Filosophy",
		Width:  1280,
		Height: 800,
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: &LocalFileHandler{},
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

// LocalFileHandler serves local files for the frontend via /loadfile/ prefix.
type LocalFileHandler struct {
	http.Handler
}

func (h *LocalFileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/loadfile/") {
		filePath := strings.TrimPrefix(r.URL.Path, "/loadfile/")
		decodedPath := filePath

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

		http.ServeFile(w, r, decodedPath)
		return
	}
	if h.Handler != nil {
		h.Handler.ServeHTTP(w, r)
	}
}
