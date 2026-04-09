// Copyright (C) 2025 Filosophy
package shared

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/cespare/xxhash"
	kreuzberg "github.com/kreuzberg-dev/kreuzberg/packages/go/v4"
	"github.com/tmc/langchaingo/textsplitter"
)

// vipsThumbnailPath returns the path to the bundled vipsthumbnail.exe.
func vipsThumbnailPath() string {
	var rel = filepath.FromSlash("vips-dev-8.18/bin/vipsthumbnail.exe")
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), rel)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if wd, err := os.Getwd(); err == nil {
		p := filepath.Join(wd, rel)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "vipsthumbnail"
}

// Metadata holds content and path information for indexing.
// Ctime and Atime are extracted once from os.FileInfo to avoid redundant syscalls.
type Metadata struct {
	Content, Path string
	Hash          int64
	Mtime         int64
	Size          int64
	Ctime         int64
	Atime         int64
}

var imageExtensions = map[string]struct{}{
	".jpg": {}, ".jpeg": {}, ".png": {}, ".gif": {},
	".bmp": {}, ".tiff": {}, ".tif": {},
	".heic": {}, ".heif": {}, ".ico": {},
	".avif": {}, ".jfif": {}, ".pjpeg": {},
	".pjp": {}, ".webp": {},
}

var empty int64 = int64(xxhash.Sum64String(""))

// IsImageFile checks if the filename has an image extension.
func IsImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	_, ok := imageExtensions[ext]
	return ok
}

// IndexBatch directly indexes a batch of metadata items using the Engine.
func IndexBatch(items []Metadata, mode string, sc *Engine) {
	if sc == nil || len(items) == 0 {
		return
	}
	contents := make([]string, len(items))
	paths := make([]string, len(items))
	for i, item := range items {
		contents[i] = item.Content
		paths[i] = item.Path
	}

	// Pre-allocate metadata slices based on item count
	filePaths := make([]string, 0, len(items))
	fileHashes := make([]int64, 0, len(items))
	fileMtimes := make([]int64, 0, len(items))
	fileSizes := make([]int64, 0, len(items))
	fileCtimes := make([]int64, 0, len(items))
	fileAtimes := make([]int64, 0, len(items))

	for _, item := range items {
		if item.Hash == empty {
			continue
		}
		filePaths = append(filePaths, item.Path)
		fileHashes = append(fileHashes, item.Hash)
		fileMtimes = append(fileMtimes, item.Mtime)
		fileSizes = append(fileSizes, item.Size)
		fileCtimes = append(fileCtimes, item.Ctime)
		fileAtimes = append(fileAtimes, item.Atime)
	}

	var err error
	if mode == "image" {
		err = sc.IndexImage(contents, paths, filePaths, fileHashes, fileMtimes, fileSizes, fileCtimes, fileAtimes)
	} else {
		err = sc.IndexText(contents, paths, filePaths, fileHashes, fileMtimes, fileSizes, fileCtimes, fileAtimes)
	}
	if err != nil {
		log.Printf("IndexBatch %s error: %v", mode, err)
	}

	// Index unique paths in FTS5 for path keyword search.
	seen := make(map[string]struct{})
	var uniquePaths []string
	for _, p := range paths {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			uniquePaths = append(uniquePaths, p)
		}
	}
	sc.IndexPathsFTS(uniquePaths)
}

// HandleChunk adds metadata to a channel and flushes when full.
// Contention issue fixed: Mutex removed from hot path as channels are natively thread-safe.
// Flushing now uses a non-blocking select to drain efficiently.
func HandleChunk(chunks chan Metadata, sc *Engine, mode, content, path string, hash, mtime, size, ctime, atime int64) {
	if sc == nil {
		return
	}
	
	item := Metadata{content, path, hash, mtime, size, ctime, atime}
	
	select {
	case chunks <- item:
		// Queued successfully
	default:
		// Channel is full, drain and flush
		items := make([]Metadata, 0, cap(chunks))
		done := false
		for !done {
			select {
			case tmp := <-chunks:
				items = append(items, tmp)
			default:
				done = true
			}
		}
		
		// Attempt to queue the current item again or just include it in batch
		if len(items) < cap(chunks) {
			items = append(items, item)
		} else {
			// Extremely rare: channel refilled while we were draining. 
			// Process separately.
			IndexBatch([]Metadata{item}, mode, sc)
		}

		// Run batch indexing outside any locks
		if len(items) > 0 {
			IndexBatch(items, mode, sc)
		}
		runtime.Gosched()
	}
}

// Global cache for files that failed thumbnailing to avoid process-spawn loops
var failedThumbnails sync.Map

// DrainRemaining drains all remaining items from a channel and indexes them.
func DrainRemaining(chunks chan Metadata, mode string, sc *Engine) {
	if sc == nil || len(chunks) == 0 {
		return
	}
	items := make([]Metadata, 0, len(chunks))
	for len(chunks) > 0 {
		items = append(items, <-chunks)
	}
	IndexBatch(items, mode, sc)
}

// ProcessorConfig holds configuration for file processing.
type ProcessorConfig struct {
	Splitter *textsplitter.RecursiveCharacter
	Chunks   chan Metadata
	Images   chan Metadata
	Engine   *Engine
	TempDir  string // temp dir for converted images, cleaned up after indexing
	Mu       *sync.Mutex
}

// ProcessImage converts an image to JPEG via vips and queues it for indexing.
func ProcessImage(path string, mtime, size, ctime, atime int64, cfg *ProcessorConfig) {
	if cfg == nil || cfg.Engine == nil {
		return
	}

	// Check if this path previously failed to avoid redundant process spawning
	if _, failed := failedThumbnails.Load(path); failed {
		return
	}

	hash := mtime ^ size
	outPath := filepath.Join(cfg.TempDir, fmt.Sprintf("%d.jpg", hash))
	
	// Skip if already converted (mtime/size match)
	if _, err := os.Stat(outPath); err == nil {
		HandleChunk(cfg.Images, cfg.Engine, "image", outPath, path, hash, mtime, size, ctime, atime)
		return
	}

	cmd := exec.Command(vipsThumbnailPath(), path, "-s", "256x256!", "-o", outPath+"[Q=80,strip]")
	if err := cmd.Run(); err != nil {
		log.Printf("vipsthumbnail failed for %s: %v", path, err)
		failedThumbnails.Store(path, true)
		return
	}


	HandleChunk(cfg.Images, cfg.Engine, "image", outPath, path, hash, mtime, size, ctime, atime)
}

// ProcessText extracts text from a file and adds chunks to the channel.
func ProcessText(path string, mtime, size, ctime, atime int64, cfg *ProcessorConfig) {
	if cfg == nil || cfg.Engine == nil {
		return
	}
	result, err := kreuzberg.ExtractFileSync(path, nil)
	if err != nil || result == nil || result.Content == "" {
		HandleChunk(cfg.Chunks, cfg.Engine, "text", "", path, empty, mtime, size, ctime, atime)
		return
	}
	hash := int64(xxhash.Sum64String(result.Content))
	splits, err := cfg.Splitter.SplitText(result.Content)
	if err != nil {
		log.Println("Failed to split text:", err)
		return
	}

	first := true
	for _, chunk := range splits {
		if first {
			HandleChunk(cfg.Chunks, cfg.Engine, "text", chunk, path, hash, mtime, size, ctime, atime)
			first = false
		} else {
			HandleChunk(cfg.Chunks, cfg.Engine, "text", chunk, path, empty, 0, 0, 0, 0)
		}
	}
}

// ProcessFile processes a single file (image or text) based on its type.
func ProcessFile(path string, cfg *ProcessorConfig) {
	if cfg == nil {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	mtime := info.ModTime().UnixNano()
	size := info.Size()
	ctime, atime := FileExtraTimesFromInfo(info)
	if IsImageFile(path) {
		ProcessImage(path, mtime, size, ctime, atime, cfg)
	} else {
		ProcessText(path, mtime, size, ctime, atime, cfg)
	}
}

// ProcessDirectory adds directory path to the chunks channel.
func ProcessDirectory(path string, cfg *ProcessorConfig) {
	if cfg == nil || cfg.Engine == nil {
		return
	}
	HandleChunk(cfg.Chunks, cfg.Engine, "text", "", path, empty, 0, 0, 0, 0)
}


// NewProcessorConfig creates a new ProcessorConfig with default settings.
// Creates a temp directory for image conversions; caller must call CleanupTempDir() when done.
// InitIndexTables is called at most once per Engine instance via sync.Once (IX-4 fix).
func NewProcessorConfig(chunkSize, chunkCap, imageCap int, sc *Engine) (*ProcessorConfig, error) {
	if sc == nil {
		return nil, fmt.Errorf("cannot create processor config: engine is nil")
	}
	sc.initTableOnce.Do(func() {
		if err := sc.InitIndexTables(); err != nil {
			log.Printf("Failed to initialize index tables: %v", err)
		}
	})

	splitter := textsplitter.NewRecursiveCharacter(func(o *textsplitter.Options) {
		o.ChunkSize = chunkSize
	})
	tmpDir, err := os.MkdirTemp("", "filosophy-img-*")
	if err != nil {
		log.Println("Failed to create temp dir for images:", err)
	}
	// Cap the image capacity high enough so batching is effective.
	if imageCap < 100 {
		imageCap = 100
	}
	return &ProcessorConfig{
		Splitter: &splitter,
		Chunks:   make(chan Metadata, chunkCap),
		Images:   make(chan Metadata, imageCap),
		Engine:   sc,
		TempDir:  tmpDir,
		Mu:       &sync.Mutex{},
	}, nil
}

// CleanupTempDir removes the temp directory used for image conversions.
func (cfg *ProcessorConfig) CleanupTempDir() {
	if cfg != nil && cfg.TempDir != "" {
		os.RemoveAll(cfg.TempDir)
	}
}
