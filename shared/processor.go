// Copyright (C) 2025 Filosophy
package shared

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
	"github.com/cespare/xxhash"
	kreuzberg "github.com/kreuzberg-dev/kreuzberg/packages/go/v4"
	"github.com/tmc/langchaingo/textsplitter"
)

// vipsThumbnailPath returns the path to the bundled vipsthumbnail.exe.
// It first checks relative to the executable (for production), then falls
// back to the working directory (for development / go run).
func vipsThumbnailPath() string {
	var rel = filepath.FromSlash("vips-dev-8.18/bin/vipsthumbnail.exe")

	// Try relative to the executable first (production layout)
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), rel)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Fall back to working directory (development / go run)
	if wd, err := os.Getwd(); err == nil {
		p := filepath.Join(wd, rel)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Last resort: rely on PATH
	return "vipsthumbnail"
}

// Send connects to the named pipe, writes {"task":…,"data":…}, and returns the response.
func Send(data interface{}, task string) (string, error) {
	pipePath := `\\.\pipe\test`
	f, err := winio.DialPipe(pipePath, nil)
	if err != nil {
		return "", fmt.Errorf("error opening pipe: %w", err)
	}
	defer f.Close()
	msg := fmt.Sprintf(`{"task": %q,"data": %s}`, task, data)
	if _, err := f.Write([]byte(msg)); err != nil {
		return "", fmt.Errorf("write error: %w", err)
	}
	chunk := make([]byte, 1024)
	if _, err := f.Read(chunk); err != nil {
		return "", fmt.Errorf("read error: %w", err)
	}
	return string(chunk), nil
}

// Metadata holds content and path information for indexing
type Metadata struct {
	Content, Path string
	Hash          int64
}

// DataType represents a batch of data to send
type DataType struct {
	Data []byte
	Mode string
}

var imageExtensions = map[string]struct{}{
	".jpg":   {},
	".jpeg":  {},
	".png":   {},
	".gif":   {},
	".bmp":   {},
	".tiff":  {},
	".tif":   {},
	".heic":  {},
	".heif":  {},
	".ico":   {},
	".avif":  {},
	".jfif":  {},
	".pjpeg": {},
	".pjp":   {},
	".svg":   {},
}

var empty int64 = int64(xxhash.Sum64String(""))

// IsImageFile checks if the filename has an image extension
func IsImageFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	_, ok := imageExtensions[ext]
	return ok
}

// SendBatch marshals and sends a batch of metadata items to the send queue
func SendBatch(items []Metadata, mode string, sendQueue chan DataType) {
	if len(items) == 0 {
		return
	}
	dataMap := make(map[string]interface{})
	contents := make([]string, 0, len(items))
	paths := make([]string, 0, len(items))
	for _, item := range items {
		contents = append(contents, item.Content)
		paths = append(paths, item.Path)
	}
	dataMap["content"] = contents
	dataMap["path"] = paths

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
	if len(filePaths) > 0 {
		dataMap["file_path"] = filePaths
		dataMap["file_hash"] = fileHashes
	}

	data, _ := json.Marshal(dataMap)
	sendQueue <- DataType{data, mode}
}

// HandleChunk adds metadata to a channel and flushes when full
func HandleChunk(chunks chan Metadata, sendQueue chan DataType, mode, content, path string, hash int64) {
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
		SendBatch(items, mode, sendQueue)
	}
}

// DrainRemaining drains all remaining items from a channel and sends them
func DrainRemaining(chunks chan Metadata, mode string, sendQueue chan DataType) {
	var items []Metadata
	for len(chunks) > 0 {
		items = append(items, <-chunks)
	}
	SendBatch(items, mode, sendQueue)
}

// ProcessorConfig holds configuration for file processing
type ProcessorConfig struct {
	Splitter  *textsplitter.RecursiveCharacter
	Chunks    chan Metadata
	Images    chan Metadata
	SendQueue chan DataType
}

// ProcessImage processes an image file using the bundled vipsthumbnail CLI
// and adds the base64-encoded PNG to the images channel.
func ProcessImage(path string, cfg *ProcessorConfig) {
	imBytes, err := os.ReadFile(path)
	if err != nil {
		log.Println("Failed to read image:", err)
		return
	}
	hash := int64(xxhash.Sum64(imBytes))
	imBytes = nil // Only needed for hashing

	tmpFile, err := os.CreateTemp("", "vips-*.png")
	if err != nil {
		log.Println("Failed to create temp file:", err)
		return
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// vipsthumbnail resizes to fit within 256x256, auto-detects input format
	cmd := exec.Command(vipsThumbnailPath(), path, "-s", "256", "-o", tmpPath+"[strip]")
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("vipsthumbnail failed for %s: %v\n%s", path, err, string(output))
		return
	}

	pngBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		log.Println("Failed to read vips output:", err)
		return
	}

	imString := base64.StdEncoding.EncodeToString(pngBytes)
	HandleChunk(cfg.Images, cfg.SendQueue, "image", imString, path, hash)
}

// ProcessText extracts text from a file and adds chunks to the channel
func ProcessText(path string, cfg *ProcessorConfig) {
	result, err := kreuzberg.ExtractFileSync(path, nil)
	if err != nil || result == nil || result.Content == "" {
		HandleChunk(cfg.Chunks, cfg.SendQueue, "text", "", path, empty)
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
			HandleChunk(cfg.Chunks, cfg.SendQueue, "text", chunk, path, hash)
			first = false
		} else {
			HandleChunk(cfg.Chunks, cfg.SendQueue, "text", chunk, path, empty)
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
	HandleChunk(cfg.Chunks, cfg.SendQueue, "text", "", path, empty)
}

// NewProcessorConfig creates a new ProcessorConfig with default settings
func NewProcessorConfig(chunkSize, chunkCap, imageCap, sendQueueCap int) *ProcessorConfig {
	splitter := textsplitter.NewRecursiveCharacter(func(o *textsplitter.Options) {
		o.ChunkSize = chunkSize
	})
	return &ProcessorConfig{
		Splitter:  &splitter,
		Chunks:    make(chan Metadata, chunkCap),
		Images:    make(chan Metadata, imageCap),
		SendQueue: make(chan DataType, sendQueueCap),
	}
}
