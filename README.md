# Filosophy

Filosophy is a local search engine for the files on your computer. It reads PDFs, code, Office documents, Markdown and images, pulls out the text, splits it into chunks, embeds the chunks locally with ONNX Runtime, and indexes everything in SQLite FTS5 and a vector store. Search uses both, so you can find a file by its name or by what it is about.

The desktop app is Go and React (Wails). Text extraction runs through a Rust library over FFI. Indexing and search run on your machine and need no hosted AI service.

I wrote a benchmark comparing lexical, semantic and hybrid retrieval. The results are in [EVALUATION.md](EVALUATION.md).

![demo gif](demo.gif)

## How it works

```
[ Files ]  PDF, code, Office, images, Markdown
    |
    v
[ 1. Ingest and extract ]  Rust library over FFI (kreuzberg-ffi), libvips for images
    |
    v
[ 2. Chunk ]  sliding window sized by tokens, plus file metadata
    |
    v
[ 3. Embed ]  local text and image encoders (static retrieval model, ONNX Runtime)
    |
    +--------------------------+
    v                          v
[ 4a. SQLite FTS5 ]      [ 4b. Vector store ]
    |                          |
    +------------+-------------+
                 v
        [ 5. Fuse and rank ]  weighted RRF
```

Indexing happens in two passes. The first pass records paths and metadata in SQLite, so filename search works almost right away. A background pass then extracts text, chunks it, and writes the full-text index and the embeddings. Semantic results show up as vectors get added.

## Quickstart

Filosophy builds on Windows x64 only. The model files and native libraries are not in this repo (see [Downloads](#downloads)), and the build does not fetch them. Download them first and extract them into the repository root.

Install:

- Go 1.25.5 or later
- Node.js 20 or later, with npm
- Wails CLI v2.16.0
- Rust and Cargo
- MSYS2 MinGW-w64 x64 GCC and binutils (`x86_64-w64-mingw32-gcc` and `ar`)
- Microsoft WebView2 Runtime

Then:

```powershell
go mod download
./scripts/build-tokenizers.ps1
Push-Location frontend
npm ci
Pop-Location
wails dev
```

`wails dev` starts the desktop app and the Vite frontend. You can preview only the UI with `cd frontend; npm run dev`, but indexing and opening files need the Go backend.

To build a release:

```powershell
Push-Location frontend
npm ci
npm run build
Pop-Location
wails build
```

The executable lands in `build/bin/filosophy.exe`. To make a Windows zip from the assets you downloaded:

```powershell
./scripts/package-release.ps1 -Version 1.0.0
```

The script rebuilds the app, checks that the required assets are there, and writes an archive to `dist/`. Test the archive on a clean Windows machine before you share it.

Run the tests and build:

```powershell
go test ./...
go build ./...
```

## Downloads

Keep the directory structure intact when you extract into the repository root.

- **Models:** [model bundle](https://drive.google.com/file/d/1-0Ctf13L4wO93oPXhkF2osZwN6ppDTJu/view?usp=sharing). It gives you the `text/` and `image/` directories with the ONNX models and tokenizer files. The optional reranker files go in the repository root.
- **libvips:** [Windows x64 releases](https://github.com/libvips/build-win64-mxe/releases/tag/v8.18.2). Download `vips-dev-x64-all-8.18.2.zip` and extract its `vips-dev-8.18/` directory into the repository root.
- **ONNX Runtime:** see below.

### GPU acceleration (DirectML)

Filosophy uses the ONNX Runtime DirectML provider. It runs on any DirectX 12 GPU (AMD, Intel, NVIDIA) and does not need CUDA or cuDNN.

1. Download the [`Microsoft.ML.OnnxRuntime.DirectML`](https://www.nuget.org/packages/Microsoft.ML.OnnxRuntime.DirectML) package from NuGet.
2. Open the `.nupkg` as a zip and take `onnxruntime.dll` and `DirectML.dll` from `runtimes/win-x64/native/`.
3. Put both next to the executable (or in the repository root while developing).

If `DirectML.dll` is there, the GPU is used automatically.

### CPU only

Download the Windows x64 CPU release from [ONNX Runtime releases](https://github.com/microsoft/onnxruntime/releases) and put `onnxruntime.dll` next to the executable. Without `DirectML.dll`, the app falls back to the CPU.

## Search

Search pulls candidates from several channels and merges their ranked lists.

- **Path and filename:** SQLite FTS matches exact terms, prefixes and substrings. Filename boosts and trigram similarity help with exact names and typos.
- **Document text:** extracted chunks are searched with SQLite FTS5. If exact matching returns little, fuzzy matching kicks in.
- **Text semantic:** a local text encoder finds chunks that are close in meaning, even when they share no words with the query.
- **Text to image:** an image encoder finds images from a text query, using a separate and smaller candidate pool.

The lists are merged with weighted Reciprocal Rank Fusion (RRF). RRF uses rank positions, not raw scores, because BM25 and vector similarity are on different scales. Default weights:

| Channel | Weight |
| :--- | :---: |
| Path | 3.0 |
| Path prefix | 1.5 |
| Image semantic | 1.1 |
| Text semantic | 1.0 |
| Content FTS | 1.0 |

After fusion, a few adjustments make the results more useful for finding files. Strong filename matches and typo matches get a boost. Path-only matches are capped so they don't beat real content matches. System files, binaries and generic code files are pushed down, unless the query closely matches their content. Recency and query filters are applied, then the list is sorted again.

If you install the optional local cross-encoder, it reranks the top 8 results using up to two matching excerpts per file. Its score is blended with the fused rank. Everything past the top 8 keeps its fused order. Embedding and reranking both run locally.

### Feedback tuning

Thumbs up and down on results feed a bounded grid search over the channel weights. It optimizes NDCG@10 and MRR@10, needs at least 10 votes, and weights older votes less (about a 7-day half-life). New weights are hot-reloaded. This tunes the ranking weights. It is not model fine-tuning.

## Evaluation

I compared three setups on 30 queries with graded relevance labels:

- **Lexical:** the real `Engine.Search` path with semantics turned off
- **Semantic:** dense vector similarity only
- **Hybrid:** the production `Engine.Search`

Queries cover exact names, keyword phrases, conceptual and synonym searches, typos, substrings and acronyms, and noise.

| Pipeline | NDCG@10 | Mean Recall@10 | MRR@10 |
| :--- | :---: | :---: | :---: |
| Lexical | 0.8380 | 86.67% | 0.8278 |
| Semantic | 0.8202 | 93.33% | 0.8098 |
| **Hybrid** | **0.9071** | **96.67%** | **0.8861** |

Hybrid is best overall and gains the most on typos (0.9262 NDCG@10, against 0.6000 for lexical).

It has some tradeoffs. On conceptual queries, pure semantic scores 0.9926 and hybrid scores 0.8250, because keyword channels pull weaker matches up.

Latency is measured per call over 300 timed runs (30 queries x 10 runs), after warmup. On the 22-file fixture, hybrid has a p50 of 1.67 ms and a p95 of 3.44 ms. The full table, per-query results and failure analysis are in [EVALUATION.md](EVALUATION.md).

Run it yourself (needs `text/model.safetensors` from the model bundle):

```powershell
go test -v -run TestRetrievalEvaluation ./shared/...
# or
go run ./cmd/eval
```

Without the weights, CI runs `TestRetrievalEvaluation_MockRegression` instead. That test is labeled as a mock. It checks the scoring and fusion code, not model quality.

## Assistant, REST and MCP (optional)

The Assistant connects to a local or remote OpenAI-compatible provider and can call file search as a tool. There are also optional local REST and MCP endpoints. None of these are needed for indexing or search.

## Project layout

- `main.go`, `frontend/`: Wails desktop app and UI
- `shared/`: indexing, extraction, query parsing, ranking, local model inference
- `daemon/`, `cmd/`: background indexer and command-line tools
- `api.go`, `mcp.go`: optional local REST and MCP endpoints
- `scripts/`: tokenizer build and Windows release packaging

## Built with

[Wails](https://wails.io), [ONNX Runtime](https://onnxruntime.ai) with DirectML, [libvips](https://www.libvips.org), [Xberg](https://github.com/xberg-io/xberg) (kreuzberg-ffi) for extraction, [static-retrieval-mrl-en-v1](https://huggingface.co/sentence-transformers/static-retrieval-mrl-en-v1) truncated to 256 dimensions as the text encoder, and [SQLite](https://sqlite.org) FTS5.
