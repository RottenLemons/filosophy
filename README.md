# Filosophy - AI-Powered Local File Search

**Filosophy** is a privacy-first desktop application that lets you search through personal files, images, and WhatsApp history using natural-language queries. It runs locally and combines semantic embeddings, SQLite-backed vector search, full-text search, and path matching without sending indexed content to the cloud.

![Screenshot](screenshot.png) <!-- TODO: Add actual screenshot -->

## Features

* **Multimodal search** - find images and documents with the same query.
* **Local-first indexing** - content extraction, embedding, ranking, and storage run on-device.
* **Live file watching** - indexed folders are scanned and updated as files change.
* **Hybrid ranking** - combines vector similarity, FTS, filename/path boosts, recency, and optional reranking.
* **External integrations** - optional localhost REST API, MCP server, CLI client, and WhatsApp indexing.

## Architecture

Filosophy is organized around a Go backend with a Svelte frontend:

1. **Desktop app** - Wails hosts the Svelte UI in `frontend/` and binds Go methods from `main.go`.
2. **Indexing pipeline** - `shared/processor.go` extracts file metadata/content, batches image thumbnailing with libvips, and queues writes.
3. **Search engine** - `shared/engine.go` owns ONNX inference, SQLite metadata, vector storage, FTS tables, ranking weights, and feedback.
4. **API/MCP surface** - `api.go`, `mcp.go`, and `cmd/filo` expose local automation endpoints.
5. **WhatsApp integration** - `whatsapp/` manages pairing, message storage, and indexing.

## Repository Layout

* `main.go`, `api.go`, `mcp.go`, `tray_windows.go` - Wails app, local API/MCP server, and tray integration.
* `shared/` - indexing, embeddings, vector search, config, ranking, and platform helpers.
* `whatsapp/` - WhatsApp client/session logic and searchable message store.
* `frontend/` - active SvelteKit UI.
* `cmd/filo/` - small CLI client for the localhost API.
* `cmd/daemon/`, `daemon/` - background daemon entrypoints and shared daemon logic.

## Prerequisites

* **Go** 1.25 or later
* **Node.js** 18+ (npm or pnpm)
* **Git** (for cloning the repository)
* Model/runtime assets expected by the app, including ONNX/tokenizer files and the libvips bundle on Windows.

## Getting Started

### 1. Clone the repository

```bash
git clone https://github.com/yourusername/filosophy.git
cd filosophy
```

### 2. Install frontend dependencies

```bash
cd frontend
npm install  # or pnpm install
cd ..
```

### 3. Run the application in development mode

```bash
wails dev
```

The application window opens, loads the configured folders, and begins indexing according to `filosophy_config.json`.

## Building for Production

To create a standalone executable:

```bash
wails build
```

The output is placed in `build/bin`. Distribute the binary together with the runtime/model assets the app expects, including `vips-dev-8.18` on Windows.

## Maintenance Notes

* Keep generated data out of version control: `*.db`, `*.db-wal`, logs, model binaries, local sessions, `frontend/.svelte-kit/`, `frontend/dist/`, and `build/bin/`.
* Prefer adding Go doc comments for exported functions and types. Comments should explain contracts, side effects, and concurrency expectations rather than restating the function name.
* Run `gofmt` after Go edits and use existing local package patterns before adding new abstractions.
* Large model/runtime artifacts live beside the app during local development but should be treated as release assets, not source.

## License

Filosophy is licensed under the GNU General Public License v3.0 (GPLv3). See the [LICENSE](LICENSE) file for the full text.

All source files must include the GPLv3 header comment. A sample header is provided in [LICENSE_HEADER](LICENSE_HEADER).

## Contributing

Contributions are welcome! Please open an issue or submit a pull request on GitHub.

## Important Notes for Publication

Before publishing this repository, ensure the following items are **not** committed:

* `filosophy.db` (the local vector database)
* Any `*.bin` vector index files
* `native/` and `kreuzberg-ffi/` (if they contain proprietary or binary dependencies)
* Personal configuration files (`.env`, `filosophy_config.json`)
* Local WhatsApp session databases

Refer to the [.gitignore](.gitignore) for a complete list of excluded patterns.

---

Built with Wails, Svelte, SQLite, ONNX Runtime, and local embedding models.
