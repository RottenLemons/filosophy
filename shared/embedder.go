package shared

// StaticEmbedder bypasses ONNX entirely for the static embedding model.
// The model.safetensors file contains a single [vocab_size × dim] float32
// matrix. Embedding a string is:
//   1. Tokenise with the same tokenizer.json used everywhere else.
//   2. Look up each token ID as a row in the matrix.
//   3. Average the rows element-wise.
//   4. L2-normalise so cosine similarity == dot product.

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"

	"github.com/daulet/tokenizers"
)

// StaticEmbedder holds the embedding matrix and tokenizer.
type StaticEmbedder struct {
	matrix [][]float32       // matrix[tokenID] = float32 vector of length dim
	tok    *tokenizers.Tokenizer
	dim    int
}

// safetensorsHeader is the JSON structure at the start of a .safetensors file.
// We only need each tensor's dtype, shape, and byte offsets.
type safetensorsHeader map[string]struct {
	Dtype       string  `json:"dtype"`
	Shape       []int64 `json:"shape"`
	DataOffsets [2]int64 `json:"data_offsets"`
}

// LoadStaticEmbedder loads the embedding matrix from modelPath (a .safetensors
// file) and the tokenizer from tokenizerPath (tokenizer.json).
func LoadStaticEmbedder(modelPath, tokenizerPath string) (*StaticEmbedder, error) {
	tok, err := tokenizers.FromFile(tokenizerPath)
	if err != nil {
		return nil, fmt.Errorf("LoadStaticEmbedder: tokenizer: %w", err)
	}

	matrix, dim, err := loadSafetensors(modelPath)
	if err != nil {
		tok.Close()
		return nil, fmt.Errorf("LoadStaticEmbedder: %w", err)
	}

	return &StaticEmbedder{matrix: matrix, tok: tok, dim: dim}, nil
}

// loadSafetensors parses a .safetensors file and returns the embedding matrix.
// Format: [uint64 header_len][header_len bytes JSON][raw float32 tensor data]
// Offsets in DataOffsets are relative to the start of the data section.
func loadSafetensors(path string) ([][]float32, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	// Read the 8-byte little-endian header length.
	var headerLen uint64
	if err := binary.Read(f, binary.LittleEndian, &headerLen); err != nil {
		return nil, 0, fmt.Errorf("read header length: %w", err)
	}

	// Read and parse the JSON header.
	headerBytes := make([]byte, headerLen)
	if _, err := io.ReadFull(f, headerBytes); err != nil {
		return nil, 0, fmt.Errorf("read header: %w", err)
	}
	var header safetensorsHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, 0, fmt.Errorf("parse header: %w", err)
	}

	// Log all tensors found for diagnostics.
	for name, meta := range header {
		if name == "__metadata__" {
			continue
		}
		log.Printf("safetensors tensor: %q dtype=%s shape=%v", name, meta.Dtype, meta.Shape)
	}

	// Find the embedding tensor: prefer known names, then any 2-D F32 or F16.
	tensorName := ""
	for _, candidate := range []string{"embedding.weight", "0.embedding.weight", "weight", "embeddings"} {
		if _, ok := header[candidate]; ok {
			tensorName = candidate
			break
		}
	}
	if tensorName == "" {
		// Fall back to the first 2-D tensor regardless of dtype.
		for name, meta := range header {
			if name == "__metadata__" {
				continue
			}
			if len(meta.Shape) == 2 {
				tensorName = name
				break
			}
		}
	}
	if tensorName == "" {
		return nil, 0, fmt.Errorf("no 2-D embedding tensor found in %q", path)
	}
	log.Printf("safetensors: using tensor %q", tensorName)

	meta := header[tensorName]
	if meta.Dtype != "F32" && meta.Dtype != "F16" && meta.Dtype != "BF16" {
		return nil, 0, fmt.Errorf("tensor %q has unsupported dtype %q (need F32/F16/BF16)", tensorName, meta.Dtype)
	}
	if len(meta.Shape) != 2 {
		return nil, 0, fmt.Errorf("tensor %q has %d dimensions, expected 2", tensorName, len(meta.Shape))
	}

	vocabSize := int(meta.Shape[0])
	dim := int(meta.Shape[1])
	byteStart := meta.DataOffsets[0]
	byteEnd := meta.DataOffsets[1]
	expectedBytes := int64(vocabSize) * int64(dim) * 4
	if byteEnd-byteStart != expectedBytes {
		return nil, 0, fmt.Errorf("tensor %q: unexpected byte range %d..%d (expected %d bytes)",
			tensorName, byteStart, byteEnd, expectedBytes)
	}

	// Seek to the tensor data (data section starts right after the JSON header).
	dataOffset := int64(8) + int64(headerLen) + byteStart
	if _, err := f.Seek(dataOffset, io.SeekStart); err != nil {
		return nil, 0, fmt.Errorf("seek to tensor data: %w", err)
	}

	// Read all float32 values in one shot.
	totalFloats := vocabSize * dim
	raw := make([]byte, totalFloats*4)
	if _, err := io.ReadFull(f, raw); err != nil {
		return nil, 0, fmt.Errorf("read tensor data: %w", err)
	}

	// Convert raw bytes (little-endian float32) into [][]float32 rows.
	matrix := make([][]float32, vocabSize)
	for i := range matrix {
		row := make([]float32, dim)
		base := i * dim * 4
		for j := range row {
			bits := binary.LittleEndian.Uint32(raw[base+j*4 : base+j*4+4])
			row[j] = math.Float32frombits(bits)
		}
		matrix[i] = row
	}

	return matrix, dim, nil
}

// Close releases tokenizer resources.
func (e *StaticEmbedder) Close() error { return e.tok.Close() }

// Dim returns the embedding dimension.
func (e *StaticEmbedder) Dim() int { return e.dim }

// EmbedString returns the L2-normalised mean embedding for text.
// Token IDs outside the matrix range are silently skipped.
func (e *StaticEmbedder) EmbedString(text string) ([]float32, error) {
	ids, _ := e.tok.Encode(text, false)
	if len(ids) == 0 {
		// Empty or whitespace-only input: return a zero vector so the chunk
		// gets stored but scores zero similarity against every query.
		return make([]float32, e.dim), nil
	}

	acc := make([]float32, e.dim)
	found := 0

	for _, id := range ids {
		idx := int(id)
		if idx < 0 || idx >= len(e.matrix) {
			continue // out-of-range token ID: skip
		}
		vec := e.matrix[idx]
		// Element-wise accumulation.
		for i, v := range vec {
			acc[i] += v
		}
		found++
	}

	if found == 0 {
		// All tokens were OOV: return zero vector (no similarity with any query).
		return make([]float32, e.dim), nil
	}

	// Divide by count → mean vector.
	inv := float32(1.0 / float64(found))
	for i := range acc {
		acc[i] *= inv
	}

	// L2-normalise → unit vector.
	return staticNormalize(acc), nil
}

// staticNormalize returns the L2-normalised copy of vec.
// A zero vector is returned unchanged.
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
