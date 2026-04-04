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

	// rerankerTopN is how many results from the RRF stage are passed to the
	// cross-encoder reranker. Everything beyond this position is returned as-is.
	rerankerTopN        = 20
	rerankerMaxToks     = 8192 // jina-reranker-turbo context length
	snippetWindowChars  = 800  // bytes per extracted window ≈ 200 subword tokens

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
	rerankerSession   *ort.DynamicAdvancedSession // ms-marco cross-encoder, nil if absent
	rerankerTok       *tokenizers.Tokenizer       // WordPiece tokenizer for reranker
	textCollectionID  int                         // cached collection ID for text embeddings
	imageCollectionID int                         // cached collection ID for image embeddings
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
		"PRAGMA busy_timeout=10000",
	} {
		if _, err := sqlDB.Exec(pragma); err != nil {
			log.Printf("pragma warning: %v", err)
		}
	}
	// Allow parallel SQLite readers across goroutines (WAL supports concurrent reads).
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)

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

	// Load the cross-encoder reranker (optional — search still works without it).
	// Expects reranker.onnx and tokenizer.json next to the running executable.
	var rerankerSession *ort.DynamicAdvancedSession
	var rerankerTok *tokenizers.Tokenizer
	rerankerOnnx, rerankerTokPath := findReranker()
	if rerankerOnnx != "" && rerankerTokPath != "" {
		rtok, err := tokenizers.FromFile(rerankerTokPath)
		if err != nil {
			log.Printf("reranker tokenizer load warning (reranking disabled): %v", err)
		} else {
			ropts, err := ort.NewSessionOptions()
			if err != nil {
				rtok.Close()
				log.Printf("reranker session opts warning (reranking disabled): %v", err)
			} else {
				defer ropts.Destroy()
				rsess, err := ort.NewDynamicAdvancedSession(
					rerankerOnnx,
					[]string{"input_ids", "attention_mask"},
					[]string{"logits"},
					ropts,
				)
				if err != nil {
					rtok.Close()
					log.Printf("reranker session load warning (reranking disabled): %v", err)
				} else {
					rerankerSession = rsess
					rerankerTok = rtok
					log.Printf("reranker loaded: %s", rerankerOnnx)
				}
			}
		}
	}

	return &Engine{
		db:                db,
		sqlDB:             sqlDB,
		staticEmb:         staticEmb,
		clipTok:           clipTok,
		clipTextSession:   clipTextSession,
		clipVisionSession: clipVisionSession,
		rerankerSession:   rerankerSession,
		rerankerTok:       rerankerTok,
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
	if s.rerankerSession != nil {
		if err := s.rerankerSession.Destroy(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.rerankerTok != nil {
		s.rerankerTok.Close()
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
		// CLIP positional embeddings have a fixed size of clipCtxLen (77).
		// The Add node requires input_ids to match that length exactly — pad with zeros.
		if len(ids64) < clipCtxLen {
			padded := make([]int64, clipCtxLen)
			copy(padded, ids64)
			ids64 = padded
		}

		inputIDs, err := ort.NewTensor(ort.NewShape(1, clipCtxLen), ids64)
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
// ext/ctime/atime are populated on insert; ctime is intentionally not overwritten on conflict
// (creation time doesn't change), while atime and ext are updated to stay current.
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
		INSERT INTO files(path, hash, mtime, size, content_indexed, ext, ctime, atime)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			hash=excluded.hash, mtime=excluded.mtime, size=excluded.size, content_indexed=1,
			ext=excluded.ext, atime=excluded.atime`)
	// ctime is intentionally omitted from the UPDATE: creation time never changes.
	if err != nil {
		return err
	}
	defer stmt.Close()

	for i := range filePaths {
		ext := strings.ToLower(filepath.Ext(filePaths[i]))
		ctime, atime := fileExtraTimes(filePaths[i])
		if _, err := stmt.ExecContext(ctx, filePaths[i], fileHashes[i], fileMtimes[i], fileSizes[i], ext, ctime, atime); err != nil {
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
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO paths_fts(searchable, path) VALUES (?, ?)")
	if err != nil {
		tx.Rollback()
		log.Printf("paths_fts: failed to prepare stmt: %v", err)
		return
	}
	defer stmt.Close()
	for _, p := range paths {
		decoded := p
		if d, err := url.PathUnescape(p); err == nil {
			decoded = d
		}
		if _, err := stmt.ExecContext(ctx, decoded, p); err != nil {
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
			content_indexed  INTEGER NOT NULL DEFAULT 0,
			ext              TEXT    NOT NULL DEFAULT '',
			ctime            INTEGER NOT NULL DEFAULT 0,
			atime            INTEGER NOT NULL DEFAULT 0
		);
	`); err != nil {
		return fmt.Errorf("failed to create index tables: %w", err)
	}

	// Auto-migrate schema backfill for old DBs
	s.sqlDB.ExecContext(ctx, "ALTER TABLE files ADD COLUMN ext TEXT NOT NULL DEFAULT ''")
	s.sqlDB.ExecContext(ctx, "ALTER TABLE files ADD COLUMN ctime INTEGER NOT NULL DEFAULT 0")
	s.sqlDB.ExecContext(ctx, "ALTER TABLE files ADD COLUMN atime INTEGER NOT NULL DEFAULT 0")


	var exists int
	s.sqlDB.QueryRowContext(ctx, "SELECT 1 FROM sqlite_master WHERE type='table' AND name='paths_fts'").Scan(&exists)
	if exists == 1 {
		var hasSearchable int
		s.sqlDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('paths_fts') WHERE name='searchable'").Scan(&hasSearchable)
		if hasSearchable == 0 {
			if _, err := s.sqlDB.ExecContext(ctx, `
				DROP TABLE paths_fts;
				CREATE VIRTUAL TABLE paths_fts USING fts5(searchable, path UNINDEXED);
			`); err != nil {
				return err
			}
			rows, err := s.sqlDB.QueryContext(ctx, "SELECT path FROM files")
			if err == nil {
				defer rows.Close()
				tx, _ := s.sqlDB.BeginTx(ctx, nil)
				stmt, _ := tx.PrepareContext(ctx, "INSERT INTO paths_fts(searchable, path) VALUES (?, ?)")
				for rows.Next() {
					var p string
					rows.Scan(&p)
					decoded := p
					if d, err := url.PathUnescape(p); err == nil {
						decoded = d
					}
					stmt.ExecContext(ctx, decoded, p)
				}
				stmt.Close()
				tx.Commit()
			}
		}
	} else {
		if _, err := s.sqlDB.ExecContext(ctx, `
			CREATE VIRTUAL TABLE paths_fts USING fts5(searchable, path UNINDEXED);
		`); err != nil {
			return err
		}
	}
	return nil
}

// IndexMetadata inserts file paths, mtimes, sizes, and file-system metadata
// (extension, creation time, last-access time) into the files table without
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

	filesStmt, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO files(path, hash, mtime, size, content_indexed, ext, ctime, atime)
		VALUES (?, 0, ?, ?, 0, ?, ?, ?)`)
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
		ext := strings.ToLower(filepath.Ext(paths[i]))
		ctime, atime := fileExtraTimes(paths[i])
		if _, err := filesStmt.ExecContext(ctx, paths[i], mtimes[i], sizes[i], ext, ctime, atime); err != nil {
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
	Path     string
	Score    float64
	Size     int64
	Modified string
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

	pq := ParseQuery(query)
	query = pq.Text

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

	// Vector search both collections. Images are capped lower than text: they add
	// semantic coverage but shouldn't flood rankings for text-heavy queries.
	textResults, err := s.db.Vector().Search(ctx, textQueryVec, core.SearchOptions{
		Collection: textCollection,
		TopK:       200,
	})
	if err != nil {
		log.Printf("text search error: %v", err)
	}

	imageResults, err := s.db.Vector().Search(ctx, imageQueryVec, core.SearchOptions{
		Collection: imageCollection,
		TopK:       50,
	})
	if err != nil {
		log.Printf("image search error: %v", err)
	}

	// Phase 1: run independent scoring signals in parallel, each writing to its own map.
	// Path FTS, content FTS, and vector results have no inter-dependencies.
	const numSigGroups = 3
	sigCh := make(chan map[string]float64, numSigGroups)
	var sigWg sync.WaitGroup

	// Group A: path FTS signals (4 queries on paths_fts)
	sigWg.Add(1)
	go func() {
		defer sigWg.Done()
		m := make(map[string]float64)
		s.addPathFTSScores(ctx, query, m, 3.0)
		s.addPathFTSAllWords(ctx, query, m, 4.0)
		s.addPathFTSPrefixScores(ctx, query, m, 1.5)
		s.addPathFTSShortPrefixScores(ctx, query, m, 1.0)
		sigCh <- m
	}()

	// Group B: content FTS signals; fuzzy only runs when exact search finds few docs.
	sigWg.Add(1)
	go func() {
		defer sigWg.Done()
		m := make(map[string]float64)
		s.addContentFTSPhraseRRF(ctx, query, m)
		s.addContentFTSRRF(ctx, query, m)
		if len(m) < 5 {
			s.addContentFTSFuzzyRRF(ctx, query, m)
		}
		sigCh <- m
	}()

	// Group C: vector result iteration (in-memory, no I/O)
	sigWg.Add(1)
	go func() {
		defer sigWg.Done()
		m := make(map[string]float64)
		for i, res := range textResults {
			m[res.Metadata["path"]] += 1.0 / (rrfK + float64(i+1))
		}
		for i, res := range imageResults {
			m[res.Metadata["path"]] += 1.0 / (rrfK + float64(i+1))
		}
		sigCh <- m
	}()

	go func() { sigWg.Wait(); close(sigCh) }()

	scores := make(map[string]float64)
	for m := range sigCh {
		for path, score := range m {
			scores[path] += score
		}
	}

	// Phase 2: filename/fuzzy boosts require the merged scores map from phase 1.
	addFilenameBoosts(query, scores)
	addFuzzyPathBoosts(query, scores)

	results := make([]SearchResult, 0, len(scores))
	for path, score := range scores {
		var size int64
		var mod string
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
			mod = info.ModTime().Format(time.RFC3339)
		}
		results = append(results, SearchResult{
			Path:     path,
			Score:    score,
			Size:     size,
			Modified: mod,
		})
	}
	sortResults(results)
	results = s.rerank(query, results)
	results = s.applyFilters(results, pq)

	log.Printf("Search total: %v (%s)", time.Since(t0), getStats(cpu0, t0))
	return results, nil
}

// TextSearch performs search over text embeddings only.
// Signals: path FTS5 exact+prefix (3x/1.5x weighted RRF) > text vector RRF > content FTS5 RRF > filename/fuzzy boosts.
func (s *Engine) TextSearch(query string) ([]SearchResult, error) {
	cpu0 := getCPUTime()
	t0 := time.Now()
	ctx := context.Background()

	pq := ParseQuery(query)
	query = pq.Text

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

	// Phase 1: parallel scoring signals.
	tsSigCh := make(chan map[string]float64, 3)
	var tsSigWg sync.WaitGroup

	tsSigWg.Add(1)
	go func() {
		defer tsSigWg.Done()
		m := make(map[string]float64)
		s.addPathFTSScores(ctx, query, m, 3.0)
		s.addPathFTSAllWords(ctx, query, m, 4.0)
		s.addPathFTSPrefixScores(ctx, query, m, 1.5)
		s.addPathFTSShortPrefixScores(ctx, query, m, 1.0)
		tsSigCh <- m
	}()

	tsSigWg.Add(1)
	go func() {
		defer tsSigWg.Done()
		m := make(map[string]float64)
		s.addContentFTSPhraseRRF(ctx, query, m)
		s.addContentFTSRRF(ctx, query, m)
		if len(m) < 5 {
			s.addContentFTSFuzzyRRF(ctx, query, m)
		}
		tsSigCh <- m
	}()

	tsSigWg.Add(1)
	go func() {
		defer tsSigWg.Done()
		m := make(map[string]float64)
		for i, res := range results {
			m[res.Metadata["path"]] += 1.0 / (rrfK + float64(i+1))
		}
		tsSigCh <- m
	}()

	go func() { tsSigWg.Wait(); close(tsSigCh) }()

	scores := make(map[string]float64)
	for m := range tsSigCh {
		for path, score := range m {
			scores[path] += score
		}
	}

	// Phase 2: filename/fuzzy boosts require the merged scores map from phase 1.
	addFilenameBoosts(query, scores)
	addFuzzyPathBoosts(query, scores)

	out := make([]SearchResult, 0, len(scores))
	for path, score := range scores {
		var size int64
		var mod string
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
			mod = info.ModTime().Format(time.RFC3339)
		}
		out = append(out, SearchResult{
			Path:     path,
			Score:    score,
			Size:     size,
			Modified: mod,
		})
	}
	sortResults(out)
	out = s.rerank(query, out)
	out = s.applyFilters(out, pq)

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

		var size int64
		var mod string
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
			mod = info.ModTime().Format(time.RFC3339)
		}

		out = append(out, SearchResult{
			Path:     path,
			Score:    res.Score,
			Size:     size,
			Modified: mod,
		})
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
		decoded := newPath
		if d, err := url.PathUnescape(newPath); err == nil {
			decoded = d
		}
		tx.ExecContext(ctx, "INSERT INTO paths_fts(searchable, path) VALUES (?, ?)", decoded, newPath)
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
		LIMIT 400
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
		LIMIT 100
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

// buildFTSQuery returns an FTS5 keyword OR query for the given text, e.g.
// "albert camus" → `"albert" OR "camus"`. Used to fetch the most relevant
// chunk per document for the reranker.
func buildFTSQuery(query string) string {
	words := strings.Fields(query)
	quoted := make([]string, 0, len(words))
	for _, w := range words {
		if w != "" {
			quoted = append(quoted, `"`+strings.ReplaceAll(w, `"`, `""`)+`"`)
		}
	}
	if len(quoted) == 0 {
		return `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
	}
	return strings.Join(quoted, " OR ")
}

// extractSnippets returns up to maxWindows windows of snippetWindowChars bytes from
// content, each centered on a cluster of query-word occurrences. When no query term
// is found the beginning of the content is returned. Overlapping windows are skipped.
func extractSnippets(content, query string, maxWindows int) []string {
	if len(content) == 0 || maxWindows <= 0 {
		return nil
	}
	if len(content) <= snippetWindowChars {
		return []string{content}
	}
	lc := strings.ToLower(content)
	var positions []int
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if len(word) < 2 {
			continue
		}
		off := 0
		for {
			idx := strings.Index(lc[off:], word)
			if idx < 0 {
				break
			}
			positions = append(positions, off+idx)
			off += idx + len(word)
		}
	}
	if len(positions) == 0 {
		return []string{content[:snippetWindowChars]}
	}
	sort.Ints(positions)
	var out []string
	covered := -1
	for _, pos := range positions {
		if pos < covered {
			continue
		}
		start := pos - snippetWindowChars/2
		if start < 0 {
			start = 0
		}
		end := start + snippetWindowChars
		if end > len(content) {
			end = len(content)
			start = end - snippetWindowChars
			if start < 0 {
				start = 0
			}
		}
		out = append(out, content[start:end])
		covered = end
		if len(out) >= maxWindows {
			break
		}
	}
	return out
}

// rerank re-scores the top rerankerTopN text results using the cross-encoder.
// Images are partitioned out before reranking (cross-encoder needs text) and
// re-inserted after by their original RRF rank, so they compete fairly with
// reranked text rather than being stranded at the bottom by score-range mismatch.
// No-ops if the reranker session was not loaded.
func (s *Engine) rerank(query string, results []SearchResult) []SearchResult {
	if s.rerankerSession == nil || len(results) == 0 {
		return results
	}

	n := len(results)
	if n > rerankerTopN {
		n = rerankerTopN
	}
	top := results[:n]
	rest := results[n:]

	// Fetch up to 3 BM25-ranked snippets per document across all top-N paths in one query.
	// snippet() extracts a ~64-token window around the actual match inside each chunk,
	// so the reranker sees the relevant excerpt rather than an arbitrary prefix.
	// Multiple snippets per doc cover spread-out occurrences; we take the max reranker
	// score across snippets as the document score.
	const maxSnippetsPerDoc = 3
	docSnippets := make(map[string][]string, n) // path → ordered snippets
	basenames := make(map[string]string, n)

	// Build path → decoded basename map and collect unique paths.
	paths := make([]string, 0, n)
	seen := make(map[string]bool, n)
	for _, r := range top {
		if seen[r.Path] {
			continue
		}
		seen[r.Path] = true
		paths = append(paths, r.Path)
		base := filepath.Base(r.Path)
		if decoded, err := url.PathUnescape(base); err == nil {
			base = decoded
		}
		basenames[r.Path] = base
	}

	ftsQuery := buildFTSQuery(query)
	placeholders := strings.Repeat("?,", len(paths))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, 1+len(paths))
	args = append(args, ftsQuery)
	for _, p := range paths {
		args = append(args, p)
	}
	// Fetch up to maxSnippetsPerDoc best BM25 chunks per path; extractSnippets will then
	// find all query-term positions within each chunk and extract 800-char windows around
	// them, covering spread-out occurrences without truncating to an arbitrary prefix.
	rows, err := s.sqlDB.QueryContext(context.Background(), `
		SELECT json_extract(e.metadata, '$.path'), content
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		  AND json_extract(e.metadata, '$.path') IN (`+placeholders+`)
		  AND length(content) > 0
		ORDER BY bm25(chunks_fts)
		LIMIT ?
	`, append(args, len(paths)*maxSnippetsPerDoc)...)
	if err == nil {
		for rows.Next() {
			var path, content string
			if rows.Scan(&path, &content) == nil {
				remaining := maxSnippetsPerDoc - len(docSnippets[path])
				if remaining > 0 {
					windows := extractSnippets(content, query, remaining)
					docSnippets[path] = append(docSnippets[path], windows...)
				}
			}
		}
		rows.Close()
	}
	// Fallback for paths with no FTS match (images, empty files): use filename only.

	// Partition: image files are held aside at their original rank and re-inserted
	// after reranking. Mixing image RRF scores (~0.016) with cross-encoder logits
	// (~-8 to +8) in the same sort would always sink images to the bottom.
	// All files (including images) get a docText entry with at least the filename,
	// so the partition is based on file type, not docText presence.
	type withRank struct {
		result   SearchResult
		origRank int
	}
	var textItems []withRank
	var nonTextItems []withRank
	for i, r := range top {
		if IsImageFile(r.Path) {
			nonTextItems = append(nonTextItems, withRank{r, i})
		} else {
			textItems = append(textItems, withRank{r, i})
		}
	}

	// Encode all (query, snippet) pairs across all text items into a single batch.
	// Each document may have up to maxSnippetsPerDoc snippets; we run them all through
	// the reranker and take the max logit as the document score.
	type scored struct {
		result SearchResult
		score  float32
	}
	rerankedText := make([]scored, 0, len(textItems))

	qEnc := s.rerankerTok.EncodeWithOptions(query, false)
	qIDs := qEnc.IDs

	type pairTokens struct {
		ids  []int64
		mask []int64
	}
	// pairs holds one entry per (doc, snippet); pairDoc maps pair index → textItems index.
	pairs := make([]pairTokens, 0, len(textItems)*maxSnippetsPerDoc)
	pairDoc := make([]int, 0, len(textItems)*maxSnippetsPerDoc)

	encodePair := func(text string) pairTokens {
		dEnc := s.rerankerTok.EncodeWithOptions(text, false)
		dIDs := dEnc.IDs
		total := 1 + len(qIDs) + 2 + len(dIDs) + 1
		ids64 := make([]int64, 0, total)
		mask64 := make([]int64, 0, total)
		push := func(id int64) { ids64 = append(ids64, id); mask64 = append(mask64, 1) }
		push(0) // <s>
		for _, id := range qIDs {
			push(int64(id))
		}
		push(2) // </s>
		push(2) // </s> (RoBERTa double separator)
		for _, id := range dIDs {
			push(int64(id))
		}
		push(2) // </s>
		if len(ids64) > rerankerMaxToks {
			ids64 = ids64[:rerankerMaxToks]
			mask64 = mask64[:rerankerMaxToks]
		}
		return pairTokens{ids64, mask64}
	}

	for i, item := range textItems {
		snippets := docSnippets[item.result.Path]
		if len(snippets) == 0 {
			// No FTS match (e.g. directory, empty file): use filename as sole snippet.
			snippets = []string{"Filename: " + basenames[item.result.Path]}
		}
		log.Printf("reranker [%s] %d snippet(s):", basenames[item.result.Path], len(snippets))
		for si, snip := range snippets {
			log.Printf("  snippet[%d]: %s", si, snip)
			pairs = append(pairs, encodePair("Filename: "+basenames[item.result.Path]+"\n"+snip))
			pairDoc = append(pairDoc, i)
		}
	}

	if len(pairs) > 0 {
		// Find max sequence length for zero-padding. Snippets are short (~64 FTS tokens
		// ≈ 400 chars ≈ 100 subword tokens) so variance across pairs is low.
		maxSeqLen := 0
		for _, p := range pairs {
			if len(p.ids) > maxSeqLen {
				maxSeqLen = len(p.ids)
			}
		}

		nPairs := len(pairs)
		flatIDs := make([]int64, nPairs*maxSeqLen)
		flatMask := make([]int64, nPairs*maxSeqLen)
		for i, p := range pairs {
			copy(flatIDs[i*maxSeqLen:], p.ids)
			copy(flatMask[i*maxSeqLen:], p.mask)
		}

		shape := ort.NewShape(int64(nPairs), int64(maxSeqLen))
		tIDs, err1 := ort.NewTensor(shape, flatIDs)
		tMask, err2 := ort.NewTensor(shape, flatMask)
		tOut, err3 := ort.NewEmptyTensor[float32](ort.NewShape(int64(nPairs), 1))
		if err1 != nil || err2 != nil || err3 != nil {
			if tIDs != nil {
				tIDs.Destroy()
			}
			if tMask != nil {
				tMask.Destroy()
			}
			if tOut != nil {
				tOut.Destroy()
			}
			for _, item := range textItems {
				rerankedText = append(rerankedText, scored{item.result, float32(item.result.Score)})
			}
		} else {
			runErr := s.rerankerSession.Run(
				[]ort.Value{tIDs, tMask},
				[]ort.Value{tOut},
			)
			logits := tOut.GetData() // []float32 of length nPairs
			tIDs.Destroy()
			tMask.Destroy()
			tOut.Destroy()

			if runErr != nil {
				log.Printf("reranker batch inference warning: %v", runErr)
				for _, item := range textItems {
					rerankedText = append(rerankedText, scored{item.result, float32(item.result.Score)})
				}
			} else {
				// Aggregate per-document: take the max logit across all its snippets.
				docMax := make([]float32, len(textItems))
				for i := range docMax {
					docMax[i] = -1e9
				}
				for pi, logit := range logits {
					di := pairDoc[pi]
					if logit > docMax[di] {
						docMax[di] = logit
					}
				}
				// Blend sigmoid(reranker logit) with normalised RRF score.
				// Pure reranker score fails when the extracted snippet doesn't contain
				// the full query context (e.g. Algiers.txt snippet missing "camus"):
				// the reranker scores it low even though RRF ranked it high.
				// sigmoid maps logits to [0,1]; RRF is normalised to [0,1] by maxRRF.
				// 50/50 blend lets a strong RRF signal preserve rank for files where
				// the snippet underrepresents relevance.
				maxRRF := 1e-9
				for _, item := range textItems {
					if item.result.Score > maxRRF {
						maxRRF = item.result.Score
					}
				}
				for i, item := range textItems {
					rerankerProb := 1.0 / (1.0 + math.Exp(float64(-docMax[i]))) // sigmoid → [0,1]
					rrfNorm := item.result.Score / maxRRF                         // normalised RRF → [0,1]
					blended := float32(0.5*rerankerProb + 0.5*rrfNorm)
					r := item.result
					r.Score = float64(blended)
					log.Printf("reranker score %.4f (logit=%.4f rrf=%.4f)  %s", blended, docMax[i], item.result.Score, item.result.Path)
					rerankedText = append(rerankedText, scored{r, blended})
				}
			}
		}
	}

	sort.Slice(rerankedText, func(i, j int) bool {
		return rerankedText[i].score > rerankedText[j].score
	})

	// Merge reranked text and non-text back together.
	// Non-text items re-enter at their original rank positions so that a highly
	// relevant image (rank 2 by RRF) doesn't fall below all text results.
	textIdx := 0
	nonIdx := 0
	out := make([]SearchResult, 0, len(results))
	for i := 0; i < n; i++ {
		// If the next non-text item originally held this rank, insert it now.
		if nonIdx < len(nonTextItems) && nonTextItems[nonIdx].origRank == i {
			out = append(out, nonTextItems[nonIdx].result)
			nonIdx++
		} else if textIdx < len(rerankedText) {
			out = append(out, rerankedText[textIdx].result)
			textIdx++
		}
	}
	// Flush any remaining items (shouldn't happen but guards against off-by-one).
	for ; textIdx < len(rerankedText); textIdx++ {
		out = append(out, rerankedText[textIdx].result)
	}
	for ; nonIdx < len(nonTextItems); nonIdx++ {
		out = append(out, nonTextItems[nonIdx].result)
	}
	out = append(out, rest...)
	log.Printf("reranker final order:")
	for i, r := range out {
		if i >= rerankerTopN {
			break
		}
		log.Printf("  #%d  %.4f  %s", i+1, r.Score, r.Path)
	}
	return out
}

// sortResults sorts SearchResults by score descending.
func sortResults(results []SearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
}

// findReranker locates reranker.onnx and its tokenizer.json by checking next to
// the executable then the current working directory. Returns empty strings if absent.
func findReranker() (onnxPath, tokPath string) {
	const onnxName = "reranker.onnx"
	const tokName = "tokenizer.json"

	dirs := []string{}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, cwd)
	}

	for _, dir := range dirs {
		op := filepath.Join(dir, onnxName)
		tp := filepath.Join(dir, tokName)
		if _, err := os.Stat(op); err != nil {
			continue
		}
		if _, err := os.Stat(tp); err != nil {
			continue
		}
		return op, tp
	}
	return "", ""
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
