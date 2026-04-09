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
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"filosophy/shared"

	"github.com/syncthing/notify"
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
const appPidFile = "filosophy-app.pid"

// FolderState describes a directory and whether it is content-indexed.
type FolderState struct {
	Name        string `json:"Name"`
	Path        string `json:"Path"`
	Indexed     bool   `json:"Indexed"`
	HasChildren bool   `json:"HasChildren"`
}

// getHomeSubdirs returns the names of all non-hidden, non-system direct subdirectories
// of the home directory. Hidden (dot) folders and AppData are excluded from the list.
func getHomeSubdirs(home string) []string {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	skip := map[string]bool{
		"appdata": true,
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if skip[strings.ToLower(name)] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// getContentDirs returns the absolute paths of home subdirectories that are
// not excluded. These are passed to pass1/pass2 for content indexing.
func (a *App) getContentDirs() []string {
	subdirs := getHomeSubdirs(a.home)
	var dirs []string
	for _, name := range subdirs {
		p := filepath.Join(a.home, name)
		if !a.config.IsExcluded(p) {
			dirs = append(dirs, p)
		}
	}
	if len(dirs) == 0 {
		dirs = []string{a.home}
	}
	return dirs
}

// hasSubdirs returns true if path contains at least one non-hidden subdirectory.
func hasSubdirs(path string) bool {
	entries, _ := os.ReadDir(path)
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			return true
		}
	}
	return false
}

// makeFolderState builds a FolderState for a given directory.
func (a *App) makeFolderState(name, path string) FolderState {
	return FolderState{
		Name:        name,
		Path:        path,
		Indexed:     !a.config.IsExcluded(path),
		HasChildren: hasSubdirs(path),
	}
}


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
func launchDaemon(cwd string) {
	if isDaemonRunning(cwd) {
		log.Println("daemon already running, skipping launch")
		return
	}

	exe, _ := os.Executable()
	daemonExe := filepath.Join(filepath.Dir(exe), "filosophy-daemon.exe")
	if _, err := os.Stat(daemonExe); err != nil {
		daemonExe = ""
	}

	var cmd *exec.Cmd
	if daemonExe != "" {
		cmd = exec.Command(daemonExe)
	} else {
		cmd = exec.Command("go", "run", "./cmd/daemon")
	}
	cmd.Dir = cwd
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		log.Printf("failed to launch daemon: %v", err)
		return
	}
	log.Printf("daemon launched (pid %d)", cmd.Process.Pid)
	cmd.Process.Release()
}

func runPass1(ctx context.Context, sc *shared.Engine, dirs []string, config *shared.AppConfig, pruneStale bool) {
	if ctx != nil {
		wailsruntime.EventsEmit(ctx, "indexing_status", "Scanning directories...")
		wailsruntime.EventsEmit(ctx, "indexing_progress", 0)
	}

	cfg := shared.NewProcessorConfig(8096, 4000, 100, sc)
	defer cfg.CleanupTempDir()

	var paths []string
	var mtimes []int64
	var sizes []int64
	var ctimes []int64
	var atimes []int64

	// Single walk: collect file metadata AND queue directories simultaneously.
	// Previously two separate walks were made over the same tree, doubling syscall count.
	for _, dir := range dirs {
		filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			select {
			case <-ctx.Done():
				return filepath.SkipDir
			default:
			}
			if err != nil {
				return nil
			}
			if info.IsDir() {
				// Skip user-excluded subdirs (but not the root dir itself).
				if path != dir && config != nil && config.IsExcluded(path) {
					return filepath.SkipDir
				}
				shared.ProcessDirectory(path, cfg)
				return nil
			}
			if isJunkFile(info.Name()) {
				return nil
			}
			if isContentSkipped(path) {
				return nil
			}
			// Extract ctime/atime from the already-loaded FileInfo — no extra syscall.
			ct, at := shared.FileExtraTimesFromInfo(info)
			paths = append(paths, path)
			mtimes = append(mtimes, info.ModTime().UnixNano())
			sizes = append(sizes, info.Size())
			ctimes = append(ctimes, ct)
			atimes = append(atimes, at)
			return nil
		})
		if ctx.Err() != nil {
			return
		}
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
	if err := sc.IndexMetadata(paths, mtimes, sizes, ctimes, atimes); err != nil {
		log.Println("pass1 metadata index error:", err)
	}

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

var systemPathSkipDirs = map[string]bool{
	"$recycle.bin":              true,
	"system volume information": true,
	"$windows.~bt":              true,
	"$windows.~ws":              true,
	"recovery":                  true,
	"perflogs":                  true,
}

var junkExtensions = map[string]bool{
	".tmp": true, ".temp": true, ".log": true, ".etl": true,
	".dmp": true, ".mdmp": true,
	".pf":  true, ".sdf": true,
	".db-wal": true, ".db-shm": true,
}

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

func isJunkFile(name string) bool {
	lower := strings.ToLower(name)
	if junkFileNames[lower] {
		return true
	}
	ext := strings.ToLower(filepath.Ext(name))
	return junkExtensions[ext]
}

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

func isContentSkipped(path string) bool {
	lower := strings.ToLower(path)
	for _, prefix := range contentSkipPrefixes {
		if strings.Contains(lower, prefix) {
			return true
		}
	}
	return false
}

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
	config        *shared.AppConfig
	hasGPU        atomic.Bool // atomic: written from GPU-detection goroutine, read from CheckSystemGPU
	isIndexing    atomic.Bool
	indexerCancel context.CancelFunc
	cwd           string
	home          string
	homeWatchStop chan struct{}
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
	a.cwd = cwd

	dbPath := filepath.Join(cwd, "filosophy.db")
	textModelPath := filepath.Join(cwd, "text")
	imageModelPath := filepath.Join(cwd, "image")

	if lf, err := os.OpenFile(filepath.Join(cwd, "filosophy.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644); err == nil {
		log.SetOutput(lf)
	}

	// Write app PID so the daemon's tray "Exit" can close us too.
	os.WriteFile(filepath.Join(cwd, appPidFile), []byte(strconv.Itoa(os.Getpid())), 0644)

	// Set home early so GetHomeFolders() works even if engine init is slow/fails.
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("Failed to get home directory: %v", err)
		home = cwd
	}
	a.home = home

	a.config = shared.LoadConfig(cwd)

	go func() {
		// Asynchronous GPU detection with hidden window to avoid terminal flashing
		cmd := exec.Command("nvidia-smi", "-L")
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err == nil {
			a.hasGPU.Store(true)
			return
		}

		out, err := exec.Command("wmic", "path", "Win32_VideoController", "get", "Name").Output()
		if err == nil {
			lower := strings.ToLower(string(out))
			for _, kw := range []string{"nvidia", "amd", "radeon", "geforce", "quadro", "arc "} {
				if strings.Contains(lower, kw) {
					a.hasGPU.Store(true)
					return
				}
			}
		}
	}()

	_, dbMissing := os.Stat(dbPath)
	forceIndex := len(os.Args) > 1 && os.Args[1] == "--index"

	engine, err := shared.New(dbPath, textModelPath, imageModelPath)
	if err != nil {
		log.Printf("Failed to initialize Engine: %v", err)
		return
	}
	a.engine = engine
	log.Println("Engine initialized")

	dirs := a.getContentDirs()
	for _, d := range dirs {
		os.MkdirAll(d, 0755)
	}

	if forceIndex {
		if err := a.engine.ResetContentIndex(); err != nil {
			log.Println("reset index error:", err)
		}
	}

	idxCtx, cancel := context.WithCancel(a.ctx)
	a.indexerCancel = cancel

	if forceIndex || os.IsNotExist(dbMissing) {
		runPass1(idxCtx, a.engine, dirs, a.config, forceIndex)
		go func() {
			runPass2(idxCtx, a.engine)
			launchDaemon(cwd)
		}()
		go runSystemPathIndex(idxCtx, a.engine)
	} else {
		launchDaemon(cwd)
	}

	// Watch home dir top level so the frontend can react to new/deleted folders.
	a.homeWatchStop = make(chan struct{})
	go a.watchHomeFolders()
}

// watchHomeFolders watches the home directory for top-level folder
// creation/deletion and emits home_folders_changed so the UI updates.
func (a *App) watchHomeFolders() {
	ch := make(chan notify.EventInfo, 64)
	// Non-recursive watch on home dir root only.
	if err := notify.Watch(a.home, ch,
		notify.FileNotifyChangeDirName,
	); err != nil {
		log.Println("home folder watcher error:", err)
		return
	}
	defer notify.Stop(ch)

	for {
		select {
		case <-a.homeWatchStop:
			return
		case ei, ok := <-ch:
			if !ok {
				return
			}
			// Only care about direct children (top-level dirs).
			if filepath.Dir(ei.Path()) == a.home {
				wailsruntime.EventsEmit(a.ctx, "home_folders_changed")
			}
		}
	}
}

func (a *App) shutdown(ctx context.Context) {
	os.Remove(filepath.Join(a.cwd, appPidFile))
	if a.homeWatchStop != nil {
		close(a.homeWatchStop)
	}
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

// GetHomeFolders returns the top-level home subdirectories with their indexing state.
func (a *App) GetHomeFolders() []FolderState {
	subdirs := getHomeSubdirs(a.home)
	result := make([]FolderState, 0, len(subdirs))
	for _, name := range subdirs {
		p := filepath.Join(a.home, name)
		result = append(result, a.makeFolderState(name, p))
	}
	return result
}

// GetFolderChildren returns the immediate non-hidden subdirectories of path
// with their current indexing state. Used to lazily expand the folder tree.
func (a *App) GetFolderChildren(path string) []FolderState {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil
	}
	var result []FolderState
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		childPath := filepath.Join(path, e.Name())
		result = append(result, a.makeFolderState(e.Name(), childPath))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

// SetFolderIndexed enables or disables content indexing for a directory (full path).
// Enabling triggers a background pass1+pass2 for that directory.
// Disabling updates the config and restarts the daemon.
func (a *App) SetFolderIndexed(folderPath string, indexed bool) error {
	a.config.SetExcluded(folderPath, !indexed)
	if err := a.config.Save(); err != nil {
		return err
	}

	// Restart daemon with updated dir list.
	stopDaemonProcess(a.cwd)

	if !indexed {
		// Just disable — restart daemon and done.
		launchDaemon(a.cwd)
		return nil
	}

	// Folder was enabled — index it in the background.
	if !a.isIndexing.CompareAndSwap(false, true) {
		// Another index run is in progress; daemon will be relaunched when it finishes.
		go func() {
			for a.isIndexing.Load() {
				time.Sleep(200 * time.Millisecond)
			}
			launchDaemon(a.cwd)
		}()
		return nil
	}

	if a.indexerCancel != nil {
		a.indexerCancel()
	}
	idxCtx, cancel := context.WithCancel(a.ctx)
	a.indexerCancel = cancel
	dir := folderPath

	go func() {
		defer a.isIndexing.Store(false)
		runPass1(idxCtx, a.engine, []string{dir}, a.config, false)
		runPass2(idxCtx, a.engine)
		launchDaemon(a.cwd)
	}()

	return nil
}

func (a *App) CheckSystemGPU() bool {
	return a.hasGPU.Load()
}

func (a *App) GetGPUAcceleration() bool {
	return a.config.GPUEnabled
}

func (a *App) SetGPUAcceleration(enabled bool) error {
	a.config.GPUEnabled = enabled
	return a.config.Save()
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
