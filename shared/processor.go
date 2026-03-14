// Copyright (C) 2025 Filosophy
package shared

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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

// Metadata holds content and path information for indexing
type Metadata struct {
	Content, Path string
	Hash          int64
}

var imageExtensions = map[string]struct{}{
	".jpg": {}, ".jpeg": {}, ".png": {}, ".gif": {},
	".bmp": {}, ".tiff": {}, ".tif": {},
	".heic": {}, ".heif": {}, ".ico": {},
	".avif": {}, ".jfif": {}, ".pjpeg": {},
	".pjp": {}, ".webp": {},
}

var empty int64 = int64(xxhash.Sum64String(""))

// IsImageFile checks if the filename has an image extension
func IsImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	_, ok := imageExtensions[ext]
	return ok
}

// IndexBatch directly indexes a batch of metadata items using the Engine.
func IndexBatch(items []Metadata, mode string, sc *Engine) {
	if len(items) == 0 {
		return
	}
	contents := make([]string, 0, len(items))
	paths := make([]string, 0, len(items))
	for _, item := range items {
		contents = append(contents, item.Content)
		paths = append(paths, item.Path)
	}

	// Deduplicate file-level hashes by path
	var filePaths []string
	var fileHashes []int64
	for _, item := range items {
		if item.Hash == empty {
			continue
		}
		filePaths = append(filePaths, item.Path)
		fileHashes = append(fileHashes, item.Hash)
	}

	var err error
	if mode == "image" {
		err = sc.IndexImage(contents, paths, filePaths, fileHashes)
	} else {
		err = sc.IndexText(contents, paths, filePaths, fileHashes)
	}
	if err != nil {
		log.Printf("IndexBatch %s error: %v", mode, err)
	}

	// Index unique paths in FTS5 for path keyword search
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

// HandleChunk adds metadata to a channel and flushes when full
func HandleChunk(chunks chan Metadata, sc *Engine, mode, content, path string, hash int64, mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	select {
	case chunks <- Metadata{content, path, hash}:
	default:
		var items []Metadata
		for j := 0; j < cap(chunks); j++ {
			select {
			case tmp := <-chunks:
				items = append(items, tmp)
			default:
			}
		}
		chunks <- Metadata{content, path, hash}
		// IndexBatch shouldn't be under the same lock if we want to add more to channel
		// but since we are limiting concurrency via maxConcurrency semaphore anyway,
		// guarding the channel drain is safer.
		IndexBatch(items, mode, sc)
	}
}

// DrainRemaining drains all remaining items from a channel and indexes them
func DrainRemaining(chunks chan Metadata, mode string, sc *Engine) {
	var items []Metadata
	for len(chunks) > 0 {
		items = append(items, <-chunks)
	}
	IndexBatch(items, mode, sc)
}

// ProcessorConfig holds configuration for file processing
type ProcessorConfig struct {
	Splitter *textsplitter.RecursiveCharacter
	Chunks   chan Metadata
	Images   chan Metadata
	Engine   *Engine
	TempDir  string // temp dir for converted images, cleaned up after indexing
	Mu       *sync.Mutex
}

// ProcessImage converts an image to PNG via vips, hashes the original raw bytes,
// and queues it for indexing. The converted PNG path is passed as "content"
// so hugot's RunWithImagePaths can load it.
func ProcessImage(path string, cfg *ProcessorConfig) {
	// Hash original raw bytes for dedup
	imBytes, err := os.ReadFile(path)
	if err != nil {
		log.Println("Failed to read image:", err)
		return
	}
	hash := int64(xxhash.Sum64(imBytes))
	imBytes = nil

	// Convert to JPEG via vips (small temp, handles all formats: HEIC, AVIF, WebP, etc.)
	outPath := filepath.Join(cfg.TempDir, fmt.Sprintf("%d.jpg", hash))
	cmd := exec.Command(vipsThumbnailPath(), path, "-s", "256x256!", "-o", outPath+"[Q=80,strip]")
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("vipsthumbnail failed for %s: %v\n%s", path, err, string(output))
		return
	}

	// Content = converted JPEG path for hugot, Path = original path for metadata
	HandleChunk(cfg.Images, cfg.Engine, "image", outPath, path, hash, cfg.Mu)
}

// ProcessText extracts text from a file and adds chunks to the channel
func ProcessText(path string, cfg *ProcessorConfig) {
	result, err := kreuzberg.ExtractFileSync(path, nil)
<<<<<<< HEAD
	if err != nil || result == nil || result.Content == "" {
		HandleChunk(cfg.Chunks, cfg.SendQueue, "text", "", path, empty)
=======
	if err != nil || result.Content == "" {
		HandleChunk(cfg.Chunks, cfg.Engine, "text", "", path, empty, cfg.Mu)
>>>>>>> main
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
			HandleChunk(cfg.Chunks, cfg.Engine, "text", chunk, path, hash, cfg.Mu)
			first = false
		} else {
			HandleChunk(cfg.Chunks, cfg.Engine, "text", chunk, path, empty, cfg.Mu)
		}
	}
}

// ProcessFile processes a single file (image or text) based on its type
func ProcessFile(path string, cfg *ProcessorConfig) {
	if IsImageFile(path) {
		ProcessImage(path, cfg)
	} else {
		ProcessText(path, cfg)
	}
}

// ProcessDirectory adds directory path to the chunks channel
func ProcessDirectory(path string, cfg *ProcessorConfig) {
	HandleChunk(cfg.Chunks, cfg.Engine, "text", "", path, empty, cfg.Mu)
}

// NewProcessorConfig creates a new ProcessorConfig with default settings.
// Creates a temp directory for image conversions; caller must call CleanupTempDir() when done.
func NewProcessorConfig(chunkSize, chunkCap, imageCap int, sc *Engine) *ProcessorConfig {
	if err := sc.InitIndexTables(); err != nil {
		log.Printf("Failed to initialize index tables: %v", err)
	}

	splitter := textsplitter.NewRecursiveCharacter(func(o *textsplitter.Options) {
		o.ChunkSize = chunkSize
	})
	tmpDir, err := os.MkdirTemp("", "filosophy-img-*")
	if err != nil {
		log.Println("Failed to create temp dir for images:", err)
	}
	// Cap the image capacity high enough batching is effective
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
	}
}

// CleanupTempDir removes the temp directory used for image conversions.
func (cfg *ProcessorConfig) CleanupTempDir() {
	if cfg.TempDir != "" {
		os.RemoveAll(cfg.TempDir)
	}
}
