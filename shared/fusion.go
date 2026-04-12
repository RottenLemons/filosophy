package shared

import (
	"context"
	"database/sql"
	"log"
	"math"
	"sync"
	"time"
)

// SearchWeights holds all tunable ranking parameters loaded from search_config.
// Fields are read by the search pipeline and written only by the reload goroutine.
type SearchWeights struct {
	WPathFTS      float64 // RRF weight: path FTS exact + all-word signals
	WPathPrefix   float64 // RRF weight: path FTS prefix signals
	WSemanticText float64 // RRF weight: text vector signal
	WSemanticImg  float64 // RRF weight: image vector signal
	WContentFTS   float64 // RRF weight: content FTS (phrase + keyword) signal
	WFilename     float64 // Multiplier applied on top of filename/fuzzy boosts
	WRecency      float64 // Additive recency score weight
	RecencyHalf   float64 // Recency half-life in days (score decays to ~37% after this)
	RRFK          float64 // RRF smoothing constant k
	MinScore      float64 // Minimum score threshold exposed to REST API
	RerankTopN    int     // How many results pass to the cross-encoder reranker
	PathOnlyCap   float64 // Score cap for path-only results (no content signal)
	UseNewPipeline bool   // Feature flag: activates the weighted fusion pipeline
}

// defaultWeights mirrors the INSERT OR IGNORE defaults in InitIndexTables.
var defaultWeights = SearchWeights{
	WPathFTS:      3.0,
	WPathPrefix:   1.5,
	WSemanticText: 1.0,
	WSemanticImg:  1.1,
	WContentFTS:   1.0,
	WFilename:     1.0,
	WRecency:      0.3,
	RecencyHalf:   30.0,
	RRFK:          60.0,
	MinScore:      0.15,
	RerankTopN:    20,
	PathOnlyCap:   0.12,
	UseNewPipeline: false,
}

// WeightStore caches search_config values and reloads them periodically.
// It is safe for concurrent reads; the reload goroutine holds a write lock.
type WeightStore struct {
	mu      sync.RWMutex
	weights SearchWeights
	db      *sql.DB
	stopCh  chan struct{}
}

// NewWeightStore creates a WeightStore, loads the initial config from db,
// and starts a background goroutine that reloads every 30 seconds.
func NewWeightStore(db *sql.DB) *WeightStore {
	ws := &WeightStore{
		weights: defaultWeights,
		db:      db,
		stopCh:  make(chan struct{}),
	}
	ws.reload()
	go ws.reloadLoop()
	return ws
}

// Get returns a snapshot of the current weights. Safe for concurrent use.
func (ws *WeightStore) Get() SearchWeights {
	ws.mu.RLock()
	defer ws.mu.RUnlock()
	return ws.weights
}

// Stop shuts down the background reload goroutine.
func (ws *WeightStore) Stop() {
	close(ws.stopCh)
}

// ForceReload triggers an immediate synchronous reload from the DB,
// bypassing the 30-second ticker. Used after admin weight updates.
func (ws *WeightStore) ForceReload() {
	ws.reload()
}

func (ws *WeightStore) reloadLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ws.reload()
		case <-ws.stopCh:
			return
		}
	}
}

func (ws *WeightStore) reload() {
	rows, err := ws.db.QueryContext(context.Background(), `SELECT key, value FROM search_config`)
	if err != nil {
		log.Printf("[WeightStore] reload error: %v", err)
		return
	}
	defer rows.Close()

	m := make(map[string]float64)
	for rows.Next() {
		var k string
		var v float64
		if rows.Scan(&k, &v) == nil {
			m[k] = v
		}
	}

	ws.mu.Lock()
	defer ws.mu.Unlock()
	w := defaultWeights // start from defaults, overlay DB values
	if v, ok := m["w_path_fts"]; ok { w.WPathFTS = v }
	if v, ok := m["w_path_prefix"]; ok { w.WPathPrefix = v }
	if v, ok := m["w_semantic_text"]; ok { w.WSemanticText = v }
	if v, ok := m["w_semantic_image"]; ok { w.WSemanticImg = v }
	if v, ok := m["w_content_fts"]; ok { w.WContentFTS = v }
	if v, ok := m["w_filename"]; ok { w.WFilename = v }
	if v, ok := m["w_recency"]; ok { w.WRecency = v }
	if v, ok := m["recency_half_life"]; ok { w.RecencyHalf = v }
	if v, ok := m["rrf_k"]; ok && v > 0 { w.RRFK = v }
	if v, ok := m["min_score"]; ok { w.MinScore = v }
	if v, ok := m["rerank_top_n"]; ok && v >= 1 { w.RerankTopN = int(v) }
	if v, ok := m["path_only_cap"]; ok { w.PathOnlyCap = v }
	if v, ok := m["use_new_pipeline"]; ok { w.UseNewPipeline = v != 0 }
	ws.weights = w
}

// ── RRF helpers ────────────────────────────────────────────────────────────────

// rrfScore returns the Reciprocal Rank Fusion contribution for a document at
// zero-indexed position i, using smoothing constant k and weight multiplier w.
//
//	score = w / (k + i + 1)
func rrfScore(i int, k, w float64) float64 {
	return w / (k + float64(i+1))
}

// MergeRRF combines multiple ranked signal maps into a single score map using
// weighted RRF. Each signal map maps path → rank position (0-indexed).
// The weight for each signal must be provided in the same order as signals.
func MergeRRF(k float64, signals []map[string]int, weights []float64) map[string]float64 {
	out := make(map[string]float64)
	for i, sig := range signals {
		w := 1.0
		if i < len(weights) {
			w = weights[i]
		}
		for path, rank := range sig {
			out[path] += rrfScore(rank, k, w)
		}
	}
	return out
}

// RecencyScore returns a value in [0,1] that decays exponentially with file age.
// mtime is nanoseconds since epoch. halfLifeDays controls decay speed:
// a file modified exactly halfLifeDays ago scores math.Exp(-1) ≈ 0.368.
// Returns 0 if halfLifeDays ≤ 0 (disabled).
func RecencyScore(mtimeNs int64, halfLifeDays float64) float64 {
	if halfLifeDays <= 0 {
		return 0
	}
	ageDays := float64(time.Now().UnixNano()-mtimeNs) / float64(24*int64(time.Hour))
	if ageDays < 0 {
		ageDays = 0
	}
	score := math.Exp(-ageDays / halfLifeDays)
	if math.IsNaN(score) || math.IsInf(score, 0) {
		return 0
	}
	return score
}
