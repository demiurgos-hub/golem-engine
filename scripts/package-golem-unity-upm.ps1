$ErrorActionPreference = "Stop"

$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "..")
$sourceRoot = Join-Path $repoRoot "golem-unity"
$artifactRoot = Join-Path $repoRoot "artifacts/upm/io.demiurgos.golemengine"

if (-not (Test-Path $sourceRoot)) {
    throw "Golem Unity source folder not found: $sourceRoot"
}

if (Test-Path $artifactRoot) {
    Remove-Item $artifactRoot -Recurse -Force
}

New-Item -ItemType Directory -Path $artifactRoot -Force | Out-Null

function Copy-PackageItem {
    param(
        [Parameter(Mandatory = $true)][string]$RelativePath
    )

    $source = Join-Path $sourceRoot $RelativePath
    if (-not (Test-Path $source)) {
        return
    }

    $destination = Join-Path $artifactRoot $RelativePath
    $destinationParent = Split-Path $destination -Parent
    New-Item -ItemType Directory -Path $destinationParent -Force | Out-Null
    Copy-Item $source $destination -Recurse -Force
}

$items = @(
    "package.json",
    "package.json.meta",
    "Runtime",
    "Runtime.meta",
    "Editor",
    "Editor.meta",
    "Tests",
    "Tests.meta",
    "README.md",
    "README.md.meta",
    "CHANGELOG.md",
    "CHANGELOG.md.meta",
    "LICENSE.md",
    "LICENSE.md.meta",
    "LICENSE",
    "LICENSE.meta"
)

foreach ($item in $items) {
    Copy-PackageItem $item
}

$excludedPatterns = @(
    "bin",
    "obj",
    "*.csproj",
    "*.sln",
    "*.dll",
    "*.pdb",
    "*.tmp",
    "*.temp"
)

foreach ($pattern in $excludedPatterns) {
    Get-ChildItem $artifactRoot -Recurse -Force -Filter $pattern -ErrorAction SilentlyContinue | Remove-Item -Recurse -Force
}

Write-Host "Built Golem Unity UPM package at $artifactRoot"
