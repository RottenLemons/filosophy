package shared

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
	Dtype       string   `json:"dtype"`
	Shape       []int64  `json:"shape"`
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

	releaseMapping := func() {
		_ = syscall.UnmapViewOfFile(ptr)
		_ = syscall.CloseHandle(hMapping)
		tok.Close()
	}
	if size < 8 {
		releaseMapping()
		return nil, fmt.Errorf("safetensors file is shorter than its header")
	}

	// 2. Parse the 8-byte header length and validate it before slicing the mapping.
	headerLenBuf := unsafe.Slice((*byte)(unsafe.Pointer(ptr)), 8)
	headerLen := binary.LittleEndian.Uint64(headerLenBuf)
	if headerLen > uint64(size-8) {
		releaseMapping()
		return nil, fmt.Errorf("safetensors header length exceeds file size")
	}
	headerJSONBuf := unsafe.Slice((*byte)(unsafe.Pointer(ptr+8)), int(headerLen))
	var header safetensorsHeader
	if err := json.Unmarshal(headerJSONBuf, &header); err != nil {
		releaseMapping()
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
		releaseMapping()
		return nil, fmt.Errorf("no embedding tensor found")
	}

	meta := header[tensorName]
	if len(meta.Shape) != 2 || meta.Shape[0] <= 0 || meta.Shape[1] <= 0 || meta.Shape[0] > int64(^uint(0)>>1)/meta.Shape[1] {
		releaseMapping()
		return nil, fmt.Errorf("embedding tensor must have a valid 2D shape")
	}
	vocabSize, dim := int(meta.Shape[0]), int(meta.Shape[1])
	totalFloats := vocabSize * dim
	if meta.DataOffsets[0] < 0 || meta.DataOffsets[1] < meta.DataOffsets[0] {
		releaseMapping()
		return nil, fmt.Errorf("invalid tensor data offsets")
	}
	payloadSize := size - 8 - int64(headerLen)
	if meta.DataOffsets[1] > payloadSize {
		releaseMapping()
		return nil, fmt.Errorf("tensor data range out of bounds")
	}

	dtype := strings.ToUpper(meta.Dtype)
	bytesPerValue := int64(0)
	switch dtype {
	case "F32":
		bytesPerValue = 4
	case "F16", "BF16":
		bytesPerValue = 2
	default:
		releaseMapping()
		return nil, fmt.Errorf("unsupported embedding dtype %q", meta.Dtype)
	}
	if int64(totalFloats) > payloadSize/bytesPerValue {
		releaseMapping()
		return nil, fmt.Errorf("tensor shape exceeds available data")
	}
	expectedBytes := int64(totalFloats) * bytesPerValue
	if meta.DataOffsets[1]-meta.DataOffsets[0] != expectedBytes {
		releaseMapping()
		return nil, fmt.Errorf("tensor data size does not match its shape and dtype")
	}

	dataStart := 8 + int64(headerLen) + meta.DataOffsets[0]
	dataPtr := ptr + uintptr(dataStart)
	var matrix []float32
	if dtype == "F32" {
		matrix = unsafe.Slice((*float32)(unsafe.Pointer(dataPtr)), totalFloats)
	} else {
		data := unsafe.Slice((*byte)(unsafe.Pointer(dataPtr)), totalFloats*2)
		matrix, err = decode16BitWeights(data, dtype)
		if err != nil {
			releaseMapping()
			return nil, err
		}
	}

	if dtype == "F32" {
		log.Printf("mmap: loaded %q (%d dimensions) zero-copy from %s", tensorName, dim, modelPath)
	} else {
		log.Printf("mmap: loaded %q (%d dimensions) converted from %s in %s", tensorName, dim, dtype, modelPath)
	}

	return &StaticEmbedder{
		matrix: matrix,
		tok:    tok,
		dim:    dim,
		handle: hMapping,
		ptr:    ptr,
	}, nil
}

func decode16BitWeights(data []byte, dtype string) ([]float32, error) {
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("16-bit tensor data has an odd byte length")
	}
	dtype = strings.ToUpper(dtype)
	if dtype != "F16" && dtype != "BF16" {
		return nil, fmt.Errorf("unsupported 16-bit tensor dtype %q", dtype)
	}

	values := make([]float32, len(data)/2)
	for i := range values {
		bits := binary.LittleEndian.Uint16(data[i*2:])
		if dtype == "BF16" {
			values[i] = math.Float32frombits(uint32(bits) << 16)
		} else {
			values[i] = float16ToFloat32(bits)
		}
	}
	return values, nil
}

func float16ToFloat32(value uint16) float32 {
	sign := uint32(value&0x8000) << 16
	exponent := (value >> 10) & 0x1f
	mantissa := uint32(value & 0x03ff)
	var bits uint32

	switch exponent {
	case 0:
		if mantissa == 0 {
			bits = sign
			break
		}
		exponent32 := uint32(127 - 15 + 1)
		for mantissa&0x0400 == 0 {
			mantissa <<= 1
			exponent32--
		}
		mantissa &= 0x03ff
		bits = sign | exponent32<<23 | mantissa<<13
	case 0x1f:
		bits = sign | 0x7f800000 | mantissa<<13
	default:
		bits = sign | (uint32(exponent)+(127-15))<<23 | mantissa<<13
	}
	return math.Float32frombits(bits)
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
	return e.EmbedTruncated(text, e.dim), nil
}

// EmbedTruncated returns the L2-normalised mean embedding of text using only
// the first outDim dimensions (Matryoshka truncation). Truncating before the
// mean is mathematically identical to truncating after it, but only touches
// outDim columns per token, which makes it several times cheaper.
func (e *StaticEmbedder) EmbedTruncated(text string, outDim int) []float32 {
	if outDim <= 0 || outDim > e.dim {
		outDim = e.dim
	}
	acc := make([]float32, outDim)
	ids, _ := e.tok.Encode(text, false)
	if len(ids) == 0 {
		return acc
	}

	vocab := len(e.matrix) / e.dim
	found := 0
	for _, id := range ids {
		idx := int(id)
		if idx < 0 || idx >= vocab {
			continue
		}
		// Direct memory access via the mmap'd view
		vec := e.matrix[idx*e.dim : idx*e.dim+outDim]
		for i, v := range vec {
			acc[i] += v
		}
		found++
	}
	if found == 0 {
		return acc
	}
	// The mean's 1/found factor cancels out under L2 normalisation.
	return staticNormalize(acc)
}

// EmbedBatch embeds texts concurrently using EmbedTruncated.
func (e *StaticEmbedder) EmbedBatch(texts []string, outDim int) [][]float32 {
	out := make([][]float32, len(texts))
	if len(texts) == 0 {
		return out
	}
	workers := runtime.NumCPU()
	if workers > len(texts) {
		workers = len(texts)
	}
	if workers <= 1 {
		for i, t := range texts {
			out[i] = e.EmbedTruncated(t, outDim)
		}
		return out
	}
	var next atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1) - 1)
				if i >= len(texts) {
					return
				}
				out[i] = e.EmbedTruncated(texts[i], outDim)
			}
		}()
	}
	wg.Wait()
	return out
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
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

