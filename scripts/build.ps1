param([string]$OutputName = "command-panel.exe", [string]$Version = "1.0.0")

$ErrorActionPreference = "Stop"
if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "Version must be a stable version like 1.2.3." }
if ([System.IO.Path]::GetFileName($OutputName) -ne $OutputName) {
    throw "OutputName must be a filename."
}
$projectRoot = Split-Path -Parent $PSScriptRoot
$resourcePath = Join-Path $projectRoot "command-panel_windows_resources.syso"
Push-Location -LiteralPath $projectRoot
try {
    go run github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0 generate bindings -names
    if ($LASTEXITCODE -ne 0) { throw "Binding generation failed." }

    if (-not (Test-Path -LiteralPath "frontend/node_modules")) {
        npm.cmd --prefix frontend ci
        if ($LASTEXITCODE -ne 0) { throw "Frontend dependency installation failed." }
    }
    npm.cmd --prefix frontend run build
    if ($LASTEXITCODE -ne 0) { throw "Frontend build failed." }

    go run ./scripts/windows-resources/main.go $resourcePath
    if ($LASTEXITCODE -ne 0) { throw "Windows resource generation failed." }

    New-Item -ItemType Directory -Path "build/bin" -Force | Out-Null
    $outputPath = Join-Path "build/bin" $OutputName
    go build -tags "desktop,production" -trimpath -ldflags "-H windowsgui -s -w -X main.appVersion=$Version" -o $outputPath .
    if ($LASTEXITCODE -ne 0) { throw "Desktop build failed." }
    Write-Output "Built: $(Join-Path $projectRoot $outputPath)"
} finally {
    if (Test-Path -LiteralPath $resourcePath) { Remove-Item -LiteralPath $resourcePath -Force }
    Pop-Location
}
