package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"filosophy/shared"

	"github.com/cespare/xxhash"
	"github.com/kardianos/service"
	"github.com/syncthing/notify"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

var logger service.Logger

const (
	// Track file/folder creation, deletion, and rename
	structureEventMask = notify.FileNotifyChangeFileName | notify.FileNotifyChangeDirName
	// Track content changes (size and last write)
	contentEventMask = notify.FileNotifyChangeSize | notify.FileNotifyChangeLastWrite
)

// shouldFilterPath returns true if the path should be ignored for content changes
func shouldFilterPath(path string) bool {
	parts := strings.Split(path, "\\")
	if len(parts) < 4 {
		return true
	}
	name := parts[3]
	// Filter out AppData, hidden files (starting with .), and ntuser.dat
	return name == "AppData" || strings.HasPrefix(name, ".") || strings.HasPrefix(strings.ToLower(name), "ntuser.dat")
}

// shouldFilterFolder returns true if the folder should be skipped
func shouldFilterFolder(name string) bool {
	return name == "AppData" || strings.HasPrefix(name, ".") || strings.HasPrefix(strings.ToLower(name), "ntuser")
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
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return int64(xxhash.Sum64(b)), nil
}

// getHash returns the cached hash for a path, or queries the DB on cache miss.
// Returns 0 if not found in either.
func (t *tracker) getHash(path string) (int64, error) {
	if h, ok := t.hashes[path]; ok {
		return h, nil
	}
	// Cache miss — query DB
	conn, err := sqlite.OpenConn(t.dbPath, sqlite.OpenReadOnly)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	var hash int64
	_ = sqlitex.Execute(conn, "SELECT hash FROM files WHERE path = ?", &sqlitex.ExecOptions{
		Args: []any{path},
		ResultFunc: func(stmt *sqlite.Stmt) error {
			hash = stmt.ColumnInt64(0)
			return nil
		},
	})
	if hash != 0 {
		t.hashes[path] = hash // cache it
	}
	return hash, nil
}

type tracker struct {
	mu      sync.Mutex
	changes map[string]*fileChanges
	hashes  map[string]int64 // path → cached hash (write-through from DB)
	dbPath  string
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

func (p *program) flush() {
	fmt.Println("[FLUSH] attempting lock...")
	if !p.flushMu.TryLock() {
		fmt.Println("[FLUSH] skipped — another flush in progress")
		return // another flush is already in progress
	}
	defer p.flushMu.Unlock()

	p.t.mu.Lock()
	changes := p.t.drain()
	p.t.mu.Unlock()

	fmt.Printf("[FLUSH] drained %d changed paths\n", len(changes))
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

	fmt.Printf("[FLUSH] deletes=%d, renames=%d, adds=%d\n", len(deletePaths), len(renamePairs), len(addPaths))

	// 1. Delete all references for removed/modified files
	if len(deletePaths) > 0 {
		fmt.Println("[FLUSH] deleting:", deletePaths)
		data, _ := json.Marshal(map[string]interface{}{"paths": deletePaths})
		if _, err := shared.Send(data, "delete"); err != nil {
			log.Println("flush delete error:", err)
		}
		// Clear cached hashes so big-file handler won't re-trigger
		p.t.mu.Lock()
		for _, path := range deletePaths {
			delete(p.t.hashes, path)
		}
		p.t.mu.Unlock()
	}

	// 2. Rename paths in DB
	if len(renamePairs) > 0 {
		fmt.Println("[FLUSH] renaming:", renamePairs)
		oldPaths := make([]string, len(renamePairs))
		newPaths := make([]string, len(renamePairs))
		for i, pair := range renamePairs {
			oldPaths[i] = pair[0]
			newPaths[i] = pair[1]
		}
		data, _ := json.Marshal(map[string]interface{}{
			"old_paths": oldPaths,
			"new_paths": newPaths,
		})
		if _, err := shared.Send(data, "rename"); err != nil {
			log.Println("flush rename error:", err)
		}
	}

	// 3. Index new/modified files via shared processor
	if len(addPaths) > 0 {
		fmt.Println("[FLUSH] indexing:", addPaths)
		cfg := shared.NewProcessorConfig(8096, 100, 50, 10)
		sendDone := make(chan struct{})
		go func() {
			for d := range cfg.SendQueue {
				fmt.Println(d.Data)
				if _, err := shared.Send(d.Data, d.Mode); err != nil {
					log.Println("flush index error:", err)
				}
			}
			sendDone <- struct{}{}
		}()

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

		for i := 0; i < len(cfg.Chunks); i++ {
			j := <-cfg.Chunks
			fmt.Println(j.Path)
			cfg.Chunks <- j
		}
		shared.DrainRemaining(cfg.Chunks, "text", cfg.SendQueue)
		shared.DrainRemaining(cfg.Images, "image", cfg.SendQueue)
		close(cfg.SendQueue)
		<-sendDone
	}
	fmt.Println("[FLUSH] done")
}

type program struct {
	t                          *tracker
	flushMu                    sync.Mutex
	exit                       chan struct{}
	structureChan, contentChan chan notify.EventInfo
	baseDir                    string
}

func (p *program) Start(s service.Service) error {
	var err error
	p.baseDir, err = "C:/Users/Mahir/Downloads/test/", nil //os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	p.exit = make(chan struct{})
	// Channel for structure changes (create, delete, rename) - ALL files
	p.structureChan = make(chan notify.EventInfo, 500)
	// Channel for content changes - filtered files only
	p.contentChan = make(chan notify.EventInfo, 500)

	// Start should not block. Do the actual work async.
	go p.run()
	return nil
}

func (p *program) run() {
	// DB lives in the project root (parent of daemon/)
	// Use Getwd instead of Executable — go run compiles to a temp dir
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal("cannot get working directory:", err)
	}
	dbPath := filepath.Join(cwd, "..", "filosophy.db")

	// Do work here
	p.t = &tracker{
		changes: make(map[string]*fileChanges),
		hashes:  make(map[string]int64),
		dbPath:  dbPath,
		prev:    sentinel{},
	}
	if err := notify.Watch(p.baseDir, p.structureChan, structureEventMask); err != nil {
		fmt.Println("Error watching structure:", err)
		return
	}
	defer notify.Stop(p.structureChan)
	entries, err := os.ReadDir(p.baseDir)
	if err != nil {
		fmt.Println("Error reading base directory:", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if shouldFilterFolder(name) {
			continue
		}

		// Set up recursive watch for this folder
		watchPath := filepath.Join(p.baseDir, name, "...")
		if err := notify.Watch(watchPath, p.contentChan, contentEventMask); err != nil {
			fmt.Println("Error watching content for", name, ":", err)
			continue
		}
		if err := notify.Watch(watchPath, p.structureChan, structureEventMask); err != nil {
			fmt.Println("Error watching structure for", name, ":", err)
			continue
		}
		fmt.Println("Watching:", watchPath)
	}

	// Also watch the base directory itself (non-recursive) for content changes
	if err := notify.Watch(p.baseDir, p.contentChan, contentEventMask); err != nil {
		fmt.Println("Error watching base directory content:", err)
	} else {
		fmt.Println("Watching base:", p.baseDir)
	}
	defer notify.Stop(p.contentChan)

	// Goroutine for structure changes (create, delete, rename) - no filtering
	go func() {
		for ei := range p.structureChan {
			p.t.mu.Lock()
			p.t.record(ei)
			shouldFlush := p.t.count >= 50
			p.t.mu.Unlock()

			fmt.Println("[STRUCTURE]", ei.Event(), ei.Path(), ei.Sys())

			// Auto-watch new top-level folders in the user's home directory
			if (ei.Event() == notify.FileActionAdded || ei.Event() == notify.FileActionRenamedNewName) &&
				filepath.Dir(ei.Path()) == p.baseDir {
				name := filepath.Base(ei.Path())
				if !shouldFilterFolder(name) {
					if info, err := os.Stat(ei.Path()); err == nil && info.IsDir() {
						watchPath := filepath.Join(p.baseDir, name, "...")
						if err := notify.Watch(watchPath, p.contentChan, contentEventMask); err != nil {
							fmt.Println("Error watching content for new folder", name, ":", err)
						}
						if err := notify.Watch(watchPath, p.structureChan, structureEventMask); err != nil {
							fmt.Println("Error watching structure for new folder", name, ":", err)
						} else {
							fmt.Println("Now watching new folder:", watchPath)
						}
					}
				}
			}

			if shouldFlush {
				go p.flush()
			}
		}
	}()

	// Goroutine for content changes - already filtered by folder selection
	go func() {
		for ei := range p.contentChan {
			if shouldFilterPath(ei.Path()) {
				continue
			}
			p.t.mu.Lock()
			if isFileTooLarge(ei.Path()) {
				// Too large — only mark modified if previously content-indexed
				if h, _ := p.t.getHash(ei.Path()); h != 0 {
					p.t.getOrCreate(ei.Path()).modified = true
					p.t.count++
				}
				p.t.prev = ei
			} else {
				p.t.record(ei)
			}
			shouldFlush := p.t.count >= 50
			p.t.mu.Unlock()

			fmt.Println("[CONTENT]", ei.Event(), ei.Path(), ei.Sys())

			if shouldFlush {
				go p.flush()
			}
		}
	}()
	// Flush every 5 minutes regardless of action count
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	go func() {
		for range ticker.C {
			p.t.mu.Lock()
			hasChanges := p.t.count > 0
			p.t.mu.Unlock()
			if hasChanges {
				go p.flush()
			}
		}
	}()

	<-p.exit
}
func (p *program) Stop(s service.Service) error {
	// Stop should not block. Return with a few seconds.
	fmt.Println(p.t.changes)
	close(p.exit)
	close(p.structureChan)
	close(p.contentChan)
	return nil
}

func main() {
	svcFlag := flag.String("service", "", "Control the system service.")
	flag.Parse()
	options := make(service.KeyValue)
	options["Restart"] = "on-success"
	options["SuccessExitStatus"] = "1 2 8 SIGKILL"
	svcConfig := &service.Config{
		Name:        "GoServiceTest",
		DisplayName: "Go Service Test",
		Description: "This is a test Go service.",
		Option:      options,
	}

	prg := &program{}
	s, err := service.New(prg, svcConfig)
	if err != nil {
		fmt.Println(err)
	}
	errs := make(chan error, 5)
	logger, err = s.Logger(errs)
	if err != nil {
		logger.Error(err)
	}

	go func() {
		for {
			err := <-errs
			if err != nil {
				log.Print(err)
			}
		}
	}()

	if len(*svcFlag) != 0 {
		err := service.Control(s, *svcFlag)
		if err != nil {
			log.Printf("Valid actions: %q\n", service.ControlAction)
			log.Fatal(err)
		}
		return
	}

	err = s.Run()
	if err != nil {
		fmt.Println(err)
	}
}

// Search C:\Users\Mahir\AppData\Roaming\Microsoft\Windows\Start Menu\Programs for program exe and search userprofile. thats it
// DO NOT READ APPDATA STUFF, and also windows stuff, just add the path
