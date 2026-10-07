// Package shared provides embedding generation and vector search functionality
// using onnxruntime_go (ONNX inference) and sqvect (SQLite vector DB).
// The Engine type owns local inference sessions, SQLite metadata, vector
// storage, and ranking helpers used by the desktop app and API server.
package shared

/*
#cgo LDFLAGS: -L${SRCDIR}/.. -ltokenizers -lkreuzberg_ffi -lws2_32 -lntdll -luserenv -lbcrypt -ladvapi32
*/
import "C"

import (
	"bytes"
	"context"
	crand "crypto/rand"
	"database/sql"
	"encoding/binary"
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
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"net/url"

	"github.com/daulet/tokenizers"
	"github.com/liliang-cn/sqvect/v2/pkg/core"
	"github.com/liliang-cn/sqvect/v2/pkg/index"
	"github.com/liliang-cn/sqvect/v2/pkg/quantization"
	ort "github.com/yalue/onnxruntime_go"
	_ "golang.org/x/image/webp"
	_ "modernc.org/sqlite"
)

const (
	textCollection  = "text"
	imageCollection = "image"

	textEmbedDim  = 256 // truncated from model's native dim
	textNativeDim = 384 // BERT model hidden size
	textMaxSeqLen = 512 // max_position_embeddings
	imageEmbedDim = 256 // truncated from CLIP's 512
	clipEmbedDim  = 512 // CLIP native output dimension
	clipCtxLen    = 77  // CLIP text context_length
	visionSize    = 256 // CLIP vision input size

	// rerankerTopN is how many results from the RRF stage are passed to the
	// cross-encoder reranker. Everything beyond this position is returned as-is.
	rerankerTopN       = 8
	rerankerMaxToks    = 8192 // jina-reranker-turbo context length
	snippetWindowChars = 800  // bytes per extracted window ≈ 200 subword tokens

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
	db                *core.SQLiteStore
	sqlDB             *sql.DB                     // separate connection for the files table
	textStatic        *StaticEmbedder             // fast static embedder (e.g. text/dd)
	textSession       *ort.DynamicAdvancedSession // BERT text encoder (text/model.onnx) fallback
	textTok           *tokenizers.Tokenizer       // tokenizer for the BERT text encoder
	clipTok           *tokenizers.Tokenizer
	clipTextSession   *ort.DynamicAdvancedSession
	clipVisionSession *ort.DynamicAdvancedSession
	rerankerSession   *ort.DynamicAdvancedSession // ms-marco cross-encoder, nil if absent
	rerankerTok       *tokenizers.Tokenizer       // WordPiece tokenizer for reranker
	textCollectionID  int                         // cached collection ID for text embeddings
	imageCollectionID int                         // cached collection ID for image embeddings
	vDB               *sql.DB                     // connection to vectors.db
	initTableOnce     sync.Once                   // ensures InitIndexTables runs at most once per Engine
	writeChan         chan WriteOperation
	writerWg          sync.WaitGroup

	// Ranking infrastructure — initialised after InitIndexTables succeeds.
	Weights     *WeightStore // hot-reloaded ranking weights from search_config
	optimizerMu sync.RWMutex
	optimizer   *TelemetryOptimizer

	gpuMutex  sync.Mutex // surgically wraps session.Run for DirectML stability
	Hardware  HardwareConfig
	closeOnce sync.Once
}

type WriteOpType int

const (
	OpIndexText WriteOpType = iota
	OpIndexImage
	OpIndexMetadata
	OpMarkContentIndexed
	OpIndexPathsFTS
	OpDeletePaths
	OpRenamePaths
	OpResetContentIndex
	OpSubmitFeedback
	OpFlush
)

type WriteOperation struct {
	Op            WriteOpType
	Paths         []string
	FilePaths     []string
	FileHashes    []int64
	FileMtimes    []int64
	FileSizes     []int64
	FileCtimes    []int64
	FileAtimes    []int64
	Contents      []string
	SqEmbs        []*core.Embedding
	CompletePaths []string
	OldPaths      []string
	NewPaths      []string
	Done          chan struct{}

	// Feedback fields (OpSubmitFeedback)
	FeedbackSessionID string
	FeedbackQuery     string
	FeedbackPath      string
	FeedbackRank      int
	FeedbackScore     float64
	FeedbackValue     int
}

// New initializes the Engine with the given database path and ONNX model paths.
// textModelPath and imageModelPath should point to directories containing the ONNX model files.
func New(dbPath, textModelPath, imageModelPath string, hw HardwareConfig) (*Engine, error) {
	dir := filepath.Dir(dbPath)
	base := filepath.Base(dbPath)
	vectorsBase := "vectors.db"
	if base != "filosophy.db" {
		vectorsBase = strings.TrimSuffix(base, filepath.Ext(base)) + "_vectors" + filepath.Ext(base)
	}
	vectorsDBPath := filepath.Join(dir, vectorsBase)

	log.Println("[Engine 1] Opening SQL databases...")
	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sql database: %w", err)
	}

	vDB, err := sql.Open("sqlite", vectorsDBPath)
	if err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("failed to open vectors sql database: %w", err)
	}

	_, availBytes := GetMemoryInfo()
	availMB := availBytes / (1024 * 1024)
	cacheKB := availMB * 5
	if cacheKB < 20000 {
		cacheKB = 20000 // minimum 20MB cache
	}

	tempStore := "MEMORY"
	if availBytes < 6*1024*1024*1024 { // switch to FILE temp_store if under 6GB available
		tempStore = "FILE"
	}

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		fmt.Sprintf("PRAGMA temp_store=%s", tempStore),
		"PRAGMA mmap_size=536870912",
		fmt.Sprintf("PRAGMA cache_size=-%d", cacheKB),
		"PRAGMA busy_timeout=10000",
		// Checkpoint every 500 pages (~2 MB) so the WAL never grows huge.
		// A large WAL causes multi-minute recovery on next open after a crash.
		"PRAGMA wal_autocheckpoint=500",
	}

	for _, dbConn := range []*sql.DB{sqlDB, vDB} {
		for _, pragma := range pragmas {
			if _, err := dbConn.Exec(pragma); err != nil {
				log.Printf("pragma warning: %v", err)
			}
		}
		dbConn.SetMaxOpenConns(4)
		dbConn.SetMaxIdleConns(4)
	}

	// Speed up all path-based lookups and joins in vectors.db
	vDB.Exec("CREATE INDEX IF NOT EXISTS idx_embeddings_path ON embeddings(json_extract(metadata, '$.path'))")

	log.Println("[Engine 2] Opening sqvect database at", vectorsDBPath)
	hnswCfg := core.DefaultHNSWConfig()
	hnswCfg.Enabled = true
	cfg := core.Config{
		Path:         vectorsDBPath,
		VectorDim:    textEmbedDim,
		SimilarityFn: core.CosineSimilarity,
		AutoDimAdapt: core.SmartAdapt,
		IndexType:    core.IndexTypeHNSW,
		HNSW:         hnswCfg,
		Quantization: core.QuantizationConfig{
			Enabled: true,
			Type:    "scalar", // SQ8: uint8 min-max scalar quantization
			NBits:   8,
		},
	}
	db, err := core.NewWithConfig(cfg)
	if err != nil {
		sqlDB.Close()
		vDB.Close()
		return nil, fmt.Errorf("failed to create vector store: %w", err)
	}
	ctx := context.Background()
	if err := db.Init(ctx); err != nil {
		sqlDB.Close()
		vDB.Close()
		return nil, fmt.Errorf("failed to initialize vector store: %w", err)
	}

	// Create collections for text and image embeddings
	log.Println("[Engine 3] Ensuring vector collections exist...")
	if _, err := db.CreateCollection(ctx, textCollection, textEmbedDim); err != nil {
		log.Printf("text collection: %v", err)
	}
	if _, err := db.CreateCollection(ctx, imageCollection, imageEmbedDim); err != nil {
		log.Printf("image collection: %v", err)
	}

	// Initialize ONNX Runtime
	ortPath := findOnnxRuntime()
	ort.SetSharedLibraryPath(ortPath)
	useGPU := hasDirectMLProvider()
	if useGPU {
		log.Printf("DirectML runtime verified — GPU acceleration enabled")
	}
	log.Println("[Engine 4] Initializing ONNX Runtime environment...")
	if err := ort.InitializeEnvironment(); err != nil {
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to initialize onnxruntime (dll=%s): %w", ortPath, err)
	}

	// Prefer text/dd static embedder if present (much faster zero-copy mmap)
	actualTextModelPath := textModelPath
	if _, err := os.Stat(filepath.Join(actualTextModelPath, "model.safetensors")); err != nil {
		if _, err := os.Stat(filepath.Join(textModelPath, "dd", "model.safetensors")); err == nil {
			actualTextModelPath = filepath.Join(textModelPath, "dd")
		}
	}

	var textStatic *StaticEmbedder
	var textSession *ort.DynamicAdvancedSession
	var textTok *tokenizers.Tokenizer

	safetensorsPath := filepath.Join(actualTextModelPath, "model.safetensors")
	tokPath := filepath.Join(actualTextModelPath, "tokenizer.json")
	if _, err := os.Stat(safetensorsPath); err == nil {
		log.Println("[Engine 5] Loading fast text/dd static embedder from", actualTextModelPath)
		var err error
		textStatic, err = LoadStaticEmbedder(safetensorsPath, tokPath)
		if err != nil {
			log.Printf("[Engine 5] Warning: failed to load static embedder (%v), falling back to ONNX", err)
		} else {
			log.Printf("[Engine 5] Fast text/dd static embedder loaded successfully (%d dimensions)", textStatic.Dim())
		}
	}

	log.Println("[Engine 6] Loading CLIP tokenizer from", imageModelPath)
	clipTok, err := tokenizers.FromFile(filepath.Join(imageModelPath, "tokenizer.json"))
	if err != nil {
		if textStatic != nil {
			textStatic.Close()
		}
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to load CLIP tokenizer: %w", err)
	}

	if textStatic == nil {
		// Load the BERT text encoder tokenizer (text/tokenizer.json).
		log.Println("[Engine 5] Loading text encoder tokenizer from", actualTextModelPath)
		textTok, err = tokenizers.FromFile(filepath.Join(actualTextModelPath, "tokenizer.json"))
		if err != nil {
			clipTok.Close()
			ort.DestroyEnvironment()
			sqlDB.Close()
			db.Close()
			return nil, fmt.Errorf("failed to load text tokenizer: %w", err)
		}

		// Create session options for the BERT text encoder.
		textOpts, err := ort.NewSessionOptions()
		if err != nil {
			textTok.Close()
			clipTok.Close()
			ort.DestroyEnvironment()
			sqlDB.Close()
			db.Close()
			return nil, fmt.Errorf("failed to create text session options: %w", err)
		}
		defer textOpts.Destroy()

		if hw.DirectMLKneecap {
			textOpts.SetMemPattern(false)
			textOpts.SetCpuMemArena(false)
		}

		if useGPU && tryAppendDirectML(textOpts) {
			log.Printf("BERT text session: DirectML GPU enabled")
		}

		log.Println("[Engine 5c] Initializing BERT text session...")
		textSession, err = ort.NewDynamicAdvancedSession(
			filepath.Join(actualTextModelPath, "model.onnx"),
			[]string{"input_ids", "attention_mask", "token_type_ids"},
			[]string{"last_hidden_state"},
			textOpts,
		)
		if err != nil {
			textTok.Close()
			clipTok.Close()
			ort.DestroyEnvironment()
			sqlDB.Close()
			db.Close()
			return nil, fmt.Errorf("failed to create BERT text session: %w", err)
		}
	}

	// Create session options for CLIP text encoder.
	opts, err := ort.NewSessionOptions()
	if err != nil {
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to create session options: %w", err)
	}
	defer opts.Destroy()

	if hw.DirectMLKneecap {
		opts.SetMemPattern(false)
		opts.SetCpuMemArena(false)
	}

	if useGPU && tryAppendDirectML(opts) {
		log.Printf("CLIP text session: DirectML GPU enabled")
	}

	log.Println("[Engine 7] Initializing CLIP text session...")
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

	if hw.DirectMLKneecap {
		visionOpts.SetMemPattern(false)
		visionOpts.SetCpuMemArena(false)
	}

	// Vision models are large, allow ONNX to use its default multi-threading
	// because Go is NOT actually processing them concurrently (IndexBatch is guarded by a Mutex).
	if useGPU && tryAppendDirectML(visionOpts) {
		log.Printf("CLIP vision session: DirectML GPU enabled")
	}

	log.Println("[Engine 8] Initializing CLIP vision session...")
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
	textCol, err := db.GetCollection(ctx2, textCollection)
	if err != nil {
		clipVisionSession.Destroy()
		clipTextSession.Destroy()

		clipTok.Close()
		ort.DestroyEnvironment()
		sqlDB.Close()
		db.Close()
		return nil, fmt.Errorf("failed to get text collection: %w", err)
	}
	imageCol, err := db.GetCollection(ctx2, imageCollection)
	if err != nil {
		clipVisionSession.Destroy()
		clipTextSession.Destroy()

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
				if hw.DirectMLKneecap {
					ropts.SetMemPattern(false)
					ropts.SetCpuMemArena(false)
				}
				if useGPU && tryAppendDirectML(ropts) {
					log.Printf("reranker session: DirectML GPU enabled")
				}
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
					log.Println("[Engine 9] Reranker model loaded successfully")
					log.Printf("reranker loaded: %s", rerankerOnnx)
				}
			}
		}
	}

	engine := &Engine{
		db:                db,
		sqlDB:             sqlDB,
		vDB:               vDB,
		textStatic:        textStatic,
		textSession:       textSession,
		textTok:           textTok,
		clipTok:           clipTok,
		clipTextSession:   clipTextSession,
		clipVisionSession: clipVisionSession,
		rerankerSession:   rerankerSession,
		rerankerTok:       rerankerTok,
		textCollectionID:  textCol.ID,
		imageCollectionID: imageCol.ID,
		writeChan:         make(chan WriteOperation, 10000),
		Hardware:          hw,
	}
	go engine.runWriter()
	return engine, nil
}

func (s *Engine) vectorIDsForPaths(ctx context.Context, paths []string) ([]string, error) {
	ids := make([]string, 0)
	seen := make(map[string]struct{})
	for _, path := range paths {
		rows, err := s.vDB.QueryContext(ctx,
			"SELECT id FROM embeddings WHERE json_extract(metadata, '$.path') = ?", path)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return ids, nil
}

func (s *Engine) allVectorIDs(ctx context.Context) ([]string, error) {
	rows, err := s.vDB.QueryContext(ctx, "SELECT id FROM embeddings")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Engine) runWriter() {
	s.writerWg.Add(1)
	defer s.writerWg.Done()

	var batch []WriteOperation
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	opsSinceCheckpoint := 0

	flush := func() {
		if len(batch) == 0 {
			return
		}

		ctx := context.Background()
		tx, err := s.sqlDB.BeginTx(ctx, nil)
		if err != nil {
			log.Printf("writer begin tx error: %v", err)
			batch = batch[:0]
			return
		}

		var sqEmbsBatch []*core.Embedding
		var deletePathIDs []string
		var completePaths []string
		vectorReadErr := error(nil)
		metadataWriteErr := error(nil)

		for _, op := range batch {
			switch op.Op {
			case OpIndexText, OpIndexImage:
				const query = `
					INSERT INTO files(path, hash, mtime, size, content_indexed, ext, ctime, atime)
					VALUES (?, ?, ?, ?, 0, ?, ?, ?)
					ON CONFLICT(path) DO UPDATE SET
						hash=excluded.hash, mtime=excluded.mtime, size=excluded.size, content_indexed=0,
						ext=excluded.ext, atime=excluded.atime`
				metadataValid := len(op.FilePaths) == len(op.FileHashes) && len(op.FilePaths) == len(op.FileMtimes) &&
					len(op.FilePaths) == len(op.FileSizes) && len(op.FilePaths) == len(op.FileCtimes) && len(op.FilePaths) == len(op.FileAtimes)
				if !metadataValid && len(op.FilePaths) > 0 {
					log.Printf("writer received incomplete file metadata for %d paths", len(op.FilePaths))
					vectorReadErr = fmt.Errorf("incomplete file metadata")
				}
				for i := range op.FilePaths {
					if !metadataValid {
						break
					}
					ext := strings.ToLower(filepath.Ext(op.FilePaths[i]))
					if _, err := tx.ExecContext(ctx, query, op.FilePaths[i], op.FileHashes[i], op.FileMtimes[i], op.FileSizes[i], ext, op.FileCtimes[i], op.FileAtimes[i]); err != nil {
						metadataWriteErr = err
						log.Printf("writer pending metadata error for %s: %v", op.FilePaths[i], err)
					}
				}
				ids, err := s.vectorIDsForPaths(ctx, op.FilePaths)
				if err != nil {
					vectorReadErr = err
					log.Printf("writer vector lookup error: %v", err)
				} else {
					deletePathIDs = append(deletePathIDs, ids...)
				}
				sqEmbsBatch = append(sqEmbsBatch, op.SqEmbs...)
				completePaths = append(completePaths, op.CompletePaths...)

			case OpIndexMetadata:
				const qFiles = `
					INSERT OR IGNORE INTO files(path, hash, mtime, size, content_indexed, ext, ctime, atime)
					VALUES (?, 0, ?, ?, 0, ?, ?, ?)`
				const qFTS = `INSERT OR IGNORE INTO paths_fts(searchable, path) VALUES (?, ?)`
				for i := range op.Paths {
					ext := strings.ToLower(filepath.Ext(op.Paths[i]))
					tx.ExecContext(ctx, qFiles, op.Paths[i], op.FileMtimes[i], op.FileSizes[i], ext, op.FileCtimes[i], op.FileAtimes[i])
					tx.ExecContext(ctx, qFTS, pathUnescape(op.Paths[i]), op.Paths[i])
				}

			case OpMarkContentIndexed:
				metadataValid := len(op.Paths) == len(op.FileHashes) && len(op.Paths) == len(op.FileMtimes) &&
					len(op.Paths) == len(op.FileSizes) && len(op.Paths) == len(op.FileCtimes) && len(op.Paths) == len(op.FileAtimes)
				for i, p := range op.Paths {
					if _, err := tx.ExecContext(ctx, "UPDATE files SET content_indexed=0 WHERE path=?", p); err != nil {
						metadataWriteErr = err
						log.Printf("writer pending metadata error for %s: %v", p, err)
					}
					if metadataValid && (op.FileMtimes[i] != 0 || op.FileSizes[i] != 0 || op.FileCtimes[i] != 0 || op.FileAtimes[i] != 0) {
						if _, err := tx.ExecContext(ctx, `UPDATE files SET hash=?, mtime=?, size=?, ext=?, ctime=?, atime=?, content_indexed=0 WHERE path=?`,
							op.FileHashes[i], op.FileMtimes[i], op.FileSizes[i], strings.ToLower(filepath.Ext(p)), op.FileCtimes[i], op.FileAtimes[i], p); err != nil {
							metadataWriteErr = err
							log.Printf("writer file metadata error for %s: %v", p, err)
						}
					}
				}
				ids, err := s.vectorIDsForPaths(ctx, op.Paths)
				if err != nil {
					vectorReadErr = err
					log.Printf("writer vector lookup error: %v", err)
				} else {
					deletePathIDs = append(deletePathIDs, ids...)
				}
				completePaths = append(completePaths, op.Paths...)

			case OpIndexPathsFTS:
				const query = `INSERT INTO paths_fts(searchable, path) VALUES (?, ?)`
				for _, p := range op.Paths {
					tx.ExecContext(ctx, "DELETE FROM paths_fts WHERE path = ?", p)
					tx.ExecContext(ctx, query, pathUnescape(p), p)
				}

			case OpDeletePaths:
				ids, err := s.vectorIDsForPaths(ctx, op.Paths)
				if err != nil {
					vectorReadErr = err
					log.Printf("writer delete lookup error: %v", err)
				} else {
					deletePathIDs = append(deletePathIDs, ids...)
				}
				for _, path := range op.Paths {
					tx.ExecContext(ctx, "DELETE FROM files WHERE path = ?", path)
					tx.ExecContext(ctx, "DELETE FROM paths_fts WHERE path = ?", path)
				}

			case OpRenamePaths:
				for i, oldPath := range op.OldPaths {
					newPath := op.NewPaths[i]
					s.vDB.ExecContext(ctx, `UPDATE embeddings SET metadata = json_set(metadata, '$.path', ?) WHERE json_extract(metadata, '$.path') = ?`, newPath, oldPath)
					tx.ExecContext(ctx, "UPDATE files SET path = ? WHERE path = ?", newPath, oldPath)
					tx.ExecContext(ctx, "DELETE FROM paths_fts WHERE path = ?", oldPath)
					tx.ExecContext(ctx, "INSERT INTO paths_fts(searchable, path) VALUES (?, ?)", pathUnescape(newPath), newPath)
				}

			case OpResetContentIndex:
				if _, err := tx.ExecContext(ctx, `UPDATE files SET content_indexed = 0, hash = 0`); err != nil {
					metadataWriteErr = err
					log.Printf("writer reset metadata error: %v", err)
				}
				if _, err := tx.ExecContext(ctx, `DELETE FROM paths_fts`); err != nil {
					metadataWriteErr = err
					log.Printf("writer reset paths error: %v", err)
				}
				ids, err := s.allVectorIDs(ctx)
				if err != nil {
					vectorReadErr = err
					log.Printf("writer reset vector lookup error: %v", err)
				} else {
					deletePathIDs = append(deletePathIDs, ids...)
				}

			case OpSubmitFeedback:
				tx.ExecContext(ctx, `INSERT INTO search_feedback
					(session_id, query, result_path, result_rank, result_score, feedback, created_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?)`,
					op.FeedbackSessionID, op.FeedbackQuery, op.FeedbackPath,
					op.FeedbackRank, op.FeedbackScore, op.FeedbackValue, time.Now().Unix())
			}
		}

		if err := tx.Commit(); err != nil {
			log.Printf("writer commit error: %v", err)
			batch = batch[:0]
			return
		}

		vectorWriteErr := vectorReadErr
		if metadataWriteErr != nil {
			vectorWriteErr = metadataWriteErr
		}
		if vectorWriteErr == nil && len(deletePathIDs) > 0 {
			if err := s.db.DeleteBatch(ctx, deletePathIDs); err != nil {
				vectorWriteErr = err
				log.Printf("writer sqvect delete error: %v", err)
			}
		}
		if vectorWriteErr == nil && len(sqEmbsBatch) > 0 {
			if err := s.db.UpsertBatch(ctx, sqEmbsBatch); err != nil {
				vectorWriteErr = err
				log.Printf("writer sqvect upsert error: %v", err)
			} else {
				opsSinceCheckpoint += len(sqEmbsBatch)
				if opsSinceCheckpoint >= 2000 {
					s.sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE);")
					s.vDB.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE);")
					opsSinceCheckpoint = 0
				}
			}
		}
		if vectorWriteErr == nil && len(completePaths) > 0 {
			markTx, err := s.sqlDB.BeginTx(ctx, nil)
			if err != nil {
				log.Printf("writer completion begin tx error: %v", err)
			} else {
				for _, path := range completePaths {
					if _, err := markTx.ExecContext(ctx, "UPDATE files SET content_indexed=1 WHERE path=?", path); err != nil {
						log.Printf("writer completion update error for %s: %v", path, err)
					}
				}
				if err := markTx.Commit(); err != nil {
					log.Printf("writer completion commit error: %v", err)
				}
			}
		}

		batch = batch[:0]
	}

	for {
		select {
		case op, ok := <-s.writeChan:
			if !ok {
				flush()
				return
			}
			if op.Op == OpFlush {
				flush()
				if op.Done != nil {
					close(op.Done)
				}
				continue
			}
			batch = append(batch, op)
			if len(batch) >= 50 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

// SetOptimizer publishes the optimizer so the app layer can query its status.
func (s *Engine) SetOptimizer(o *TelemetryOptimizer) {
	s.optimizerMu.Lock()
	s.optimizer = o
	s.optimizerMu.Unlock()
}

// GetOptimizerStatus returns the optimizer's last-run snapshot, or nil if
// the optimizer hasn't been started yet.
func (s *Engine) GetOptimizerStatus() *OptimizerStatus {
	s.optimizerMu.RLock()
	optimizer := s.optimizer
	s.optimizerMu.RUnlock()
	if optimizer == nil {
		return nil
	}
	st := optimizer.Status()
	return &st
}

// SubmitFeedback routes a feedback write through the serialized write channel
// so it never contends with indexing transactions for the SQLite write lock.
func (s *Engine) SubmitFeedback(sessionID, query, path string, rank int, score float64, feedback int) {
	log.Printf("[Feedback] Submitting vote %d for '%s' (query: %q, rank: %d, score: %.3f)", feedback, path, query, rank, score)
	s.writeChan <- WriteOperation{
		Op:                OpSubmitFeedback,
		FeedbackSessionID: sessionID,
		FeedbackQuery:     query,
		FeedbackPath:      path,
		FeedbackRank:      rank,
		FeedbackScore:     score,
		FeedbackValue:     feedback,
	}
}

// Flush deterministically blocks until all queued operations are committed.
func (s *Engine) Flush() error {
	done := make(chan struct{})
	s.writeChan <- WriteOperation{
		Op:   OpFlush,
		Done: done,
	}
	<-done
	return nil
}

// Close shuts down the Engine, releasing all resources.
// GetSQLDB returns the underlying *sql.DB for the main filosophy.db connection.
// Used by the REST API layer to write directly to search_config without going
// through the Engine's write-serialisation channel.
func (s *Engine) GetSQLDB() *sql.DB {
	return s.sqlDB
}

// SaveVectorSnapshot serializes the in-memory HNSW index and quantizer directly
// into the index_snapshots table in vectors.db so subsequent startups load instantly
// instead of rebuilding the graph from raw vectors (~30+ minutes for large datasets).
func (s *Engine) SaveVectorSnapshot(ctx context.Context) error {
	if s.db == nil || s.vDB == nil {
		return nil
	}

	val := reflect.ValueOf(s.db)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	hnswField := val.FieldByName("hnswIndex")
	quantField := val.FieldByName("quantizer")

	if !hnswField.IsValid() {
		return fmt.Errorf("hnswIndex field not found on SQLiteStore")
	}

	hnswPtr := *(**index.HNSW)(unsafe.Pointer(hnswField.UnsafeAddr()))
	if hnswPtr == nil {
		return nil
	}

	var hBuf bytes.Buffer
	if err := hnswPtr.Save(&hBuf); err != nil {
		return fmt.Errorf("saving hnsw index: %w", err)
	}

	if _, err := s.vDB.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS index_snapshots (type TEXT PRIMARY KEY, data BLOB, created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP);"); err != nil {
		return fmt.Errorf("ensuring index_snapshots table: %w", err)
	}

	if _, err := s.vDB.ExecContext(ctx, "INSERT OR REPLACE INTO index_snapshots (type, data, created_at) VALUES (?, ?, CURRENT_TIMESTAMP)", "HNSW", hBuf.Bytes()); err != nil {
		return fmt.Errorf("storing hnsw snapshot: %w", err)
	}

	if quantField.IsValid() && !quantField.IsNil() {
		quantInterface := reflect.NewAt(quantField.Type(), unsafe.Pointer(quantField.UnsafeAddr())).Elem().Interface()
		if sq, ok := quantInterface.(*quantization.ScalarQuantizer); ok && sq != nil {
			var qBuf bytes.Buffer
			if err := sq.Save(&qBuf); err == nil {
				if _, err := s.vDB.ExecContext(ctx, "INSERT OR REPLACE INTO index_snapshots (type, data, created_at) VALUES (?, ?, CURRENT_TIMESTAMP)", "QUANTIZER", qBuf.Bytes()); err != nil {
					log.Printf("[Engine] storing quantizer snapshot error: %v", err)
				}
			}
		}
	}
	log.Printf("[Engine] Successfully saved vector index snapshot (%d bytes)", hBuf.Len())
	return nil
}

func (s *Engine) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		if s.writeChan != nil {
			close(s.writeChan)
			s.writerWg.Wait()
		}

		ctx := context.Background()

		// 1. Explicitly save HNSW index and quantizer snapshot to index_snapshots table
		if saveErr := s.SaveVectorSnapshot(ctx); saveErr != nil {
			log.Printf("[Engine] SaveVectorSnapshot error on close: %v", saveErr)
		}

		var errs []error
		if s.textStatic != nil {
			if err := s.textStatic.Close(); err != nil {
				errs = append(errs, err)
			}
		}
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
		if s.rerankerSession != nil {
			if err := s.rerankerSession.Destroy(); err != nil {
				errs = append(errs, err)
			}
		}
		if s.rerankerTok != nil {
			s.rerankerTok.Close()
		}
		if ortErr := ort.DestroyEnvironment(); ortErr != nil {
			errs = append(errs, ortErr)
		}

		// 2. Close sqvect store (s.db) so its connection pool releases all locks
		if s.db != nil {
			if dbErr := s.db.Close(); dbErr != nil {
				errs = append(errs, dbErr)
			}
			s.db = nil
		}

		// 3. With s.db closed, run PRAGMA wal_checkpoint(TRUNCATE) on both DBs to collapse WAL files
		if s.sqlDB != nil {
			if _, chkErr := s.sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);"); chkErr != nil {
				log.Printf("[Engine] wal_checkpoint TRUNCATE sqlDB error: %v", chkErr)
			}
		}
		if s.vDB != nil {
			if _, chkErr := s.vDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);"); chkErr != nil {
				log.Printf("[Engine] wal_checkpoint TRUNCATE vDB error: %v", chkErr)
			}
		}

		// 4. Finally close s.vDB and s.sqlDB
		if s.vDB != nil {
			if vErr := s.vDB.Close(); vErr != nil {
				errs = append(errs, vErr)
			}
			s.vDB = nil
		}
		if s.sqlDB != nil {
			if sqlErr := s.sqlDB.Close(); sqlErr != nil {
				errs = append(errs, sqlErr)
			}
			s.sqlDB = nil
		}

		if len(errs) > 0 {
			closeErr = fmt.Errorf("close errors: %v", errs)
		}
	})
	return closeErr
}

// TruncateWAL executes a checkpoint TRUNCATE to clear out the -wal file
// generated by batched inserts.
func (s *Engine) TruncateWAL() error {
	ctx := context.Background()
	var firstErr error
	if s.sqlDB != nil {
		if _, err := s.sqlDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
			log.Printf("WAL truncate sqlDB error: %v", err)
			firstErr = err
		}
	}
	if s.vDB != nil {
		if _, err := s.vDB.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE);"); err != nil {
			log.Printf("WAL truncate vDB error: %v", err)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// idPrefix is a session-unique 8-byte random hex string set once at startup.
// idSeq is an atomic counter for the per-session sequence number.
// Together they produce globally unique, cheaply generated IDs.
var (
	idPrefix string
	idSeq    atomic.Uint64
)

func init() {
	var b [8]byte
	if _, err := crand.Read(b[:]); err != nil {
		// Fallback: use current time nanoseconds as seed material
		ns := uint64(time.Now().UnixNano())
		binary.LittleEndian.PutUint64(b[:], ns)
	}
	idPrefix = fmt.Sprintf("%016x", b)
}

// generateID returns a unique string ID. It calls crypto/rand once per process
// (in init) and uses an atomic counter for subsequent calls — avoiding the
// per-call syscall overhead of uuid.New().
func generateID() string {
	return fmt.Sprintf("%s-%016x", idPrefix, idSeq.Add(1))
}

// ---------------------------------------------------------------------------
// Embedding helpers
// ---------------------------------------------------------------------------

func (s *Engine) embedText(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if s.textStatic != nil {
		return s.textStatic.EmbedBatch(texts, textEmbedDim), nil
	}

	const batchSize = 16
	result := make([][]float32, len(texts))

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[i:end]
		n := len(batch)

		// Tokenize all texts and find the max sequence length in this batch.
		type tokenized struct {
			ids  []uint32
			mask []int64
		}
		toks := make([]tokenized, n)
		maxLen := 0
		for j, text := range batch {
			ids, _ := s.textTok.Encode(text, true)
			if len(ids) > textMaxSeqLen {
				ids = ids[:textMaxSeqLen]
			}
			seqLen := len(ids)
			if seqLen > maxLen {
				maxLen = seqLen
			}
			mask := make([]int64, seqLen)
			for k := range mask {
				mask[k] = 1
			}
			toks[j] = tokenized{ids: ids, mask: mask}
		}
		if maxLen == 0 {
			maxLen = 1
		}

		// Build padded flat tensors [n, maxLen].
		inputIDs := make([]int64, n*maxLen)
		attentionMask := make([]int64, n*maxLen)
		tokenTypeIDs := make([]int64, n*maxLen) // all zeros for single-segment

		for j, t := range toks {
			off := j * maxLen
			for k, id := range t.ids {
				inputIDs[off+k] = int64(id)
			}
			copy(attentionMask[off:], t.mask)
			// tokenTypeIDs stays zero-filled (single sentence)
			// padding positions stay zero for input_ids, attention_mask, token_type_ids
		}

		shape := ort.NewShape(int64(n), int64(maxLen))

		idsTensor, err := ort.NewTensor(shape, inputIDs)
		if err != nil {
			return nil, fmt.Errorf("text input_ids tensor: %w", err)
		}
		maskTensor, err := ort.NewTensor(shape, attentionMask)
		if err != nil {
			idsTensor.Destroy()
			return nil, fmt.Errorf("text attention_mask tensor: %w", err)
		}
		typeTensor, err := ort.NewTensor(shape, tokenTypeIDs)
		if err != nil {
			idsTensor.Destroy()
			maskTensor.Destroy()
			return nil, fmt.Errorf("text token_type_ids tensor: %w", err)
		}

		outShape := ort.NewShape(int64(n), int64(maxLen), textNativeDim)
		outTensor, err := ort.NewEmptyTensor[float32](outShape)
		if err != nil {
			idsTensor.Destroy()
			maskTensor.Destroy()
			typeTensor.Destroy()
			return nil, fmt.Errorf("text output tensor: %w", err)
		}

		s.gpuMutex.Lock()
		runErr := s.textSession.Run(
			[]ort.Value{idsTensor, maskTensor, typeTensor},
			[]ort.Value{outTensor},
		)
		s.gpuMutex.Unlock()

		if runErr != nil {
			outTensor.Destroy()
			idsTensor.Destroy()
			maskTensor.Destroy()
			typeTensor.Destroy()
			return nil, fmt.Errorf("text inference failed: %w", runErr)
		}

		// Mean pooling over the sequence dimension, masked by attention_mask.
		data := outTensor.GetData()
		for j := 0; j < n; j++ {
			seqOff := j * maxLen * textNativeDim
			maskOff := j * maxLen
			emb := make([]float32, textNativeDim)
			var count float32
			for k := 0; k < maxLen; k++ {
				if attentionMask[maskOff+k] == 0 {
					continue
				}
				tokOff := seqOff + k*textNativeDim
				for d := 0; d < textNativeDim; d++ {
					emb[d] += data[tokOff+d]
				}
				count++
			}
			if count > 0 {
				inv := 1.0 / count
				for d := range emb {
					emb[d] *= inv
				}
			}
			// Truncate to textEmbedDim, then normalise.
			if len(emb) > textEmbedDim {
				emb = emb[:textEmbedDim]
			}
			normalize(emb)
			result[i+j] = emb
		}

		outTensor.Destroy()
		idsTensor.Destroy()
		maskTensor.Destroy()
		typeTensor.Destroy()
	}

	return result, nil
}

// embedClipText encodes text queries with the CLIP text encoder.
// Sequences are truncated to clipCtxLen (77), no padding is applied.
// Processed one-by-one as queries are typically single search terms.
func (s *Engine) embedClipText(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	const batchSize = 16
	result := make([][]float32, len(texts))

	for i := 0; i < len(texts); i += batchSize {
		end := i + batchSize
		if end > len(texts) {
			end = len(texts)
		}

		currentBatchTexts := texts[i:end]
		numTexts := len(currentBatchTexts)
		allIDs := make([]int64, int(numTexts)*clipCtxLen)

		for j, text := range currentBatchTexts {
			ids, _ := s.clipTok.Encode(text, true)
			ids64 := uint32ToInt64(ids)
			if len(ids64) > clipCtxLen {
				ids64 = ids64[:clipCtxLen]
			}
			// Copy ids into the flattened batch buffer at the correct offset
			copy(allIDs[j*clipCtxLen:], ids64)
		}

		inputIDs, err := ort.NewTensor(ort.NewShape(int64(numTexts), clipCtxLen), allIDs)
		if err != nil {
			return nil, fmt.Errorf("clip text batch input_ids tensor: %w", err)
		}

		outTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(int64(numTexts), clipEmbedDim))
		if err != nil {
			inputIDs.Destroy()
			return nil, fmt.Errorf("clip text batch output tensor: %w", err)
		}

		s.gpuMutex.Lock()
		runErr := s.clipTextSession.Run(
			[]ort.Value{inputIDs},
			[]ort.Value{outTensor},
		)
		s.gpuMutex.Unlock()

		if runErr != nil {
			outTensor.Destroy()
			inputIDs.Destroy()
			return nil, fmt.Errorf("clip text batch inference failed: %w", runErr)
		}

		data := outTensor.GetData()
		for j := 0; j < numTexts; j++ {
			emb := make([]float32, imageEmbedDim)
			offset := j * clipEmbedDim
			copy(emb, data[offset:offset+imageEmbedDim])
			normalize(emb)
			result[i+j] = emb
		}

		outTensor.Destroy()
		inputIDs.Destroy()
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

	const batchSize = 16
	pixelsPerImage := 3 * visionSize * visionSize
	result := make([][]float32, len(imagePaths))

	// Pre-initialize result with zero vectors in case of failures
	for i := range result {
		result[i] = make([]float32, imageEmbedDim)
	}

	for i := 0; i < len(imagePaths); i += batchSize {
		end := i + batchSize
		if end > len(imagePaths) {
			end = len(imagePaths)
		}

		currentBatchPaths := imagePaths[i:end]
		validIndicesInBatch := make([]int, 0, len(currentBatchPaths))
		batchPixels := make([]float32, 0, len(currentBatchPaths)*pixelsPerImage)

		for j, imgPath := range currentBatchPaths {
			pixels, err := loadAndPreprocess(imgPath)
			if err != nil {
				log.Printf("image preprocess %s: %v", imgPath, err)
				continue
			}
			validIndicesInBatch = append(validIndicesInBatch, j)
			batchPixels = append(batchPixels, pixels...)
		}

		if len(validIndicesInBatch) == 0 {
			continue
		}

		numValid := int64(len(validIndicesInBatch))
		inputTensor, err := ort.NewTensor(
			ort.NewShape(numValid, 3, int64(visionSize), int64(visionSize)), batchPixels)
		if err != nil {
			return nil, fmt.Errorf("vision batch input tensor error: %w", err)
		}

		outTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(numValid, int64(clipEmbedDim)))
		if err != nil {
			inputTensor.Destroy()
			return nil, fmt.Errorf("vision batch output tensor error: %w", err)
		}

		s.gpuMutex.Lock()
		runErr := s.clipVisionSession.Run(
			[]ort.Value{inputTensor},
			[]ort.Value{outTensor},
		)
		s.gpuMutex.Unlock()

		if runErr != nil {
			outTensor.Destroy()
			inputTensor.Destroy()
			return nil, fmt.Errorf("vision batch inference failed: %w", runErr)
		}

		data := outTensor.GetData()
		for batchIdx, originalIdx := range validIndicesInBatch {
			emb := make([]float32, imageEmbedDim)
			offset := batchIdx * clipEmbedDim
			copy(emb, data[offset:offset+imageEmbedDim])
			normalize(emb)
			result[i+originalIdx] = emb
		}

		outTensor.Destroy()
		inputTensor.Destroy()
	}

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

// pathUnescape decodes percent-encoded characters in a path.
// It skips the url.PathUnescape allocation entirely when the path contains no '%',
// which covers the vast majority of Windows file paths.
func pathUnescape(p string) string {
	if !strings.ContainsRune(p, '%') {
		return p
	}
	if d, err := url.PathUnescape(p); err == nil {
		return d
	}
	return p
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
	duration := time.Since(startTime).Seconds()
	if duration <= 0 {
		return "CPU: 0.0%"
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
	return fmt.Sprintf("CPU: %.1f%%", cpuUsage)
}

// ---------------------------------------------------------------------------
// Indexing
// ---------------------------------------------------------------------------

// IndexText generates text embeddings and stores them in the vector DB.
// contents and paths must have the same length.
// filePaths/fileHashes are optional file-level metadata for deduplication.
// Embedding generation runs outside the write lock; only the DB writes are locked.
func (s *Engine) IndexText(contents, paths []string, filePaths []string, fileHashes, fileMtimes, fileSizes, fileCtimes, fileAtimes []int64, completePaths []string) error {
	// Generate embeddings outside the lock — ONNX session is thread-safe behind gpuMutex.
	embs, err := s.embedText(contents)
	if err != nil {
		return fmt.Errorf("text embedding failed: %w", err)
	}

	// Build sqvect embedding objects (no shared mutable state).
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

	s.writeChan <- WriteOperation{
		Op:            OpIndexText,
		FilePaths:     filePaths,
		FileHashes:    fileHashes,
		FileMtimes:    fileMtimes,
		FileSizes:     fileSizes,
		FileCtimes:    fileCtimes,
		FileAtimes:    fileAtimes,
		SqEmbs:        sqEmbs,
		CompletePaths: completePaths,
	}
	return nil
}

// IndexImage generates image embeddings from image file paths and stores them.
// imagePaths are the filesystem paths to load images from.
// paths are the logical paths stored as metadata.
// Embedding generation runs outside the write lock; only the DB writes are locked.
func (s *Engine) IndexImage(imagePaths, paths []string, filePaths []string, fileHashes, fileMtimes, fileSizes, fileCtimes, fileAtimes []int64, completePaths []string) error {
	// Generate image embeddings outside the lock.
	// In practice IndexImage is always called from a single draining goroutine
	// (HandleChunk/DrainRemaining), never concurrently, so clipVisionSession is safe.
	embs, err := s.embedImage(imagePaths)
	if err != nil {
		return fmt.Errorf("image embedding failed: %w", err)
	}

	// Build sqvect embedding objects (no shared mutable state).
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

	s.writeChan <- WriteOperation{
		Op:            OpIndexImage,
		FilePaths:     filePaths,
		FileHashes:    fileHashes,
		FileMtimes:    fileMtimes,
		FileSizes:     fileSizes,
		FileCtimes:    fileCtimes,
		FileAtimes:    fileAtimes,
		SqEmbs:        sqEmbs,
		CompletePaths: completePaths,
	}
	return nil
}

// IndexPathsFTS inserts unique paths into the paths_fts FTS5 table.
// Called from the processor layer after indexing embeddings.
func (s *Engine) IndexPathsFTS(paths []string) {
	if len(paths) == 0 {
		return
	}
	s.writeChan <- WriteOperation{
		Op:    OpIndexPathsFTS,
		Paths: paths,
	}
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
	s.sqlDB.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_files_content_indexed ON files(content_indexed)")
	if s.vDB != nil {
		s.vDB.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_embeddings_path ON embeddings(json_extract(metadata, '$.path'))")
	}

	// search_config: key/value store for tunable ranking weights.
	// All weights are loaded at startup and hot-reloaded every 30s.
	if _, err := s.sqlDB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS search_config (
			key   TEXT PRIMARY KEY,
			value REAL NOT NULL
		);
	`); err != nil {
		return fmt.Errorf("failed to create search_config table: %w", err)
	}

	// Insert defaults — INSERT OR IGNORE so existing tuned values are never overwritten.
	defaultConfigs := [][2]any{
		{"w_path_fts", 3.0},         // RRF weight for path FTS exact/all-word signals
		{"w_path_prefix", 1.5},      // RRF weight for path FTS prefix signals
		{"w_semantic_text", 1.0},    // RRF weight for text vector signal
		{"w_semantic_image", 1.1},   // RRF weight for image vector signal
		{"w_content_fts", 1.0},      // RRF weight for content FTS signal
		{"w_filename", 1.0},         // Multiplier applied to filename/fuzzy boosts
		{"w_recency", 0.3},          // Additive recency score weight
		{"recency_half_life", 30.0}, // Days for recency score to decay to 37%
		{"rrf_k", 60.0},             // RRF smoothing constant
		{"min_score", 0.15},         // Minimum score threshold (used by REST API)
		{"rerank_top_n", 8.0},       // How many results to pass to cross-encoder reranker
		{"w_reranker_blend", 0.5},   // Blend alpha: 0=pure RRF, 1=pure reranker
		{"path_only_cap", 0.12},     // Score cap for path-only results (no content signal)
		{"use_new_pipeline", 1.0},   // Feature flag: 0=old pipeline, 1=new weighted pipeline
	}
	for _, kv := range defaultConfigs {
		s.sqlDB.ExecContext(ctx, `INSERT OR IGNORE INTO search_config(key, value) VALUES (?, ?)`, kv[0], kv[1])
	}

	// Existing databases may still carry the old default (0). Flip that default
	// once, while preserving the user's ability to turn the pipeline off later.
	var migratedNewPipelineDefault int
	_ = s.sqlDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_config WHERE key = 'migrated_new_pipeline_default'`).Scan(&migratedNewPipelineDefault)
	if migratedNewPipelineDefault == 0 {
		if _, err := s.sqlDB.ExecContext(ctx, `UPDATE search_config SET value = 1 WHERE key = 'use_new_pipeline' AND value = 0`); err != nil {
			return fmt.Errorf("failed to migrate use_new_pipeline default: %w", err)
		}
		if _, err := s.sqlDB.ExecContext(ctx, `INSERT OR IGNORE INTO search_config(key, value) VALUES ('migrated_new_pipeline_default', 1)`); err != nil {
			return fmt.Errorf("failed to mark use_new_pipeline migration: %w", err)
		}
	}

	if err := migrateRerankerBreadth(ctx, s.sqlDB); err != nil {
		return err
	}

	// search_feedback: telemetry for ranking weight adjustment.
	// feedback: +1 = thumbs up, -1 = thumbs down, 0 = click-through.
	if _, err := s.sqlDB.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS search_feedback (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id   TEXT    NOT NULL,
			query        TEXT    NOT NULL,
			result_path  TEXT    NOT NULL,
			result_rank  INTEGER NOT NULL,
			result_score REAL    NOT NULL,
			feedback     INTEGER NOT NULL,
			created_at   INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_feedback_query ON search_feedback(query);
		CREATE INDEX IF NOT EXISTS idx_feedback_path  ON search_feedback(result_path);
	`); err != nil {
		return fmt.Errorf("failed to create search_feedback table: %w", err)
	}

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
					stmt.ExecContext(ctx, pathUnescape(p), p)
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

	// Initialise ranking infrastructure now that tables exist.
	if s.Weights == nil {
		s.Weights = NewWeightStore(s.sqlDB)
	}

	// Probe atime availability in the background so it doesn't delay startup.
	go CheckAtimeEnabled(s.sqlDB)

	return nil
}

func migrateRerankerBreadth(ctx context.Context, db *sql.DB) error {
	var migrated int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM search_config WHERE key = 'migrated_rerank_breadth'`).Scan(&migrated); err != nil {
		return err
	}
	if migrated != 0 {
		return nil
	}
	if _, err := db.ExecContext(ctx, `UPDATE search_config SET value = 8 WHERE key = 'rerank_top_n' AND value = 20`); err != nil {
		return fmt.Errorf("failed to migrate reranker breadth: %w", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT OR IGNORE INTO search_config(key, value) VALUES ('migrated_rerank_breadth', 1)`); err != nil {
		return fmt.Errorf("failed to mark reranker breadth migration: %w", err)
	}
	return nil
}

// IndexMetadata inserts file paths, mtimes, sizes, and file-system metadata
// (extension, creation time, last-access time) into the files table without
// marking them as content-indexed, and populates paths_fts so path-based search
// works immediately after Pass 1. Already-present rows are left untouched (INSERT OR IGNORE).
// ctimes and atimes are extracted from os.FileInfo by the caller — no extra os.Stat here.
func (s *Engine) IndexMetadata(paths []string, mtimes, sizes, ctimes, atimes []int64) error {
	if len(paths) == 0 {
		return nil
	}
	s.writeChan <- WriteOperation{
		Op:         OpIndexMetadata,
		Paths:      paths,
		FileMtimes: mtimes,
		FileSizes:  sizes,
		FileCtimes: ctimes,
		FileAtimes: atimes,
	}
	return nil
}

// PruneStale removes index entries for paths that no longer exist on disk.
// livePaths is the complete set of current paths from a directory walk.
func (s *Engine) PruneStale(livePaths []string) error {
	// Nothing to prune against — skip the full table scan.
	if len(livePaths) == 0 {
		return nil
	}
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

// PruneOutsideRoots deletes indexed records whose paths no longer live under
// any configured root. It is used when the user narrows the indexing scope so
// old results do not remain searchable.
func (s *Engine) PruneOutsideRoots(roots []string) error {
	if len(roots) == 0 {
		return nil
	}
	normalizedRoots := make([]string, 0, len(roots)*2)
	for _, root := range roots {
		clean := filepath.Clean(root)
		if clean == "." || clean == "" {
			continue
		}
		for _, candidate := range []string{clean, filepath.ToSlash(clean)} {
			candidate = strings.ToLower(candidate)
			if !strings.HasSuffix(candidate, string(filepath.Separator)) && !strings.HasSuffix(candidate, "/") {
				candidate += "/"
			}
			normalizedRoots = append(normalizedRoots, candidate)
		}
	}
	if len(normalizedRoots) == 0 {
		return nil
	}

	paths, err := s.allIndexedPaths()
	if err != nil {
		return err
	}
	var outside []string
	for _, p := range paths {
		lower := strings.ToLower(filepath.ToSlash(p))
		inScope := false
		for _, root := range normalizedRoots {
			root = filepath.ToSlash(root)
			if lower == strings.TrimSuffix(root, "/") || strings.HasPrefix(lower, root) {
				inScope = true
				break
			}
		}
		if !inScope {
			outside = append(outside, p)
		}
	}
	return s.DeletePaths(outside)
}

// DeletePathsUnderDir removes all indexed records for dirPath and descendants.
// Both file metadata and path-only FTS entries are scanned because directories
// can be indexed without corresponding file records.
func (s *Engine) DeletePathsUnderDir(dirPath string) error {
	clean := filepath.Clean(dirPath)
	prefixes := []string{
		strings.TrimRight(clean, `\/`) + string(filepath.Separator),
		strings.TrimRight(filepath.ToSlash(clean), `\/`) + "/",
	}
	var matches []string
	for _, prefix := range prefixes {
		for _, table := range []string{"files", "paths_fts"} {
			rows, err := s.sqlDB.QueryContext(context.Background(), `SELECT path FROM `+table+` WHERE path = ? OR path LIKE ?`, strings.TrimRight(prefix, `\/`), prefix+"%")
			if err != nil {
				return err
			}
			for rows.Next() {
				var p string
				if err := rows.Scan(&p); err != nil {
					rows.Close()
					return err
				}
				matches = append(matches, p)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
		}
	}
	return s.DeletePaths(matches)
}

func (s *Engine) allIndexedPaths() ([]string, error) {
	seen := make(map[string]bool)
	var paths []string
	for _, table := range []string{"files", "paths_fts"} {
		rows, err := s.sqlDB.QueryContext(context.Background(), `SELECT path FROM `+table)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				rows.Close()
				return nil, err
			}
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

// MarkContentIndexed queues files with no extractable content for completion
// after any existing vectors have been removed.
func (s *Engine) MarkContentIndexed(paths []string, hashes, mtimes, sizes, ctimes, atimes []int64) error {
	if len(paths) == 0 {
		return nil
	}
	s.writeChan <- WriteOperation{
		Op:         OpMarkContentIndexed,
		Paths:      paths,
		FileHashes: hashes,
		FileMtimes: mtimes,
		FileSizes:  sizes,
		FileCtimes: ctimes,
		FileAtimes: atimes,
	}
	return nil
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

// Checkpoint runs a passive WAL checkpoint, flushing written pages back into
// the main database file. Call this periodically during long indexing runs to
// prevent the WAL from growing so large that the next startup hangs on recovery.
func (s *Engine) Checkpoint() {
	if _, err := s.sqlDB.Exec("PRAGMA wal_checkpoint(PASSIVE)"); err != nil {
		log.Printf("wal_checkpoint warning: %v", err)
	}
}

// ResetContentIndex clears content_indexed/hashes and paths_fts, forcing a full
// re-index on the next run. Called when --index is passed.
func (s *Engine) ResetContentIndex() error {
	s.writeChan <- WriteOperation{
		Op: OpResetContentIndex,
	}
	return s.Flush()
}

// ResetContentIndexForDir resets the content_indexed flag for all files under
// a given directory prefix. Used when removing a path-only restriction so that
// files in that directory become eligible for content indexing on the next pass.
func (s *Engine) ResetContentIndexForDir(dirPath string) error {
	// Normalize to forward slashes with trailing separator so the LIKE
	// pattern matches only files actually inside this directory.
	normalized := filepath.ToSlash(dirPath)
	if !strings.HasSuffix(normalized, "/") {
		normalized += "/"
	}
	prefix := normalized + "%"
	_, err := s.sqlDB.Exec(
		`UPDATE files SET content_indexed=0, hash=0 WHERE path LIKE ? OR path LIKE ?`,
		filepath.FromSlash(prefix),
		prefix,
	)
	return err
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

// RawSearchSignals holds unweighted signal maps for a query. This is used by
// the telemetry optimizer to run in-memory parameter sweeps without repeating
// vector search and reranker inference for each candidate weight set.
type RawSearchSignals struct {
	PathFTS          map[string]float64
	PathPrefix       map[string]float64
	ContentFTS       map[string]float64
	SemanticText     map[string]float64
	SemanticImage    map[string]float64
	Reranker         map[string]float64
	CodeQueryMatches map[string]bool
}

// GetRawSignals performs vector, FTS, and optional reranker scoring but returns
// raw, unweighted signal maps for each query.
func (s *Engine) GetRawSignals(query string) RawSearchSignals {
	ctx := context.Background()
	pq := ParseQuery(query)
	qText := pq.Text

	signals := RawSearchSignals{
		PathFTS:          make(map[string]float64),
		PathPrefix:       make(map[string]float64),
		ContentFTS:       make(map[string]float64),
		SemanticText:     make(map[string]float64),
		SemanticImage:    make(map[string]float64),
		Reranker:         make(map[string]float64),
		CodeQueryMatches: make(map[string]bool),
	}

	var textQueryVec []float32
	textEmbs, err := s.embedText([]string{qText})
	if err == nil {
		textQueryVec = textEmbs[0]
	}

	var textResults []core.ScoredEmbedding
	if textQueryVec != nil {
		textResults, _ = s.db.Search(ctx, textQueryVec, core.SearchOptions{
			Collection: textCollection,
			TopK:       200,
		})
	}

	var imageResults []core.ScoredEmbedding
	if clipEmbs, err := s.embedClipText([]string{qText}); err == nil && len(clipEmbs) > 0 {
		imageResults, _ = s.db.Search(ctx, clipEmbs[0], core.SearchOptions{
			Collection: imageCollection,
			TopK:       50,
		})
	}

	var sigWg sync.WaitGroup
	var mu sync.Mutex

	sigWg.Add(1)
	go func() {
		defer sigWg.Done()
		exact := make(map[string]float64)
		prefix := make(map[string]float64)
		s.addPathFTSScores(ctx, qText, exact, 1.0)
		s.addPathFTSAllWords(ctx, qText, exact, 4.0/3.0)
		s.addPathFTSPrefixScores(ctx, qText, prefix, 1.0)
		s.addPathFTSShortPrefixScores(ctx, qText, prefix, 2.0/3.0)
		s.addPathSubstringScores(ctx, qText, prefix, 0.5)
		mu.Lock()
		for p, v := range exact {
			signals.PathFTS[p] = v
		}
		for p, v := range prefix {
			signals.PathPrefix[p] = v
		}
		mu.Unlock()
	}()

	sigWg.Add(1)
	go func() {
		defer sigWg.Done()
		m := make(map[string]float64)
		phraseMatches := s.addContentFTSPhraseRRF(ctx, qText, m)
		s.addContentFTSRRF(ctx, qText, m)
		if len(m) < 5 {
			phraseMatches = mergePathMatches(phraseMatches, s.addContentFTSFuzzyRRF(ctx, qText, m))
		}
		mu.Lock()
		signals.CodeQueryMatches = phraseMatches
		for p, v := range m {
			signals.ContentFTS[p] = v
		}
		mu.Unlock()
	}()

	sigWg.Add(1)
	go func() {
		defer sigWg.Done()
		k := rrfK
		for i, res := range textResults {
			signals.SemanticText[res.Metadata["path"]] += 1.0 / (k + float64(i+1))
		}
		for i, res := range imageResults {
			signals.SemanticImage[res.Metadata["path"]] += 1.0 / (k + float64(i+1))
		}
	}()

	sigWg.Wait()

	candidateSet := make(map[string]bool)
	for _, signal := range []map[string]float64{signals.PathFTS, signals.PathPrefix, signals.ContentFTS, signals.SemanticText, signals.SemanticImage} {
		for p := range signal {
			candidateSet[p] = true
		}
	}
	candidates := make([]string, 0, len(candidateSet))
	for p := range candidateSet {
		candidates = append(candidates, p)
	}
	signals.Reranker = s.scoreRerankerCandidates(qText, candidates)
	return signals
}

func (s *Engine) Search(query string) ([]SearchResult, error) {
	ctx := context.Background()

	pq := ParseQuery(query)
	query = pq.Text

	// Load ranking weights. Falls back to hardcoded defaults if WeightStore is nil
	// (e.g., during unit tests before InitIndexTables has run).
	var w SearchWeights
	if s.Weights != nil {
		w = s.Weights.Get()
	} else {
		w = defaultWeights
	}

	k := rrfK
	if w.UseNewPipeline {
		k = w.RRFK
	}

	// Phase 1: run all 4 independent scoring channels concurrently:
	//   1. Path FTS signals
	//   2. Content FTS signals
	//   3. Text Vector Search (fast embedText + sqvect search)
	//   4. Image Vector Search (embedClipText + sqvect search)
	const numSigGroups = 4
	sigCh := make(chan map[string]float64, numSigGroups)
	var sigWg sync.WaitGroup

	contentScores := make(map[string]float64)
	codePhraseMatches := make(map[string]bool)
	var contentMu sync.Mutex

	// Channel 1: Path FTS signals
	sigWg.Add(1)
	go func() {
		defer sigWg.Done()
		m := make(map[string]float64)
		if w.UseNewPipeline {
			s.addPathFTSScores(ctx, query, m, w.WPathFTS)
			s.addPathFTSAllWords(ctx, query, m, w.WPathFTS*4.0/3.0) // all-word gets 33% more than exact
			s.addPathFTSPrefixScores(ctx, query, m, w.WPathPrefix)
			s.addPathFTSShortPrefixScores(ctx, query, m, w.WPathPrefix*2.0/3.0)
			if len(m) < 20 {
				s.addPathSubstringScores(ctx, query, m, w.WPathPrefix*0.5)
			}
		} else {
			s.addPathFTSScores(ctx, query, m, 3.0)
			s.addPathFTSAllWords(ctx, query, m, 4.0)
			s.addPathFTSPrefixScores(ctx, query, m, 1.5)
			s.addPathFTSShortPrefixScores(ctx, query, m, 1.0)
			if len(m) < 20 {
				s.addPathSubstringScores(ctx, query, m, 0.75)
			}
		}
		sigCh <- m
	}()

	// Channel 2: Content FTS signals
	sigWg.Add(1)
	go func() {
		defer sigWg.Done()
		m := make(map[string]float64)
		phraseMatches := s.addContentFTSPhraseRRF(ctx, query, m)
		s.addContentFTSRRF(ctx, query, m)
		if len(m) < 5 && len(query) >= 3 {
			phraseMatches = mergePathMatches(phraseMatches, s.addContentFTSFuzzyRRF(ctx, query, m))
		}
		// Scale content FTS signal by w_content_fts when new pipeline is active.
		if w.UseNewPipeline && w.WContentFTS != 1.0 {
			for p, v := range m {
				m[p] = v * w.WContentFTS
			}
		}
		contentMu.Lock()
		codePhraseMatches = phraseMatches
		for p, v := range m {
			contentScores[p] += v
		}
		contentMu.Unlock()
		sigCh <- m
	}()

	// Channel 3: Text Vector Search (embed + HNSW search in parallel)
	sigWg.Add(1)
	go func() {
		defer sigWg.Done()
		m := make(map[string]float64)
		wText := 1.0
		if w.UseNewPipeline {
			wText = w.WSemanticText
		}
		textEmbs, err := s.embedText([]string{query})
		if err == nil && len(textEmbs) > 0 && textEmbs[0] != nil {
			textResults, err := s.db.Search(ctx, textEmbs[0], core.SearchOptions{
				Collection: textCollection,
				TopK:       200,
			})
			if err == nil {
				for i, res := range textResults {
					m[res.Metadata["path"]] += wText / (k + float64(i+1))
				}
			}
		}
		contentMu.Lock()
		for p, v := range m {
			contentScores[p] += v
		}
		contentMu.Unlock()
		sigCh <- m
	}()

	// Channel 4: Image Vector Search (CLIP text embed + HNSW search in parallel)
	sigWg.Add(1)
	go func() {
		defer sigWg.Done()
		m := make(map[string]float64)
		wImg := 1.0
		if w.UseNewPipeline {
			wImg = w.WSemanticImg
		}
		clipEmbs, err := s.embedClipText([]string{query})
		if err == nil && len(clipEmbs) > 0 && clipEmbs[0] != nil {
			imageResults, err := s.db.Search(ctx, clipEmbs[0], core.SearchOptions{
				Collection: imageCollection,
				TopK:       50,
			})
			if err == nil {
				for i, res := range imageResults {
					m[res.Metadata["path"]] += wImg / (k + float64(i+1))
				}
			}
		}
		contentMu.Lock()
		for p, v := range m {
			contentScores[p] += v
		}
		contentMu.Unlock()
		sigCh <- m
	}()

	go func() {
		sigWg.Wait()
		close(sigCh)
	}()

	scores := make(map[string]float64)
	for m := range sigCh {
		for path, score := range m {
			scores[path] += score
		}
	}

	// Phase 2: filename/fuzzy boosts require the merged scores map from phase 1.
	// Track the filename-boost delta per path so the path-only cap can exempt
	// files that genuinely matched the query by name (for example, images).
	filenameDeltas := make(map[string]float64, len(scores))
	if w.UseNewPipeline && w.WFilename != 1.0 {
		// Capture pre-boost scores to compute the delta, then scale the delta.
		pre := make(map[string]float64, len(scores))
		for p, v := range scores {
			pre[p] = v
		}
		addFilenameBoosts(query, scores)
		addFuzzyPathBoosts(query, scores)
		for p, post := range scores {
			delta := post - pre[p]
			filenameDeltas[p] = delta * w.WFilename
			scores[p] = pre[p] + delta*w.WFilename
		}
	} else {
		pre := make(map[string]float64, len(scores))
		for p, v := range scores {
			pre[p] = v
		}
		addFilenameBoosts(query, scores)
		addFuzzyPathBoosts(query, scores)
		for p, post := range scores {
			filenameDeltas[p] = post - pre[p]
		}
	}

	// Cap path-only results. Any path with zero content signal (no vector hit,
	// no BM25 hit) can only have scored from path FTS + filename/fuzzy boosts.
	// Cap these so they can never beat a real content match — BUT exempt paths
	// that got a strong filename match (for example, images). These files
	// have no text content to generate a content signal, yet the user clearly
	// searched for them by name.
	pathOnlyScoreCap := 0.12
	if w.UseNewPipeline {
		pathOnlyScoreCap = w.PathOnlyCap
	}
	for path, score := range scores {
		if _, hasContent := contentScores[path]; !hasContent && score > pathOnlyScoreCap {
			// If filename matching contributed significantly, don't suppress.
			if filenameDeltas[path] >= filenamePrefixBoost {
				continue
			}
			scores[path] = pathOnlyScoreCap
		}
	}

	// Penalize binary/system files that have no business appearing in
	// document searches. Apply a multiplier based on extension and path prefix.
	applyNoisePenalties(scores)

	// Batch-fetch size/mtime from the files table — replaces per-result os.Stat.
	results := s.buildResultsFromScores(ctx, scores)
	sortResults(results)

	// New pipeline: apply recency boost after sort so the score change doesn't
	// affect sort order — recency adjusts absolute scores for the telemetry log
	// but we re-sort once afterwards.
	if w.UseNewPipeline && w.WRecency > 0 {
		ApplyRecencyBoost(results, w.WRecency, w.RecencyHalf)
		sortResults(results)
	}

	rerankN := rerankerTopN
	if w.RerankTopN > 0 {
		rerankN = w.RerankTopN
	}
	results = s.rerank(query, results, rerankN, w.WRerankerBlend)
	applyCodeFilePenalty(results, query, codePhraseMatches)
	results = s.applyFilters(results, pq)
	return results, nil
}

// buildResultsFromScores converts a path→score map to []SearchResult, fetching
// size and mtime from the files table in a single batched query instead of
// issuing an os.Stat syscall for each result path.
func (s *Engine) buildResultsFromScores(ctx context.Context, scores map[string]float64) []SearchResult {
	if len(scores) == 0 {
		return nil
	}
	pathList := make([]string, 0, len(scores))
	for p := range scores {
		pathList = append(pathList, p)
	}
	type fileMeta struct {
		size int64
		mod  string
	}
	meta := make(map[string]fileMeta, len(pathList))
	ph := strings.Repeat("?,", len(pathList))
	ph = ph[:len(ph)-1]
	args := make([]any, len(pathList))
	for i, p := range pathList {
		args[i] = p
	}
	rows, err := s.sqlDB.QueryContext(ctx,
		`SELECT path, size, mtime FROM files WHERE path IN (`+ph+`)`, args...)
	if err == nil {
		for rows.Next() {
			var p string
			var sz, mt int64
			if rows.Scan(&p, &sz, &mt) == nil {
				meta[p] = fileMeta{sz, time.Unix(0, mt).UTC().Format(time.RFC3339)}
			}
		}
		rows.Close()
	}
	results := make([]SearchResult, 0, len(scores))
	for path, score := range scores {
		m := meta[path]
		results = append(results, SearchResult{
			Path:     path,
			Score:    score,
			Size:     m.size,
			Modified: m.mod,
		})
	}
	return results
}

// TextSearch performs search over text embeddings only.
// Signals: path FTS5 exact+prefix (3x/1.5x weighted RRF) > text vector RRF > content FTS5 RRF > filename/fuzzy boosts.
func (s *Engine) TextSearch(query string) ([]SearchResult, error) {
	ctx := context.Background()

	pq := ParseQuery(query)
	query = pq.Text

	// Phase 1: parallel scoring signals.
	tsSigCh := make(chan map[string]float64, 3)
	var tsSigWg sync.WaitGroup
	codePhraseMatches := make(map[string]bool)

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
		phraseMatches := s.addContentFTSPhraseRRF(ctx, query, m)
		s.addContentFTSRRF(ctx, query, m)
		if len(m) < 5 && len(query) >= 3 {
			phraseMatches = mergePathMatches(phraseMatches, s.addContentFTSFuzzyRRF(ctx, query, m))
		}
		codePhraseMatches = phraseMatches
		tsSigCh <- m
	}()

	tsSigWg.Add(1)
	go func() {
		defer tsSigWg.Done()
		m := make(map[string]float64)
		embs, err := s.embedText([]string{query})
		if err == nil && len(embs) > 0 && embs[0] != nil {
			results, err := s.db.Search(ctx, embs[0], core.SearchOptions{
				Collection: textCollection,
				TopK:       200,
			})
			if err == nil {
				for i, res := range results {
					m[res.Metadata["path"]] += 1.0 / (rrfK + float64(i+1))
				}
			}
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

	out := s.buildResultsFromScores(ctx, scores)
	sortResults(out)
	rerankN := rerankerTopN
	rerankerBlend := defaultWeights.WRerankerBlend
	if s.Weights != nil {
		w := s.Weights.Get()
		rerankerBlend = w.WRerankerBlend
		if w.RerankTopN > 0 {
			rerankN = w.RerankTopN
		}
	}
	out = s.rerank(query, out, rerankN, rerankerBlend)
	applyCodeFilePenalty(out, query, codePhraseMatches)
	out = s.applyFilters(out, pq)
	return out, nil
}

// ImageSearch performs search over image embeddings only.
func (s *Engine) ImageSearch(query string) ([]SearchResult, error) {
	ctx := context.Background()

	// Encode outside the lock.
	embs, err := s.embedClipText([]string{query})
	if err != nil {
		return nil, fmt.Errorf("image query encoding failed: %w", err)
	}
	queryVec := embs[0]

	results, err := s.db.Search(ctx, queryVec, core.SearchOptions{
		Collection: imageCollection,
		TopK:       50,
	})
	if err != nil {
		return nil, fmt.Errorf("image search failed: %w", err)
	}

	// Deduplicate by path.
	seen := map[string]bool{}
	scores := make(map[string]float64, len(results))
	for _, res := range results {
		path := res.Metadata["path"]
		if !seen[path] {
			seen[path] = true
			scores[path] = float64(res.Score)
		}
	}
	return s.buildResultsFromScores(ctx, scores), nil
}

// ---------------------------------------------------------------------------
// Delete / Rename / Hash
// ---------------------------------------------------------------------------

// DeletePaths removes all embeddings for the given file paths.
func (s *Engine) DeletePaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	s.writeChan <- WriteOperation{
		Op:    OpDeletePaths,
		Paths: paths,
	}
	return s.Flush()
}

// RenamePaths updates path references in sqvect embeddings and the files table.
func (s *Engine) RenamePaths(oldPaths, newPaths []string) error {
	if len(oldPaths) == 0 {
		return nil
	}
	s.writeChan <- WriteOperation{
		Op:       OpRenamePaths,
		OldPaths: oldPaths,
		NewPaths: newPaths,
	}
	return nil
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
		// Use 4-char prefix minimum so "camus" → "camu*" only matches near-exact
		// tokens (camus, camusian…) and not unrelated "cam"-prefix words like
		// camera, camembert, camouflage.
		if len(w) < 4 {
			continue
		}
		escaped := strings.ReplaceAll(w[:4], `"`, `""`)
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

// addPathSubstringScores uses a plain SQL LIKE query on the files table to find
// paths that contain query words as substrings anywhere in the filename. This
// catches cases FTS5 misses: searching "cv" matches "mahircv.pdf" because FTS5
// only tokenizes on word boundaries and cannot find infix matches.
func (s *Engine) addPathSubstringScores(ctx context.Context, query string, scores map[string]float64, boost float64) {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return
	}
	// Build: WHERE (LOWER(path) LIKE '%word1%' OR LOWER(path) LIKE '%word2%')
	var clauses []string
	var args []any
	for _, w := range words {
		if len(w) < 2 {
			continue
		}
		// Escape LIKE wildcards in the search term itself
		escaped := strings.ReplaceAll(w, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `%`, `\%`)
		escaped = strings.ReplaceAll(escaped, `_`, `\_`)
		clauses = append(clauses, "LOWER(path) LIKE ? ESCAPE '\\'")
		args = append(args, "%"+escaped+"%")
	}
	if len(clauses) == 0 {
		return
	}
	sql := "SELECT path FROM files WHERE (" + strings.Join(clauses, " OR ") + ") LIMIT 30"
	rows, err := s.sqlDB.QueryContext(ctx, sql, args...)
	if err != nil {
		log.Printf("path substring search warning: %v", err)
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

	rows, err := s.vDB.QueryContext(ctx, `
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

// addContentFTSPhraseRRF finds exact content phrases and scores multi-word matches
// by top-K chunk average (k-MAX). Single-word matches are returned for code-query
// exemptions but do not receive a second ranking signal.
func (s *Engine) addContentFTSPhraseRRF(ctx context.Context, query string, scores map[string]float64) map[string]bool {
	words := strings.Fields(query)
	if len(words) == 0 {
		return nil
	}
	escaped := strings.ReplaceAll(query, `"`, `""`)
	ftsQuery := `"` + escaped + `"`

	rows, err := s.vDB.QueryContext(ctx, `
		SELECT json_extract(e.metadata, '$.path'), -bm25(chunks_fts)
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 500
	`, ftsQuery)
	if err != nil {
		log.Printf("chunks_fts phrase search warning: %v", err)
		return nil
	}
	defer rows.Close()

	docScores := topKAvg(rows, contentTopK)
	if len(words) >= 2 {
		applyDocRRF(docScores, scores, contentPhraseBoost)
	}
	matches := make(map[string]bool, len(docScores))
	for path := range docScores {
		matches[path] = true
	}
	return matches
}

// addContentFTSFuzzyRRF runs a short-prefix content FTS5 query using 3-char
// anchors. A chunk containing "Albert" and "Camus" is found by "alb"* AND "cam"*
// even when the query is "albret camuls". Weighted at 0.8x to stay below the
// exact-keyword signal but above noise.
func (s *Engine) addContentFTSFuzzyRRF(ctx context.Context, query string, scores map[string]float64) map[string]bool {
	words := strings.Fields(strings.ToLower(query))
	anchors := make([]string, 0, len(words))
	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		prefixLen := 3
		if len(w) >= 4 {
			prefixLen = 4
		}
		escaped := strings.ReplaceAll(w[:prefixLen], `"`, `""`)
		anchors = append(anchors, `"`+escaped+`"*`)
	}
	if len(anchors) == 0 {
		return nil
	}
	ftsQuery := strings.Join(anchors, " ") // AND semantics

	rows, err := s.vDB.QueryContext(ctx, `
		SELECT json_extract(e.metadata, '$.path'), -bm25(chunks_fts)
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		ORDER BY bm25(chunks_fts)
		LIMIT 50
	`, ftsQuery)
	if err != nil {
		log.Printf("chunks_fts fuzzy search warning: %v", err)
		return nil
	}
	defer rows.Close()

	docScores := topKAvg(rows, contentTopK)
	applyDocRRF(docScores, scores, contentFuzzyBoost)
	matches := make(map[string]bool, len(docScores))
	for path := range docScores {
		matches[path] = true
	}
	return matches
}

func mergePathMatches(first, second map[string]bool) map[string]bool {
	if len(second) == 0 {
		return first
	}
	if first == nil {
		first = make(map[string]bool, len(second))
	}
	for path := range second {
		first[path] = true
	}
	return first
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

	// Total query characters (only words ≥ 3 chars participate).
	queryCharTotal := 0
	for _, qw := range queryWords {
		if len(qw) >= 3 {
			queryCharTotal += len(qw)
		}
	}
	if queryCharTotal == 0 {
		return
	}

	for path := range scores {
		base := filepath.Base(path)
		baseLen := len(base)
		if baseLen == 0 {
			continue
		}
		pathWords := splitWords(base)
		if len(pathWords) == 0 {
			continue
		}

		totalSim := 0.0
		matchedQueryChars := 0
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
			matchedQueryChars += len(qw)
		}
		avgSim := totalSim / float64(len(queryWords))
		if avgSim < fuzzyMinWordSim {
			continue
		}

		// Coverage ratio: matched query chars vs filename length.
		// A 5-char query matching inside a 30-char filename scores ~0.17×,
		// while a 5-char query matching a 6-char filename scores ~0.83×.
		// This prevents long unrelated paths from cheating via a short
		// incidental substring match.
		coverageRatio := float64(matchedQueryChars) / float64(baseLen)
		if coverageRatio > 1.0 {
			coverageRatio = 1.0
		}

		scores[path] += fuzzyPathBoost * avgSim * coverageRatio
	}
}

var codeFileExtensions = map[string]bool{
	".go": true, ".py": true, ".pyi": true, ".js": true, ".jsx": true,
	".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".java": true,
	".kt": true, ".kts": true, ".rs": true, ".c": true, ".h": true,
	".cc": true, ".cpp": true, ".cxx": true, ".hh": true, ".hpp": true,
	".cs": true, ".fs": true, ".fsx": true, ".vb": true, ".php": true,
	".rb": true, ".swift": true, ".scala": true, ".sql": true, ".sh": true,
	".bash": true, ".zsh": true, ".ps1": true, ".psm1": true, ".bat": true,
	".cmd": true, ".lua": true, ".pl": true, ".r": true, ".dart": true,
	".ex": true, ".exs": true, ".erl": true, ".hrl": true, ".hs": true,
	".lhs": true, ".clj": true, ".cljs": true, ".vue": true, ".svelte": true,
	".html": true, ".htm": true, ".css": true, ".scss": true, ".less": true,
	".proto": true, ".graphql": true, ".gql": true,
}

const codeFileScoreMultiplier = 0.12

func codeFileMultiplier(path, query string, contentQueryMatches map[string]bool) float64 {
	if !codeFileExtensions[strings.ToLower(filepath.Ext(path))] || contentQueryMatches[path] || filenameNearQuery(query, path) {
		return 1
	}
	return codeFileScoreMultiplier
}

func filenameNearQuery(query, path string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	if ext := filepath.Ext(query); codeFileExtensions[ext] {
		query = strings.TrimSuffix(query, ext)
	}
	name := filepath.Base(path)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	name = strings.ToLower(name)
	if query == "" || name == "" {
		return false
	}
	if query == name || (len(query) >= 4 && strings.Contains(name, query)) {
		return true
	}

	queryWords := splitWords(query)
	nameWords := splitWords(name)
	if len(queryWords) == 0 || len(nameWords) == 0 {
		return false
	}
	matched := 0
	for _, queryWord := range queryWords {
		for _, nameWord := range nameWords {
			if queryWord == nameWord || (len(queryWord) >= 4 && wordEditSim(queryWord, nameWord) >= 0.8) {
				matched++
				break
			}
		}
	}
	return matched*5 >= len(queryWords)*4
}

func applyCodeFilePenalty(results []SearchResult, query string, contentQueryMatches map[string]bool) {
	for i := range results {
		results[i].Score *= codeFileMultiplier(results[i].Path, query, contentQueryMatches)
	}
	sortResults(results)
}

// applyNoisePenalties multiplies down scores for files that are unlikely to be
// useful document search results:
//   - Known binary/compiled extensions (.class, .exe, .dll, .inf, .sys, etc.)
//   - System path prefixes (C:\Windows, C:\Program Files, /usr/lib, etc.)
func applyNoisePenalties(scores map[string]float64) {
	// Extensions that are never useful document results — heavy penalty
	binaryExts := map[string]bool{
		".class": true, ".exe": true, ".dll": true, ".sys": true,
		".inf": true, ".ini": true, ".dat": true, ".bin": true,
		".obj": true, ".lib": true, ".pdb": true, ".ilk": true,
		".so": true, ".dylib": true, ".o": true, ".a": true,
		".pyc": true, ".pyo": true, ".wasm": true, ".node": true,
	}

	// Path prefixes that are system/runtime noise
	systemPrefixes := []string{
		`c:\windows\`,
		`c:\program files\`,
		`c:\program files (x86)\`,
		`/usr/lib/`, `/usr/share/`, `/System/Library/`,
	}

	for path, score := range scores {
		ext := strings.ToLower(filepath.Ext(path))
		lower := strings.ToLower(filepath.ToSlash(path))

		if binaryExts[ext] {
			scores[path] = score * 0.1
			continue
		}

		for _, pfx := range systemPrefixes {
			if strings.HasPrefix(lower, pfx) {
				scores[path] = score * 0.05
				break
			}
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

// serializeMetadataPayload constructs a token-efficient header for the cross-encoder payload.
// It pre-allocates strings to prevent memory fragmentation and strips heavy absolute root paths.
func serializeMetadataPayload(path string, size int64, modified string, snippet string) string {
	ext := filepath.Ext(path)
	szStr := strconv.FormatInt(size, 10)

	mod := modified
	if len(mod) >= 10 {
		mod = mod[:10] // Extrude YYYY-MM-DD from RFC3339
	}

	pSlash := filepath.ToSlash(path)
	parts := strings.Split(pSlash, "/")
	var cleanParts []string
	for _, p := range parts {
		if p != "" && !strings.Contains(p, ":") { // Drop Windows Drive letters
			cleanParts = append(cleanParts, p)
		}
	}
	relPath := strings.Join(cleanParts, "/")
	// Keep up to 4 terminal segments for a dense hierarchy
	if len(cleanParts) > 4 {
		relPath = strings.Join(cleanParts[len(cleanParts)-4:], "/")
	}

	var sb strings.Builder
	// Capacity heuristic: [Path: ] [Ext: ] [Size: ] [Modified: ] | Content:  (approx 55 bytes)
	sb.Grow(55 + len(relPath) + len(ext) + len(szStr) + len(mod) + len(snippet))

	sb.WriteString("[Path: ")
	sb.WriteString(relPath)
	sb.WriteString("] [Ext: ")
	sb.WriteString(ext)
	sb.WriteString("] [Size: ")
	sb.WriteString(szStr)
	sb.WriteString("] [Modified: ")
	sb.WriteString(mod)
	sb.WriteString("] | Content: ")
	sb.WriteString(snippet)

	return sb.String()
}

// rerank re-scores the top rerankTopN text results using the cross-encoder.
// Images are partitioned out before reranking (cross-encoder needs text) and
// re-inserted after by their original RRF rank, so they compete fairly with
// reranked text rather than being stranded at the bottom by score-range mismatch.
// No-ops if the reranker session was not loaded.
func (s *Engine) rerank(query string, results []SearchResult, rerankTopN int, rerankerBlend float64) []SearchResult {
	if s.rerankerSession == nil || len(results) == 0 {
		return results
	}
	if rerankTopN <= 0 {
		rerankTopN = rerankerTopN
	}
	if rerankerBlend < 0 {
		rerankerBlend = 0
	}
	if rerankerBlend > 1 {
		rerankerBlend = 1
	}

	n := len(results)
	if n > rerankTopN {
		n = rerankTopN
	}
	top := results[:n]
	rest := results[n:]

	// Fetch the best BM25-ranked snippet per document across all top-N paths in one query.
	// snippet() extracts a ~64-token window around the actual match inside each chunk,
	// so the reranker sees the relevant excerpt rather than an arbitrary prefix.
	const maxSnippetsPerDoc = 1
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
	rows, err := s.vDB.QueryContext(context.Background(), `
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

	const debug = false

	for i, item := range textItems {
		snippets := docSnippets[item.result.Path]
		if len(snippets) == 0 {
			// No FTS match (e.g. directory, empty file): use filename as sole snippet.
			snippets = []string{"Filename: " + basenames[item.result.Path]}
		}
		if debug {
			log.Printf("reranker [%s] %d snippet(s):", basenames[item.result.Path], len(snippets))
		}
		for si, snip := range snippets {
			if debug {
				log.Printf("  snippet[%d]: %s", si, snip)
			}
			payload := serializeMetadataPayload(item.result.Path, item.result.Size, item.result.Modified, snip)
			pairs = append(pairs, encodePair(payload))
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
			s.gpuMutex.Lock()
			runErr := s.rerankerSession.Run(
				[]ort.Value{tIDs, tMask},
				[]ort.Value{tOut},
			)
			s.gpuMutex.Unlock()
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
					rrfNorm := item.result.Score / maxRRF                       // normalised RRF → [0,1]
					blended := float32(rerankerBlend*rerankerProb + (1-rerankerBlend)*rrfNorm)
					r := item.result
					r.Score = float64(blended)
					if debug {
						log.Printf("reranker score %.4f (logit=%.4f rrf=%.4f)  %s", blended, docMax[i], item.result.Score, item.result.Path)
					}
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
	if debug {
		log.Printf("reranker final order:")
		for i, r := range out {
			if i >= rerankTopN {
				break
			}
			log.Printf("  #%d  %.4f  %s", i+1, r.Score, r.Path)
		}
	}
	return out
}

// scoreRerankerCandidates returns sigmoid-normalized cross-encoder scores for
// candidate paths. It is used by the optimizer once per unique query, then the
// cached scores can be blended with many simulated RRF weight combinations.
func (s *Engine) scoreRerankerCandidates(query string, paths []string) map[string]float64 {
	out := make(map[string]float64)
	if s.rerankerSession == nil || len(paths) == 0 {
		return out
	}

	const maxSnippetsPerDoc = 3
	docSnippets := make(map[string][]string, len(paths))
	basenames := make(map[string]string, len(paths))
	unique := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if p == "" || seen[p] || IsImageFile(p) {
			continue
		}
		seen[p] = true
		unique = append(unique, p)
		base := filepath.Base(p)
		if decoded, err := url.PathUnescape(base); err == nil {
			base = decoded
		}
		basenames[p] = base
	}
	if len(unique) == 0 {
		return out
	}

	ftsQuery := buildFTSQuery(query)
	placeholders := strings.Repeat("?,", len(unique))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, 0, 1+len(unique)+1)
	args = append(args, ftsQuery)
	for _, p := range unique {
		args = append(args, p)
	}
	rows, err := s.vDB.QueryContext(context.Background(), `
		SELECT json_extract(e.metadata, '$.path'), content
		FROM chunks_fts
		JOIN embeddings e ON chunks_fts.rowid = e.rowid
		WHERE chunks_fts MATCH ?
		  AND json_extract(e.metadata, '$.path') IN (`+placeholders+`)
		  AND length(content) > 0
		ORDER BY bm25(chunks_fts)
		LIMIT ?
	`, append(args, len(unique)*maxSnippetsPerDoc)...)
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

	qEnc := s.rerankerTok.EncodeWithOptions(query, false)
	qIDs := qEnc.IDs
	type pairTokens struct {
		ids  []int64
		mask []int64
	}
	pairs := make([]pairTokens, 0, len(unique)*maxSnippetsPerDoc)
	pairPath := make([]string, 0, len(unique)*maxSnippetsPerDoc)

	encodePair := func(text string) pairTokens {
		dEnc := s.rerankerTok.EncodeWithOptions(text, false)
		dIDs := dEnc.IDs
		total := 1 + len(qIDs) + 2 + len(dIDs) + 1
		ids64 := make([]int64, 0, total)
		mask64 := make([]int64, 0, total)
		push := func(id int64) { ids64 = append(ids64, id); mask64 = append(mask64, 1) }
		push(0)
		for _, id := range qIDs {
			push(int64(id))
		}
		push(2)
		push(2)
		for _, id := range dIDs {
			push(int64(id))
		}
		push(2)
		if len(ids64) > rerankerMaxToks {
			ids64 = ids64[:rerankerMaxToks]
			mask64 = mask64[:rerankerMaxToks]
		}
		return pairTokens{ids64, mask64}
	}

	for _, path := range unique {
		snippets := docSnippets[path]
		if len(snippets) == 0 {
			snippets = []string{"Filename: " + basenames[path]}
		}
		for _, snip := range snippets {
			payload := serializeMetadataPayload(path, 0, "", snip)
			pairs = append(pairs, encodePair(payload))
			pairPath = append(pairPath, path)
		}
	}
	if len(pairs) == 0 {
		return out
	}

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
		return out
	}

	s.gpuMutex.Lock()
	runErr := s.rerankerSession.Run([]ort.Value{tIDs, tMask}, []ort.Value{tOut})
	s.gpuMutex.Unlock()
	logits := tOut.GetData()
	tIDs.Destroy()
	tMask.Destroy()
	tOut.Destroy()
	if runErr != nil {
		log.Printf("optimizer reranker inference warning: %v", runErr)
		return out
	}

	for i, logit := range logits {
		path := pairPath[i]
		score := 1.0 / (1.0 + math.Exp(float64(-logit)))
		if score > out[path] {
			out[path] = score
		}
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

// hasDirectMLProvider returns true if DirectML.dll is present, indicating
// that GPU acceleration is likely available on this Windows machine.
func hasDirectMLProvider() bool {
	const dllName = "DirectML.dll"
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), dllName)
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		p := filepath.Join(cwd, dllName)
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}

// tryAppendDirectML attempts to register the DirectML execution provider on opts.
// Returns true on success; on any error it logs and leaves opts unchanged (CPU fallback).
func tryAppendDirectML(opts *ort.SessionOptions) bool {
	// Use Device 0 (primary GPU)
	if err := opts.AppendExecutionProviderDirectML(0); err != nil {
		log.Printf("DirectML EP append failed (falling back to CPU): %v", err)
		return false
	}
	return true
}
