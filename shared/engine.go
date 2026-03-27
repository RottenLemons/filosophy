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
	"syscall"
	"time"

	"net/url"

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

	// rrfK is the Reciprocal Rank Fusion constant. Larger values reduce the
	// penalty gap between adjacent ranks, smoothing signal contributions.
	rrfK = 60.0

	// Filename scoring constants (addFilenameBoosts).
	// Each query word that exactly matches a filename word contributes filenameWordBoost.
	// filenameMultiWordBonus is multiplied by (matchedWords - 1) so that files sharing
	// more query words rank proportionally higher — no flat ceiling for "all present".
	filenameWordBoost      = 0.30 // per matched query word (exact)
	filenamePrefixBoost    = 0.10 // per matched query word (prefix/suffix)
	filenameSubstrBoost    = 0.04 // per matched query word (substring)
	filenameMultiWordBonus = 0.60 // × (matchedWords-1), grows with word coverage

	// Fuzzy path scoring constants (addFuzzyPathBoosts).
	// fuzzyPathBoost scales the Levenshtein similarity contribution.
	// fuzzyMinWordSim is the minimum average word similarity to apply any boost
	// (e.g. "albret"→"albert"=0.67, "tyop"→"typo"=0.75 both pass; random noise ~0.3 fails).
	fuzzyPathBoost  = 0.15
	fuzzyMinWordSim = 0.55

	// Content FTS RRF boost constants — applied after document-level aggregation.
	// Phrase match is much stronger than keyword OR: a document containing "Albert Camus"
	// adjacent should always beat one that merely contains both words separately.
	// At rank 1: phrase = 50/61 ≈ 0.82, keyword = 10/61 ≈ 0.16, fuzzy = 0.8/61 ≈ 0.013
	contentPhraseBoost  = 50.0
	contentKeywordBoost = 10.0
	contentFuzzyBoost   = 0.8

	// contentTopK is the number of top-scoring chunks per document used to compute
	// the document-level BM25 score. Averaging the top-K (k-MAX) respects IDF like
	// pure MAX while still rewarding documents with multiple strong matches.
	// k=1 → pure MAX; k=∞ → full average (vulnerable to count-dominates-IDF).
	contentTopK = 3
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
	staticEmb         *StaticEmbedder
	clipTok           *tokenizers.Tokenizer
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

	// Load the static text embedder (reads model.safetensors + tokenizer.json directly,
	// no ONNX inference needed for text).
	staticEmb, err := LoadStaticEmbedder(
		filepath.Join(textModelPath, "model.safetensors"),
		filepath.Join(textModelPath, "tokenizer.json"),
	)
	if err != nil {
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to load static embedder: %w", err)
	}

	clipTok, err := tokenizers.FromFile(filepath.Join(imageModelPath, "tokenizer.json"))
	if err != nil {
		staticEmb.Close()
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

	// Create ONNX sessions for CLIP only (text uses the static embedder).
	clipTextSession, err := ort.NewDynamicAdvancedSession(
		filepath.Join(imageModelPath, "text_model.onnx"),
		[]string{"input_ids"},
		[]string{"text_embeds"},
		opts,
	)
	if err != nil {

		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to create CLIP text session: %w", err)
	}

	visionOpts, err := ort.NewSessionOptions()
	if err != nil {
		clipTextSession.Destroy()

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

		staticEmb.Close()
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

		staticEmb.Close()
		clipTok.Close()
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to get image collection: %w", err)
	}

	return &Engine{
		db:                db,
		sqlDB:             sqlDB,
		staticEmb:         staticEmb,
		clipTok:           clipTok,
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
	if s.staticEmb != nil {
		if err := s.staticEmb.Close(); err != nil {
			errs = append(errs, err)
		}
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


func (s *Engine) embedText(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	t0 := time.Now()
	result := make([][]float32, len(texts))

	// Worker pool: each goroutine calls StaticEmbedder.EmbedString (pure Go, no CGO lock).
	numWorkers := runtime.NumCPU()
	if numWorkers < 1 {
		numWorkers = 1
	}
	jobs := make(chan int, len(texts))
	for i := range texts {
		jobs <- i
	}
	close(jobs)

	type outcome struct {
		idx int
		emb []float32
		err error
	}
	results := make(chan outcome, len(texts))

	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				emb, err := s.staticEmb.EmbedString(texts[idx])
				if err != nil {
					// Shouldn't happen — EmbedString returns zero vec on empty input.
					results <- outcome{idx: idx, err: err}
					continue
				}
				// MRL model: truncate to textEmbedDim, then re-normalise.
				if len(emb) > textEmbedDim {
					emb = emb[:textEmbedDim]
					normalize(emb)
				}
				results <- outcome{idx: idx, emb: emb}
			}
		}()
	}
	wg.Wait()
	close(results)

	for o := range results {
		if o.err != nil {
			log.Printf("embedText[%d]: %v", o.idx, o.err)
			continue
		}
		result[o.idx] = o.emb
	}

	log.Printf("  -> Static embedText: %v for %d texts", time.Since(t0), len(texts))
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

	cpu0 := getCPUTime()
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
	log.Printf("  -> loadAndPreprocess: %v for %d images (%s)", tPreprocess.Sub(t0), len(imagePaths), getStats(cpu0, t0))

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
	log.Printf("  -> ONNX single-batch inference loop run: %v (%s)", tInference.Sub(tTensor), getStats(cpu0, tTensor))

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
// Resource tracking (Windows)
// ---------------------------------------------------------------------------

func getCPUTime() int64 {
	var creationTime, exitTime, kernelTime, userTime syscall.Filetime
	h := syscall.Handle(^uintptr(0)) // GetCurrentProcess()
	err := syscall.GetProcessTimes(h, &creationTime, &exitTime, &kernelTime, &userTime)
	if err != nil {
		return 0
	}
	// units of 100ns
	k := int64(kernelTime.LowDateTime) | (int64(kernelTime.HighDateTime) << 32)
	u := int64(userTime.LowDateTime) | (int64(userTime.HighDateTime) << 32)
	return k + u
}

func getStats(startCPU int64, startTime time.Time) string {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	ramStr := fmt.Sprintf("%.1f MiB", float64(m.Sys)/1024/1024)

	duration := time.Since(startTime).Seconds()
	if duration <= 0 {
		return fmt.Sprintf("RAM: %s, CPU: 0.0%%", ramStr)
	}

	endCPU := getCPUTime()
	cpuUnits := endCPU - startCPU
	// 10,000,000 units of 100ns = 1s
	cpuSecs := float64(cpuUnits) / 10000000.0
	// Show usage normalized by CPU count (system perspective)
	cpuUsage := (cpuSecs / duration / float64(runtime.NumCPU())) * 100.0
	if cpuUsage > 100.0 {
		cpuUsage = 100.0
	}
	return fmt.Sprintf("RAM: %s, CPU: %.1f%%", ramStr, cpuUsage)
}

// ---------------------------------------------------------------------------
// Indexing
// ---------------------------------------------------------------------------

// upsertFiles inserts or updates file-level hashes, mtimes, sizes, and marks the file as content-indexed.
func (s *Engine) upsertFiles(ctx context.Context, filePaths []string, fileHashes, fileMtimes, fileSizes []int64) error {
	if len(filePaths) == 0 {
		return nil
	}
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO files(path, hash, mtime, size, content_indexed) VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(path) DO UPDATE SET hash=excluded.hash, mtime=excluded.mtime, size=excluded.size, content_indexed=1`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i := range filePaths {
		if _, err := stmt.ExecContext(ctx, filePaths[i], fileHashes[i], fileMtimes[i], fileSizes[i]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// IndexText generates text embeddings and stores them in the vector DB.
// contents and paths must have the same length.
// filePaths/fileHashes are optional file-level metadata for deduplication.
func (s *Engine) IndexText(contents, paths []string, filePaths []string, fileHashes, fileMtimes, fileSizes []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cpu0 := getCPUTime()
	t0 := time.Now()
	ctx := context.Background()

	// Generate embeddings
	embs, err := s.embedText(contents)
	if err != nil {
		return fmt.Errorf("text embedding failed: %w", err)
	}
	log.Printf("Encode %d text: %v (%s)", len(embs), time.Since(t0), getStats(cpu0, t0))

	// Upsert file hashes
	tFiles := time.Now()
	if err := s.upsertFiles(ctx, filePaths, fileHashes, fileMtimes, fileSizes); err != nil {
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
	log.Printf("  -> files upsert: %v (%s)", tVec.Sub(tFiles), getStats(cpu0, tFiles))
	log.Printf("  -> vector upsert: %v (%s)", tEnd.Sub(tVec), getStats(cpu0, tVec))
	log.Printf("Total text indexing: %v (%s)", tEnd.Sub(t0), getStats(cpu0, t0))
	return nil
}

// IndexImage generates image embeddings from image file paths and stores them.
// imagePaths are the filesystem paths to load images from.
// paths are the logical paths stored as metadata.
func (s *Engine) IndexImage(imagePaths, paths []string, filePaths []string, fileHashes, fileMtimes, fileSizes []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cpu0 := getCPUTime()
	t0 := time.Now()
	ctx := context.Background()

	// Generate image embeddings using CLIP vision encoder
	embs, err := s.embedImage(imagePaths)
	if err != nil {
		return fmt.Errorf("image embedding failed: %w", err)
	}
	log.Printf("Encode %d images: %v (%s)", len(embs), time.Since(t0), getStats(cpu0, t0))

	// Upsert file hashes
	tFiles := time.Now()
	if err := s.upsertFiles(ctx, filePaths, fileHashes, fileMtimes, fileSizes); err != nil {
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
	log.Printf("  -> files upsert: %v (%s)", tVec.Sub(tFiles), getStats(cpu0, tFiles))
	log.Printf("  -> vector upsert: %v (%s)", tEnd.Sub(tVec), getStats(cpu0, tVec))
	log.Printf("Total image indexing: %v (%s)", tEnd.Sub(t0), getStats(cpu0, t0))
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
			path             TEXT    PRIMARY KEY,
			hash             INTEGER NOT NULL DEFAULT 0,
			mtime            INTEGER NOT NULL DEFAULT 0,
			size             INTEGER NOT NULL DEFAULT 0,
			content_indexed  INTEGER NOT NULL DEFAULT 0
		);
		CREATE VIRTUAL TABLE IF NOT EXISTS paths_fts USING fts5(path);
	`); err != nil {
		return fmt.Errorf("failed to create index tables: %w", err)
	}
	return nil
}

// IndexMetadata inserts file paths, mtimes, and sizes into the files table without
// marking them as content-indexed, and populates paths_fts so path-based search
// works immediately after Pass 1. Already-present rows are left untouched (INSERT OR IGNORE).
func (s *Engine) IndexMetadata(paths []string, mtimes, sizes []int64) error {
	if len(paths) == 0 {
		return nil
	}
	ctx := context.Background()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	filesStmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO files(path, hash, mtime, size, content_indexed) VALUES (?, 0, ?, ?, 0)`)
	if err != nil {
		return err
	}
	defer filesStmt.Close()

	ftsStmt, err := tx.PrepareContext(ctx, `INSERT INTO paths_fts(path) VALUES (?)`)
	if err != nil {
		return err
	}
	defer ftsStmt.Close()

	for i := range paths {
		if _, err := filesStmt.ExecContext(ctx, paths[i], mtimes[i], sizes[i]); err != nil {
			return err
		}
		if _, err := ftsStmt.ExecContext(ctx, paths[i]); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PruneStale removes index entries for paths that no longer exist on disk.
// livePaths is the complete set of current paths from a directory walk.
func (s *Engine) PruneStale(livePaths []string) error {
	liveSet := make(map[string]struct{}, len(livePaths))
	for _, p := range livePaths {
		liveSet[p] = struct{}{}
	}

	rows, err := s.sqlDB.QueryContext(context.Background(), `SELECT path FROM files`)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		if _, ok := liveSet[p]; !ok {
			stale = append(stale, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	if len(stale) == 0 {
		return nil
	}
	return s.DeletePaths(stale)
}

// UnindexedFiles returns all files that have not yet been content-indexed, ordered
// by mtime descending so Pass 2 prioritises the most recently modified files first.
func (s *Engine) UnindexedFiles() (paths []string, mtimes []int64, err error) {
	rows, err := s.sqlDB.QueryContext(context.Background(),
		`SELECT path, mtime FROM files WHERE content_indexed = 0 ORDER BY mtime DESC`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var m int64
		if err := rows.Scan(&p, &m); err != nil {
			return nil, nil, err
		}
		paths = append(paths, p)
		mtimes = append(mtimes, m)
	}
	return paths, mtimes, rows.Err()
}

// ResetContentIndex clears content_indexed/hashes and paths_fts, forcing a full
// re-index on the next run. Called when --index is passed.
func (s *Engine) ResetContentIndex() error {
	ctx := context.Background()
	tx, err := s.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE files SET content_indexed = 0, hash = 0`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM paths_fts`); err != nil {
		return err
	}
	return tx.Commit()
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
// Signals (all RRF-normalized for consistent scale):
//  1. Path FTS5 exact word match (3x weighted RRF)
//  2. Path FTS5 prefix match (1.5x weighted RRF) — handles partial typing
//  3. Text vector RRF
//  4. Content FTS5 RRF
//  5. Image vector RRF (1.1x weighted)
//  6. Exact/prefix/substring filename match boost
//  7. Trigram fuzzy path similarity — typo tolerance
func (s *Engine) Search(query string) ([]SearchResult, error) {
	cpu0 := getCPUTime()
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

	log.Printf("Search encode: %v (%s)", time.Since(t0), getStats(cpu0, t0))

	// Vector search both collections, capped at 200 to reduce noise
	textResults, err := s.db.Vector().Search(ctx, textQueryVec, core.SearchOptions{
		Collection: textCollection,
		TopK:       200,
	})
	if err != nil {
		log.Printf("text search error: %v", err)
	}

	imageResults, err := s.db.Vector().Search(ctx, imageQueryVec, core.SearchOptions{
		Collection: imageCollection,
		TopK:       200,
	})
	if err != nil {
		log.Printf("image search error: %v", err)
	}

	scores := map[string]float64{}

	// Signal 1: Path FTS5 exact word OR (3x)
	s.addPathFTSScores(ctx, query, scores, 3.0)
	// Signal 2: Path FTS5 ALL words AND — boosts paths containing every query word (4x)
	s.addPathFTSAllWords(ctx, query, scores, 4.0)
	// Signal 3: Path FTS5 full-word prefix OR (1.5x)
	s.addPathFTSPrefixScores(ctx, query, scores, 1.5)
	// Signal 4: Path FTS5 3-char anchor AND — "alb"* "cam"* (1.0x)
	s.addPathFTSShortPrefixScores(ctx, query, scores, 1.0)

	// Signal 5: Text vector RRF
	for i, res := range textResults {
		scores[res.Metadata["path"]] += 1.0 / (rrfK + float64(i+1))
	}
	// Signal 6: Content phrase match — "Albert Camus" adjacent in text (2.5x)
	s.addContentFTSPhraseRRF(ctx, query, scores)
	// Signal 7: Content FTS5 exact keyword OR RRF
	s.addContentFTSRRF(ctx, query, scores)
	// Signal 8: Content FTS5 3-char prefix fuzzy RRF (0.8x)
	s.addContentFTSFuzzyRRF(ctx, query, scores)

	// Signal 9: Image vector RRF (1.1x)
	for i, res := range imageResults {
		path := res.Metadata["path"]
		scores[path] += 1.1 / (rrfK + float64(i+1))
	}

	// Signal 10: Filename word-set match + URL-decode + multi-word coverage bonus
	addFilenameBoosts(query, scores)
	// Signal 11: Word-level Levenshtein fuzzy path boost
	addFuzzyPathBoosts(query, scores)

	results := make([]SearchResult, 0, len(scores))
	for path, score := range scores {
		results = append(results, SearchResult{Path: path, Score: score})
	}
	sortResults(results)

	log.Printf("Search total: %v (%s)", time.Since(t0), getStats(cpu0, t0))
	return results, nil
}

// TextSearch performs search over text embeddings only.
// Signals: path FTS5 exact+prefix (3x/1.5x weighted RRF) > text vector RRF > content FTS5 RRF > filename/fuzzy boosts.
func (s *Engine) TextSearch(query string) ([]SearchResult, error) {
	cpu0 := getCPUTime()
	t0 := time.Now()
	ctx := context.Background()

	embs, err := s.embedText([]string{query})
	if err != nil {
		return nil, fmt.Errorf("text query encoding failed: %w", err)
	}
	queryVec := embs[0]
	log.Printf("Search encode: %v (%s)", time.Since(t0), getStats(cpu0, t0))

	results, err := s.db.Vector().Search(ctx, queryVec, core.SearchOptions{
		Collection: textCollection,
		TopK:       200,
	})
	if err != nil {
		return nil, fmt.Errorf("text search failed: %w", err)
	}

	scores := map[string]float64{}

	// Path FTS5 exact OR (3x) + ALL-words AND (4x) + full-word prefix (1.5x) + 3-char anchor (1.0x)
	s.addPathFTSScores(ctx, query, scores, 3.0)
	s.addPathFTSAllWords(ctx, query, scores, 4.0)
	s.addPathFTSPrefixScores(ctx, query, scores, 1.5)
	s.addPathFTSShortPrefixScores(ctx, query, scores, 1.0)

	// Text vector RRF + content phrase (2.5x) + exact keyword + fuzzy prefix
	for i, res := range results {
		scores[res.Metadata["path"]] += 1.0 / (rrfK + float64(i+1))
	}
	s.addContentFTSPhraseRRF(ctx, query, scores)
	s.addContentFTSRRF(ctx, query, scores)
	s.addContentFTSFuzzyRRF(ctx, query, scores)

	// Filename word-set match + URL-decode + multi-word coverage bonus + Levenshtein fuzzy
	addFilenameBoosts(query, scores)
	addFuzzyPathBoosts(query, scores)

	out := make([]SearchResult, 0, len(scores))
	for path, score := range scores {
		out = append(out, SearchResult{Path: path, Score: score})
	}
	sortResults(out)

	log.Printf("Search total: %v (%s)", time.Since(t0), getStats(cpu0, t0))
	return out, nil
}

// ImageSearch performs search over image embeddings only.
func (s *Engine) ImageSearch(query string) ([]SearchResult, error) {
	cpu0 := getCPUTime()
	t0 := time.Now()
	ctx := context.Background()

	embs, err := s.embedClipText([]string{query})
	if err != nil {
		return nil, fmt.Errorf("image query encoding failed: %w", err)
	}
	queryVec := embs[0]
	log.Printf("Search encode: %v (%s)", time.Since(t0), getStats(cpu0, t0))

	results, err := s.db.Vector().Search(ctx, queryVec, core.SearchOptions{
		Collection: imageCollection,
		TopK:       50,
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

	log.Printf("Search total: %v (%s)", time.Since(t0), getStats(cpu0, t0))
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

// addPathFTSScores queries the paths_fts table for exact word matches and adds
// RRF-normalized scores. Using rank position (not raw BM25 magnitude) keeps
// this signal on the same scale as vector RRF contributions.
func (s *Engine) addPathFTSScores(ctx context.Context, query string, scores map[string]float64, boost float64) {
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
		"SELECT path FROM paths_fts WHERE paths_fts MATCH ? ORDER BY rank LIMIT 20", ftsQuery)
	if err != nil {
		log.Printf("paths_fts search warning: %v", err)
		return
	}
	defer rows.Close()

	rankPos := 1
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			continue
		}
		scores[path] += boost / (rrfK + float64(rankPos))
		rankPos++
	}
}

// addPathFTSAllWords queries paths_fts requiring ALL query words to be present
// (FTS5 implicit AND semantics). This is only useful for multi-word queries and
// gives a strong boost to paths like "Albert%20Camus.png" over "Albert.txt".
func (s *Engine) addPathFTSAllWords(ctx context.Context, query string, scores map[string]float64, boost float64) {
	words := strings.Fields(query)
	if len(words) < 2 {
		return // AND is only meaningful for multi-word queries
	}
	quoted := make([]string, len(words))
	for i, w := range words {
		quoted[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	// Space-separated = implicit AND in FTS5
	ftsQuery := strings.Join(quoted, " ")

	rows, err := s.sqlDB.QueryContext(ctx,
		"SELECT path FROM paths_fts WHERE paths_fts MATCH ? ORDER BY rank LIMIT 20", ftsQuery)
	if err != nil {
		log.Printf("paths_fts AND search warning: %v", err)
		return
	}
	defer rows.Close()

	rankPos := 1
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			continue
		}
		scores[path] += boost / (rrfK + float64(rankPos))
		rankPos++
	}
}

// addPathFTSPrefixScores queries paths_fts with FTS5 prefix syntax ("word"*)
// to match partial query terms. Handles mid-typing and abbreviated filenames.
func (s *Engine) addPathFTSPrefixScores(ctx context.Context, query string, scores map[string]float64, boost float64) {
	words := strings.Fields(query)
	prefixed := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) >= 2 {
			prefixed = append(prefixed, `"`+strings.ReplaceAll(w, `"`, `""`)+`"*`)
		}
	}
	if len(prefixed) == 0 {
		return
	}
	ftsQuery := strings.Join(prefixed, " OR ")

	rows, err := s.sqlDB.QueryContext(ctx,
		"SELECT path FROM paths_fts WHERE paths_fts MATCH ? ORDER BY rank LIMIT 20", ftsQuery)
	if err != nil {
		log.Printf("paths_fts prefix search warning: %v", err)
		return
	}
	defer rows.Close()

	rankPos := 1
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			continue
		}
		scores[path] += boost / (rrfK + float64(rankPos))
		rankPos++
	}
}

// addPathFTSShortPrefixScores uses the first 3 characters of each query word as
// an FTS5 prefix anchor. This bridges transposition typos that the full-word
// prefix query cannot: "albret"[:3]="alb" matches "albert", "album", etc.;
// "camuls"[:3]="cam" matches "camus", "camel", etc. Uses AND semantics
// (space-separated terms) so multi-word queries require all anchors to appear.
func (s *Engine) addPathFTSShortPrefixScores(ctx context.Context, query string, scores map[string]float64, boost float64) {
	words := strings.Fields(strings.ToLower(query))
	anchors := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		escaped := strings.ReplaceAll(w[:3], `"`, `""`)
		anchors = append(anchors, `"`+escaped+`"*`)
	}
	if len(anchors) == 0 {
		return
	}
	// AND semantics: all anchors must be present in the path
	ftsQuery := strings.Join(anchors, " ")

	rows, err := s.sqlDB.QueryContext(ctx,
		"SELECT path FROM paths_fts WHERE paths_fts MATCH ? ORDER BY rank LIMIT 20", ftsQuery)
	if err != nil {
		log.Printf("paths_fts short-prefix search warning: %v", err)
		return
	}
	defer rows.Close()

	rankPos := 1
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			continue
		}
		scores[path] += boost / (rrfK + float64(rankPos))
		rankPos++
	}
}

// addContentFTSRRF queries sqvect's chunks_fts table for content keyword matches.
// Documents are scored by the average BM25 of their top contentTopK chunks (k-MAX):
// respects IDF like pure MAX but rewards documents with multiple strong matches.
// Aggregation is done in Go because bm25() cannot be used inside SQLite CTEs.
func (s *Engine) addContentFTSRRF(ctx context.Context, query string, scores map[string]float64) {
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
		SELECT json_extract(e.metadata, '$.path'), -bm25(chunks_fts)
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 1000
	`, ftsQuery)
	if err != nil {
		log.Printf("chunks_fts search warning: %v", err)
		return
	}
	defer rows.Close()

	docScores := topKAvg(rows, contentTopK)
	applyDocRRF(docScores, scores, contentKeywordBoost)
}

// addContentFTSPhraseRRF queries content for the query as an FTS5 phrase
// (words adjacent, in order). Documents are scored by top-K chunk average (k-MAX).
// Only runs for multi-word queries.
func (s *Engine) addContentFTSPhraseRRF(ctx context.Context, query string, scores map[string]float64) {
	words := strings.Fields(query)
	if len(words) < 2 {
		return
	}
	escaped := strings.ReplaceAll(query, `"`, `""`)
	ftsQuery := `"` + escaped + `"`

	rows, err := s.sqlDB.QueryContext(ctx, `
		SELECT json_extract(e.metadata, '$.path'), -bm25(chunks_fts)
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 500
	`, ftsQuery)
	if err != nil {
		log.Printf("chunks_fts phrase search warning: %v", err)
		return
	}
	defer rows.Close()

	docScores := topKAvg(rows, contentTopK)
	applyDocRRF(docScores, scores, contentPhraseBoost)
}

// addContentFTSFuzzyRRF runs a short-prefix content FTS5 query using 3-char
// anchors. A chunk containing "Albert" and "Camus" is found by "alb"* AND "cam"*
// even when the query is "albret camuls". Weighted at 0.8x to stay below the
// exact-keyword signal but above noise.
func (s *Engine) addContentFTSFuzzyRRF(ctx context.Context, query string, scores map[string]float64) {
	words := strings.Fields(strings.ToLower(query))
	anchors := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		escaped := strings.ReplaceAll(w[:3], `"`, `""`)
		anchors = append(anchors, `"`+escaped+`"*`)
	}
	if len(anchors) == 0 {
		return
	}
	ftsQuery := strings.Join(anchors, " ") // AND semantics

	rows, err := s.sqlDB.QueryContext(ctx, `
		SELECT json_extract(e.metadata, '$.path'), -bm25(chunks_fts)
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 500
	`, ftsQuery)
	if err != nil {
		log.Printf("chunks_fts fuzzy search warning: %v", err)
		return
	}
	defer rows.Close()

	docScores := topKAvg(rows, contentTopK)
	applyDocRRF(docScores, scores, contentFuzzyBoost)
}

// topKAvg reads (path, score) rows, keeps the top-k scores per document,
// and returns a map of path → average of those top-k scores.
// rows must be ordered best-first (ORDER BY bm25 ASC, since bm25 is negative).
func topKAvg(rows *sql.Rows, k int) map[string]float64 {
	type docAcc struct {
		sum   float64
		count int
	}
	acc := make(map[string]*docAcc)
	counts := make(map[string]int) // total chunks seen per doc (for top-k gating)
	for rows.Next() {
		var path string
		var score float64
		if err := rows.Scan(&path, &score); err != nil {
			continue
		}
		counts[path]++
		if counts[path] > k {
			continue // already have k chunks for this doc
		}
		d := acc[path]
		if d == nil {
			d = &docAcc{}
			acc[path] = d
		}
		d.sum += score
		d.count++
	}
	result := make(map[string]float64, len(acc))
	for path, d := range acc {
		if d.count > 0 {
			result[path] = d.sum / float64(d.count)
		}
	}
	return result
}

// applyDocRRF sorts documents by their aggregated score, then applies RRF
// (boost / (rrfK + rank)) to the shared scores map.
func applyDocRRF(docScores map[string]float64, scores map[string]float64, boost float64) {
	type entry struct {
		path  string
		score float64
	}
	ranked := make([]entry, 0, len(docScores))
	for path, score := range docScores {
		ranked = append(ranked, entry{path, score})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})
	for i, e := range ranked {
		scores[e.path] += boost / (rrfK + float64(i+1))
	}
}

// addFilenameBoosts applies score bonuses based on how many query words appear
// in the filename. Filenames are URL-decoded ("Albert%20Camus.png" → "Albert Camus.png")
// and split into words so multi-word coverage is measured correctly.
//
// Scoring:
//   - All query words present in filename words → +0.6 (full-name exact match)
//   - Per matched word: exact → +0.30, prefix → +0.10, substring → +0.04
//   - Multi-word coverage bonus: +0.30 × (matched − 1) for each word beyond first
//
// This ensures "Albert Camus.txt" and "Albert%20Camus.png" (both words) rank
// above "Albert.txt" (one word) for the query "Albert Camus".
func addFilenameBoosts(query string, scores map[string]float64) {
	queryWords := splitWords(query)
	if len(queryWords) == 0 {
		return
	}

	for path := range scores {
		rawName := filepath.Base(path)
		// URL-decode so "Albert%20Camus.png" becomes "Albert Camus.png"
		if decoded, err := url.PathUnescape(rawName); err == nil {
			rawName = decoded
		}
		name := strings.ToLower(rawName)
		ext := filepath.Ext(name)
		nameNoExt := strings.TrimSuffix(name, ext)
		nameWords := splitWords(nameNoExt)

		// Build a set of filename words for O(1) lookup
		nameWordSet := make(map[string]bool, len(nameWords))
		for _, nw := range nameWords {
			nameWordSet[nw] = true
		}

		// Per-word matching: score each query word against all filename words.
		// filenameMultiWordBonus scales with (matched-1) so that files sharing more
		// query words always rank higher than files sharing fewer — no flat ceiling.
		matched := 0
		for _, qw := range queryWords {
			if len(qw) < 2 {
				continue
			}
			for _, nw := range nameWords {
				if nw == qw {
					scores[path] += filenameWordBoost
					matched++
					break
				} else if strings.HasPrefix(nw, qw) || strings.HasPrefix(qw, nw) {
					scores[path] += filenamePrefixBoost
					matched++
					break
				} else if strings.Contains(nw, qw) || strings.Contains(qw, nw) {
					scores[path] += filenameSubstrBoost
					matched++
					break
				}
			}
		}
		// Coverage bonus: grows with number of matched words, rewarding files whose
		// names share more of the query (e.g. "Albert Camus.jpg" vs "Albert.txt").
		if matched >= 2 {
			scores[path] += filenameMultiWordBonus * float64(matched-1)
		}
	}
}

// computeTrigrams returns the set of character 3-grams for s.
func computeTrigrams(s string) map[string]struct{} {
	tg := make(map[string]struct{}, len(s))
	for i := 0; i+3 <= len(s); i++ {
		tg[s[i:i+3]] = struct{}{}
	}
	return tg
}

// trigramJaccard returns the Jaccard similarity (0–1) between two trigram sets.
func trigramJaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for t := range a {
		if _, ok := b[t]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// splitWords tokenises a filename or query into lowercase word tokens,
// splitting on separators common in file paths (space, dash, underscore, dot, slash).
func splitWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == ' ' || r == '/'
	})
}

// levenshtein returns the edit distance between a and b.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i, ca := range a {
		curr[0] = i + 1
		for j, cb := range b {
			cost := 1
			if ca == cb {
				cost = 0
			}
			del := curr[j] + 1
			ins := prev[j+1] + 1
			sub := prev[j] + cost
			if del < ins {
				if del < sub {
					curr[j+1] = del
				} else {
					curr[j+1] = sub
				}
			} else {
				if ins < sub {
					curr[j+1] = ins
				} else {
					curr[j+1] = sub
				}
			}
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// wordEditSim returns 1 - normalised Levenshtein distance (range 0–1).
// "albret" vs "albert" → 0.67; "camuls" vs "camus" → 0.83.
func wordEditSim(a, b string) float64 {
	dist := levenshtein(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	if maxLen == 0 {
		return 1.0
	}
	return 1.0 - float64(dist)/float64(maxLen)
}

// addFuzzyPathBoosts applies word-level edit-distance similarity between each
// query word and each word in the candidate filename. Full-string trigrams miss
// transposition typos like "albret"→"albert"; word-level Levenshtein catches them.
func addFuzzyPathBoosts(query string, scores map[string]float64) {
	if len(scores) == 0 {
		return
	}
	queryWords := splitWords(query)
	if len(queryWords) == 0 {
		return
	}

	for path := range scores {
		pathWords := splitWords(filepath.Base(path))
		if len(pathWords) == 0 {
			continue
		}
		totalSim := 0.0
		for _, qw := range queryWords {
			if len(qw) < 3 {
				continue
			}
			best := 0.0
			for _, pw := range pathWords {
				if len(pw) < 3 {
					continue
				}
				if sim := wordEditSim(qw, pw); sim > best {
					best = sim
				}
			}
			totalSim += best
		}
		avgSim := totalSim / float64(len(queryWords))
		if avgSim >= fuzzyMinWordSim {
			scores[path] += fuzzyPathBoost * avgSim
		}
	}
}

// sortResults sorts SearchResults by score descending.
func sortResults(results []SearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
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
