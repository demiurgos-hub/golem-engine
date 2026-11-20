# Builds the Golem Inspector UI and embeds it into the hub binary.
# Usage: scripts/build-golem-inspector.ps1 [-Output bin/golem-inspector.exe]
param(
    [string]$Output = (Join-Path $PSScriptRoot '..\bin\golem-inspector.exe')
)
$ErrorActionPreference = 'Stop'
$root = Resolve-Path (Join-Path $PSScriptRoot '..')

Push-Location (Join-Path $root 'golem-inspector')
try {
    npm ci --no-audit --no-fund
    if ($LASTEXITCODE -ne 0) { throw "npm ci failed" }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "UI build failed" }
} finally {
    Pop-Location
}

Push-Location $root
try {
    $env:GOWORK = 'off'
    $env:CGO_ENABLED = '0'
    go build -trimpath -o $Output ./cmd/golem-inspector
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
} finally {
    Remove-Item Env:GOWORK, Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}
Write-Host "built $Output"
