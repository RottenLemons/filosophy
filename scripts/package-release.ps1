param(
    [Parameter(Mandatory = $true)]
    [string]$Version
)

$ErrorActionPreference = "Stop"

if ($Version -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$') {
    throw "Version must contain only letters, numbers, dots, underscores, and hyphens."
}

$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$exe = Join-Path $root "build/bin/filosophy.exe"
$runtime = Join-Path $root "dist/filosophy-$Version-windows-amd64"
$zip = Join-Path $root "dist/filosophy-$Version-windows-amd64.zip"

& wails build
if ($LASTEXITCODE -ne 0) {
    throw "Wails build failed."
}

$required = @(
    $exe,
    (Join-Path $root "onnxruntime.dll"),
    (Join-Path $root "text/tokenizer.json"),
    (Join-Path $root "text/model.onnx"),
    (Join-Path $root "image/tokenizer.json"),
    (Join-Path $root "image/text_model.onnx"),
    (Join-Path $root "image/vision_model.onnx"),
    (Join-Path $root "vips-dev-8.18/bin/vipsthumbnail.exe")
)
foreach ($path in $required) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required release asset is missing: $path"
    }
}

New-Item -ItemType Directory -Path (Join-Path $root "dist") -Force | Out-Null
New-Item -ItemType Directory -Path $runtime -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $runtime "text") -Force | Out-Null
New-Item -ItemType Directory -Path (Join-Path $runtime "image") -Force | Out-Null
Copy-Item -LiteralPath $exe -Destination $runtime -Force
Copy-Item -LiteralPath (Join-Path $root "onnxruntime.dll") -Destination $runtime -Force
foreach ($file in @("tokenizer.json", "model.onnx")) {
    Copy-Item -LiteralPath (Join-Path $root "text/$file") -Destination (Join-Path $runtime "text") -Force
}
foreach ($file in @("tokenizer.json", "text_model.onnx", "vision_model.onnx")) {
    Copy-Item -LiteralPath (Join-Path $root "image/$file") -Destination (Join-Path $runtime "image") -Force
}
Copy-Item -LiteralPath (Join-Path $root "vips-dev-8.18") -Destination $runtime -Recurse -Force
Compress-Archive -Path (Join-Path $runtime "*") -DestinationPath $zip -Force

Write-Output "Created $zip"
