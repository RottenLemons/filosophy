package shared

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cespare/xxhash"
	"github.com/liliang-cn/sqvect/v2/pkg/core"
)

// EvalQuery represents a benchmark query with ground-truth relevance annotations.
type EvalQuery struct {
	ID        int
	Query     string
	Category  string
	Relevance map[string]float64 // path -> graded relevance (3: Highly relevant, 2: Relevant, 1: Marginally, 0: Irrelevant)
}

// EvalResult stores aggregated metric values for a retrieval pipeline.
type EvalResult struct {
	Name           string
	NDCG10         float64
	Recall10       float64 // Mean Recall@10
	MRR10          float64
	MeanLatMs      float64 // End-to-end latency including query embedding
	P50LatMs       float64 // Per-query median latency
	P95LatMs       float64 // Per-query tail latency (95th percentile)
	IndexOnlyLatMs float64 // Raw index traversal latency excluding embedding
}

// BenchmarkModelInfo contains provenance and cryptographic verification for evaluated weights.
type BenchmarkModelInfo struct {
	Name     string
	Backend  string
	Path     string
	FileSize int64
	SHA256   string
	Dim      int
}

// QueryEvalLog records per-query scoring and top-1 retrieved result.
type QueryEvalLog struct {
	ID       int
	Query    string
	Category string
	LexNDCG  float64
	SemNDCG  float64
	HybNDCG  float64
	Top1Hit  string
}

// BenchmarkReport contains the complete summary and per-category breakdown of a benchmark run.
type BenchmarkReport struct {
	ModelInfo        BenchmarkModelInfo
	Results          map[string]*EvalResult
	CategoryMetrics  map[string]map[string][3]float64 // category -> mode -> [ndcg, recall, mrr]
	SortedCategories []string
	Queries          []EvalQuery
	QueryLogs        []QueryEvalLog
}

// BuildEvalCorpus creates a realistic test corpus with 22 documents across diverse categories.
func BuildEvalCorpus() map[string]string {
	return map[string]string{
		// Financial & Business Reports
		"docs/finance/Q3_2024_Financial_Report.pdf":   "Quarterly financial report for Q3 2024. Total revenue reached $14.2M, representing 18% YoY growth. Operating margins improved to 24.5% due to reduced infrastructure costs and optimized cloud spending. Net cash flow from operating activities was positive at $3.8M.",
		"docs/invoices/invoice_stripe_nov2024.pdf":    "Stripe billing invoice for November 2024. Charge of $450.00 for payment processing services, merchant transaction fees, and automated payout management. Account ID acct_982347.",
		"docs/expenses/receipt_uber_airport.pdf":      "Uber trip receipt. Airport dropoff ride to Terminal 2 on October 14, 2024. Total fare $54.20 paid with corporate visa card. Driver: Alex K. Pickup at 450 Market St, Dropoff at SFO International.",
		"docs/legal/contract_agreement_v2_signed.pdf": "Master service agreement contract between Acme Corp and client. Non-disclosure terms, intellectual property assignment, indemnity clauses, and termination policies signed on August 2024.",
		"archive/tax/old_tax_return_2022.pdf":         "Individual income tax return for fiscal year 2022. W2 earnings, deductions, schedule C filings, and refund calculations.",

		// Technical Documentation & Notes
		"docs/hr/onboarding_guide.md":                      "Engineering onboarding guide and new employee setup checklist. Instructions for setting up development environment, SSH keys, VPN access, requesting 1Password vault access, and completing security compliance training.",
		"specs/system_architecture_spec.md":                "System architecture specification document. Technical overview of high-availability distributed storage, SQLite FTS indexing, and hybrid vector search.",
		"docs/notes/meeting_notes_2024_10_12.txt":          "Sprint planning meeting notes. Action items: Alice to finalize API schema, Bob to investigate database migration foreign key constraint, Charlie to benchmark vector search latency.",
		"docs/philosophy/notes_camus_myth_of_sisyphus.txt": "Philosophical notes on Albert Camus and The Myth of Sisyphus. Analysis of absurdism, existential revolt, and the acceptance of meaninglessness in human existence.",

		// Source Code
		"src/backend/auth_service.go":    "package auth. Implementation of UserAuthenticationService. ValidateToken parses JWT bearer tokens, verifies cryptographic signature, checks claims expiration, and injects user context into request handler.",
		"src/backend/token_validator.ts": "import jwt from 'jsonwebtoken'. ValidateToken extracts header bearer claims, verifies signature, checks issuer and audience, handles token renewal.",
		"src/database/db_migrator.py":    "import sqlalchemy metadata. Database migration runner executing schema changes and resolving database migration foreign key constraint errors during table alter operations.",
		"src/pipeline/data_pipeline.py":  "High-throughput data extraction and batch ingestion pipeline. Handles chunking, normalization, error handling logger, and streaming parquet serialization.",
		"src/utils/logger.go":            "package utils. Structured error handling logger with JSON formatting, log rotation, and panic recovery middleware.",

		// Media & Diagrams
		"photos/nature/DSC_0942_sunset_over_mountains.jpg": "Vibrant orange sunset over snow-capped mountain ridge with scenic lake reflection.",
		"diagrams/whiteboard_er_diagram.png":               "Whiteboard photo depicting database schema entity relationship drawing, tables, relationships, foreign keys, and indexes.",
		"photos/portraits/profile_photo_mahir.png":         "Professional headshot portrait photograph of Mahir in business attire.",
		"screenshots/system_architecture_spec.png":         "Architecture diagram slide illustrating microservices, API gateway, vector database, and async message queue.",

		// Personal & Infix cases
		"personal/career/resume_mahircv.pdf": "Curriculum vitae and professional resume of Mahir. Experience in software engineering, distributed systems, machine learning, and full-stack development.",

		// Adversarial Distractors & Noise
		"c:/windows/system32/drivers/windows_system_driver.sys": "System kernel driver binary. System hardware abstraction and kernel dispatch routines.",
		"cache/temp_data/unrelated_log_archive.log":             "Temporary debug log archive containing timestamped traces, socket allocations, and garbage collection pauses.",
		"temp/backup/cache.dat":                                 "Binary data cache file with system buffers.",
	}
}

// Build30EvalQueries returns 30 curated queries across 7 search categories.
func Build30EvalQueries() []EvalQuery {
	return []EvalQuery{
		// Category 1: Exact Filename & Path Matches
		{
			ID: 1, Query: "Q3_2024_Financial_Report", Category: "1. Exact Filename/Path",
			Relevance: map[string]float64{"docs/finance/Q3_2024_Financial_Report.pdf": 3.0},
		},
		{
			ID: 2, Query: "auth_service.go", Category: "1. Exact Filename/Path",
			Relevance: map[string]float64{"src/backend/auth_service.go": 3.0},
		},
		{
			ID: 3, Query: "DSC_0942", Category: "1. Exact Filename/Path",
			Relevance: map[string]float64{"photos/nature/DSC_0942_sunset_over_mountains.jpg": 3.0},
		},
		{
			ID: 4, Query: "onboarding_guide", Category: "1. Exact Filename/Path",
			Relevance: map[string]float64{"docs/hr/onboarding_guide.md": 3.0},
		},
		{
			ID: 5, Query: "system_architecture_spec", Category: "1. Exact Filename/Path",
			Relevance: map[string]float64{"specs/system_architecture_spec.md": 3.0, "screenshots/system_architecture_spec.png": 2.0},
		},

		// Category 2: Keyword / Content Extraction
		{
			ID: 6, Query: "jwt token expiration claims", Category: "2. Content Keyword Phrase",
			Relevance: map[string]float64{"src/backend/auth_service.go": 3.0, "src/backend/token_validator.ts": 2.0},
		},
		{
			ID: 7, Query: "uber receipt airport dropoff", Category: "2. Content Keyword Phrase",
			Relevance: map[string]float64{"docs/expenses/receipt_uber_airport.pdf": 3.0},
		},
		{
			ID: 8, Query: "stripe payment invoice $450", Category: "2. Content Keyword Phrase",
			Relevance: map[string]float64{"docs/invoices/invoice_stripe_nov2024.pdf": 3.0},
		},
		{
			ID: 9, Query: "database migration foreign key constraint", Category: "2. Content Keyword Phrase",
			Relevance: map[string]float64{"src/database/db_migrator.py": 3.0, "docs/notes/meeting_notes_2024_10_12.txt": 2.0},
		},
		{
			ID: 10, Query: "meeting notes action items", Category: "2. Content Keyword Phrase",
			Relevance: map[string]float64{"docs/notes/meeting_notes_2024_10_12.txt": 3.0},
		},

		// Category 3: Conceptual / Semantic (Synonym / No exact keyword match in title or body)
		{
			ID: 11, Query: "business sales profitability forecast", Category: "3. Conceptual / Semantic",
			Relevance: map[string]float64{"docs/finance/Q3_2024_Financial_Report.pdf": 3.0},
		},
		{
			ID: 12, Query: "new hire starting setup package", Category: "3. Conceptual / Semantic",
			Relevance: map[string]float64{"docs/hr/onboarding_guide.md": 3.0},
		},
		{
			ID: 13, Query: "existential dread and absurd condition", Category: "3. Conceptual / Semantic",
			Relevance: map[string]float64{"docs/philosophy/notes_camus_myth_of_sisyphus.txt": 3.0},
		},
		{
			ID: 14, Query: "database schema relationships diagram", Category: "3. Conceptual / Semantic",
			Relevance: map[string]float64{"diagrams/whiteboard_er_diagram.png": 3.0},
		},
		{
			ID: 15, Query: "protect routes with bearer credentials", Category: "3. Conceptual / Semantic",
			Relevance: map[string]float64{"src/backend/auth_service.go": 3.0, "src/backend/token_validator.ts": 2.0},
		},
		{
			ID: 16, Query: "commute travel ride reimbursement", Category: "3. Conceptual / Semantic",
			Relevance: map[string]float64{"docs/expenses/receipt_uber_airport.pdf": 3.0},
		},

		// Category 4: Typo / Fuzzy / Misspelling
		{
			ID: 17, Query: "albret camuls sisifus", Category: "4. Typo / Fuzzy Misspelling",
			Relevance: map[string]float64{"docs/philosophy/notes_camus_myth_of_sisyphus.txt": 3.0},
		},
		{
			ID: 18, Query: "finacial repot q3", Category: "4. Typo / Fuzzy Misspelling",
			Relevance: map[string]float64{"docs/finance/Q3_2024_Financial_Report.pdf": 3.0},
		},
		{
			ID: 19, Query: "authenication servise", Category: "4. Typo / Fuzzy Misspelling",
			Relevance: map[string]float64{"src/backend/auth_service.go": 3.0},
		},
		{
			ID: 20, Query: "onbordng guid", Category: "4. Typo / Fuzzy Misspelling",
			Relevance: map[string]float64{"docs/hr/onboarding_guide.md": 3.0},
		},
		{
			ID: 21, Query: "witebord diagrm", Category: "4. Typo / Fuzzy Misspelling",
			Relevance: map[string]float64{"diagrams/whiteboard_er_diagram.png": 3.0},
		},

		// Category 5: Acronym & Substring / Infix Matches
		{
			ID: 22, Query: "cv", Category: "5. Infix / Substring Acronym",
			Relevance: map[string]float64{"personal/career/resume_mahircv.pdf": 3.0},
		},
		{
			ID: 23, Query: "arch spec", Category: "5. Infix / Substring Acronym",
			Relevance: map[string]float64{"specs/system_architecture_spec.md": 3.0, "screenshots/system_architecture_spec.png": 2.0},
		},
		{
			ID: 24, Query: "migrator", Category: "5. Infix / Substring Acronym",
			Relevance: map[string]float64{"src/database/db_migrator.py": 3.0},
		},
		{
			ID: 25, Query: "sunset", Category: "5. Infix / Substring Acronym",
			Relevance: map[string]float64{"photos/nature/DSC_0942_sunset_over_mountains.jpg": 3.0},
		},

		// Category 6: Semantic Query Test
		{
			ID: 26, Query: "proprietary ownership and non disclosure clauses", Category: "6. Semantic Query Test",
			Relevance: map[string]float64{"docs/legal/contract_agreement_v2_signed.pdf": 3.0},
		},
		{
			ID: 27, Query: "fiscal year income filing deductions", Category: "6. Semantic Query Test",
			Relevance: map[string]float64{"archive/tax/old_tax_return_2022.pdf": 3.0},
		},
		{
			ID: 28, Query: "resilient distributed database infrastructure", Category: "6. Semantic Query Test",
			Relevance: map[string]float64{"specs/system_architecture_spec.md": 3.0},
		},

		// Category 7: Adversarial Distractors & Noise Filtering
		{
			ID: 29, Query: "system driver", Category: "7. Noise / Distractor Suppression",
			Relevance: map[string]float64{"specs/system_architecture_spec.md": 1.0}, // The .sys file is non-relevant noise (rel=0)
		},
		{
			ID: 30, Query: "temporary cache backup", Category: "7. Noise / Distractor Suppression",
			Relevance: map[string]float64{"src/pipeline/data_pipeline.py": 1.0}, // The .dat and .log files are noise (rel=0)
		},
	}
}

// ComputeMetrics calculates NDCG@10, Recall@10, and MRR@10 for ranked paths against ground truth.
func ComputeMetrics(rankedPaths []string, relMap map[string]float64, k int) (ndcg, recall, mrr float64) {
	if len(relMap) == 0 {
		return 1.0, 1.0, 1.0
	}

	var idealGains []float64
	totalRelevant := 0
	for _, gain := range relMap {
		if gain > 0 {
			idealGains = append(idealGains, gain)
			totalRelevant++
		}
	}
	if totalRelevant == 0 {
		return 1.0, 1.0, 1.0
	}

	sort.Sort(sort.Reverse(sort.Float64Slice(idealGains)))
	idcg := 0.0
	for i := 0; i < len(idealGains) && i < k; i++ {
		idcg += (math.Pow(2, idealGains[i]) - 1) / math.Log2(float64(i+2))
	}

	dcg := 0.0
	retrievedRelevant := 0
	firstRelRank := 0

	for i := 0; i < len(rankedPaths) && i < k; i++ {
		p := rankedPaths[i]
		gain := relMap[p]
		if gain > 0 {
			dcg += (math.Pow(2, gain) - 1) / math.Log2(float64(i+2))
			retrievedRelevant++
			if firstRelRank == 0 {
				firstRelRank = i + 1
			}
		}
	}

	if idcg > 0 {
		ndcg = dcg / idcg
	}
	recall = float64(retrievedRelevant) / float64(totalRelevant)
	if firstRelRank > 0 {
		mrr = 1.0 / float64(firstRelRank)
	}

	return ndcg, recall, mrr
}

// DeterministicSubwordEmbedder provides a deterministic 256-d subword hashing embedder
// for environments (such as headless CI test runners) where local model weights are absent.
type DeterministicSubwordEmbedder struct {
	dim int
}

func NewDeterministicSubwordEmbedder(dim int) *DeterministicSubwordEmbedder {
	return &DeterministicSubwordEmbedder{dim: dim}
}

func (d *DeterministicSubwordEmbedder) Dim() int { return d.dim }

func (d *DeterministicSubwordEmbedder) EmbedTruncated(text string, outDim int) []float32 {
	if outDim <= 0 || outDim > d.dim {
		outDim = d.dim
	}
	vec := make([]float32, outDim)
	lower := strings.ToLower(text)
	words := strings.Fields(lower)
	for _, w := range words {
		h := xxhash.Sum64String(w)
		idx := int(h % uint64(outDim))
		sign := float32(1.0)
		if (h>>32)&1 == 1 {
			sign = -1.0
		}
		vec[idx] += sign
		if len(w) >= 3 {
			for i := 0; i <= len(w)-3; i++ {
				gh := xxhash.Sum64String(w[i : i+3])
				gIdx := int(gh % uint64(outDim))
				gSign := float32(0.5)
				if (gh>>32)&1 == 1 {
					gSign = -0.5
				}
				vec[gIdx] += gSign
			}
		}
	}
	return staticNormalize(vec)
}

func (d *DeterministicSubwordEmbedder) EmbedBatch(texts []string, outDim int) [][]float32 {
	res := make([][]float32, len(texts))
	for i, t := range texts {
		res[i] = d.EmbedTruncated(t, outDim)
	}
	return res
}

// findModelFile searches candidate relative directories for real model weights.
func findModelFile() (string, string, error) {
	candidates := []string{
		"text/model.safetensors",
		"../text/model.safetensors",
		"../../text/model.safetensors",
		"text/dd/model.safetensors",
		"../text/dd/model.safetensors",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			tokP := filepath.Join(filepath.Dir(p), "tokenizer.json")
			if _, err := os.Stat(tokP); err == nil {
				return p, tokP, nil
			}
		}
	}
	return "", "", fmt.Errorf("model weights file text/model.safetensors not found in search paths")
}

// computeFileSHA256 returns the hex-encoded SHA-256 hash and byte size of a file.
func computeFileSHA256(filePath string) (string, int64, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), info.Size(), nil
}

// RunBenchmark executes the 30-query retrieval benchmark using verified local model weights.
// The benchmark FAILS if real model weights cannot be loaded.
func RunBenchmark() (*BenchmarkReport, error) {
	modelPath, tokPath, err := findModelFile()
	if err != nil {
		return nil, fmt.Errorf("RunBenchmark requires real model weights on disk: %w\nDownload the model bundle into text/ or run RunMockRegressionBenchmark for synthetic tests", err)
	}

	staticEmb, err := LoadStaticEmbedder(modelPath, tokPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load StaticEmbedder from %s: %w", modelPath, err)
	}
	defer staticEmb.Close()

	hash, size, err := computeFileSHA256(modelPath)
	if err != nil {
		return nil, fmt.Errorf("compute model hash: %w", err)
	}

	modelInfo := BenchmarkModelInfo{
		Name:     "static-retrieval-256d",
		Backend:  "StaticEmbedder (zero-copy mmap, 256-d Matryoshka truncation)",
		Path:     modelPath,
		FileSize: size,
		SHA256:   hash,
		Dim:      textEmbedDim,
	}

	return runBenchmarkWithEmbedder(staticEmb, modelInfo)
}

// RunMockRegressionBenchmark executes an explicitly labeled regression benchmark using synthetic
// subword hashing. This test runs in headless CI environments without model assets to verify pipeline integrity.
func RunMockRegressionBenchmark() (*BenchmarkReport, error) {
	mockEmb := NewDeterministicSubwordEmbedder(textEmbedDim)
	modelInfo := BenchmarkModelInfo{
		Name:     "[MOCK / SYNTHETIC REGRESSION TEST]",
		Backend:  "DeterministicSubwordEmbedder (xxhash subwords/trigrams, non-learned mock)",
		Path:     "(none - synthetic hash generator)",
		FileSize: 0,
		SHA256:   "(synthetic)",
		Dim:      textEmbedDim,
	}
	return runBenchmarkWithEmbedder(mockEmb, modelInfo)
}

func runBenchmarkWithEmbedder(embedder TextEmbedder, modelInfo BenchmarkModelInfo) (*BenchmarkReport, error) {
	corpus := BuildEvalCorpus()
	queries := Build30EvalQueries()

	ctx := context.Background()
	dir, err := os.MkdirTemp("", "eval_benchmark_*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	dbPath := filepath.Join(dir, "eval_main.db")
	vectorPath := filepath.Join(dir, "eval_vectors.db")
	dim := textEmbedDim

	store, err := core.New(vectorPath, dim)
	if err != nil {
		return nil, fmt.Errorf("create sqvect: %w", err)
	}
	if err := store.Init(ctx); err != nil {
		return nil, fmt.Errorf("init sqvect: %w", err)
	}
	defer store.Close()

	coll, err := store.CreateCollection(ctx, textCollection, dim)
	if err != nil {
		return nil, fmt.Errorf("text coll: %w", err)
	}
	imgColl, err := store.CreateCollection(ctx, imageCollection, dim)
	if err != nil {
		return nil, fmt.Errorf("img coll: %w", err)
	}

	vDB, err := sql.Open("sqlite", vectorPath)
	if err != nil {
		return nil, fmt.Errorf("open vDB: %w", err)
	}
	defer vDB.Close()
	vDB.Exec("PRAGMA journal_mode=WAL")
	vDB.Exec("PRAGMA busy_timeout=10000")

	sqlDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlDB: %w", err)
	}
	defer sqlDB.Close()
	sqlDB.Exec("PRAGMA journal_mode=WAL")
	sqlDB.Exec("PRAGMA busy_timeout=10000")

	if _, err := sqlDB.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS paths_fts USING fts5(searchable, path UNINDEXED)`); err != nil {
		return nil, fmt.Errorf("create paths_fts: %w", err)
	}

	hybridEngine := &Engine{
		sqlDB:        sqlDB,
		vDB:          vDB,
		db:           store,
		textEmbedder: embedder,
	}
	if err := hybridEngine.InitIndexTables(); err != nil {
		return nil, fmt.Errorf("init index tables: %w", err)
	}

	// Exact ablation: same database and tables, but textEmbedder is nil so semantics is completely disabled.
	lexEngine := &Engine{
		sqlDB:        sqlDB,
		vDB:          vDB,
		db:           store,
		textEmbedder: nil,
	}

	docIdx := 1
	for path, content := range corpus {
		now := time.Now().Unix()
		ext := filepath.Ext(path)
		if _, err := sqlDB.Exec(`INSERT INTO files(path, hash, mtime, size, content_indexed, ext) VALUES (?, ?, ?, ?, 1, ?)`,
			path, int64(docIdx*100), now, int64(len(content)), ext); err != nil {
			return nil, fmt.Errorf("insert files: %w", err)
		}

		searchable := strings.ToLower(filepath.ToSlash(path))
		if _, err := sqlDB.Exec(`INSERT INTO paths_fts(searchable, path) VALUES (?, ?)`, searchable, path); err != nil {
			return nil, fmt.Errorf("insert paths_fts: %w", err)
		}

		vec := embedder.EmbedTruncated(content, dim)
		targetColl := coll.ID
		if IsImageFile(path) {
			targetColl = imgColl.ID
		}
		if err := store.Upsert(ctx, &core.Embedding{
			ID:           fmt.Sprintf("doc-%d", docIdx),
			CollectionID: targetColl,
			Vector:       vec,
			Content:      content,
			Metadata:     map[string]string{"path": path},
		}); err != nil {
			return nil, fmt.Errorf("upsert vector: %w", err)
		}

		docIdx++
	}

	// Warmup passes: prime SQLite page cache and memory mappings
	for _, q := range queries {
		_, _ = lexEngine.Search(q.Query)
		_, _ = hybridEngine.Search(q.Query)
	}

	modes := []struct {
		name string
	}{
		{"Lexical Ablation (Semantics Off)"},
		{"Pure Semantic"},
		{"Filosophy Hybrid"},
	}

	results := make(map[string]*EvalResult)
	for _, m := range modes {
		results[m.name] = &EvalResult{Name: m.name}
	}

	catQueries := make(map[string][]EvalQuery)
	for _, q := range queries {
		catQueries[q.Category] = append(catQueries[q.Category], q)
	}
	var sortedCategories []string
	for cat := range catQueries {
		sortedCategories = append(sortedCategories, cat)
	}
	sort.Strings(sortedCategories)

	catMetrics := make(map[string]map[string][3]float64)
	for _, cat := range sortedCategories {
		catMetrics[cat] = make(map[string][3]float64)
	}

	queryLogs := make([]QueryEvalLog, len(queries))
	for i, q := range queries {
		queryLogs[i] = QueryEvalLog{
			ID:       q.ID,
			Query:    q.Query,
			Category: q.Category,
		}
	}

	for _, m := range modes {
		var latencies []float64
		var indexLatencies []float64
		totalNDCG, totalRecall, totalMRR := 0.0, 0.0, 0.0

		for qIdx, q := range queries {
			const iters = 10
			var rankedPaths []string
			var dur time.Duration
			var indexDur time.Duration

			if m.name == "Lexical Ablation (Semantics Off)" {
				start := time.Now()
				for it := 0; it < iters; it++ {
					res, err := lexEngine.Search(q.Query)
					if err != nil {
						return nil, fmt.Errorf("lexical search %q: %w", q.Query, err)
					}
					if it == 0 {
						rankedPaths = make([]string, len(res))
						for i, item := range res {
							rankedPaths[i] = item.Path
						}
					}
				}
				dur = time.Since(start) / iters
				indexDur = dur

			} else if m.name == "Pure Semantic" {
				// Disentangled query embedding latency
				embedStart := time.Now()
				for it := 0; it < iters; it++ {
					_ = embedder.EmbedTruncated(q.Query, dim)
				}
				embedDur := time.Since(embedStart) / iters

				// Disentangled index traversal latency
				qVec := embedder.EmbedTruncated(q.Query, dim)
				idxStart := time.Now()
				for it := 0; it < iters; it++ {
					scores := make(map[string]float64)
					tRes, err := hybridEngine.db.Search(ctx, qVec, core.SearchOptions{
						Collection: textCollection,
						TopK:       10,
					})
					if err == nil {
						for _, res := range tRes {
							p := res.Metadata["path"]
							sim := float64(res.Score)
							if sim > scores[p] {
								scores[p] = sim
							}
						}
					}
					if it == 0 {
						type scoredDoc struct {
							path  string
							score float64
						}
						var ranked []scoredDoc
						for p, s := range scores {
							ranked = append(ranked, scoredDoc{p, s})
						}
						sort.Slice(ranked, func(i, j int) bool {
							return ranked[i].score > ranked[j].score
						})
						rankedPaths = make([]string, len(ranked))
						for i, item := range ranked {
							rankedPaths[i] = item.path
						}
					}
				}
				idxDur := time.Since(idxStart) / iters
				dur = embedDur + idxDur
				indexDur = idxDur

			} else { // Filosophy Hybrid
				start := time.Now()
				for it := 0; it < iters; it++ {
					res, err := hybridEngine.Search(q.Query)
					if err != nil {
						return nil, fmt.Errorf("hybrid search %q: %w", q.Query, err)
					}
					if it == 0 {
						rankedPaths = make([]string, len(res))
						for i, item := range res {
							rankedPaths[i] = item.Path
						}
					}
				}
				dur = time.Since(start) / iters
				indexDur = dur
			}

			latMs := float64(dur.Microseconds()) / 1000.0
			latencies = append(latencies, latMs)

			idxLatMs := float64(indexDur.Microseconds()) / 1000.0
			indexLatencies = append(indexLatencies, idxLatMs)

			ndcg, recall, mrr := ComputeMetrics(rankedPaths, q.Relevance, 10)
			totalNDCG += ndcg
			totalRecall += recall
			totalMRR += mrr

			cm := catMetrics[q.Category][m.name]
			cm[0] += ndcg
			cm[1] += recall
			cm[2] += mrr
			catMetrics[q.Category][m.name] = cm

			top1 := "(none)"
			if len(rankedPaths) > 0 {
				top1 = rankedPaths[0]
			}
			if m.name == "Lexical Ablation (Semantics Off)" {
				queryLogs[qIdx].LexNDCG = ndcg
			} else if m.name == "Pure Semantic" {
				queryLogs[qIdx].SemNDCG = ndcg
			} else {
				queryLogs[qIdx].HybNDCG = ndcg
				queryLogs[qIdx].Top1Hit = top1
			}
		}

		n := float64(len(queries))
		sort.Float64s(latencies)
		p50 := latencies[len(latencies)*50/100]
		p95 := latencies[len(latencies)*95/100]
		sumLat := 0.0
		for _, l := range latencies {
			sumLat += l
		}
		sumIndexLat := 0.0
		for _, l := range indexLatencies {
			sumIndexLat += l
		}

		res := results[m.name]
		res.NDCG10 = totalNDCG / n
		res.Recall10 = totalRecall / n
		res.MRR10 = totalMRR / n
		res.MeanLatMs = sumLat / n
		res.P50LatMs = p50
		res.P95LatMs = p95
		res.IndexOnlyLatMs = sumIndexLat / n
	}

	for _, cat := range sortedCategories {
		count := float64(len(catQueries[cat]))
		for _, m := range modes {
			cm := catMetrics[cat][m.name]
			cm[0] /= count
			cm[1] /= count
			cm[2] /= count
			catMetrics[cat][m.name] = cm
		}
	}

	return &BenchmarkReport{
		ModelInfo:        modelInfo,
		Results:          results,
		CategoryMetrics:  catMetrics,
		SortedCategories: sortedCategories,
		Queries:          queries,
		QueryLogs:        queryLogs,
	}, nil
}

// PrintBenchmarkReport outputs the complete benchmark report including cryptographic provenance,
// pipeline results, category breakdown, and detailed per-query ranked logs.
func PrintBenchmarkReport(logFn func(format string, args ...any), report *BenchmarkReport) {
	logFn("\n=========================================================================================")
	logFn("                    FILOSOPHY RETRIEVAL BENCHMARK & ABLATION REPORT                      ")
	logFn("=========================================================================================")
	logFn("MODEL PROVENANCE & WEIGHT VERIFICATION:")
	logFn("  Model Name   : %s", report.ModelInfo.Name)
	logFn("  Backend      : %s", report.ModelInfo.Backend)
	logFn("  Weight Path  : %s", report.ModelInfo.Path)
	if report.ModelInfo.FileSize > 0 {
		logFn("  File Size    : %d bytes (%.2f MB)", report.ModelInfo.FileSize, float64(report.ModelInfo.FileSize)/(1024*1024))
	}
	logFn("  SHA-256 Hash : %s", report.ModelInfo.SHA256)
	logFn("  Dimension    : %d (Matryoshka truncation)", report.ModelInfo.Dim)
	logFn("  Corpus Scope : Text & Document Retrieval (22 files: reports, specs, code, notes)")
	logFn("=========================================================================================")

	modes := []string{"Lexical Ablation (Semantics Off)", "Pure Semantic", "Filosophy Hybrid"}
	logFn("%-32s | %-10s | %-10s | %-10s | %-10s | %-10s | %-10s | %-10s",
		"Pipeline", "NDCG@10", "MeanRecall", "MRR@10", "Mean (ms)", "p50 (ms)", "p95 (ms)", "IndexOnly")
	logFn("-----------------------------------------------------------------------------------------------------------------")
	for _, modeName := range modes {
		res := report.Results[modeName]
		logFn("%-32s | %-10.4f | %-10.4f | %-10.4f | %-10.2f | %-10.2f | %-10.2f | %-10.2f",
			res.Name, res.NDCG10, res.Recall10, res.MRR10, res.MeanLatMs, res.P50LatMs, res.P95LatMs, res.IndexOnlyLatMs)
	}

	logFn("=========================================================================================")
	logFn("                               CATEGORY BREAKDOWN (NDCG@10)                              ")
	logFn("-----------------------------------------------------------------------------------------")
	logFn("%-35s | %-18s | %-15s | %-15s", "Category", "Lexical Ablation", "Pure Semantic", "Filosophy Hybrid")
	logFn("-----------------------------------------------------------------------------------------")
	for _, cat := range report.SortedCategories {
		lex := report.CategoryMetrics[cat]["Lexical Ablation (Semantics Off)"][0]
		sem := report.CategoryMetrics[cat]["Pure Semantic"][0]
		hyb := report.CategoryMetrics[cat]["Filosophy Hybrid"][0]
		logFn("%-35s | %-18.4f | %-15.4f | %-15.4f", cat, lex, sem, hyb)
	}

	logFn("=========================================================================================")
	logFn("                               PER-QUERY DETAILED EVALUATION LOG                         ")
	logFn("-----------------------------------------------------------------------------------------")
	logFn("%-3s | %-32s | %-8s | %-8s | %-8s | %-30s", "ID", "Query", "LexNDCG", "SemNDCG", "HybNDCG", "Hybrid Top-1 Match")
	logFn("-----------------------------------------------------------------------------------------")
	for _, ql := range report.QueryLogs {
		qStr := ql.Query
		if len(qStr) > 32 {
			qStr = qStr[:29] + "..."
		}
		top1 := ql.Top1Hit
		if len(top1) > 30 {
			top1 = "..." + top1[len(top1)-27:]
		}
		logFn("%-3d | %-32s | %-8.4f | %-8.4f | %-8.4f | %-30s", ql.ID, qStr, ql.LexNDCG, ql.SemNDCG, ql.HybNDCG, top1)
	}
	logFn("=========================================================================================\n")
}
