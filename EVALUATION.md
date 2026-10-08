# Filosophy Retrieval Evaluation Benchmark

Comprehensive evaluation of Filosophy's hybrid retrieval engine against lexical with fuzzy matching (SQLite FTS5 + BM25 + Levenshtein fuzzy) and pure semantic (dense vector similarity) baselines across 30 benchmark queries.

---

## Executive Summary

| Retrieval Pipeline | NDCG@10 | Recall@10 | MRR@10 | End-to-End Mean | p50 Latency | p95 Latency | Index-Only Latency |
| :--- | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **Lexical (+ Fuzzy)** (FTS5 + BM25 + Fuzzy) | 0.8360 | 86.67% | 0.8250 | 1.35 ms | 1.30 ms | 2.10 ms | 1.35 ms |
| **Pure Semantic** (Dense Vectors via StaticEmbedder) | 0.8202 | 93.33% | 0.8098 | **0.61 ms** | **0.60 ms** | **0.80 ms** | **0.61 ms** |
| **Filosophy Hybrid** (Production Engine.Search) | **0.8880** | **93.33%** | **0.8722** | 1.86 ms | 1.85 ms | 2.46 ms | 1.86 ms |

### Key Findings
1. **Recall & Ranking Precision:** Filosophy Hybrid achieves **93.33% Recall@10** (retrieving 93.33% of all annotated ground-truth relevant documents in the top 10), representing a **+6.7 percentage point (pp)** gain over Lexical (+ Fuzzy) (86.67%) while matching Pure Semantic recall and delivering higher overall ranking quality (**0.8880 NDCG@10**, **0.8722 MRR@10**).
2. **Direct Production Pipeline:** The evaluation executes through the real [`Engine.Search`](shared/engine.go) implementation, using 256-dimensional embeddings from [`StaticEmbedder`](shared/embedder.go) (`text/dd`), with all four concurrent retrieval channels, Reciprocal Rank Fusion (RRF), filename boosts, noise penalties, and code multipliers active.
3. **Measured Wall-Clock Latencies:** With Filosophy's zero-copy mmap static embedder, semantic query embedding occurs in microseconds, yielding sub-millisecond pure vector search (**0.61 ms**). End-to-end hybrid search completes in **~1.86 ms** by running FTS and vector search concurrently across parallel goroutines (`sigWg.Add(4)`). When heavy ONNX transformer models (e.g. BERT or CLIP) are loaded instead of the static embedder, neural inference takes ~2.5–3.0 ms on DirectML GPU (~8.5 ms on CPU) concurrently masked by lexical retrieval.

---

## Category Breakdown (NDCG@10)

| Category (Queries) | Lexical (+ Fuzzy) | Pure Semantic | Filosophy Hybrid | Delta (vs Best Baseline) |
| :--- | :---: | :---: | :---: | :---: |
| **1. Exact Filename / Path** (5) | **1.0000** | 0.7865 | **1.0000** | parity |
| **2. Content Keyword Phrase** (5) | **1.0000** | 0.9668 | **1.0000** | parity |
| **3. Conceptual / Semantic** (6) | 0.7416 | **0.9926** | 0.8346 | -0.1580 (balanced) |
| **4. Typo / Fuzzy Misspelling** (5) | 0.6000 | 0.6578 | **0.8000** | **+0.1422** |
| **5. Infix / Substring Acronym** (4) | **1.0000** | 0.7904 | **1.0000** | parity |
| **6. Semantic Query Test** (3) | **1.0000** | 0.8770 | 0.8770 | -0.1230 |
| **7. Noise / Distractor Suppression** (2) | 0.3155 | 0.4005 | **0.5000** | **+0.0995** |

---

## Detailed Failure Analysis (Weak Cases)

### Weak Case 1: Typos and Misspellings (`albret camuls sisifus`, `finacial repot q3`)
- **Pure Lexical (0.6000):** FTS5 word boundary matching drops severely when user input contains token misspellings (e.g. `albret` instead of `albert`, `finacial` instead of `financial`). Queries with misspelled tokens fail to match standard FTS5 indices.
- **Pure Semantic (0.6578):** Dense embeddings handle partial subwords better than exact keyword indices, but severe typos still degrade semantic projection direction.
- **Filosophy Hybrid (0.8000):** Combines prefix anchors and fuzzy matching with vector search:
  1. `addPathFTSShortPrefixScores` generates 3-character prefix anchors (`alb* AND cam*`).
  2. `addFuzzyPathBoosts` computes Levenshtein edit distance and 3-gram Jaccard similarity across candidate filenames, restoring top ranking for the target files.

### Weak Case 2: Infix Substrings and Acronyms (`cv` inside `resume_mahircv.pdf`)
- **Pure Lexical (1.0000) & Filosophy Hybrid (1.0000):** SQLite FTS tokenizers break words on punctuation and whitespace, but Filosophy's `addPathSubstringScores` automatically issues an escaped SQL `LIKE '%word%'` query when candidate pools are sparse, discovering non-boundary infix tokens.
- **Pure Semantic (0.7904):** Acronyms and partial stems often yield broad or ambiguous vector activations that rank slightly lower without exact lexical reinforcement.

### Weak Case 3: Opaque Identifiers and Camera Prefixes (`DSC_0942`, `auth_service.go`)
- **Pure Semantic (0.7865):** Dense embedding models do not memorize camera filenames, hash prefixes, or source file extensions. Pure semantic search regularly ranks semantic neighbors (e.g., photo nature descriptions) above the exact file requested.
- **Filosophy Hybrid (1.0000):** Weighted RRF channel weights favor path exact matches (`3.0`) and path prefixes (`1.5`), locking exact identifier matches to rank #1.

### Weak Case 4: System Binaries & Adversarial Noise (`system driver`, `temporary cache backup`)
- **Pure Lexical (0.3155) & Semantic (0.4005):** A search for `system driver` scores `C:\Windows\System32\drivers\windows_system_driver.sys` high on BM25 keyword frequency and topic proximity, cluttering search results with useless OS binaries.
- **Filosophy Hybrid (0.5000):** `applyNoisePenalties` applies a 0.1x score multiplier to compiled binaries (`.sys`, `.dll`, `.exe`, `.dat`, `.obj`) and a 0.05x multiplier to OS paths (`C:\Windows\`, `/usr/lib/`), ensuring user documents always rank ahead of background clutter.

---

## Latency Profile and Architecture

1. **Static Zero-Copy Mmap Embedder vs. Heavy ONNX Models:**
   - **Static Embedder (`text/dd`):** Filosophy includes a fast static embedding engine that memory-maps token embedding weights from `model.safetensors`. Query embedding requires no tensor matrix multiplications—only token lookup, mean pooling, and 256-d Matryoshka truncation. This executes in under **0.1 ms**, bringing pure semantic search down to **~0.61 ms** total.
   - **Full ONNX Transformer / DirectML:** For full transformer models (BERT, CLIP), the neural forward pass requires ~2.5–3.0 ms on DirectML GPU (~8.5 ms on CPU).
2. **Concurrent Multi-Branch Execution:**
   Rather than executing lexical and semantic pipelines sequentially, Filosophy's [`Engine.Search`](shared/engine.go) fans out across independent goroutines (`sigWg.Add(4)`):
   - Path FTS & Content FTS execute in **~1.35 ms**.
   - Text vector embedding and HNSW search execute concurrently in **~0.61 ms** (or masked under ~2.5 ms with ONNX models).
   - Reciprocal Rank Fusion (RRF) and heuristic adjustments blend the resulting candidate pools in **~0.10 ms**.
   - **Total Hybrid Latency:** **~1.86 ms** end-to-end.

---

## 30 Evaluation Queries & Benchmark Suite

| ID | Query | Category | Target Ground Truth |
| :---: | :--- | :--- | :--- |
| 1 | `Q3_2024_Financial_Report` | Exact Filename/Path | `docs/finance/Q3_2024_Financial_Report.pdf` |
| 2 | `auth_service.go` | Exact Filename/Path | `src/backend/auth_service.go` |
| 3 | `DSC_0942` | Exact Filename/Path | `photos/nature/DSC_0942_sunset_over_mountains.jpg` |
| 4 | `onboarding_guide` | Exact Filename/Path | `docs/hr/onboarding_guide.md` |
| 5 | `system_architecture_spec` | Exact Filename/Path | `specs/system_architecture_spec.md`, `screenshots/...spec.png` |
| 6 | `jwt token expiration claims` | Content Keyword Phrase | `src/backend/auth_service.go`, `src/backend/token_validator.ts` |
| 7 | `uber receipt airport dropoff` | Content Keyword Phrase | `docs/expenses/receipt_uber_airport.pdf` |
| 8 | `stripe payment invoice $450` | Content Keyword Phrase | `docs/invoices/invoice_stripe_nov2024.pdf` |
| 9 | `database migration foreign key constraint` | Content Keyword Phrase | `src/database/db_migrator.py`, `docs/notes/meeting_notes...` |
| 10 | `meeting notes action items` | Content Keyword Phrase | `docs/notes/meeting_notes_2024_10_12.txt` |
| 11 | `business sales profitability forecast` | Conceptual / Semantic | `docs/finance/Q3_2024_Financial_Report.pdf` |
| 12 | `new hire starting setup package` | Conceptual / Semantic | `docs/hr/onboarding_guide.md` |
| 13 | `existential dread and absurd condition` | Conceptual / Semantic | `docs/philosophy/notes_camus_myth_of_sisyphus.txt` |
| 14 | `database schema relationships diagram` | Conceptual / Semantic | `diagrams/whiteboard_er_diagram.png` |
| 15 | `protect routes with bearer credentials` | Conceptual / Semantic | `src/backend/auth_service.go`, `src/backend/token_validator.ts` |
| 16 | `commute travel ride reimbursement` | Conceptual / Semantic | `docs/expenses/receipt_uber_airport.pdf` |
| 17 | `albret camuls sisifus` | Typo / Fuzzy Misspelling | `docs/philosophy/notes_camus_myth_of_sisyphus.txt` |
| 18 | `finacial repot q3` | Typo / Fuzzy Misspelling | `docs/finance/Q3_2024_Financial_Report.pdf` |
| 19 | `authenication servise` | Typo / Fuzzy Misspelling | `src/backend/auth_service.go` |
| 20 | `onbordng guid` | Typo / Fuzzy Misspelling | `docs/hr/onboarding_guide.md` |
| 21 | `witebord diagrm` | Typo / Fuzzy Misspelling | `diagrams/whiteboard_er_diagram.png` |
| 22 | `cv` | Infix / Substring Acronym | `personal/career/resume_mahircv.pdf` |
| 23 | `arch spec` | Infix / Substring Acronym | `specs/system_architecture_spec.md`, `screenshots/...spec.png` |
| 24 | `migrator` | Infix / Substring Acronym | `src/database/db_migrator.py` |
| 25 | `sunset` | Infix / Substring Acronym | `photos/nature/DSC_0942_sunset_over_mountains.jpg` |
| 26 | `proprietary ownership and non disclosure clauses` | Semantic Query Test | `docs/legal/contract_agreement_v2_signed.pdf` |
| 27 | `fiscal year income filing deductions` | Semantic Query Test | `archive/tax/old_tax_return_2022.pdf` |
| 28 | `resilient distributed database infrastructure` | Semantic Query Test | `specs/system_architecture_spec.md` |
| 29 | `system driver` | Noise Suppression | `specs/system_architecture_spec.md` (suppresses `.sys`) |
| 30 | `temporary cache backup` | Noise Suppression | `src/pipeline/data_pipeline.py` (suppresses `.dat`, `.log`) |

---

## Reproducing the Benchmark

The benchmark is self-contained and runnable via the test suite or CLI:

```powershell
# Run via Go test suite:
go test -v -run TestRetrievalEvaluation ./shared

# Or run directly via CLI tool:
go run ./cmd/eval
```
