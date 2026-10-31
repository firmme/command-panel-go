param([string]$OutputName = "command-panel.exe")

$ErrorActionPreference = "Stop"
if ([System.IO.Path]::GetFileName($OutputName) -ne $OutputName) {
    throw "OutputName must be a filename."
}
$projectRoot = Split-Path -Parent $PSScriptRoot
$resourcePath = Join-Path $projectRoot "command-panel_windows_resources.syso"
Push-Location -LiteralPath $projectRoot
try {
    # Uses Wails' native generator without the CLI's older source parser.
    go run -tags bindings .
    if ($LASTEXITCODE -ne 0) { throw "Binding generation failed." }
    # Wails emits whitespace-only lines in models.ts; keep generated diffs clean.
    $modelsPath = Join-Path $projectRoot "frontend/wailsjs/go/models.ts"
    $modelLines = [System.IO.File]::ReadAllLines($modelsPath) | ForEach-Object { $_.TrimEnd() }
    [System.IO.File]::WriteAllText($modelsPath, ($modelLines -join "`n") + "`n", [System.Text.UTF8Encoding]::new($false))

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
    go build -tags "desktop,production" -trimpath -ldflags "-H windowsgui -s -w" -o $outputPath .
    if ($LASTEXITCODE -ne 0) { throw "Desktop build failed." }
    Write-Output "Built: $(Join-Path $projectRoot $outputPath)"
} finally {
    if (Test-Path -LiteralPath $resourcePath) { Remove-Item -LiteralPath $resourcePath -Force }
    Pop-Location
}
