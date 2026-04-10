// Copyright (C) 2025 Filosophy

package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"filosophy/daemon"
	"filosophy/shared"

	"github.com/getlantern/systray"
	"github.com/syncthing/notify"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/build
var assets embed.FS

// Pass-2 file extraction (kreuzberg) is I/O-bound: 2×CPU keeps the pipeline
// saturated while half the goroutines wait on disk reads.
var maxConcurrency = runtime.NumCPU() * 2
var imageBatchSize = 100 // Defaults, adapted at startup

const appPidFile = "filosophy-app.pid"

// FolderState describes a directory and whether it is content-indexed.
type FolderState struct {
	Name        string `json:"Name"`
	Path        string `json:"Path"`
	Indexed     bool   `json:"Indexed"`
	HasChildren bool   `json:"HasChildren"`
}

// IndexingStatus represents the current state of the backend indexer
type IndexingStatus struct {
	IsIndexing    bool   `json:"isIndexing"`
	StatusMessage string `json:"statusMessage"`
	Progress      int    `json:"progress"`
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
	// Append extra directories (manually added) from config.
	for _, d := range a.config.GetExtraDirs() {
		if !a.config.IsExcluded(d) {
			dirs = append(dirs, d)
		}
	}
	// Auto-detect mapped network drives and include them.
	seen := make(map[string]bool)
	for _, d := range dirs {
		seen[strings.ToLower(d)] = true
	}
	for _, nd := range shared.NetworkDrives() {
		if !seen[strings.ToLower(nd)] && !a.config.IsExcluded(nd) {
			dirs = append(dirs, nd)
		}
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



func (a *App) runPass1(ctx context.Context, sc *shared.Engine, dirs []string, config *shared.AppConfig, pruneStale bool) {
	log.Println("[Indexer] Phase 1/2: Starting metadata scan...")
	if ctx != nil {
		a.setStatus(true, "Phase 1/2: Scanning directories...", 0)
	}

	cfg, err := shared.NewProcessorConfig(8096, 4000, imageBatchSize, sc)
	if err != nil {
		log.Printf("runPass1 error: %v", err)
		return
	}
	defer cfg.CleanupTempDir()

	var paths []string
	var mtimes []int64
	var sizes []int64
	var ctimes []int64
	var atimes []int64

	// Single walk: collect file metadata AND queue directories simultaneously.
	// Previously two separate walks were made over the same tree, doubling syscall count.
	for _, dir := range dirs {
		filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if ctx != nil {
				select {
				case <-ctx.Done():
					return filepath.SkipDir
				default:
				}
			}
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if path != dir {
					// Skip entire subtree for user-excluded dirs.
					if config != nil && config.IsExcluded(path) {
						return filepath.SkipDir
					}
					// Skip entire subtree for known-useless dir names.
					if isContentSkippedDir(d.Name()) {
						return filepath.SkipDir
					}
				}
				shared.ProcessDirectory(path, cfg)
				return nil
			}
			if isJunkFile(d.Name()) {
				return nil
			}
			if isContentSkippedPath(path) {
				return nil
			}
			// Extract ctime/atime from info — WalkDir doesn't provide it by default,
			// but we need it for metadata indexing. Stat is required here.
			info, err := d.Info()
			if err != nil {
				return nil
			}
			ct, at := shared.FileExtraTimesFromInfo(info)
			paths = append(paths, path)
			mtimes = append(mtimes, info.ModTime().UnixNano())
			sizes = append(sizes, info.Size())
			ctimes = append(ctimes, ct)
			atimes = append(atimes, at)
			return nil
		})

		if ctx != nil && ctx.Err() != nil {
			return
		}
	}

	if pruneStale && sc != nil {
		if ctx != nil {
			a.setStatus(true, "Phase 1/2: Pruning stale files...", 0)
		}
		log.Println("[Indexer] Phase 1/2: Pruning stale files...")
		if err := sc.PruneStale(paths); err != nil {
			log.Println("pass1 prune error:", err)
		}
	}

	if sc != nil {
		if ctx != nil {
			a.setStatus(true, "Phase 1/2: Indexing metadata...", 0)
		}
		log.Println("[Indexer] Phase 1/2: Indexing metadata...")
		if err := sc.IndexMetadata(paths, mtimes, sizes, ctimes, atimes); err != nil {
			log.Println("pass1 metadata index error:", err)
		}
	}

	if ctx != nil && ctx.Err() != nil {
		return
	}
	shared.DrainRemaining(cfg.Chunks, "text", sc)
	log.Println("[Indexer] Phase 1/2: Metadata scan complete.")
}

func (a *App) runPass2(ctx context.Context, sc *shared.Engine) {
	if sc == nil {
		return
	}
	log.Println("[Indexer] Phase 2/2: Querying unindexed files...")
	paths, _, err := sc.UnindexedFiles()
	if err != nil {
		log.Println("pass2 query error:", err)
		if ctx != nil {
			a.setStatus(false, "Error querying unindexed files", 0)
		}
		return
	}

	total := len(paths)
	if total == 0 {
		if ctx != nil {
			a.setStatus(false, "Indexing complete.", 100)
		}
		log.Println("[Indexer] Phase 2/2: No files to index.")
		return
	}
	log.Printf("[Indexer] Phase 2/2: Starting semantic indexing for %d files...", total)

	if ctx != nil {
		a.setStatus(true, "Phase 2/2: Starting...", 0)
	}

	start := time.Now()
	cfg, err := shared.NewProcessorConfig(8096, 4000, imageBatchSize, sc)
	if err != nil {
		log.Printf("runPass2 error: %v", err)
		return
	}
	defer cfg.CleanupTempDir()

	var processed int32

	doneChan := make(chan struct{})
	if ctx != nil {
		go func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()

			var lastETA string
			var lastETATime time.Time
			var lastLoggedPct int = -1

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
							statusMsg = fmt.Sprintf("Phase 2/2: %d%% — %s", progressPct, lastETA)
						} else {
							statusMsg = fmt.Sprintf("Phase 2/2: %d%%", progressPct)
						}
					} else {
						statusMsg = fmt.Sprintf("Phase 2/2: %d%%", progressPct)
					}

					if progressPct%10 == 0 && progressPct != lastLoggedPct {
						log.Printf("[Indexer] Phase 2/2 progress: %d%%", progressPct)
						lastLoggedPct = progressPct
					}

					a.setStatus(true, statusMsg, progressPct)
				}
			}
		}()
	}

	sem := make(chan struct{}, maxConcurrency)
	for _, path := range paths {
		if ctx != nil {
			select {
			case <-ctx.Done():
				close(doneChan)
				return
			default:
			}
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

	cfg.Flush() // Execute any lingering batch images
	shared.DrainRemaining(cfg.Chunks, "text", sc)
	shared.DrainRemaining(cfg.Images, "image", sc)
	sc.TruncateWAL()
	log.Printf("[Indexer] Phase 2/2: Indexing complete (%v)", time.Since(start))

	if ctx != nil {
		a.setStatus(false, "Indexing complete.", 100)
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

// contentSkipDirNames is matched against the lowercased *directory name* (not full path).
// When the walker enters a directory whose name is in this set, the entire subtree
// is skipped with filepath.SkipDir — no files inside are ever stat'd or indexed.
//
// Rule of thumb for inclusion: would you ever semantic-search for content *inside*
// this folder? If no, it belongs here.
var contentSkipDirNames = map[string]bool{
	// ── System / Windows ────────────────────────────────────────────────────
	"appdata":        true, // roaming + local + locallow
	"program files":  true,
	"program files (x86)": true,
	"programdata":    true,
	"windows":        true,
	"$windows.~bt":   true,
	"$windows.~ws":   true,
	"system volume information": true,
	"$recycle.bin":   true,
	"recovery":       true,

	// ── Package / dependency caches ─────────────────────────────────────────
	// Go
	"pkg":            true, // catches go\pkg\mod — checked via path prefix below too
	// Rust
	".cargo":         true,
	// Java / Kotlin
	".m2":            true,
	".gradle":        true,
	// .NET
	".nuget":         true,
	// Python
	"__pycache__":   true,
	".venv":         true,
	"venv":          true,
	"env":           true,
	".tox":          true,
	"site-packages": true,
	// Node
	"node_modules": true,
	// Ruby
	".bundle": true,
	"gems":    true,

	// ── Version control internals ────────────────────────────────────────────
	".git":           true,
	".hg":            true,
	".svn":           true,

	// ── IDE / editor state ───────────────────────────────────────────────────
	".vscode":        true,
	".idea":          true,
	".vs":            true,
	".eclipse":       true,
	".metadata":      true, // Eclipse workspace metadata
	".settings":      true, // Eclipse project settings

	// ── Build outputs ────────────────────────────────────────────────────────
	"target":         true, // Rust / Maven / Gradle build output
	"dist":           true, // JS/Python dist
	"build":          true, // common build dir
	"out":            true, // common output dir
	"bin":            true, // compiled binaries
	"obj":            true, // .NET intermediate objects
	".next":         true, // Next.js build cache
	".nuxt":         true,
	".output":       true,
	".cache":        true, // generic tool caches
	".parcel-cache": true,

	// ── Container / VM ───────────────────────────────────────────────────────
	".docker":        true,
	"virtualbox vms": true,
	"virtual machines": true, // Hyper-V / VMware
	"vmware":         true,

	// ── Games (Documents sub-folders & top-level) ────────────────────────────
	"my games":               true,
	"electronic arts":        true,
	"rockstar games":         true,
	"activision":             true,
	"ubisoft game launcher":  true,
	"paradox interactive":    true,
	"bethesda softworks":     true,
	"2k games":               true,
	"epic games":             true,
	"steam":                  true, // Steam library (games data)
	"steamapps":              true,
	"riot games":             true,
	"battlenet":              true,
	"battle.net":             true,
	"blizzard entertainment": true,
	"square enix":            true,
	"sega":                   true,
	"cd projekt red":         true,

	// ── Browser / app caches ─────────────────────────────────────────────────
	"cache":          true,
	"caches":         true,
	"crashreports":   true,
	"crashpad":       true,
	"logs":           true,
	"temp":           true,
	"tmp":            true,
	"thumbnailcache": true,

	// ── Media libraries (large files, not text-searchable content) ───────────
	"music":          true,
	"videos":         true,
	"movies":         true,
	"tv shows":       true,

	// ── Misc tool dirs ───────────────────────────────────────────────────────
	".android": true, // Android SDK AVDs
	"android":  true,
	".ssh":           true, // private keys — never index
	".gnupg":         true, // GPG keys
	".aws":           true, // credentials
	".azure":         true,
	".kube":          true, // kubeconfig
}

// isContentSkippedDir returns true if this directory should be entirely skipped.
// Called with info.IsDir() == true; returns filepath.SkipDir when true.
func isContentSkippedDir(name string) bool {
	return contentSkipDirNames[strings.ToLower(name)]
}

// isContentSkippedPath returns true for files whose full path contains a
// system-level prefix that should never be content-indexed (belt-and-suspenders
// for paths that enter via a symlink or unusual root).
func isContentSkippedPath(path string) bool {
	lower := strings.ToLower(path)
	return strings.Contains(lower, `\go\pkg\mod\`) ||
		strings.Contains(lower, `/go/pkg/mod/`)
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
	if sc == nil {
		return
	}
	drives := availableDrives()
	log.Printf("[sysindex] Starting system-wide path index across %d drive(s)...", len(drives))

	// Walk each drive in parallel. Each goroutine accumulates its own batch
	// so there is no cross-goroutine contention on the slice until flush.
	const batchSize = 2000
	var wg sync.WaitGroup
	for _, root := range drives {
		wg.Add(1)
		go func(root string) {
			defer wg.Done()
			batch := make([]string, 0, batchSize)
			flush := func() {
				if len(batch) > 0 {
					sc.IndexPathsFTS(batch)
					batch = batch[:0]
				}
			}
			filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				select {
				case <-ctx.Done():
					return filepath.SkipAll
				default:
				}
				if d.IsDir() {
					if systemPathSkipDirs[strings.ToLower(d.Name())] {
						return filepath.SkipDir
					}
					return nil
				}
				if isJunkFile(d.Name()) {
					return nil
				}
				batch = append(batch, path)
				if len(batch) >= batchSize {
					flush()
				}
				return nil
			})
			flush()
		}(root)
	}
	wg.Wait()
	log.Println("[sysindex] System-wide path index complete.")
}

// App struct holds the application state
type App struct {
	ctx           context.Context
	engine        *shared.Engine
	config        *shared.AppConfig
	hasGPU        atomic.Bool // atomic: written from GPU-detection goroutine, read from CheckSystemGPU
	
	// Thread-safe state tracking for the frontend fetch
	statusMutex   sync.RWMutex
	isIndexing    bool
	statusMessage string
	progress      int

	indexerCancel context.CancelFunc
	cwd           string
	home          string
	homeWatchStop chan struct{}
	mu            sync.Mutex
	daemon        *daemon.Daemon
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
	a.config = shared.LoadConfig(cwd)

	// --- Memory Adaptive Engine ---
	_, availBytes := shared.GetMemoryInfo()
	if availBytes > 0 {
		availGB := availBytes / (1024 * 1024 * 1024)
		debug.SetMemoryLimit(int64(float64(availBytes) * 0.75))

		concurrency := int(availGB / 2)
		cpuCap := int(float64(runtime.NumCPU()) * 1.5)
		if concurrency > cpuCap {
			concurrency = cpuCap
		}
		if concurrency < 1 {
			concurrency = 1
		}
		maxConcurrency = concurrency

		batch := int(availGB * 5)
		if batch < 20 {
			batch = 20
		} else if batch > 100 {
			batch = 100
		}
		imageBatchSize = batch
		log.Printf("[Indexer] Adaptive RAM %dGB available -> concurrency=%d, batchSize=%d", availGB, maxConcurrency, imageBatchSize)
	}

	// Stale temp file cleanup
	tempMatches, _ := filepath.Glob(filepath.Join(os.TempDir(), "filosophy-img-*"))
	for _, m := range tempMatches {
		os.RemoveAll(m)
	}

	// Memory watchdog
	go func() {
		var m runtime.MemStats
		limit := int64(float64(availBytes) * 0.75)
		if limit <= 0 {
			return
		}
		for {
			time.Sleep(5 * time.Second)
			runtime.ReadMemStats(&m)
			if int64(m.Alloc) > int64(float64(limit)*0.9) {
				log.Printf("⚠️ MEMORY WATCHDOG: Heap usage exceeds 90%% of soft limit (In-use: %v MB)", m.Alloc/(1024*1024))
			}
		}
	}()

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

	if a.config.HasCheckedGPU {
		a.hasGPU.Store(a.config.HasGPU)
	} else {
		go func() {
			// Asynchronous GPU detection with hidden window to avoid terminal flashing
			cmd := exec.Command("nvidia-smi", "-L")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
			hasGPU := false
			if err := cmd.Run(); err == nil {
				hasGPU = true
			} else {
				out, err := exec.Command("wmic", "path", "Win32_VideoController", "get", "Name").Output()
				if err == nil {
					lower := strings.ToLower(string(out))
					for _, kw := range []string{"nvidia", "amd", "radeon", "geforce", "quadro", "arc "} {
						if strings.Contains(lower, kw) {
							hasGPU = true
							break
						}
					}
				}
			}
			a.hasGPU.Store(hasGPU)
			a.config.HasGPU = hasGPU
			a.config.HasCheckedGPU = true
			a.config.Save()
		}()
	}


	log.Println("[Boot 3] Initializing logger...")

	_, err = os.Stat(dbPath)
	dbMissing := os.IsNotExist(err)
	forceIndex := len(os.Args) > 1 && os.Args[1] == "--index"

	// Initialize config
	log.Println("[Boot 4] Loading configuration from database...")
	// a.config already loaded above

	dirs := a.getContentDirs()
	for _, d := range dirs {
		os.MkdirAll(d, 0755)
	}

	// Launch Engine
	log.Println("[Boot 5] Spawning Engine initialization...")
	engine, err := shared.New(dbPath, textModelPath, imageModelPath)
	if err != nil {
		log.Printf("[Boot Error] CRITICAL initialization failed: %v", err)
		log.Printf("The application will continue with search backend disabled.")
	} else {
		a.engine = engine
		log.Println("[Boot 6] Engine initialized successfully")
		if forceIndex {
			if err := a.engine.ResetContentIndex(); err != nil {
				log.Println("reset index error:", err)
			}
		}
	}

	idxCtx, cancel := context.WithCancel(a.ctx)
	a.indexerCancel = cancel

	if a.engine != nil {
		log.Println("[Boot 7] Initializing background indexing...")
		// Always launch indexing in the background on boot to synchronize changes and resume partial scans
		go func() {
			log.Println("[Boot 8] Running background metadata sync...")
			// runPass1 checks for new/deleted files (Metadata)
			a.runPass1(idxCtx, a.engine, dirs, a.config, forceIndex)
			
			// Deterministically block until pass 1 metadata has landed on disk
			a.engine.Flush()

			// runPass2 processes any unindexed content (Semantic)
			a.runPass2(idxCtx, a.engine)
			
			log.Println("[Boot 9] Initial sync complete. Launching in-process daemon...")
			a.daemon = daemon.NewDaemon(dirs, a.engine)
			a.daemon.Start()
			go runSystemPathIndex(idxCtx, a.engine)
		}()
	} else {
		// Even if engine is nil, launch daemon if it's not a fresh install (best effort)
		if !dbMissing {
			log.Println("[Boot 10] Engine offline but DB exists; launching daemon (best-effort)...")
			a.daemon = daemon.NewDaemon(dirs, a.engine)
			a.daemon.Start()
		}
	}

	// Watch home dir top level so the frontend can react to new/deleted folders.
	a.homeWatchStop = make(chan struct{})
	go a.watchHomeFolders()
	log.Println("[Boot 13] Startup sequence complete (Main Thread Released)")
}

// RetryEngineInit attempts to initialize the engine if it previously failed.
func (a *App) RetryEngineInit() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.engine != nil {
		return nil // Already initialized
	}

	cwd, _ := os.Getwd()
	dbPath := filepath.Join(cwd, "filosophy.db")
	textModelPath := filepath.Join(cwd, "text")
	imageModelPath := filepath.Join(cwd, "image")

	log.Println("[Retry] Attempting manual Engine re-initialization...")
	engine, err := shared.New(dbPath, textModelPath, imageModelPath)
	if err != nil {
		log.Printf("[Retry Error] Re-initialization failed: %v", err)
		return fmt.Errorf("initialization failed: %w", err)
	}

	a.engine = engine
	log.Println("[Retry Success] Engine is now online")
	return nil
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
	if a.daemon != nil {
		a.daemon.Stop()
	}
	if a.engine != nil {
		a.engine.Close()
	}
}

// GetIndexingStatus allows the frontend to fetch the exact state on mount
func (a *App) GetIndexingStatus() IndexingStatus {
	a.statusMutex.RLock()
	defer a.statusMutex.RUnlock()
	return IndexingStatus{
		IsIndexing:    a.isIndexing,
		StatusMessage: a.statusMessage,
		Progress:      a.progress,
	}
}

// Helper method to safely update state and emit to frontend
func (a *App) setStatus(isIndexing bool, message string, progress int) {
	a.statusMutex.Lock()
	a.isIndexing = isIndexing
	a.statusMessage = message
	a.progress = progress
	a.statusMutex.Unlock()

	// Emit the standard Wails event so active listeners update
	wailsruntime.EventsEmit(a.ctx, "indexing_status", IndexingStatus{
		IsIndexing:    isIndexing,
		StatusMessage: message,
		Progress:      progress,
	})
}

func (a *App) GetEngineStatus() bool {
	return a.engine != nil
}

func (a *App) Search(query string) ([]shared.SearchResult, error) {
	if a.engine == nil {
		return nil, fmt.Errorf("backend engine not initialized")
	}
	log.Printf("App.Search called with query: %s", query)
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
	// Append manually added extra directories.
	seen := make(map[string]bool)
	for _, d := range a.config.GetExtraDirs() {
		name := filepath.Base(d)
		result = append(result, a.makeFolderState(name+" (extra)", d))
		seen[strings.ToLower(d)] = true
	}
	// Auto-detected network drives.
	for _, nd := range shared.NetworkDrives() {
		if !seen[strings.ToLower(nd)] {
			result = append(result, a.makeFolderState(nd+" (network)", nd))
		}
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
	if a.engine == nil {
		return fmt.Errorf("backend engine not initialized")
	}
	a.config.SetExcluded(folderPath, !indexed)
	if err := a.config.Save(); err != nil {
		return err
	}

	// Restart daemon with updated dir list.
	if a.daemon != nil {
		a.daemon.Stop()
	}

	if !indexed {
		// Just disable — restart daemon and done.
		a.daemon = daemon.NewDaemon(a.getContentDirs(), a.engine)
		a.daemon.Start()
		return nil
	}

	// Folder was enabled — index it in the background.
	a.statusMutex.Lock()
	if a.isIndexing {
		a.statusMutex.Unlock()
		// Another index run is in progress; daemon will be relaunched when it finishes.
		go func() {
			for {
				a.statusMutex.RLock()
				stillBusy := a.isIndexing
				a.statusMutex.RUnlock()
				if !stillBusy {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			a.daemon = daemon.NewDaemon(a.getContentDirs(), a.engine)
			a.daemon.Start()
		}()
		return nil
	}
	a.isIndexing = true
	a.statusMutex.Unlock()

	if a.indexerCancel != nil {
		a.indexerCancel()
	}
	idxCtx, cancel := context.WithCancel(a.ctx)
	a.indexerCancel = cancel
	dir := folderPath

	go func() {
		a.runPass1(idxCtx, a.engine, []string{dir}, a.config, false)
		a.runPass2(idxCtx, a.engine)
		a.daemon = daemon.NewDaemon(a.getContentDirs(), a.engine)
		a.daemon.Start()
	}()

	return nil
}

// AddExtraDirectory adds an arbitrary directory path (e.g. a network drive)
// to the set of content-indexed directories. It triggers indexing immediately.
func (a *App) AddExtraDirectory(dirPath string) error {
	if a.engine == nil {
		return fmt.Errorf("backend engine not initialized")
	}
	// Validate the directory exists and is accessible.
	info, err := os.Stat(dirPath)
	if err != nil {
		return fmt.Errorf("cannot access directory: %v", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", dirPath)
	}
	if !a.config.AddExtraDir(dirPath) {
		return nil // already present
	}
	if err := a.config.Save(); err != nil {
		return err
	}
	log.Printf("Added extra directory: %s", dirPath)

	// Trigger indexing for the newly added directory.
	return a.SetFolderIndexed(dirPath, true)
}

// RemoveExtraDirectory removes an extra directory from indexing.
func (a *App) RemoveExtraDirectory(dirPath string) error {
	if !a.config.RemoveExtraDir(dirPath) {
		return nil // wasn't present
	}
	if err := a.config.Save(); err != nil {
		return err
	}
	log.Printf("Removed extra directory: %s", dirPath)
	if a.daemon != nil {
		a.daemon.Stop()
	}
	a.daemon = daemon.NewDaemon(a.getContentDirs(), a.engine)
	a.daemon.Start()
	return nil
}

// BrowseForDirectory opens a native folder picker dialog and returns the selected path.
func (a *App) BrowseForDirectory() (string, error) {
	result, err := wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title: "Select a directory to index",
	})
	if err != nil {
		return "", err
	}
	return result, nil
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

	go systray.Run(func() {
		systray.SetTitle("Filosophy")
		systray.SetTooltip("Filosophy Search")
		
		// Note: providing an empty []byte works for an invisible/default icon, 
		// but typically you load an icon file here: systray.SetIcon(iconBytes)
		// Since we don't have an icon loaded, we rely on the OS default generic icon.
		
		mShow := systray.AddMenuItem("Show Filosophy", "Show the main window")
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit", "Quit the whole app")

		for {
			select {
			case <-mShow.ClickedCh:
				wailsruntime.WindowShow(app.ctx)
			case <-mQuit.ClickedCh:
				systray.Quit()
				wailsruntime.Quit(app.ctx)
			}
		}
	}, func() {
		// Ensure teardown if systray exits
	})

	err := wails.Run(&options.App{
		Title:  "Filosophy",
		Width:  1280,
		Height: 800,
		HideWindowOnClose: true,
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
		default:
			w.Header().Set("Content-Type", "application/octet-stream")
		}

		data, err := os.ReadFile(decodedPath)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		w.Write(data)
	}
}
