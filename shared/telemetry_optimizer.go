package shared

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"sync"
	"time"
)

// OptimizerStatus is the snapshot returned to the frontend so users can see
// what the background optimizer is doing.
type OptimizerStatus struct {
	FeedbackCount  int            `json:"feedbackCount"`
	LastRunAt      string         `json:"lastRunAt"`      // ISO-8601 or "" if never ran
	LastAction     string         `json:"lastAction"`     // human-readable summary
	CurrentWeights SearchWeights  `json:"currentWeights"`
	DefaultWeights SearchWeights  `json:"defaultWeights"`
	WeightDeltas   map[string]float64 `json:"weightDeltas"` // non-zero deltas from default
}

// TelemetryOptimizer periodically re-evaluates ranking parameters based on feedback.
type TelemetryOptimizer struct {
	engine *Engine
	ctx    context.Context

	mu             sync.RWMutex
	lastRunAt      time.Time
	lastAction     string
	lastFeedbackN  int
}

// NewTelemetryOptimizer creates a background optimizer instance.
func NewTelemetryOptimizer(ctx context.Context, engine *Engine) *TelemetryOptimizer {
	return &TelemetryOptimizer{
		engine: engine,
		ctx:    ctx,
	}
}

// Start launches the optimizer's run loop in a background goroutine.
func (o *TelemetryOptimizer) Start() {
	o.engine.SetOptimizer(o)
	go o.runLoop()
}

// Status returns a snapshot of the optimizer's last run for the UI.
func (o *TelemetryOptimizer) Status() OptimizerStatus {
	o.mu.RLock()
	defer o.mu.RUnlock()

	current := o.engine.Weights.Get()
	defaults := defaultWeights

	deltas := make(map[string]float64)
	if d := current.WPathFTS - defaults.WPathFTS; d != 0 { deltas["Path FTS"] = d }
	if d := current.WContentFTS - defaults.WContentFTS; d != 0 { deltas["Content FTS"] = d }
	if d := current.WSemanticText - defaults.WSemanticText; d != 0 { deltas["Semantic Text"] = d }
	if d := current.WSemanticImg - defaults.WSemanticImg; d != 0 { deltas["Semantic Image"] = d }
	if d := current.WPathPrefix - defaults.WPathPrefix; d != 0 { deltas["Path Prefix"] = d }
	if d := current.WFilename - defaults.WFilename; d != 0 { deltas["Filename"] = d }
	if d := current.WRecency - defaults.WRecency; d != 0 { deltas["Recency"] = d }

	lastRun := ""
	if !o.lastRunAt.IsZero() {
		lastRun = o.lastRunAt.Format(time.RFC3339)
	}

	return OptimizerStatus{
		FeedbackCount:  o.lastFeedbackN,
		LastRunAt:      lastRun,
		LastAction:     o.lastAction,
		CurrentWeights: current,
		DefaultWeights: defaults,
		WeightDeltas:   deltas,
	}
}

func (o *TelemetryOptimizer) runLoop() {
	// Delay the first execution so we don't compete with startup indexing or high CPU spikes.
	select {
	case <-time.After(2 * time.Minute):
	case <-o.ctx.Done():
		return
	}

	o.optimize()

	// Trigger periodically. For active testing, we will run it every 15 seconds.
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-o.ctx.Done():
			return
		case <-ticker.C:
			o.optimize()
		}
	}
}

type feedbackRow struct {
	Query      string
	ResultPath string
	Feedback   int     // +1 or -1
	Weight     float64 // time-decay weight (1.0 = most recent, decays toward 0)
}

// feedbackDecayHalfLife controls how quickly old feedback loses influence.
// A vote 7 days old has ~50% the weight of a brand new vote.
const feedbackDecayHalfLifeDays = 7.0

func (o *TelemetryOptimizer) optimize() {
	db := o.engine.GetSQLDB()
	if db == nil {
		return
	}

	now := time.Now().Unix()

	// 1. Fetch ALL feedback rows with timestamps. We apply exponential decay
	// so old lessons fade gradually instead of falling off a hard window.
	rows, err := db.QueryContext(o.ctx, `
		SELECT query, result_path, feedback, created_at
		FROM search_feedback
		ORDER BY created_at DESC
	`)
	if err != nil {
		log.Printf("[Optimizer] Failed to read feedback telemetry: %v", err)
		return
	}
	defer rows.Close()

	var feedbacks []feedbackRow
	seen := make(map[string]bool)
	decayLambda := math.Ln2 / (feedbackDecayHalfLifeDays * 86400) // per-second decay rate

	for rows.Next() {
		var query, path string
		var feedback int
		var createdAt int64
		if err := rows.Scan(&query, &path, &feedback, &createdAt); err != nil {
			continue
		}
		// Deduplicate: ORDER BY created_at DESC means first occurrence is most recent.
		key := query + "|" + path
		if seen[key] {
			continue
		}
		seen[key] = true
		if feedback == 0 {
			continue
		}

		ageSec := float64(now - createdAt)
		if ageSec < 0 {
			ageSec = 0
		}
		w := math.Exp(-decayLambda * ageSec)

		// Skip votes that have decayed below 1% — they contribute noise, not signal.
		if w < 0.01 {
			continue
		}

		feedbacks = append(feedbacks, feedbackRow{
			Query:      query,
			ResultPath: path,
			Feedback:   feedback,
			Weight:     w,
		})
	}
	if len(feedbacks) < 2 {
		// Not enough telemetry data to perform a statistically meaningful optimization pass.
		o.mu.Lock()
		o.lastRunAt = time.Now()
		o.lastFeedbackN = len(feedbacks)
		o.lastAction = "Not enough feedback yet (need at least 2 votes)"
		o.mu.Unlock()
		return
	}

	log.Printf("[Optimizer] Starting bounded grid search on last %d feedback events...", len(feedbacks))

	// 2. Extract Raw Signals into Memory.
	// We only run GetRawSignals ONCE per unique query to avoid hitting ONNX 
	// multiple times during the grid search loop. 
	type signalsCache struct {
		Path    map[string]float64
		Content map[string]float64
		Vector  map[string]float64
	}
	queryCache := make(map[string]signalsCache)

	for _, f := range feedbacks {
		if _, exists := queryCache[f.Query]; !exists {
			p, c, v := o.engine.GetRawSignals(f.Query)
			queryCache[f.Query] = signalsCache{Path: p, Content: c, Vector: v}
		}
	}

	// 3. The Objective Function (Bounded Grid Search)
	factors := []float64{0.5, 1.0, 1.5, 2.0, 3.0}

	bestScore := math.Inf(-1)
	baselineScore := math.Inf(-1)
	var bestWPath, bestWContent, bestWVec float64
	currentWeights := o.engine.Weights.Get()

	// 5x5x5 grid = 125 total iterations over purely in-memory maps.
	for _, wPath := range factors {
		for _, wContent := range factors {
			for _, wVec := range factors {

				score := 0.0

				for _, f := range feedbacks {
					sigs := queryCache[f.Query]

					// Simulate RRF math using current weights
					combined := make(map[string]float64)
					for p, v := range sigs.Path {
						combined[p] += v * wPath
					}
					for p, v := range sigs.Content {
						combined[p] += v * wContent
					}
					for p, v := range sigs.Vector {
						combined[p] += v * wVec
					}

					// Convert to slice and sort to find simulated rank position
					type docScore struct {
						Path  string
						Score float64
					}
					docs := make([]docScore, 0, len(combined))
					for p, s := range combined {
						docs = append(docs, docScore{Path: p, Score: s})
					}
					sort.Slice(docs, func(i, j int) bool {
						return docs[i].Score > docs[j].Score
					})

					rank := -1
					for i, d := range docs {
						if d.Path == f.ResultPath {
							rank = i + 1
							break
						}
					}

					if rank > 0 {
						if f.Feedback == 1 {
							score += f.Weight * 100.0 / float64(rank)
						} else if f.Feedback == -1 {
							score -= f.Weight * 100.0 / float64(rank)
						}
					} else {
						if f.Feedback == 1 {
							score -= f.Weight * 10.0 // penalty for losing a liked document
						} else {
							score += f.Weight * 10.0 // reward for burying a disliked document
						}
					}
				}

				// Capture the baseline score for logging comparisons
				if wPath == currentWeights.WPathFTS && wContent == currentWeights.WContentFTS && wVec == currentWeights.WSemanticText {
					baselineScore = score
				}

				if score > bestScore {
					bestScore = score
					bestWPath = wPath
					bestWContent = wContent
					bestWVec = wVec
				}
			}
		}
	}

	log.Printf("[Optimizer] Grid search evaluated. Baseline Score: %.2f (Path: %.1f, Content: %.1f, Vector: %.1f)", baselineScore, currentWeights.WPathFTS, currentWeights.WContentFTS, currentWeights.WSemanticText)

	// 4. Atomic Updates.
	// Check if grid search found a fundamentally different, strictly superior tuning.
	if bestWPath != currentWeights.WPathFTS || bestWContent != currentWeights.WContentFTS || bestWVec != currentWeights.WSemanticText {
		log.Printf("[Optimizer] Applying New Weights! Score improved to %.2f. Deltas:", bestScore)
		log.Printf("[Optimizer]   WPathFTS:      %.1f -> %.1f", currentWeights.WPathFTS, bestWPath)
		log.Printf("[Optimizer]   WContentFTS:   %.1f -> %.1f", currentWeights.WContentFTS, bestWContent)
		log.Printf("[Optimizer]   WSemanticText: %.1f -> %.1f", currentWeights.WSemanticText, bestWVec)
		
		tx, err := db.BeginTx(o.ctx, nil)
		if err != nil {
			log.Printf("[Optimizer] Failed to begin transaction: %v", err)
			return
		}

		queries := []struct {
			key   string
			value float64
		}{
			{"w_path_fts", bestWPath},
			{"w_content_fts", bestWContent},
			{"w_semantic_text", bestWVec},
		}

		for _, q := range queries {
			_, err := tx.ExecContext(o.ctx, `
				INSERT INTO search_config(key, value) VALUES(?, ?) 
				ON CONFLICT(key) DO UPDATE SET value=?
			`, q.key, q.value, q.value)
			
			if err != nil {
				tx.Rollback()
				log.Printf("[Optimizer] Failed to update %s: %v", q.key, err)
				return
			}
		}

		if err := tx.Commit(); err == nil {
			log.Println("[Optimizer] Committing new weights to global WeightStore.")
			o.engine.Weights.ForceReload()
			o.mu.Lock()
			o.lastRunAt = time.Now()
			o.lastFeedbackN = len(feedbacks)
			o.lastAction = fmt.Sprintf("Updated weights — Path: %.1f→%.1f, Content: %.1f→%.1f, Vector: %.1f→%.1f (score %.0f→%.0f)",
				currentWeights.WPathFTS, bestWPath,
				currentWeights.WContentFTS, bestWContent,
				currentWeights.WSemanticText, bestWVec,
				baselineScore, bestScore)
			o.mu.Unlock()
		} else {
			log.Printf("[Optimizer] Failed to commit transactions: %v", err)
		}
	} else {
		log.Println("[Optimizer] Grid search complete. Current weights are already optimal.")
		o.mu.Lock()
		o.lastRunAt = time.Now()
		o.lastFeedbackN = len(feedbacks)
		o.lastAction = fmt.Sprintf("No change needed — current weights are optimal (score %.0f, %d votes)", bestScore, len(feedbacks))
		o.mu.Unlock()
	}
}
