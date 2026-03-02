# Filosophy – AI‑Powered Local File Search

**Filosophy** is a privacy‑first desktop application that lets you search through your personal files (images and text) using natural language queries. It runs entirely on your machine, leveraging modern AI models (MobileCLIP) and vector search (VectorLite) to deliver fast, accurate results without sending any data to the cloud.

![Screenshot](screenshot.png) <!-- TODO: Add actual screenshot -->

## ✨ Features

* **Multimodal search** – find images and text files with the same query.
* **100% local** – no data leaves your computer; all processing happens on‑device.
* **Real‑time indexing** – automatically watches your directories for changes.
* **Cross‑platform** – built with Wails (Go + Svelte) for Windows, macOS, and Linux support.
* **Hybrid ranking** – combines vector similarity, full‑text search, and file‑path matching.

## 🏗 Architecture

Filosophy is built as a three‑layer system:

1. **Frontend** – Svelte + Carbon Components Svelte, providing a clean, responsive UI.
2. **Orchestrator** – Go (Wails) that manages file‑system watching, inter‑process communication, and the main application window.
3. **Inference sidecar** – Python process that handles embedding generation (using Sentence‑Transformers and MobileCLIP) and vector‑search operations (via SQLite + VectorLite).

## 📦 Prerequisites

* **Go** 1.25 or later
* **Node.js** 18+ (npm or pnpm)
* **Python** 3.10+ (with pip)
* **Git** (for cloning the repository)

## 🚀 Getting Started

### 1. Clone the repository

```bash
git clone https://github.com/yourusername/filosophy.git
cd filosophy
```

### 2. Install Python dependencies

```bash
pip install -r requirements.txt
```

> **Note**: If you need GPU acceleration for PyTorch, visit [pytorch.org](https://pytorch.org/get-started/locally/) to install a CUDA‑enabled version.

### 3. Install Node dependencies

```bash
cd frontend
npm install  # or pnpm install
cd ..
```

### 4. Run the application in development mode

```bash
wails dev
```

The application window will open, and you can start searching files in your `~/Downloads/test` directory (or the path configured in `main.go`).

## 🔧 Building for Production

To create a standalone executable:

```bash
wails build
```

The output will be placed in `build/bin`. You can distribute this binary together with the `vips-dev-8.18` folder (for image thumbnailing) and the `sidecar.py` script.

## 📄 License

Filosophy is licensed under the GNU General Public License v3.0 (GPLv3). See the [LICENSE](LICENSE) file for the full text.

All source files must include the GPLv3 header comment. A sample header is provided in [LICENSE_HEADER](LICENSE_HEADER).

## 🤝 Contributing

Contributions are welcome! Please open an issue or submit a pull request on GitHub.

## ⚠️ Important Notes for Publication

Before publishing this repository, ensure the following items are **not** committed:

* `filosophy.db` (the local vector database)
* Any `*.bin` vector index files
* `native/` and `kreuzberg-ffi/` (if they contain proprietary or binary dependencies)
* Personal configuration files (`.env`, `config.json`)
* Python virtual environments (`venv/`, `env/`)

Refer to the [.gitignore](.gitignore) for a complete list of excluded patterns.

---

Built with ❤️ using [Wails](https://wails.io), [Svelte](https://svelte.dev), [VectorLite](https://github.com/superfly/vectorlite), and [MobileCLIP](https://github.com/apple/ml-mobileclip).
