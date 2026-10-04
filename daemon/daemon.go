// Copyright (C) 2025 Filosophy
package daemon

import (
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"filosophy/shared"

	"github.com/syncthing/notify"
)

const (
	// Track file/folder creation, deletion, and rename
	structureEventMask = notify.FileNotifyChangeFileName | notify.FileNotifyChangeDirName
	// Track content changes (size and last write)
	contentEventMask = notify.FileNotifyChangeSize | notify.FileNotifyChangeLastWrite
)

// shouldFilterPath returns true if the path should be ignored for content changes
func shouldFilterPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(path))
	for _, segment := range strings.Split(lower, "/") {
		if shouldFilterFolder(segment) {
			return true
		}
	}
	return false
}

// shouldFilterFolder returns true if the folder should be skipped
func shouldFilterFolder(name string) bool {
	lowerName := strings.ToLower(name)
	if strings.HasPrefix(lowerName, ".") || strings.HasPrefix(lowerName, "ntuser") {
		return true
	}
	switch lowerName {
	case "appdata", "program files", "program files (x86)", "programdata", "windows",
		"$windows.~bt", "$windows.~ws", "system volume information", "$recycle.bin",
		"recovery", "perflogs", "node_modules", "vendor", "bower_components",
		"coverage", "build", "dist", "out", "bin", "obj", "target", "__pycache__",
		".venv", "venv", "env", ".tox", "site-packages", ".cargo", ".m2", ".gradle",
		".nuget", ".bundle", "gems", ".git", ".hg", ".svn", ".vscode", ".idea",
		".vs", ".eclipse", ".metadata", ".settings", ".next", ".nuxt", ".output",
		".cache", ".parcel-cache", ".pytest_cache", ".mypy_cache", ".ruff_cache",
		".terraform", ".serverless", "pods", "carthage", "deriveddata", ".yarn",
		".pnpm-store", ".turbo", ".vite", ".svelte-kit", ".angular", ".astro",
		".docker", "virtualbox vms", "virtual machines", "vmware", "cache", "caches",
		"crashreports", "crashpad", "logs", "temp", "tmp", "thumbnailcache",
		".android", "android", ".ssh", ".gnupg", ".aws", ".azure", ".kube":
		return true
	default:
		return false
	}
}

// isFileTooLarge returns true if file is larger than 100MB
const maxFileSize = 100 * 1024 * 1024 // 100MB

func isFileTooLarge(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false // If we can't stat, don't filter
	}
	if info.IsDir() {
		return false
	}
	return info.Size() > maxFileSize
}

type change struct {
	action string
	old    string // only set for renames (stores the previous path)
}

type fileChanges struct {
	actions  []change
	modified bool
}

// sentinel implements notify.EventInfo as a safe default for prev
type sentinel struct{}

func (s sentinel) Event() notify.Event { return 0 }
func (s sentinel) Path() string        { return "" }
func (s sentinel) Sys() interface{}    { return nil }

// hashFile returns the xxhash of a file's contents as int64 (for SQLite compatibility).
func hashFile(path string) (int64, error) {
	return shared.HashFile(path)
}

// getHash returns the cached hash for a path, or queries the DB on cache miss.
// Returns 0 if not found in either.
func (t *tracker) getHash(path string) (int64, error) {
	if h, ok := t.hashes[path]; ok {
		return h, nil
	}
	// Cache miss — query via the shared engine
	if t.engine == nil {
		return 0, nil
	}
	hash, err := t.engine.GetFileHash(path)
	if err != nil {
		return 0, err
	}
	if hash != 0 {
		t.hashes[path] = hash // cache it
	}
	return hash, nil
}

type tracker struct {
	mu      sync.Mutex
	changes map[string]*fileChanges
	hashes  map[string]int64 // path → cached hash (write-through from DB)
	engine  *shared.Engine
	prev    notify.EventInfo
	count   int
}

func (t *tracker) getOrCreate(path string) *fileChanges {
	fc := t.changes[path]
	if fc == nil {
		fc = &fileChanges{}
		t.changes[path] = fc
	}
	return fc
}

func (t *tracker) rename(newPath, oldPath string) {
	oldFC := t.changes[oldPath]
	newFC := t.getOrCreate(newPath)

	if oldFC == nil || len(oldFC.actions) == 0 {
		// File existed before tracking started — record as a rename
		newFC.actions = append(newFC.actions, change{action: "rename", old: oldPath})
		// Carry over hash from old path
		if h, ok := t.hashes[oldPath]; ok {
			t.hashes[newPath] = h
			delete(t.hashes, oldPath)
		}
		return
	}

	first := oldFC.actions[0]
	rest := oldFC.actions[1:]

	var entry change
	switch first.action {
	case "add":
		// File was created this session — it's still just "new" at the new path
		entry = change{action: "add"}
	case "rename":
		// Already renamed before — chain back to the original old path
		entry = change{action: "rename", old: first.old}
	default:
		// First change is something else — record rename and keep all old changes
		newFC.actions = append(newFC.actions, change{action: "rename", old: oldPath})
		newFC.actions = append(newFC.actions, oldFC.actions...)
		newFC.modified = newFC.modified || oldFC.modified
		delete(t.changes, oldPath)
		if h, ok := t.hashes[oldPath]; ok {
			t.hashes[newPath] = h
			delete(t.hashes, oldPath)
		}
		return
	}

	// Replace the first entry with the new one, copy the rest
	newFC.actions = append(newFC.actions, entry)
	newFC.actions = append(newFC.actions, rest...)
	newFC.modified = newFC.modified || oldFC.modified
	delete(t.changes, oldPath)
	if h, ok := t.hashes[oldPath]; ok {
		t.hashes[newPath] = h
		delete(t.hashes, oldPath)
	}
}

func (t *tracker) record(info notify.EventInfo) {
	if shared.IsWhatsAppDatabase(info.Path()) {
		fc := t.getOrCreate(info.Path())
		fc.actions = []change{{action: "remove"}}
		fc.modified = false
		t.count++
		t.prev = info
		return
	}

	switch info.Event() {
	case notify.FileActionAdded:
		t.getOrCreate(info.Path()).actions = append(t.getOrCreate(info.Path()).actions, change{action: "add"})
	case notify.FileActionRemoved:
		if t.prev.Event() == notify.FileActionAdded && filepath.Base(t.prev.Path()) == filepath.Base(info.Path()) {
			t.rename(info.Path(), t.prev.Path())
		} else {
			// Remove wipes any prior changes for this path
			t.changes[info.Path()] = &fileChanges{
				actions: []change{{action: "remove"}}, modified: false,
			}
		}
	case notify.FileActionModified:
		old, errO := t.getHash(info.Path())
		if errO != nil {
			log.Println(errO)
		}
		new, errN := hashFile(info.Path())
		if errN != nil {
			log.Println(errN)
		}
		t.getOrCreate(info.Path()).modified = true && old != new && errO == nil && errN == nil
	case notify.FileActionRenamedNewName:
		t.rename(info.Path(), t.prev.Path())
	case notify.FileActionRenamedOldName:
	}

	t.count++
	t.prev = info
}

// drain grabs the current changes, resets the tracker, and returns them.
func (t *tracker) drain() map[string]*fileChanges {
	result := t.changes
	t.changes = make(map[string]*fileChanges)
	t.count = 0
	return result
}

// Daemon watches a set of directories for file changes and indexes them using the shared Engine.
// It runs in the background and does not interfere with search or indexing from the main app.
type Daemon struct {
	t                          *tracker
	sc                         *shared.Engine
	flushMu                    sync.Mutex
	exit                       chan struct{}
	structureChan, contentChan chan notify.EventInfo
	baseDirs                   []string
}

// NewDaemon creates a new Daemon linked to the given Engine.
// The daemon shares the Engine with the main App — it watches for file changes
// and indexes them in the background without interfering with search operations.
func NewDaemon(baseDirs []string, engine *shared.Engine) *Daemon {
	return &Daemon{
		baseDirs: baseDirs,
		sc:       engine,
		exit:     make(chan struct{}),
	}
}

// isDirectChild returns true if path is a direct child of any of the watched base directories.
func (d *Daemon) isDirectChild(path string) bool {
	parent := filepath.Dir(path)
	for _, base := range d.baseDirs {
		if parent == base {
			return true
		}
	}
	return false
}

func (d *Daemon) flush() {
	if !d.flushMu.TryLock() {
		return // another flush is already in progress
	}
	defer d.flushMu.Unlock()

	d.t.mu.Lock()
	changes := d.t.drain()
	d.t.mu.Unlock()

	if len(changes) == 0 {
		return
	}

	var deletePaths []string
	var renamePairs [][2]string
	var addPaths []string

	for path, fc := range changes {
		if len(fc.actions) == 0 {
			if fc.modified {
				deletePaths = append(deletePaths, path)
				addPaths = append(addPaths, path)
			}
			continue
		}

		first := fc.actions[0]
		switch first.action {
		case "remove":
			deletePaths = append(deletePaths, path)
		case "add":
			addPaths = append(addPaths, path)
		case "rename":
			if fc.modified {
				// Content changed — delete old, re-index at new path
				deletePaths = append(deletePaths, first.old)
				addPaths = append(addPaths, path)
			} else {
				renamePairs = append(renamePairs, [2]string{first.old, path})
			}
		}
	}

	// 1. Delete all references for removed/modified files
	if len(deletePaths) > 0 {
		if err := d.sc.DeletePaths(deletePaths); err != nil {
			log.Println("flush delete error:", err)
		}
		// Clear cached hashes so big-file handler won't re-trigger
		d.t.mu.Lock()
		for _, path := range deletePaths {
			delete(d.t.hashes, path)
		}
		d.t.mu.Unlock()
	}

	// 2. Rename paths in DB
	if len(renamePairs) > 0 {
		oldPaths := make([]string, len(renamePairs))
		newPaths := make([]string, len(renamePairs))
		for i, pair := range renamePairs {
			oldPaths[i] = pair[0]
			newPaths[i] = pair[1]
		}
		if err := d.sc.RenamePaths(oldPaths, newPaths); err != nil {
			log.Println("flush rename error:", err)
		}
	}

	// 3. Index new/modified files via shared processor
	if len(addPaths) > 0 {
		cfg, err := shared.NewProcessorConfig(8096, 1000, 200, d.sc, d.sc.Hardware)
		if err != nil {
			log.Println("[FLUSH] NewProcessorConfig error:", err)
			return
		}

		for _, path := range addPaths {
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			if info.IsDir() || info.Size() > maxFileSize {
				// Directory or file too large — index path only
				shared.ProcessDirectory(path, cfg)
			} else {
				shared.ProcessFile(path, cfg)
			}
		}

		cfg.Flush()
		shared.DrainRemaining(cfg.Chunks, "text", d.sc)
		shared.DrainRemaining(cfg.Images, "image", d.sc)
		cfg.CleanupTempDir()

		// Release memory back to Windows only if the batch was significant.
		// Uses the hardware-adaptive GCInterval.
		if len(addPaths) >= d.sc.Hardware.GCInterval/5 || len(addPaths) > 10 {
			runtime.GC()
			debug.FreeOSMemory()
		}
	}
}

func (d *Daemon) Start() error {
	// Channel for structure changes (create, delete, rename) - ALL files
	d.structureChan = make(chan notify.EventInfo, 500)
	// Channel for content changes - filtered files only
	d.contentChan = make(chan notify.EventInfo, 500)

	// Start should not block. Do the actual work async.
	go d.run()
	return nil
}

func (d *Daemon) run() {
	// Do work here
	d.t = &tracker{
		changes: make(map[string]*fileChanges),
		hashes:  make(map[string]int64),
		engine:  d.sc,
		prev:    sentinel{},
	}
	// Watch all configured base directories.
	for _, baseDir := range d.baseDirs {
		if err := notify.Watch(baseDir, d.structureChan, structureEventMask); err != nil {
			log.Println("Error watching structure for", baseDir, ":", err)
			continue
		}
		entries, err := os.ReadDir(baseDir)
		if err != nil {
			log.Println("Error reading base directory:", baseDir, err)
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if shouldFilterFolder(name) {
				continue
			}
			watchPath := filepath.Join(baseDir, name, "...")
			if err := notify.Watch(watchPath, d.contentChan, contentEventMask); err != nil {
				continue
			}
			if err := notify.Watch(watchPath, d.structureChan, structureEventMask); err != nil {
				continue
			}
		}
		if err := notify.Watch(baseDir, d.contentChan, contentEventMask); err != nil {
		} else {
		}
	}
	defer notify.Stop(d.structureChan)
	defer notify.Stop(d.contentChan)

	// Goroutine for structure changes (create, delete, rename) - no filtering
	go func() {
		for ei := range d.structureChan {
			d.t.mu.Lock()
			d.t.record(ei)
			shouldFlush := d.t.count >= 50
			d.t.mu.Unlock()

			// Auto-watch new top-level folders inside any watched base directory.
			if (ei.Event() == notify.FileActionAdded || ei.Event() == notify.FileActionRenamedNewName) &&
				d.isDirectChild(ei.Path()) {
				name := filepath.Base(ei.Path())
				if !shouldFilterFolder(name) {
					if info, err := os.Stat(ei.Path()); err == nil && info.IsDir() {
						watchPath := filepath.Join(filepath.Dir(ei.Path()), name, "...")
						if err := notify.Watch(watchPath, d.contentChan, contentEventMask); err != nil {
						}
						if err := notify.Watch(watchPath, d.structureChan, structureEventMask); err != nil {
						}
					}
				}
			}

			if shouldFlush {
				go d.flush()
			}
		}
	}()

	// Goroutine for content changes - already filtered by folder selection
	go func() {
		for ei := range d.contentChan {
			if shouldFilterPath(ei.Path()) {
				continue
			}
			d.t.mu.Lock()
			if isFileTooLarge(ei.Path()) {
				// Too large — only mark modified if previously content-indexed
				if h, _ := d.t.getHash(ei.Path()); h != 0 {
					d.t.getOrCreate(ei.Path()).modified = true
					d.t.count++
				}
				d.t.prev = ei
			} else {
				d.t.record(ei)
			}
			shouldFlush := d.t.count >= 50
			d.t.mu.Unlock()

			if shouldFlush {
				go d.flush()
			}
		}
	}()
	// Flush every 30 minutes regardless of action count
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			d.t.mu.Lock()
			hasChanges := d.t.count > 0
			d.t.mu.Unlock()
			if hasChanges {
				go d.flush()
			}
		}
	}()

	// The initial indexing crawl was historically performed here. It has been entirely ripped out
	// because `main.go` already triggers a structured 2-pass crawl via `runPass1` and `runPass2` before
	// the Daemon boots up! Performing it here was duplicating work 1:1 and causing huge lock contention.

	<-d.exit
}

func (d *Daemon) Stop() error {
	// Stop should not block. Return with a few seconds.
	close(d.exit)
	if d.structureChan != nil {
		notify.Stop(d.structureChan)
	}
	if d.contentChan != nil {
		notify.Stop(d.contentChan)
	}
	return nil
}
