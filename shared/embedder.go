package shared

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"syscall"
	"unsafe"

	"github.com/daulet/tokenizers"
)

// StaticEmbedder holds the embedding matrix and tokenizer.
// It uses memory-mapped I/O for zero-copy access to the matrix.
type StaticEmbedder struct {
	matrix []float32 // zero-copy view into the mmap'd file
	tok    *tokenizers.Tokenizer
	dim    int
	// Windows-specific handles for unmapping
	handle syscall.Handle
	ptr    uintptr
}

// safetensorsHeader is the JSON structure at the start of a .safetensors file.
type safetensorsHeader map[string]struct {
	Dtype       string  `json:"dtype"`
	Shape       []int64 `json:"shape"`
	DataOffsets [2]int64 `json:"data_offsets"`
}

// LoadStaticEmbedder loads the embedding matrix from modelPath (a .safetensors
// file) using mmap for zero-copy loading, and the tokenizer from tokenizerPath.
func LoadStaticEmbedder(modelPath, tokenizerPath string) (*StaticEmbedder, error) {
	tok, err := tokenizers.FromFile(tokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("LoadStaticEmbedder: tokenizer: %w", err)
	}

	f, err := os.Open(modelPath)
	if err != nil {
		tok.Close()
		return nil, fmt.Errorf("open %q: %w", modelPath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		tok.Close()
		return nil, fmt.Errorf("stat %q: %w", modelPath, err)
	}
	size := info.Size()

	// 1. Memory-map the entire file (Windows-specific native implementation)
	// Create a file mapping object
	hMapping, err := syscall.CreateFileMapping(syscall.Handle(f.Fd()), nil, syscall.PAGE_READONLY, 0, 0, nil)
	if err != nil {
		tok.Close()
		return nil, fmt.Errorf("CreateFileMapping: %w", err)
	}

	// Map the view into virtual memory
	ptr, err := syscall.MapViewOfFile(hMapping, syscall.FILE_MAP_READ, 0, 0, 0)
	if err != nil {
		syscall.CloseHandle(hMapping)
		tok.Close()
		return nil, fmt.Errorf("MapViewOfFile: %w", err)
	}

	// 2. Parse the header (always 8 bytes for length + JSON)
	// We can access the memory directly via unsafe pointer
	headerLenBuf := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), 8)
	headerLen := binary.LittleEndian.Uint64(headerLenBuf)

	headerJSONBuf := unsafe.Slice((*byte)(unsafe.Pointer(ptr+8)), headerLen)
	var header safetensorsHeader
	if err := json.Unmarshal(headerJSONBuf, &header); err != nil {
		syscall.UnmapViewOfFile(ptr)
		syscall.CloseHandle(hMapping)
		tok.Close()
		return nil, fmt.Errorf("parse safetensors header: %w", err)
	}

	// 3. Find the embedding tensor
	tensorName := ""
	for _, candidate := range []string{"embedding.weight", "0.embedding.weight", "weight", "embeddings"} {
		if _, ok := header[candidate]; ok {
			tensorName = candidate
			break
		}
	}
	if tensorName == "" {
		for name, meta := range header {
			if name != "__metadata__" && len(meta.Shape) == 2 {
				tensorName = name
				break
			}
		}
	}
	if tensorName == "" {
		syscall.UnmapViewOfFile(ptr)
		syscall.CloseHandle(hMapping)
		tok.Close()
		return nil, fmt.Errorf("no embedding tensor found")
	}

	meta := header[tensorName]
	vocabSize := int(meta.Shape[0])
	dim := int(meta.Shape[1])
	totalFloats := vocabSize * dim

	// 4. Zero-copy slicing into the data section
	// Data section starts after 8 bytes + headerLen
	dataStart := 8 + int64(headerLen) + meta.DataOffsets[0]
	dataEnd := 8 + int64(headerLen) + meta.DataOffsets[1]

	if dataEnd > size {
		syscall.UnmapViewOfFile(ptr)
		syscall.CloseHandle(hMapping)
		tok.Close()
		return nil, fmt.Errorf("tensor data range out of bounds")
	}

	// 5. Direct cast: map the byte range to a float32 slice
	// This only works if the system is Little Endian (Safetensors default).
	// Modern Windows/Intel/AMD CPUs are Little Endian.
	matrixPtr := ptr + uintptr(dataStart)
	matrix := unsafe.Slice((*float32)(unsafe.Pointer(matrixPtr)), totalFloats)

	log.Printf("mmap: loaded %q (%d dimensions) zero-copy from %s", tensorName, dim, modelPath)

	return &StaticEmbedder{
		matrix: matrix,
		tok:    tok,
		dim:    dim,
		handle: hMapping,
		ptr:    ptr,
	}, nil
}

// Close releases virtual memory mapping and tokenizer resources.
func (e *StaticEmbedder) Close() error {
	var errs []error
	if e.ptr != 0 {
		if err := syscall.UnmapViewOfFile(e.ptr); err != nil {
			errs = append(errs, fmt.Errorf("UnmapViewOfFile: %w", err))
		}
	}
	if e.handle != 0 {
		if err := syscall.CloseHandle(e.handle); err != nil {
			errs = append(errs, fmt.Errorf("CloseHandle: %w", err))
		}
	}
	if e.tok != nil {
		e.tok.Close()
	}
	if len(errs) > 0 {
		return fmt.Errorf("mmap close errors: %v", errs)
	}
	return nil
}

// Dim returns the embedding dimension.
func (e *StaticEmbedder) Dim() int { return e.dim }

// EmbedString returns the L2-normalised mean embedding for text.
// Now extremely fast as adding rows uses the zero-copy mmap'd matrix.
func (e *StaticEmbedder) EmbedString(text string) ([]float32, error) {
	ids, _ := e.tok.Encode(text, false)
	if len(ids) == 0 {
		return make([]float32, e.dim), nil
	}

	acc := make([]float32, e.dim)
	found := 0

	for _, id := range ids {
		idx := int(id)
		if idx < 0 || idx >= len(e.matrix)/e.dim {
			continue
		}
		// Direct memory access via the mmap'd view
		vec := e.matrix[idx*e.dim : (idx+1)*e.dim]
		for i, v := range vec {
			acc[i] += v
		}
		found++
	}

	if found == 0 {
		return make([]float32, e.dim), nil
	}

	inv := float32(1.0 / float64(found))
	for i := range acc {
		acc[i] *= inv
	}

	return staticNormalize(acc), nil
}

func staticNormalize(vec []float32) []float32 {
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v) * float64(v)
	}
	if sumSq == 0 {
		return vec
	}
	norm := float32(math.Sqrt(sumSq))
	out := make([]float32, len(vec))
	for i, v := range vec {
		out[i] = v / norm
	}
	return out
}
