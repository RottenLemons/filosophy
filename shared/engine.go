// Package Engine provides embedding generation and vector search functionality
// using onnxruntime_go (ONNX inference) and sqvect (SQLite vector DB).
// It replaces the Python Engine.py with a native Go library.
package shared

/*
#cgo LDFLAGS: -L${SRCDIR}/.. -ltokenizers -lws2_32 -lntdll -luserenv -lbcrypt -ladvapi32
*/
import "C"

import (
	"context"
	"database/sql"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/daulet/tokenizers"
	"github.com/google/uuid"
	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/sqvect"
	ort "github.com/yalue/onnxruntime_go"
	_ "golang.org/x/image/webp"
	_ "modernc.org/sqlite"
)

const (
	textCollection  = "text"
	imageCollection = "image"

	textEmbedDim  = 256 // truncated from model's native 1024
	imageEmbedDim = 256 // truncated from CLIP's 512
	clipEmbedDim  = 512 // CLIP native output dimension
	clipCtxLen    = 77  // CLIP text context_length
	visionSize    = 256 // CLIP vision input size
)

// CLIP image normalization constants (OpenAI CLIP standard).
var (
	clipMean = [3]float64{0.48145466, 0.4578275, 0.40821073}
	clipStd  = [3]float64{0.26862954, 0.26130258, 0.27577711}
)

// Engine holds the models and database connection for embedding operations.
type Engine struct {
	db                *sqvect.DB
	sqlDB             *sql.DB // separate connection for the files table
	textTok           *tokenizers.Tokenizer
	clipTok           *tokenizers.Tokenizer
	textSession       *ort.DynamicAdvancedSession
	clipTextSession   *ort.DynamicAdvancedSession
	clipVisionSession *ort.DynamicAdvancedSession
	textCollectionID  int // cached collection ID for text embeddings
	imageCollectionID int // cached collection ID for image embeddings
	mu                sync.Mutex
}

// New initializes the Engine with the given database path and ONNX model paths.
// textModelPath and imageModelPath should point to directories containing the ONNX model files.
func New(dbPath, textModelPath, imageModelPath string) (*Engine, error) {
	// Open sqvect database for vector operations
	cfg := sqvect.Config{
		Path:         dbPath,
		Dimensions:   0, // auto-detect
		SimilarityFn: core.CosineSimilarity,
		IndexType:    core.IndexTypeHNSW,
	}
	db, err := sqvect.Open(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqvect database: %w", err)
	}

	// Open a separate sql.DB for the files dedup table
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to open sql database: %w", err)
	}

	// WAL mode and pragmas for concurrency
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA temp_store=MEMORY",
		"PRAGMA mmap_size=536870912",
		"PRAGMA cache_size=-200000",
	} {
		if _, err := sqlDB.Exec(pragma); err != nil {
			log.Printf("pragma warning: %v", err)
		}
	}

	ctx := context.Background()

	// Create collections for text and image embeddings
	if _, err := db.Vector().CreateCollection(ctx, textCollection, textEmbedDim); err != nil {
		log.Printf("text collection: %v", err)
	}
	if _, err := db.Vector().CreateCollection(ctx, imageCollection, imageEmbedDim); err != nil {
		log.Printf("image collection: %v", err)
	}


	// Initialize ONNX Runtime
	ortPath := findOnnxRuntime()
	ort.SetSharedLibraryPath(ortPath)
	if err := ort.InitializeEnvironment(); err != nil {
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to initialize onnxruntime (dll=%s): %w", ortPath, err)
	}

	// Load tokenizers (daulet/tokenizers — CGO)
	textTok, err := tokenizers.FromFile(filepath.Join(textModelPath, "tokenizer.json"))
	if err != nil {
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to load text tokenizer: %w", err)
	}

	clipTok, err := tokenizers.FromFile(filepath.Join(imageModelPath, "tokenizer.json"))
	if err != nil {
		textTok.Close()
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to load CLIP tokenizer: %w", err)
	}

	// Limit ORT thread spawning to prevent contention with Go's concurrent processing
	opts, err := ort.NewSessionOptions()
	if err != nil {
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to create session options: %w", err)
	}
	defer opts.Destroy()

	// We are synchronizing inference with Engine.mu.Lock() inside IndexText/IndexImage,
	// so it's safe to let ONNX use its default multi-threading across all cores.

	// Create ONNX sessions (DynamicAdvancedSession allows variable input shapes)
	textSession, err := ort.NewDynamicAdvancedSession(
		filepath.Join(textModelPath, "model.onnx"),
		[]string{"input_ids", "attention_mask"},
		[]string{"sentence_embedding"},
		opts,
	)
	if err != nil {
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to create text session: %w", err)
	}

	clipTextSession, err := ort.NewDynamicAdvancedSession(
		filepath.Join(imageModelPath, "text_model.onnx"),
		[]string{"input_ids"},
		[]string{"text_embeds"},
		opts,
	)
	if err != nil {
		textSession.Destroy()
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to create CLIP text session: %w", err)
	}

	visionOpts, err := ort.NewSessionOptions()
	if err != nil {
		clipTextSession.Destroy()
		textSession.Destroy()
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to create vision session options: %w", err)
	}
	defer visionOpts.Destroy()

	// Vision models are large, allow ONNX to use its default multi-threading
	// because Go is NOT actually processing them concurrently (IndexBatch is guarded by a Mutex).

	clipVisionSession, err := ort.NewDynamicAdvancedSession(
		filepath.Join(imageModelPath, "vision_model.onnx"),
		[]string{"pixel_values"},
		[]string{"image_embeds"},
		visionOpts,
	)
	if err != nil {
		clipTextSession.Destroy()
		textSession.Destroy()
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to create CLIP vision session: %w", err)
	}

	// Cache collection IDs to avoid per-row SQL lookups in UpsertBatch
	ctx2 := context.Background()
	textCol, err := db.Vector().GetCollection(ctx2, textCollection)
	if err != nil {
		clipVisionSession.Destroy()
		clipTextSession.Destroy()
		textSession.Destroy()
		textTok.Close()
		clipTok.Close()
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to get text collection: %w", err)
	}
	imageCol, err := db.Vector().GetCollection(ctx2, imageCollection)
	if err != nil {
		clipVisionSession.Destroy()
		clipTextSession.Destroy()
		textSession.Destroy()
		textTok.Close()
		clipTok.Close()
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to get image collection: %w", err)
	}

	return &Engine{
		db:                db,
		sqlDB:             sqlDB,
		textTok:           textTok,
		clipTok:           clipTok,
		textSession:       textSession,
		clipTextSession:   clipTextSession,
		clipVisionSession: clipVisionSession,
		textCollectionID:  textCol.ID,
		imageCollectionID: imageCol.ID,
	}, nil
}

// Close shuts down the Engine, releasing all resources.
func (s *Engine) Close() error {
	var errs []error
	if s.clipVisionSession != nil {
		if err := s.clipVisionSession.Destroy(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.clipTextSession != nil {
		if err := s.clipTextSession.Destroy(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.textSession != nil {
		if err := s.textSession.Destroy(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.textTok != nil {
		s.textTok.Close()
	}
	if s.clipTok != nil {
		s.clipTok.Close()
	}
	if err := ort.DestroyEnvironment(); err != nil {
		errs = append(errs, err)
	}
	if s.sqlDB != nil {
		if err := s.sqlDB.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.db != nil {
		if err := s.db.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}
	return nil
}

func generateID() string {
	return uuid.New().String()
}

// ---------------------------------------------------------------------------
// Embedding helpers
// ---------------------------------------------------------------------------

type seqData struct {
	Index int
	IDs   []uint32
}

func (s *Engine) embedText(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	batch := int64(len(texts))

	t0 := time.Now()
	seqs := make([]seqData, batch)

	// Use a worker pool based on CPU count to avoid 4000 goroutines fighting for CGO locks
	numWorkers := runtime.NumCPU()
	if numWorkers < 1 {
		numWorkers = 1
	}
	jobs := make(chan int, batch)
	for i := 0; i < int(batch); i++ {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				ids, _ := s.textTok.Encode(texts[idx], true)
				seqs[idx] = seqData{Index: idx, IDs: ids}
			}
		}()
	}
	wg.Wait()

	tTok := time.Now()
	log.Printf("  -> Tokenize: %v for %d texts", tTok.Sub(t0), len(texts))

	// Sort sequences by length (shortest to longest) to minimize padding variance
	sort.Slice(seqs, func(i, j int) bool {
		return len(seqs[i].IDs) < len(seqs[j].IDs)
	})

	tSort := time.Now()

	result := make([][]float32, batch)

	// Process in sub-batches of 512
	subBatchSize := int64(512)
	for start := int64(0); start < batch; start += subBatchSize {
		end := start + subBatchSize
		if end > batch {
			end = batch
		}
		currBatch := end - start

		// Find max sequence length in this specific sub-batch
		maxLen := 0
		for i := start; i < end; i++ {
			if len(seqs[i].IDs) > maxLen {
				maxLen = len(seqs[i].IDs)
			}
		}
		seqLen := int64(maxLen)

		// Build flat padded tensors [currBatch, seqLen]
		flatIDs := make([]int64, currBatch*seqLen)
		flatMask := make([]int64, currBatch*seqLen)
		for i := int64(0); i < currBatch; i++ {
			seq := seqs[start+i]
			off := i * seqLen
			ids := uint32ToInt64(seq.IDs)
			copy(flatIDs[off:off+seqLen], ids)
			// Manually fill mask with 1s for the length of IDs (0s for padding already exist)
			for k := int64(0); k < int64(len(ids)); k++ {
				flatMask[off+k] = 1
			}
		}

		inputIDs, err := ort.NewTensor(ort.NewShape(currBatch, seqLen), flatIDs)
		if err != nil {
			return nil, fmt.Errorf("text input_ids tensor: %w", err)
		}

		attnMask, err := ort.NewTensor(ort.NewShape(currBatch, seqLen), flatMask)
		if err != nil {
			inputIDs.Destroy()
			return nil, fmt.Errorf("text attention_mask tensor: %w", err)
		}

		outTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(currBatch, 1024))
		if err != nil {
			attnMask.Destroy()
			inputIDs.Destroy()
			return nil, fmt.Errorf("text output tensor: %w", err)
		}

		if err := s.textSession.Run(
			[]ort.Value{inputIDs, attnMask},
			[]ort.Value{outTensor},
		); err != nil {
			outTensor.Destroy()
			attnMask.Destroy()
			inputIDs.Destroy()
			return nil, fmt.Errorf("text inference failed: %w", err)
		}

		// Extract per-sample embeddings, truncate to textEmbedDim, L2-normalize
		data := outTensor.GetData()
		for i := int64(0); i < currBatch; i++ {
			emb := make([]float32, textEmbedDim)
			copy(emb, data[i*1024:i*1024+int64(textEmbedDim)])
			normalize(emb)
			// Place back in original index
			origIdx := seqs[start+i].Index
			result[origIdx] = emb
		}

		outTensor.Destroy()
		attnMask.Destroy()
		inputIDs.Destroy()
	}

	tInfer := time.Now()
	log.Printf("  -> Text Sub-Batched ONNX: %v", tInfer.Sub(tSort))

	return result, nil
}

// embedClipText encodes text queries with the CLIP text encoder.
// Sequences are truncated to clipCtxLen (77), no padding is applied.
// Processed one-by-one as queries are typically single search terms.
func (s *Engine) embedClipText(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	result := make([][]float32, len(texts))
	for i, text := range texts {
		ids, _ := s.clipTok.Encode(text, true)

		ids64 := uint32ToInt64(ids)
		if len(ids64) > clipCtxLen {
			ids64 = ids64[:clipCtxLen]
		}
		seqLen := int64(len(ids64))

		inputIDs, err := ort.NewTensor(ort.NewShape(1, seqLen), ids64)
		if err != nil {
			return nil, fmt.Errorf("clip text input_ids tensor: %w", err)
		}

		outTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, clipEmbedDim))
		if err != nil {
			inputIDs.Destroy()
			return nil, fmt.Errorf("clip text output tensor: %w", err)
		}

		err = s.clipTextSession.Run(
			[]ort.Value{inputIDs},
			[]ort.Value{outTensor},
		)
		data := outTensor.GetData()
		outTensor.Destroy()
		inputIDs.Destroy()

		if err != nil {
			return nil, fmt.Errorf("clip text inference failed: %w", err)
		}

		emb := make([]float32, imageEmbedDim)
		copy(emb, data[:imageEmbedDim])
		normalize(emb)
		result[i] = emb
	}
	return result, nil
}

// embedImage generates image embeddings in a single batched inference call.
// Images are loaded, resized to 256x256, CLIP-normalized, and concatenated
// into one [batch, 3, 256, 256] tensor.
func (s *Engine) embedImage(imagePaths []string) ([][]float32, error) {
	if len(imagePaths) == 0 {
		return nil, nil
	}

	t0 := time.Now()

	pixelsPerImage := 3 * visionSize * visionSize
	// Preprocess all images; track which ones failed
	validIndices := make([]int, 0, len(imagePaths))
	allPixels := make([]float32, 0, len(imagePaths)*pixelsPerImage)
	result := make([][]float32, len(imagePaths))

	for i, imgPath := range imagePaths {
		pixels, err := loadAndPreprocess(imgPath)
		if err != nil {
			log.Printf("image preprocess %s: %v", imgPath, err)
			result[i] = make([]float32, imageEmbedDim) // zero vector
			continue
		}
		validIndices = append(validIndices, i)
		allPixels = append(allPixels, pixels...)
	}

	tPreprocess := time.Now()
	log.Printf("  -> loadAndPreprocess: %v for %d images", tPreprocess.Sub(t0), len(imagePaths))

	if len(validIndices) == 0 {
		return result, nil
	}

	tTensor := time.Now()

	// CPU Cache Optimization: batching 100 high-res image tensors sequentially blows up CPU L3 cache
	// processing them with batch size = 1 keeps intermediate activations inside cache bounds and executes
	// much faster (~10s for 100 images compared to ~60s).
	for j, idx := range validIndices {
		singlePixels := allPixels[j*pixelsPerImage : (j+1)*pixelsPerImage]
		inputTensor, err := ort.NewTensor(
			ort.NewShape(1, 3, int64(visionSize), int64(visionSize)), singlePixels)
		if err != nil {
			return nil, fmt.Errorf("vision input tensor error on img %d: %w", j, err)
		}

		outTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(clipEmbedDim)))
		if err != nil {
			inputTensor.Destroy()
			return nil, fmt.Errorf("vision output tensor error on img %d: %w", j, err)
		}

		if err := s.clipVisionSession.Run(
			[]ort.Value{inputTensor},
			[]ort.Value{outTensor},
		); err != nil {
			outTensor.Destroy()
			inputTensor.Destroy()
			return nil, fmt.Errorf("vision inference failed on img %d: %w", j, err)
		}

		data := outTensor.GetData()
		emb := make([]float32, imageEmbedDim)
		copy(emb, data[:imageEmbedDim])
		normalize(emb)
		result[idx] = emb

		outTensor.Destroy()
		inputTensor.Destroy()
	}

	tInference := time.Now()
	log.Printf("  -> ONNX single-batch inference loop run: %v", tInference.Sub(tTensor))

	return result, nil
}

// ---------------------------------------------------------------------------
// Image preprocessing
// ---------------------------------------------------------------------------

// loadAndPreprocess loads an image (already resized to 256x256 by vips)
// and converts to CLIP-normalized CHW float32 slice.
func loadAndPreprocess(path string) ([]float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	img, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	bounds := img.Bounds()
	if bounds.Dx() != visionSize || bounds.Dy() != visionSize {
		return nil, fmt.Errorf("expected %dx%d image, got %dx%d", visionSize, visionSize, bounds.Dx(), bounds.Dy())
	}

	// Convert to CHW float32 with CLIP normalization
	pixels := make([]float32, 3*visionSize*visionSize)
	hw := visionSize * visionSize

	switch m := img.(type) {
	case *image.YCbCr:
		// JPEGs are usually YCbCr
		for y := 0; y < visionSize; y++ {
			for x := 0; x < visionSize; x++ {
				yi := m.YOffset(bounds.Min.X+x, bounds.Min.Y+y)
				ci := m.COffset(bounds.Min.X+x, bounds.Min.Y+y)
				r, g, b := color.YCbCrToRGB(m.Y[yi], m.Cb[ci], m.Cr[ci])

				idx := y*visionSize + x
				pixels[0*hw+idx] = float32((float64(r)/255.0 - clipMean[0]) / clipStd[0])
				pixels[1*hw+idx] = float32((float64(g)/255.0 - clipMean[1]) / clipStd[1])
				pixels[2*hw+idx] = float32((float64(b)/255.0 - clipMean[2]) / clipStd[2])
			}
		}
	case *image.RGBA:
		// Fast-path for PNGs/already-converted RGBA
		offset := 0
		for y := 0; y < visionSize; y++ {
			for x := 0; x < visionSize; x++ {
				r := m.Pix[offset]
				g := m.Pix[offset+1]
				b := m.Pix[offset+2]
				offset += 4

				idx := y*visionSize + x
				pixels[0*hw+idx] = float32((float64(r)/255.0 - clipMean[0]) / clipStd[0])
				pixels[1*hw+idx] = float32((float64(g)/255.0 - clipMean[1]) / clipStd[1])
				pixels[2*hw+idx] = float32((float64(b)/255.0 - clipMean[2]) / clipStd[2])
			}
		}
	default:
		// Fallback for others
		for y := 0; y < visionSize; y++ {
			for x := 0; x < visionSize; x++ {
				c := color.RGBAModel.Convert(img.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.RGBA)
				idx := y*visionSize + x
				pixels[0*hw+idx] = float32((float64(c.R)/255.0 - clipMean[0]) / clipStd[0])
				pixels[1*hw+idx] = float32((float64(c.G)/255.0 - clipMean[1]) / clipStd[1])
				pixels[2*hw+idx] = float32((float64(c.B)/255.0 - clipMean[2]) / clipStd[2])
			}
		}
	}

	return pixels, nil
}

// ---------------------------------------------------------------------------
// Utility functions
// ---------------------------------------------------------------------------

func uint32ToInt64(ids []uint32) []int64 {
	out := make([]int64, len(ids))
	for i, v := range ids {
		out[i] = int64(v)
	}
	return out
}

func padOrTruncate(ids []int64, maxLen int) []int64 {
	if len(ids) >= maxLen {
		return ids[:maxLen]
	}
	padded := make([]int64, maxLen)
	copy(padded, ids)
	return padded
}

func normalize(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := float32(math.Sqrt(sum))
	if norm > 0 {
		for i := range v {
			v[i] /= norm
		}
	}
}

// ---------------------------------------------------------------------------
// Indexing
// ---------------------------------------------------------------------------

// upsertFiles inserts or updates file-level hashes.
func (s *Engine) upsertFiles(ctx context.Context, filePaths []string, fileHashes []int64) error {
	if len(filePaths) == 0 {
		return nil
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		"INSERT INTO files(path, hash) VALUES (?, ?) ON CONFLICT(path) DO UPDATE SET hash=excluded.hash")
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i := range filePaths {
		if _, err := stmt.ExecContext(ctx, filePaths[i], fileHashes[i]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IndexText generates text embeddings and stores them in the vector DB.
// contents and paths must have the same length.
// filePaths/fileHashes are optional file-level metadata for deduplication.
func (s *Engine) IndexText(contents, paths []string, filePaths []string, fileHashes []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t0 := time.Now()
	ctx := context.Background()

	// Generate embeddings
	embs, err := s.embedText(contents)
	if err != nil {
		return fmt.Errorf("text embedding failed: %w", err)
	}
	log.Printf("Encode %d text: %v", len(embs), time.Since(t0))

	// Upsert file hashes
	tFiles := time.Now()
	if err := s.upsertFiles(ctx, filePaths, fileHashes); err != nil {
		return fmt.Errorf("upsert files failed: %w", err)
	}

	// Build embeddings for sqvect
	sqEmbs := make([]*core.Embedding, len(embs))
	for i, emb := range embs {
		sqEmbs[i] = &core.Embedding{
			ID:           generateID(),
			CollectionID: s.textCollectionID,
			Vector:       emb,
			Content:      contents[i],
			Metadata: map[string]string{
				"path": paths[i],
			},
		}
	}

	// Batch upsert to sqvect (single source of truth for content + path)
	tVec := time.Now()
	if err := s.db.Vector().UpsertBatch(ctx, sqEmbs); err != nil {
		return fmt.Errorf("vector upsert failed: %w", err)
	}

	tEnd := time.Now()
	log.Printf("  -> files upsert: %v", tVec.Sub(tFiles))
	log.Printf("  -> vector upsert: %v", tEnd.Sub(tVec))
	log.Printf("Total text indexing: %v", tEnd.Sub(t0))
	return nil
}

// IndexImage generates image embeddings from image file paths and stores them.
// imagePaths are the filesystem paths to load images from.
// paths are the logical paths stored as metadata.
func (s *Engine) IndexImage(imagePaths, paths []string, filePaths []string, fileHashes []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t0 := time.Now()
	ctx := context.Background()

	// Generate image embeddings using CLIP vision encoder
	embs, err := s.embedImage(imagePaths)
	if err != nil {
		return fmt.Errorf("image embedding failed: %w", err)
	}
	log.Printf("Encode %d images: %v", len(embs), time.Since(t0))

	// Upsert file hashes
	tFiles := time.Now()
	if err := s.upsertFiles(ctx, filePaths, fileHashes); err != nil {
		return fmt.Errorf("upsert files failed: %w", err)
	}

	// Build embeddings for sqvect
	sqEmbs := make([]*core.Embedding, len(embs))
	for i, emb := range embs {
		sqEmbs[i] = &core.Embedding{
			ID:           generateID(),
			CollectionID: s.imageCollectionID,
			Vector:       emb,
			Content:      "",
			Metadata: map[string]string{
				"path": paths[i],
			},
		}
	}

	// Batch upsert to sqvect (single source of truth for path)
	tVec := time.Now()
	if err := s.db.Vector().UpsertBatch(ctx, sqEmbs); err != nil {
		return fmt.Errorf("vector upsert failed: %w", err)
	}

	tEnd := time.Now()
	log.Printf("  -> files upsert: %v", tVec.Sub(tFiles))
	log.Printf("  -> vector upsert: %v", tEnd.Sub(tVec))
	log.Printf("Total image indexing: %v", tEnd.Sub(t0))
	return nil
}

// IndexPathsFTS inserts unique paths into the paths_fts FTS5 table.
// Called from the processor layer after indexing embeddings.
func (s *Engine) IndexPathsFTS(paths []string) {
	if len(paths) == 0 {
		return
	}
	ctx := context.Background()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("paths_fts: failed to begin tx: %v", err)
		return
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO paths_fts(path) VALUES (?)")
	if err != nil {
		tx.Rollback()
		log.Printf("paths_fts: failed to prepare stmt: %v", err)
		return
	}
	defer stmt.Close()
	for _, p := range paths {
		if _, err := stmt.ExecContext(ctx, p); err != nil {
			log.Printf("paths_fts insert warning: %v", err)
		}
	}
	tx.Commit()
}

// InitIndexTables creates the necessary SQLite tables for indexing (files, paths_fts).
// Called by the processor layer before indexing begins.
func (s *Engine) InitIndexTables() error {
	ctx := context.Background()
	if _, err := s.sqlDB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS files (
			path TEXT PRIMARY KEY,
			hash INTEGER NOT NULL
		);
		CREATE VIRTUAL TABLE IF NOT EXISTS paths_fts USING fts5(path);
	`); err != nil {
		return fmt.Errorf("failed to create index tables: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

// SearchResult holds a search result path.
type SearchResult struct {
	Path  string
	Score float64
}

// Search performs a combined text + image search over both collections.
// Signals: path FTS5 (dominates) > RRF(text vector + content FTS5) + image vectors.
func (s *Engine) Search(query string) ([]SearchResult, error) {
	t0 := time.Now()
	ctx := context.Background()

	// Encode query with text model
	textEmbs, err := s.embedText([]string{query})
	if err != nil {
		return nil, fmt.Errorf("text query encoding failed: %w", err)
	}
	textQueryVec := textEmbs[0]

	// Encode query with CLIP text encoder (for image similarity search)
	clipEmbs, err := s.embedClipText([]string{query})
	if err != nil {
		log.Printf("image text encoding unavailable, text-only search: %v", err)
		return s.TextSearch(query)
	}
	imageQueryVec := clipEmbs[0]

	log.Printf("Search encode: %v", time.Since(t0))

	// Vector search both collections (cosine similarity, no limit)
	textResults, err := s.db.Vector().Search(ctx, textQueryVec, core.SearchOptions{
		Collection: textCollection,
		TopK:       0,
	})
	if err != nil {
		log.Printf("text search error: %v", err)
	}

	imageResults, err := s.db.Vector().Search(ctx, imageQueryVec, core.SearchOptions{
		Collection: imageCollection,
		TopK:       0,
	})
	if err != nil {
		log.Printf("image search error: %v", err)
	}

	scores := map[string]float64{}
	rrfK := 60.0

	// Signal 1: Path FTS5 (raw scores — intentionally overpowers)
	s.addPathFTSScores(ctx, query, scores, 3.0)

	// Signal 2: RRF(text vector + content FTS5)
	for i, res := range textResults {
		scores[res.Metadata["path"]] += 1.0 / (rrfK + float64(i+1))
	}
	s.addContentFTSRRF(ctx, query, scores, rrfK)

	// Signal 3: Image vector ranking (1.1x boost via RRF)
	for i, res := range imageResults {
		path := res.Metadata["path"]
		scores[path] += 1.1 / (rrfK + float64(i+1))
	}

	results := make([]SearchResult, 0, len(scores))
	for path, score := range scores {
		results = append(results, SearchResult{Path: path, Score: score})
	}
	sortResults(results)

	log.Printf("Search total: %v", time.Since(t0))
	return results, nil
}

// TextSearch performs search over text embeddings only.
// Signals: path FTS5 (dominates) > RRF(text vector + content FTS5).
func (s *Engine) TextSearch(query string) ([]SearchResult, error) {
	t0 := time.Now()
	ctx := context.Background()

	embs, err := s.embedText([]string{query})
	if err != nil {
		return nil, fmt.Errorf("text query encoding failed: %w", err)
	}
	queryVec := embs[0]
	log.Printf("Search encode: %v", time.Since(t0))

	results, err := s.db.Vector().Search(ctx, queryVec, core.SearchOptions{
		Collection: textCollection,
		TopK:       0,
	})
	if err != nil {
		return nil, fmt.Errorf("text search failed: %w", err)
	}

	scores := map[string]float64{}
	rrfK := 60.0

	// Path FTS5 (dominates)
	s.addPathFTSScores(ctx, query, scores, 3.0)

	// RRF(text vector + content FTS5)
	for i, res := range results {
		scores[res.Metadata["path"]] += 1.0 / (rrfK + float64(i+1))
	}
	s.addContentFTSRRF(ctx, query, scores, rrfK)

	out := make([]SearchResult, 0, len(scores))
	for path, score := range scores {
		out = append(out, SearchResult{Path: path, Score: score})
	}
	sortResults(out)

	log.Printf("Search total: %v", time.Since(t0))
	return out, nil
}

// ImageSearch performs search over image embeddings only.
func (s *Engine) ImageSearch(query string) ([]SearchResult, error) {
	t0 := time.Now()
	ctx := context.Background()

	embs, err := s.embedClipText([]string{query})
	if err != nil {
		return nil, fmt.Errorf("image query encoding failed: %w", err)
	}
	queryVec := embs[0]
	log.Printf("Search encode: %v", time.Since(t0))

	results, err := s.db.Vector().Search(ctx, queryVec, core.SearchOptions{
		Collection: imageCollection,
		TopK:       10,
	})
	if err != nil {
		return nil, fmt.Errorf("image search failed: %w", err)
	}

	out := make([]SearchResult, 0, len(results))
	seen := map[string]bool{}
	for _, res := range results {
		path := res.Metadata["path"]
		if seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, SearchResult{Path: path, Score: res.Score})
	}

	log.Printf("Search total: %v", time.Since(t0))
	return out, nil
}

// ---------------------------------------------------------------------------
// Delete / Rename / Hash
// ---------------------------------------------------------------------------

// DeletePaths removes all embeddings for the given file paths.
func (s *Engine) DeletePaths(paths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()

	for _, path := range paths {
		// Find embedding IDs by path in sqvect's embeddings table
		rows, err := s.sqlDB.QueryContext(ctx,
			"SELECT id FROM embeddings WHERE json_extract(metadata, '$.path') = ?", path)
		if err != nil {
			return fmt.Errorf("query embeddings by path failed: %w", err)
		}

		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()

		// Delete from sqvect
		if len(ids) > 0 {
			if err := s.db.Vector().DeleteBatch(ctx, ids); err != nil {
				return fmt.Errorf("delete embeddings failed: %w", err)
			}
		}

		// Delete from files table
		if _, err := s.sqlDB.ExecContext(ctx, "DELETE FROM files WHERE path = ?", path); err != nil {
			return fmt.Errorf("delete files failed: %w", err)
		}

		// Delete from paths FTS5
		if _, err := s.sqlDB.ExecContext(ctx,
			"DELETE FROM paths_fts WHERE path = ?", path); err != nil {
			log.Printf("paths_fts delete warning: %v", err)
		}
	}
	return nil
}

// RenamePaths updates path references in sqvect embeddings and the files table.
func (s *Engine) RenamePaths(oldPaths, newPaths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ctx := context.Background()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for i, oldPath := range oldPaths {
		newPath := newPaths[i]
		// Update path in sqvect's embeddings metadata JSON
		if _, err := tx.ExecContext(ctx,
			`UPDATE embeddings SET metadata = json_set(metadata, '$.path', ?) WHERE json_extract(metadata, '$.path') = ?`,
			newPath, oldPath); err != nil {
			return fmt.Errorf("rename embedding paths failed: %w", err)
		}
		// Update files table
		if _, err := tx.ExecContext(ctx, "UPDATE files SET path = ? WHERE path = ?", newPath, oldPath); err != nil {
			return fmt.Errorf("rename files failed: %w", err)
		}
		// Update paths FTS5 (delete old, insert new)
		tx.ExecContext(ctx, "DELETE FROM paths_fts WHERE path = ?", oldPath)
		tx.ExecContext(ctx, "INSERT INTO paths_fts(path) VALUES (?)", newPath)
	}

	return tx.Commit()
}

// GetFileHash returns the stored hash for a file path, or 0 if not found.
func (s *Engine) GetFileHash(path string) (int64, error) {
	var hash int64
	err := s.sqlDB.QueryRow("SELECT hash FROM files WHERE path = ?", path).Scan(&hash)
	if err != nil {
		return 0, nil // not found
	}
	return hash, nil
}

// ---------------------------------------------------------------------------
// FTS5 helpers
// ---------------------------------------------------------------------------

// addPathFTSScores queries the paths_fts table and adds matching path scores.
func (s *Engine) addPathFTSScores(ctx context.Context, query string, scores map[string]float64, boost float64) {
	// Convert query to FTS5 match expression: "word1" OR "word2" OR ...
	words := strings.Fields(query)
	if len(words) == 0 {
		return
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	ftsQuery := strings.Join(quoted, " OR ")

	rows, err := s.sqlDB.QueryContext(ctx,
		"SELECT path, rank FROM paths_fts WHERE paths_fts MATCH ? ORDER BY rank LIMIT 20", ftsQuery)
	if err != nil {
		log.Printf("paths_fts search warning: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var path string
		var rank float64
		if err := rows.Scan(&path, &rank); err != nil {
			continue
		}
		// FTS5 rank is negative (lower = better), convert to positive score
		scores[path] += boost * (-rank)
	}
}

// addContentFTSRRF queries sqvect's chunks_fts table for content keyword matches
// and adds rank-based RRF scores to the provided scores map.
func (s *Engine) addContentFTSRRF(ctx context.Context, query string, scores map[string]float64, rrfK float64) {
	words := strings.Fields(query)
	if len(words) == 0 {
		return
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	ftsQuery := strings.Join(quoted, " OR ")

	rows, err := s.sqlDB.QueryContext(ctx, `
		SELECT json_extract(e.metadata, '$.path')
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
	`, ftsQuery)
	if err != nil {
		log.Printf("chunks_fts search warning: %v", err)
		return
	}
	defer rows.Close()

	rank := 1
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			continue
		}
		scores[path] += 1.0 / (rrfK + float64(rank))
		rank++
	}
}

// sortResults sorts SearchResults by score descending.
func sortResults(results []SearchResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && results[j].Score > results[j-1].Score; j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}

// findOnnxRuntime locates onnxruntime.dll by checking:
// 1. The directory containing the running executable
// 2. The current working directory
// Returns the absolute path to the DLL, or "onnxruntime.dll" as a fallback.
func findOnnxRuntime() string {
	const dllName = "onnxruntime.dll"

	// Check next to the executable
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), dllName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Check the current working directory
	if cwd, err := os.Getwd(); err == nil {
		p := filepath.Join(cwd, dllName)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	// Fallback: let the OS search PATH
	return dllName
}
