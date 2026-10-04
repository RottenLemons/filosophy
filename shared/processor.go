// Copyright (C) 2025 Filosophy
package shared

import (
	"fmt"
	"io"
	"log"
	"os"
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
	FileDone      bool
}

var imageExtensions = map[string]struct{}{
	// Formats vipsthumbnail reliably handles and that are meaningful for CLIP.
	".jpg": {}, ".jpeg": {}, ".jfif": {}, ".pjpeg": {}, ".pjp": {},
	".png":  {},
	".gif":  {},
	".webp": {},
	// Explicitly excluded (vips fails or format is useless for semantic image search):
	// .ico  — Windows icons, tiny, vips exits 0xffffffff
	// .bmp  — raw uncompressed, usually system/app assets, huge files
	// .tiff/.tif — often multi-page scanner output, vips can fail on unusual sub-types
	// .cur/.ani — cursor files
}

// imageSkipExtensions are formats that look like images but should be skipped entirely:
// vipsthumbnail fails on them and they have no semantic search value.
var imageSkipExtensions = map[string]struct{}{
	".ico": {}, ".cur": {}, ".ani": {},
	".bmp":  {},
	".tiff": {}, ".tif": {},
	".heic": {}, ".heif": {},
	".avif": {},
}

// textAllowExtensions controls which file types get text extraction via kreuzberg.
// Files NOT in this set are immediately marked content_indexed=1 without calling
// kreuzberg — they are still findable by filename/path via paths_fts.
var textAllowExtensions = map[string]bool{
	// Documents
	".pdf": true, ".docx": true, ".doc": true,
	".xlsx": true, ".xls": true,
	".pptx": true, ".ppt": true,
	// Plain text / markup
	".txt": true, ".md": true,
	".html": true, ".htm": true,
}

// IsWhatsAppDatabase identifies WhatsApp's local SQLite stores so indexing
// never reads or exposes their private message databases.
func IsWhatsAppDatabase(path string) bool {
	normalizedPath := strings.ReplaceAll(filepath.ToSlash(path), `\`, "/")
	lowerPath := strings.ToLower(normalizedPath)
	base := strings.ToLower(filepath.Base(normalizedPath))
	if base == "whatsapp.db" || base == "msgstore.db" {
		return true
	}
	return filepath.Ext(lowerPath) == ".db" && strings.Contains(lowerPath, "/whatsapp/")
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
	plan := makeIndexBatchPlan(items)

	if len(plan.skipPaths) > 0 {
		if err := sc.MarkContentIndexed(plan.skipPaths, plan.skipHashes, plan.skipMtimes, plan.skipSizes, plan.skipCtimes, plan.skipAtimes); err != nil {
			log.Printf("IndexBatch MarkContentIndexed error: %v", err)
		}
	}

	if len(plan.contentItems) == 0 {
		return
	}

	contents := make([]string, len(plan.contentItems))
	paths := make([]string, len(plan.contentItems))

	for i, item := range plan.contentItems {
		contents[i] = item.Content
		paths[i] = item.Path
	}

	var err error
	if mode == "image" {
		err = sc.IndexImage(contents, paths, plan.filePaths, plan.fileHashes, plan.fileMtimes, plan.fileSizes, plan.fileCtimes, plan.fileAtimes, plan.completePaths)
	} else {
		err = sc.IndexText(contents, paths, plan.filePaths, plan.fileHashes, plan.fileMtimes, plan.fileSizes, plan.fileCtimes, plan.fileAtimes, plan.completePaths)
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

type indexBatchPlan struct {
	contentItems                      []Metadata
	skipPaths                         []string
	skipHashes, skipMtimes, skipSizes []int64
	skipCtimes, skipAtimes            []int64
	filePaths                         []string
	fileHashes, fileMtimes, fileSizes []int64
	fileCtimes, fileAtimes            []int64
	completePaths                     []string
}

func makeIndexBatchPlan(items []Metadata) indexBatchPlan {
	var plan indexBatchPlan
	seenFiles := make(map[string]struct{})
	for _, item := range items {
		if item.Content == "" {
			plan.skipPaths = append(plan.skipPaths, item.Path)
			plan.skipHashes = append(plan.skipHashes, item.Hash)
			plan.skipMtimes = append(plan.skipMtimes, item.Mtime)
			plan.skipSizes = append(plan.skipSizes, item.Size)
			plan.skipCtimes = append(plan.skipCtimes, item.Ctime)
			plan.skipAtimes = append(plan.skipAtimes, item.Atime)
			continue
		}
		plan.contentItems = append(plan.contentItems, item)
		if item.FileDone {
			plan.completePaths = append(plan.completePaths, item.Path)
		}
		if item.Mtime == 0 && item.Size == 0 && item.Ctime == 0 && item.Atime == 0 {
			continue
		}
		if _, exists := seenFiles[item.Path]; exists {
			continue
		}
		seenFiles[item.Path] = struct{}{}
		plan.filePaths = append(plan.filePaths, item.Path)
		plan.fileHashes = append(plan.fileHashes, item.Hash)
		plan.fileMtimes = append(plan.fileMtimes, item.Mtime)
		plan.fileSizes = append(plan.fileSizes, item.Size)
		plan.fileCtimes = append(plan.fileCtimes, item.Ctime)
		plan.fileAtimes = append(plan.fileAtimes, item.Atime)
	}
	return plan
}

// HandleChunk adds metadata to a channel and flushes when full.
// Contention issue fixed: Mutex removed from hot path as channels are natively thread-safe.
// Flushing now uses a non-blocking select to drain efficiently.
func HandleChunk(chunks chan Metadata, sc *Engine, mode, content, path string, hash, mtime, size, ctime, atime int64) {
	if sc == nil {
		return
	}

	enqueueChunk(chunks, sc, mode, Metadata{
		Content:  content,
		Path:     path,
		Hash:     hash,
		Mtime:    mtime,
		Size:     size,
		Ctime:    ctime,
		Atime:    atime,
		FileDone: true,
	})
}

func enqueueChunk(chunks chan Metadata, sc *Engine, mode string, item Metadata) {
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
	Splitter   *textsplitter.RecursiveCharacter
	Chunks     chan Metadata
	Images     chan Metadata
	Engine     *Engine
	TempDir    string // temp dir for converted images, cleaned up after indexing
	Mu         *sync.Mutex
	Batcher    *VipsBatcher
	IsPathOnly func(string) bool // if non-nil, skip content/embedding for matching paths
	Hardware   HardwareConfig
}

// ProcessImage converts an image to JPEG via vips and queues it for indexing.
func ProcessImage(path string, mtime, size, ctime, atime int64, cfg *ProcessorConfig) {
	if cfg == nil || cfg.Engine == nil {
		return
	}
	hash, err := HashFile(path)
	if err != nil {
		log.Printf("ProcessImage: hash %s: %v", path, err)
		return
	}

	// Add job to batched vips pipeline
	cfg.Batcher.Add(ImageJob{
		Path:  path,
		Hash:  hash,
		MTime: mtime,
		Size:  size,
		CTime: ctime,
		ATime: atime,
	})

}

// ProcessText extracts text from a file and adds chunks to the channel.
func ProcessText(path string, mtime, size, ctime, atime int64, cfg *ProcessorConfig) {
	if cfg == nil || cfg.Engine == nil {
		return
	}
	if IsWhatsAppDatabase(path) {
		return
	}
	hash, err := HashFile(path)
	if err != nil {
		log.Printf("ProcessText: hash %s: %v", path, err)
		return
	}

	// Unsupported file types remain available to path search but have no text vectors.
	ext := strings.ToLower(filepath.Ext(path))
	if !textAllowExtensions[ext] {
		HandleChunk(cfg.Chunks, cfg.Engine, "text", "", path, hash, mtime, size, ctime, atime)
		return
	}
	result, err := kreuzberg.ExtractFileSync(path, nil)
	if err != nil || result == nil || result.Content == "" {
		HandleChunk(cfg.Chunks, cfg.Engine, "text", "", path, hash, mtime, size, ctime, atime)
		return
	}
	splits, err := cfg.Splitter.SplitText(result.Content)
	if err != nil {
		log.Println("Failed to split text:", err)
		return
	}
	if len(splits) == 0 {
		HandleChunk(cfg.Chunks, cfg.Engine, "text", "", path, hash, mtime, size, ctime, atime)
		return
	}

	first := true
	for i, chunk := range splits {
		item := Metadata{Content: chunk, Path: path, FileDone: i == len(splits)-1}
		if first {
			item.Hash, item.Mtime, item.Size, item.Ctime, item.Atime = hash, mtime, size, ctime, atime
			first = false
		}
		enqueueChunk(cfg.Chunks, cfg.Engine, "text", item)
	}
}

// HashFile returns the xxhash of the complete raw file contents.
func HashFile(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	h := xxhash.New()
	if _, err := io.Copy(h, f); err != nil {
		return 0, err
	}
	return int64(h.Sum64()), nil
}

// ProcessFile processes a single file (image or text) based on its type.
func ProcessFile(path string, cfg *ProcessorConfig) {
	if cfg == nil || IsWhatsAppDatabase(path) {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	mtime := info.ModTime().UnixNano()
	size := info.Size()
	ctime, atime := FileExtraTimesFromInfo(info) // no extra syscall

	// Path-only directories: the file is already in paths_fts from Pass 1.
	// Skip content extraction and embedding — just mark as indexed so it
	// never re-appears in UnindexedFiles() on future startups.
	if cfg.IsPathOnly != nil && cfg.IsPathOnly(path) {
		hash, err := HashFile(path)
		if err != nil {
			log.Printf("ProcessFile: hash %s: %v", path, err)
			return
		}
		HandleChunk(cfg.Chunks, cfg.Engine, "text", "", path, hash, mtime, size, ctime, atime)
		return
	}

	if IsOfflineFile(info) {
		// Log to know we're skipping offline/cloud-only files without attempting retrieval.
		log.Printf("ProcessFile: skipping offline/cloud-only file %s", path)
		HandleChunk(cfg.Chunks, cfg.Engine, "text", "", path, empty, mtime, size, ctime, atime)
		return
	}

	ext := strings.ToLower(filepath.Ext(path))

	// Image formats that vips can't handle or have no semantic search value:
	// skip immediately and mark as indexed so they're never retried.
	if _, skip := imageSkipExtensions[ext]; skip {
		hash, err := HashFile(path)
		if err != nil {
			log.Printf("ProcessFile: hash %s: %v", path, err)
			return
		}
		HandleChunk(cfg.Chunks, cfg.Engine, "text", "", path, hash, mtime, size, ctime, atime)
		return
	}
	if IsImageFile(path) {
		ProcessImage(path, mtime, size, ctime, atime, cfg)
	} else {
		ProcessText(path, mtime, size, ctime, atime, cfg)
	}
}

// ProcessDirectory indexes a directory path for path/filename search.
func ProcessDirectory(path string, cfg *ProcessorConfig) {
	if cfg == nil || cfg.Engine == nil {
		return
	}
	HandleChunk(cfg.Chunks, cfg.Engine, "text", "", path, empty, 0, 0, 0, 0)
}

// NewProcessorConfig creates a new ProcessorConfig with default settings.
// Creates a temp directory for image conversions; caller must call CleanupTempDir() when done.
// InitIndexTables is called at most once per Engine instance via sync.Once (IX-4 fix).
func NewProcessorConfig(chunkSize, chunkCap, imageCap int, sc *Engine, hw HardwareConfig) (*ProcessorConfig, error) {
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
		o.ChunkOverlap = chunkSize / 5 // 20% overlap
	})
	tmpDir, err := os.MkdirTemp("", "filosophy-img-*")
	if err != nil {
		log.Println("Failed to create temp dir for images:", err)
	}
	cfg := &ProcessorConfig{
		Splitter: &splitter,
		Chunks:   make(chan Metadata, chunkCap),
		Images:   make(chan Metadata, imageCap),
		Engine:   sc,
		TempDir:  tmpDir,
		Mu:       &sync.Mutex{},
		Hardware: hw,
	}
	cfg.Batcher = NewVipsBatcher(cfg, imageCap)
	return cfg, nil
}

// Flush forces pending image and text batches through the indexing pipeline.
func (cfg *ProcessorConfig) Flush() {
	if cfg != nil && cfg.Batcher != nil {
		cfg.Batcher.Flush()
	}
}

// CleanupTempDir removes the temp directory used for image conversions.
func (cfg *ProcessorConfig) CleanupTempDir() {
	if cfg != nil && cfg.TempDir != "" {
		os.RemoveAll(cfg.TempDir)
	}
}
