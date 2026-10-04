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
	FeedbackCount   int                `json:"feedbackCount"`
	LastRunAt       string             `json:"lastRunAt"`  // ISO-8601 or "" if never ran
	LastAction      string             `json:"lastAction"` // human-readable summary
	PreferenceScore float64            `json:"preferenceScore"`
	BaselineScore   float64            `json:"baselineScore"`
	NDCG10          float64            `json:"ndcg10"`
	MRR10           float64            `json:"mrr10"`
	RecallAtRerank  float64            `json:"recallAtRerank"`
	DownvotePenalty float64            `json:"downvotePenalty"`
	CurrentWeights  SearchWeights      `json:"currentWeights"`
	DefaultWeights  SearchWeights      `json:"defaultWeights"`
	WeightDeltas    map[string]float64 `json:"weightDeltas"` // non-zero deltas from default
}

// TelemetryOptimizer periodically re-evaluates ranking parameters based on feedback.
type TelemetryOptimizer struct {
	engine *Engine
	ctx    context.Context

	mu            sync.RWMutex
	startOnce     sync.Once
	lastRunAt     time.Time
	lastAction    string
	lastFeedbackN int
	lastMetrics   optimizerMetrics
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
	o.startOnce.Do(func() { go o.runLoop() })
}

// Status returns a snapshot of the optimizer's last run for the UI.
func (o *TelemetryOptimizer) Status() OptimizerStatus {
	o.mu.RLock()
	defer o.mu.RUnlock()

	current := o.engine.Weights.Get()
	defaults := defaultWeights

	deltas := make(map[string]float64)
	if d := current.WPathFTS - defaults.WPathFTS; d != 0 {
		deltas["Path FTS"] = d
	}
	if d := current.WContentFTS - defaults.WContentFTS; d != 0 {
		deltas["Content FTS"] = d
	}
	if d := current.WSemanticText - defaults.WSemanticText; d != 0 {
		deltas["Semantic Text"] = d
	}
	if d := current.WSemanticImg - defaults.WSemanticImg; d != 0 {
		deltas["Semantic Image"] = d
	}
	if d := current.WPathPrefix - defaults.WPathPrefix; d != 0 {
		deltas["Path Prefix"] = d
	}
	if d := current.WFilename - defaults.WFilename; d != 0 {
		deltas["Filename"] = d
	}
	if d := current.WRecency - defaults.WRecency; d != 0 {
		deltas["Recency"] = d
	}
	if d := current.WRerankerBlend - defaults.WRerankerBlend; d != 0 {
		deltas["Reranker Blend"] = d
	}

	lastRun := ""
	if !o.lastRunAt.IsZero() {
		lastRun = o.lastRunAt.Format(time.RFC3339)
	}

	return OptimizerStatus{
		FeedbackCount:   o.lastFeedbackN,
		LastRunAt:       lastRun,
		LastAction:      o.lastAction,
		PreferenceScore: o.lastMetrics.Score * 100,
		BaselineScore:   o.lastMetrics.BaselineScore * 100,
		NDCG10:          o.lastMetrics.NDCG10,
		MRR10:           o.lastMetrics.MRR10,
		RecallAtRerank:  o.lastMetrics.RecallAtRerank,
		DownvotePenalty: o.lastMetrics.DownvotePenalty,
		CurrentWeights:  current,
		DefaultWeights:  defaults,
		WeightDeltas:    deltas,
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

	// Trigger periodically. For calibration, we run it every 10 minutes.
	ticker := time.NewTicker(10 * time.Minute)
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

type docScore struct {
	Path  string
	Score float64
}

type optimizerMetrics struct {
	Score           float64
	BaselineScore   float64
	NDCG10          float64
	MRR10           float64
	RecallAtRerank  float64
	DownvotePenalty float64
}

// feedbackDecayHalfLife controls how quickly old feedback loses influence.
// A vote 7 days old has ~50% the weight of a brand new vote.
const feedbackDecayHalfLifeDays = 7.0

func feedbackTimeWeight(createdAt, now int64) float64 {
	ageSeconds := now - createdAt
	if ageSeconds < 0 {
		ageSeconds = 0
	}
	ageDays := float64(ageSeconds) / (24 * 60 * 60)
	return math.Exp2(-ageDays / feedbackDecayHalfLifeDays)
}

const (
	preferenceEvalK       = 10
	relevantGain          = 3.0
	ndcgObjectiveWeight   = 0.45
	mrrObjectiveWeight    = 0.30
	recallObjectiveWeight = 0.20
	downvotePenaltyWeight = 1.25
)

func groupFeedbackByQuery(feedbacks []feedbackRow) map[string][]feedbackRow {
	grouped := make(map[string][]feedbackRow)
	for _, f := range feedbacks {
		grouped[f.Query] = append(grouped[f.Query], f)
	}
	return grouped
}

func positiveFeedbackMetrics(positives []feedbackRow, rankByPath map[string]int, rerankTopN int) (ndcg, mrr, recall float64) {
	if len(positives) == 0 {
		return 0, 0, 0
	}

	positiveWeight := 0.0
	maxWeight := 0.0
	for _, p := range positives {
		positiveWeight += p.Weight
		if p.Weight > maxWeight {
			maxWeight = p.Weight
		}
	}
	if positiveWeight <= 0 || maxWeight <= 0 {
		return 0, 0, 0
	}

	dcg := 0.0
	idcg := 0.0
	recalledWeight := 0.0
	idealWeights := make([]float64, 0, len(positives))
	for _, p := range positives {
		idealWeights = append(idealWeights, p.Weight)
		rank := rankByPath[p.ResultPath]
		if rank > 0 {
			if rank <= preferenceEvalK {
				weightedRR := p.Weight / float64(rank) / maxWeight
				if weightedRR > mrr {
					mrr = weightedRR
				}
			}
			if rank <= preferenceEvalK {
				dcg += (math.Pow(2, relevantGain) - 1) * p.Weight / math.Log2(float64(rank+1))
			}
			if rank <= rerankTopN {
				recalledWeight += p.Weight
			}
		}
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(idealWeights)))
	idealN := len(idealWeights)
	if idealN > preferenceEvalK {
		idealN = preferenceEvalK
	}
	for i := 0; i < idealN; i++ {
		idcg += (math.Pow(2, relevantGain) - 1) * idealWeights[i] / math.Log2(float64(i+2))
	}
	if idcg > 0 {
		ndcg = dcg / idcg
	}
	recall = recalledWeight / positiveWeight
	return ndcg, mrr, recall
}

func rankWeightedSignals(query string, sigs RawSearchSignals, w SearchWeights) []docScore {
	combined := make(map[string]float64)
	for p, v := range sigs.PathFTS {
		combined[p] += v * w.WPathFTS
	}
	for p, v := range sigs.PathPrefix {
		combined[p] += v * w.WPathPrefix
	}
	for p, v := range sigs.ContentFTS {
		combined[p] += v * w.WContentFTS
	}
	for p, v := range sigs.SemanticText {
		combined[p] += v * w.WSemanticText
	}
	for p, v := range sigs.SemanticImage {
		combined[p] += v * w.WSemanticImg
	}

	maxCombined := 1e-9
	for _, score := range combined {
		if score > maxCombined {
			maxCombined = score
		}
	}
	blend := w.WRerankerBlend
	if blend < 0 {
		blend = 0
	}
	if blend > 1 {
		blend = 1
	}
	if blend > 0 && len(sigs.Reranker) > 0 {
		for p, score := range combined {
			rrfNorm := score / maxCombined
			if rerankerScore, ok := sigs.Reranker[p]; ok {
				combined[p] = blend*rerankerScore + (1-blend)*rrfNorm
			} else {
				combined[p] = (1 - blend) * rrfNorm
			}
		}
	}
	for path, score := range combined {
		combined[path] = score * codeFileMultiplier(path, query, sigs.CodeQueryMatches)
	}

	docs := make([]docScore, 0, len(combined))
	for p, s := range combined {
		docs = append(docs, docScore{Path: p, Score: s})
	}
	sort.Slice(docs, func(i, j int) bool {
		return docs[i].Score > docs[j].Score
	})
	return docs
}

func evaluatePreferenceMetrics(grouped map[string][]feedbackRow, queryCache map[string]RawSearchSignals, w SearchWeights) optimizerMetrics {
	rerankTopN := w.RerankTopN
	if rerankTopN <= 0 {
		rerankTopN = rerankerTopN
	}

	totalWeight := 0.0
	out := optimizerMetrics{}

	for query, judgments := range grouped {
		docs := rankWeightedSignals(query, queryCache[query], w)
		rankByPath := make(map[string]int, len(docs))
		for i, d := range docs {
			rankByPath[d.Path] = i + 1
		}

		queryWeight := 0.0
		var positives []feedbackRow
		var negatives []feedbackRow
		for _, j := range judgments {
			queryWeight += j.Weight
			if j.Feedback > 0 {
				positives = append(positives, j)
			} else if j.Feedback < 0 {
				negatives = append(negatives, j)
			}
		}
		if queryWeight <= 0 {
			queryWeight = 1
		}

		ndcg10, mrr10, recallAtRerank := positiveFeedbackMetrics(positives, rankByPath, rerankTopN)

		downvotePenalty := 0.0
		for _, n := range negatives {
			rank := rankByPath[n.ResultPath]
			if rank > 0 && rank <= preferenceEvalK {
				downvotePenalty += n.Weight / math.Log2(float64(rank+1))
			}
		}
		downvotePenalty /= queryWeight

		score := ndcgObjectiveWeight*ndcg10 +
			mrrObjectiveWeight*mrr10 +
			recallObjectiveWeight*recallAtRerank -
			downvotePenaltyWeight*downvotePenalty

		out.Score += score * queryWeight
		out.NDCG10 += ndcg10 * queryWeight
		out.MRR10 += mrr10 * queryWeight
		out.RecallAtRerank += recallAtRerank * queryWeight
		out.DownvotePenalty += downvotePenalty * queryWeight
		totalWeight += queryWeight
	}

	if totalWeight > 0 {
		out.Score /= totalWeight
		out.NDCG10 /= totalWeight
		out.MRR10 /= totalWeight
		out.RecallAtRerank /= totalWeight
		out.DownvotePenalty /= totalWeight
	}
	return out
}

func (o *TelemetryOptimizer) optimize() {
	db := o.engine.GetSQLDB()
	if db == nil {
		return
	}

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
	now := time.Now().Unix()
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

		w := feedbackTimeWeight(createdAt, now)
		if w == 0 {
			continue
		}

		feedbacks = append(feedbacks, feedbackRow{
			Query:      query,
			ResultPath: path,
			Feedback:   feedback,
			Weight:     w,
		})
	}
	if len(feedbacks) < 10 {
		// Not enough telemetry data to perform a statistically meaningful optimization pass.
		o.mu.Lock()
		o.lastRunAt = time.Now()
		o.lastFeedbackN = len(feedbacks)
		o.lastMetrics = optimizerMetrics{}
		o.lastAction = "Not enough feedback yet (need at least 10 votes)"
		o.mu.Unlock()
		return
	}

	log.Printf("[Optimizer] Starting bounded grid search on last %d feedback events...", len(feedbacks))

	// 2. Extract Raw Signals into Memory.
	// We only run GetRawSignals ONCE per unique query to avoid hitting ONNX
	// multiple times during the grid search loop.
	queryCache := make(map[string]RawSearchSignals)

	for _, f := range feedbacks {
		if _, exists := queryCache[f.Query]; !exists {
			queryCache[f.Query] = o.engine.GetRawSignals(f.Query)
		}
	}
	groupedFeedback := groupFeedbackByQuery(feedbacks)

	// 3. The Objective Function (Bounded Relative Grid Search)
	// Factors are relative multipliers centered on the current weights.
	factors := []float64{0.7, 0.9, 1.0, 1.1, 1.3}
	blendFactors := []float64{-0.15, 0.0, 0.15}

	bestScore := math.Inf(-1)
	baselineScore := math.Inf(-1)
	var bestMetrics optimizerMetrics
	var baselineMetrics optimizerMetrics
	var bestWeights SearchWeights
	currentWeights := o.engine.Weights.Get()
	bestWeights = currentWeights

	// 5^5 * 3 grid = 9375 total iterations over purely in-memory maps.
	for _, fPath := range factors {
		for _, fPrefix := range factors {
			for _, fContent := range factors {
				for _, fText := range factors {
					for _, fImage := range factors {
						for _, blendDelta := range blendFactors {
							candidate := currentWeights
							candidate.WPathFTS *= fPath
							candidate.WPathPrefix *= fPrefix
							candidate.WContentFTS *= fContent
							candidate.WSemanticText *= fText
							candidate.WSemanticImg *= fImage
							candidate.WRerankerBlend += blendDelta
							if candidate.WRerankerBlend < 0 {
								candidate.WRerankerBlend = 0
							}
							if candidate.WRerankerBlend > 1 {
								candidate.WRerankerBlend = 1
							}

							metrics := evaluatePreferenceMetrics(groupedFeedback, queryCache, candidate)
							score := metrics.Score

							// Capture the baseline score for logging comparisons.
							if fPath == 1.0 && fPrefix == 1.0 && fContent == 1.0 && fText == 1.0 && fImage == 1.0 && blendDelta == 0.0 {
								baselineScore = score
								baselineMetrics = metrics
							}

							if score > bestScore {
								bestScore = score
								bestMetrics = metrics
								bestWeights = candidate
							}
						}
					}
				}
			}
		}
	}
	bestMetrics.BaselineScore = baselineMetrics.Score

	log.Printf("[Optimizer] Grid search evaluated. Baseline Preference Score: %.1f (Path: %.1f, Prefix: %.1f, Content: %.1f, Text: %.1f, Image: %.1f, Blend: %.2f)",
		baselineScore*100,
		currentWeights.WPathFTS,
		currentWeights.WPathPrefix,
		currentWeights.WContentFTS,
		currentWeights.WSemanticText,
		currentWeights.WSemanticImg,
		currentWeights.WRerankerBlend,
	)

	// 4. Atomic Updates.
	// Check if grid search found a fundamentally different, strictly superior tuning.
	if bestWeights.WPathFTS != currentWeights.WPathFTS ||
		bestWeights.WPathPrefix != currentWeights.WPathPrefix ||
		bestWeights.WContentFTS != currentWeights.WContentFTS ||
		bestWeights.WSemanticText != currentWeights.WSemanticText ||
		bestWeights.WSemanticImg != currentWeights.WSemanticImg ||
		bestWeights.WRerankerBlend != currentWeights.WRerankerBlend {
		log.Printf("[Optimizer] Applying New Weights! Preference score improved to %.1f. Deltas:", bestScore*100)
		log.Printf("[Optimizer]   WPathFTS:       %.1f -> %.1f", currentWeights.WPathFTS, bestWeights.WPathFTS)
		log.Printf("[Optimizer]   WPathPrefix:    %.1f -> %.1f", currentWeights.WPathPrefix, bestWeights.WPathPrefix)
		log.Printf("[Optimizer]   WContentFTS:    %.1f -> %.1f", currentWeights.WContentFTS, bestWeights.WContentFTS)
		log.Printf("[Optimizer]   WSemanticText:  %.1f -> %.1f", currentWeights.WSemanticText, bestWeights.WSemanticText)
		log.Printf("[Optimizer]   WSemanticImage: %.1f -> %.1f", currentWeights.WSemanticImg, bestWeights.WSemanticImg)
		log.Printf("[Optimizer]   WRerankerBlend: %.2f -> %.2f", currentWeights.WRerankerBlend, bestWeights.WRerankerBlend)

		tx, err := db.BeginTx(o.ctx, nil)
		if err != nil {
			log.Printf("[Optimizer] Failed to begin transaction: %v", err)
			return
		}

		queries := []struct {
			key   string
			value float64
		}{
			{"w_path_fts", bestWeights.WPathFTS},
			{"w_path_prefix", bestWeights.WPathPrefix},
			{"w_content_fts", bestWeights.WContentFTS},
			{"w_semantic_text", bestWeights.WSemanticText},
			{"w_semantic_image", bestWeights.WSemanticImg},
			{"w_reranker_blend", bestWeights.WRerankerBlend},
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
			o.lastMetrics = bestMetrics
			o.lastAction = fmt.Sprintf("Updated weights — Path %.1f→%.1f, Prefix %.1f→%.1f, Content %.1f→%.1f, Text %.1f→%.1f, Image %.1f→%.1f, Blend %.2f→%.2f (preference %.1f→%.1f)",
				currentWeights.WPathFTS, bestWeights.WPathFTS,
				currentWeights.WPathPrefix, bestWeights.WPathPrefix,
				currentWeights.WContentFTS, bestWeights.WContentFTS,
				currentWeights.WSemanticText, bestWeights.WSemanticText,
				currentWeights.WSemanticImg, bestWeights.WSemanticImg,
				currentWeights.WRerankerBlend, bestWeights.WRerankerBlend,
				baselineScore*100, bestScore*100)
			o.mu.Unlock()
		} else {
			log.Printf("[Optimizer] Failed to commit transactions: %v", err)
		}
	} else {
		log.Println("[Optimizer] Grid search complete. Current weights are already optimal.")
		o.mu.Lock()
		o.lastRunAt = time.Now()
		o.lastFeedbackN = len(feedbacks)
		o.lastMetrics = bestMetrics
		o.lastAction = fmt.Sprintf("No change needed — current weights are optimal (preference %.1f, %d votes)", bestScore*100, len(feedbacks))
		o.mu.Unlock()
	}
}
