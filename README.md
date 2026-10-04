# Filosophy

Filosophy is a Windows desktop app for searching files stored on your computer. It combines filename and document-text search with locally computed text and image embeddings. Indexing and search do not require a hosted AI service. The optional Assistant can connect to a local or remote OpenAI-compatible provider when configured.

## Search and retrieval

Filosophy uses a staged, local hybrid retriever. The first indexing pass records eligible paths and metadata in SQLite, making filename and path keyword search available quickly. A background enhanced pass then extracts document text, chunks it, indexes it for full-text search, and writes text and image embeddings. Semantic matches become available as vectors are added; after the initial enhanced pass completes, the full hybrid index is ready. Search continues to combine keyword and semantic evidence rather than switching to semantic-only results.

### Candidate retrieval and ranking

Search gathers candidates from independent retrieval channels, then fuses their ranked lists:

- **Path and filename retrieval:** SQLite FTS matches exact terms, all query terms, prefixes, and substrings. Filename boosts and trigram similarity improve exact-name and typo-tolerant discovery.
- **Document-text retrieval:** Extracted chunks are searched with SQLite FTS5, including phrase and keyword signals; fuzzy content matching is used when exact retrieval returns few candidates.
- **Semantic retrieval:** A local text encoder retrieves related document chunks from the vector store, including conceptually similar passages that do not share query keywords. A text-to-image encoder retrieves relevant images through a separate, smaller candidate pool.

The ranked lists are combined with weighted Reciprocal Rank Fusion (RRF). RRF combines rank positions instead of adding raw BM25 and vector-similarity values, which have different score scales. Its smoothing constant limits how sharply a single rank position changes a channel's contribution. The default channel weights favor path matches (`3.0`) and path prefixes (`1.5`), with text semantics (`1.0`), image semantics (`1.1`), and content full-text (`1.0`) contributing complementary evidence.

Post-fusion adjustments keep results useful for real file discovery: strong filename matches and typo tolerance are boosted; path-only matches are capped so they do not outrank meaningful content matches; noisy system/binary files and generic code files are penalized; recency and query filters are applied; and results are sorted again. Code-file penalties are relaxed when the query closely matches extracted code content, so relevant source files can still rank well.

If the optional local cross-encoder is installed, it reranks the top eight fused candidates using up to two matching text excerpts per file. The reranker score is blended with the fused rank; candidates outside that reranking window retain their fused ordering. Embedding and reranking inference run locally.

### Feedback-driven weight tuning

Thumbs-up and thumbs-down feedback lets the ranking optimizer tune the balance between path, content, text-semantic, image-semantic, and reranker signals. After initial indexing, it waits two minutes before its first run, then evaluates feedback every ten minutes. It requires at least ten votes before tuning and gives recent feedback more influence, with its weight halving about every seven days.

For each distinct feedback query, the optimizer computes retrieval signals once, then runs a bounded in-memory grid search over nearby channel-weight combinations and reranker blends. It scores candidate rankings using NDCG@10, MRR@10, recall within the rerank window, and a penalty for highly ranked downvoted results. New weights are saved only when the measured preference objective improves, then hot-reloaded into search without restarting the app. This is feedback-based ranking calibration, not model fine-tuning: the encoders and reranker do not change.

### RAG boundary

The hybrid retriever provides a RAG-oriented retrieval layer: lexical retrieval handles exact names and terms, dense retrieval adds semantic recall, and fusion plus reranking improves the candidate order. The optional Assistant can invoke file search as a tool, but the current confirmation flow gives it the selected result paths, not automatically extracted passages. The Assistant's provider and credentials are configured separately in Settings; prompts sent to that provider leave the machine.

## External downloads

Model and runtime bundles are not stored in this repository. Keep the downloaded directory structure intact when extracting into the repository root; the release packaging script validates required local assets but does not download them.

- **Models:** [Download the model bundle](https://drive.google.com/file/d/1bBVwQ-Q1WAg5EuUUwlqAiXi5w3MaMUBg/view?usp=sharing). It should provide the `text/` and `image/` directories, including their ONNX models and tokenizer files. The optional reranker files belong in the repository root.
- **libvips:** [Windows x64 releases](https://github.com/libvips/build-win64-mxe/releases/tag/v8.18.2). Download `vips-dev-x64-all-8.18.2.zip` and extract its `vips-dev-8.18/` directory into the repository root.
- **ONNX Runtime:** [Windows x64 release v1.24.0](https://github.com/microsoft/onnxruntime/releases/tag/v1.24.0). Download the CPU package `onnxruntime-win-x64-1.24.0.zip` and place its `onnxruntime.dll` beside the application executable (or in the repository root for development).

## Build and run from source

The supported desktop build target is Windows x64. Install:

- Go 1.25.5 or later
- Node.js 20 or later with npm
- Wails CLI v2.16.0
- Rust and Cargo
- MSYS2 MinGW-w64 x64 GCC and binutils, including `x86_64-w64-mingw32-gcc` and `ar`
- The Microsoft WebView2 Runtime

Model and native runtime assets are distributed separately from this source repository and are not downloaded automatically. Download the assets below and extract them into the repository root before building or packaging.

```powershell
go mod download
./scripts/build-tokenizers.ps1
Push-Location frontend
npm ci
Pop-Location
wails dev
```

`wails dev` starts the desktop application and its Vite frontend. The UI alone can be previewed with `cd frontend; npm run dev`, but desktop features such as indexing and file opening require the Wails Go backend.

Build the frontend and Go application for release with:

```powershell
Push-Location frontend
npm ci
npm run build
Pop-Location
wails build
```

The executable is written to `build/bin/filosophy.exe`. To create a Windows zip from the staged runtime assets:

```powershell
./scripts/package-release.ps1 -Version 1.0.0
```

The script rebuilds the app, checks its required assets, and writes an archive under `dist/`. It does not fetch model files. Test the resulting archive on a clean Windows machine before publishing.

## Verify a source checkout

Run the Go tests and build:

```powershell
go test ./...
go build ./...
```

Build the frontend independently:

```powershell
Push-Location frontend
npm ci
npm run build
Pop-Location
```

## Project layout

- `main.go`, `frontend/`: Wails desktop application and UI
- `shared/`: indexing, extraction, query parsing, ranking, and local model inference
- `daemon/`, `cmd/`: background indexer and command-line entry points
- `api.go`, `mcp.go`: optional local REST and MCP endpoints
- `scripts/`: native tokenizer build and Windows release packaging
