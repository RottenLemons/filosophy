package shared

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"

	vips "github.com/cshum/vipsgen/vips817"
	kreuzberg "github.com/kreuzberg-dev/kreuzberg/packages/go/v4"
	"github.com/tmc/langchaingo/textsplitter"
)

// Metadata holds content and path information for indexing
type Metadata struct {
	Content, Path string
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
}

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
	dataMap := make(map[string][]string)
	for _, item := range items {
		dataMap["content"] = append(dataMap["content"], item.Content)
		dataMap["path"] = append(dataMap["path"], item.Path)
	}
	data, _ := json.Marshal(dataMap)
	sendQueue <- DataType{data, mode}
}

// HandleChunk adds metadata to a channel and flushes when full
func HandleChunk(chunks chan Metadata, sendQueue chan DataType, mode, content, path string) {
	select {
	case chunks <- Metadata{content, path}:
	default:
		var items []Metadata
		for j := 0; j < cap(chunks); j++ {
			select {
			case tmp := <-chunks:
				items = append(items, tmp)
			default:
			}
		}
		chunks <- Metadata{content, path}
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

// ProcessImage processes an image file and adds it to the images channel
func ProcessImage(path string, cfg *ProcessorConfig) {
	imBytes, err := os.ReadFile(path)
	if err != nil {
		log.Println("Failed to read image:", err)
		return
	}
	vImg, err := vips.NewThumbnailBuffer(imBytes, 256, &vips.ThumbnailBufferOptions{
		Height: 256,
		FailOn: vips.FailOnError,
	})
	imBytes = nil // Free original image bytes
	if err != nil {
		log.Println("Failed to load image:", err)
		return
	}
	pngBytes, err := vImg.PngsaveBuffer(nil)
	defer vImg.Close()
	if err != nil {
		log.Println("Failed to save image as buffer:", err)
		return
	}
	imString := base64.StdEncoding.EncodeToString(pngBytes)
	pngBytes = nil // Free PNG bytes after encoding
	HandleChunk(cfg.Images, cfg.SendQueue, "image", imString, path)
}

// ProcessText extracts text from a file and adds chunks to the channel
func ProcessText(path string, cfg *ProcessorConfig) {
	result, err := kreuzberg.ExtractFileSync(path, nil)
	if err != nil {
		HandleChunk(cfg.Chunks, cfg.SendQueue, "text", "", path)
		return
	}

	splits, err := cfg.Splitter.SplitText(result.Content)
	if err != nil {
		log.Println("Failed to split text:", err)
		return
	}

	for _, chunk := range splits {
		HandleChunk(cfg.Chunks, cfg.SendQueue, "text", chunk, path)
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
	HandleChunk(cfg.Chunks, cfg.SendQueue, "text", "", path)
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
