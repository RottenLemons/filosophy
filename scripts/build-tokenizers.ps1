$ErrorActionPreference = "Stop"

$moduleCache = (go env GOMODCACHE).Trim()
$moduleDir = Join-Path $moduleCache "github.com/daulet/tokenizers@v1.25.0"
$manifest = Join-Path $moduleDir "crates/tokenizers/Cargo.toml"
$buildDir = Join-Path $env:TEMP "filosophy-tokenizers-build"
$archive = Join-Path $buildDir "x86_64-pc-windows-gnu/release/libtokenizers_ffi.a"
$destination = Join-Path (Resolve-Path (Join-Path $PSScriptRoot "..")).Path "libtokenizers.a"

if (-not (Test-Path $manifest)) {
    throw "Go module source not found at $moduleDir. Run 'go mod download github.com/daulet/tokenizers' first."
}

cargo build --release --manifest-path $manifest -p tokenizers-ffi --target x86_64-pc-windows-gnu --target-dir $buildDir
if ($LASTEXITCODE -ne 0) {
    throw "Failed to build the tokenizers static library."
}
if (-not (Test-Path $archive)) {
    throw "Expected tokenizer archive not found at $archive."
}

Copy-Item -LiteralPath $archive -Destination $destination -Force
Write-Output "Created $destination"
