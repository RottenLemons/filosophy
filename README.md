# Filosophy

Filosophy is a Windows desktop application for local file search. It combines filename and full-text search with semantic text and image search. Indexes and model inference run on the local machine; optional API and model-provider integrations are disabled until configured.

## Project layout

- `main.go` and `frontend/` contain the Wails desktop application.
- `shared/` contains file processing, indexing, query parsing, and search.
- `daemon/` and `cmd/` contain the background indexer and command-line entry points.
- `api.go` and `mcp.go` provide optional local HTTP and MCP endpoints.

## Build requirements

The supported build target is Windows x64. Install:

- Go 1.25.5 or later
- Node.js 20 or later with npm
- Wails CLI v2
- Rust and Cargo
- A C/C++ GNU toolchain that provides `x86_64-w64-mingw32-gcc` and `ar`

The application also needs runtime assets that are not committed to this repository:

- `text/tokenizer.json` and `text/model.onnx`
- `image/tokenizer.json`, `image/text_model.onnx`, and `image/vision_model.onnx`
- `onnxruntime.dll` (and any provider DLLs required by the selected runtime)
- `vips-dev-8.18/bin/vipsthumbnail.exe` and the matching libvips runtime files

Place model and runtime folders beside the application executable for release builds. At development time, the app also checks paths relative to the working directory. The ONNX Runtime and model versions must be compatible; GPU acceleration is optional.

## Development build

From the repository root, prepare the native tokenizer library and frontend dependencies:

```powershell
go mod download
./scripts/build-tokenizers.ps1
Push-Location frontend
npm ci
Pop-Location
wails dev
```

Create a production Windows build with:

```powershell
Push-Location frontend
npm ci
Pop-Location
wails build
```

The executable is written to `build/bin/filosophy.exe`. A distributable release must include the model and native runtime assets listed above; the Wails build does not download or bundle those assets automatically.

Create a versioned Windows zip from the local runtime assets with:

```powershell
./scripts/package-release.ps1 -Version 1.0.0
```

The script rebuilds the app, validates the required assets, and writes the zip under `dist/`. Review the archive contents and test it on a clean Windows machine before publishing.

## Verification

Run the Go tests and compile all Go packages with:

```powershell
go test ./...
go build ./...
```

Build the frontend independently with:

```powershell
Push-Location frontend
npm ci
npm run build
Pop-Location
```

## Local data and privacy

The app stores its index, configuration, API keys, and logs locally. These files are excluded from version control. Review release archives before publishing and do not include personal indexes, credentials, model-provider keys, or machine-specific data.

## License

Filosophy is licensed under GPL-3.0. See [LICENSE](LICENSE).
