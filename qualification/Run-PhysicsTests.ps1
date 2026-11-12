[CmdletBinding()]
param(
    [string]$ValidationRoot = 'D:\Demiurgos\tests\physics-validation',
    [string]$ForkRoot = 'C:\Demiurgos\box3d-go',
    [string]$RunID = ('unit-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff')),
    [string]$GoToolchain = 'go1.27.1'
)
$ErrorActionPreference = 'Stop'
if ($RunID -notmatch '^[A-Za-z0-9_.-]+$') { throw 'RunID must be one path component.' }
$engineRoot = Split-Path -Parent $PSScriptRoot
$validationPath = [IO.Path]::GetFullPath($ValidationRoot)
$runPath = Join-Path $validationPath "runs/$RunID"
if (Test-Path -LiteralPath $runPath) { throw "Completed or existing run must not be overwritten: $runPath" }
$workspacePath = Join-Path $validationPath "workspaces/$RunID"
if (Test-Path -LiteralPath $workspacePath) { throw "Existing source snapshot must not be overwritten: $workspacePath" }
foreach ($path in @($runPath,$workspacePath,(Join-Path $validationPath 'cache/gocache'),(Join-Path $validationPath 'cache/gomod'),(Join-Path $validationPath 'tmp'),(Join-Path $validationPath 'bin'))) {
    New-Item -ItemType Directory -Path $path -Force | Out-Null
}
$env:GOCACHE = Join-Path $validationPath 'cache/gocache'
$env:GOMODCACHE = Join-Path $validationPath 'cache/gomod'
$env:GOTMPDIR = Join-Path $validationPath 'tmp'
$env:TEMP = $env:GOTMPDIR
$env:TMP = $env:GOTMPDIR
$env:GOTOOLCHAIN = $GoToolchain
$env:CGO_ENABLED = '0'
$snapshots = @()
foreach ($source in @(@{ Name='engine'; Root=$engineRoot },@{ Name='box3d-go'; Root=$ForkRoot })) {
    $destination = Join-Path $workspacePath $source.Name
    $files = @(& git -C $source.Root -c core.quotepath=false ls-files --cached --others --exclude-standard)
    if ($LASTEXITCODE -ne 0) { throw "Cannot enumerate source $($source.Root)" }
    $hashes = @()
    foreach ($relative in $files) {
        $from = Join-Path $source.Root $relative
        if (-not (Test-Path -LiteralPath $from -PathType Leaf)) { continue }
        $to = Join-Path $destination $relative
        New-Item -ItemType Directory -Path (Split-Path -Parent $to) -Force | Out-Null
        Copy-Item -LiteralPath $from -Destination $to
        $hashes += [ordered]@{ path=$relative; sha256=(Get-FileHash -LiteralPath $to -Algorithm SHA256).Hash.ToLowerInvariant() }
    }
    $snapshots += [ordered]@{ name=$source.Name; origin=$source.Root; commit=(& git -C $source.Root rev-parse HEAD); files=$hashes }
}
$engineSnapshot = Join-Path $workspacePath 'engine'
$forkSnapshot = Join-Path $workspacePath 'box3d-go'
$env:GOWORK = Join-Path $workspacePath 'go.work'
$usePaths = @((Join-Path $engineSnapshot 'golem/physics3d'),(Join-Path $engineSnapshot 'golem/physics3d/box3d'),$forkSnapshot)
$workText = "go 1.27.1`nuse (`n" + (($usePaths | ForEach-Object { '  ' + (ConvertTo-Json -InputObject $_ -Compress) }) -join "`n") + "`n)`n"
[IO.File]::WriteAllText($env:GOWORK,$workText,[Text.UTF8Encoding]::new($false))
$evidence = [ordered]@{ runID=$RunID; startedUTC=[DateTime]::UtcNow.ToString('o'); kind='local-source-unit'; publishedResolution=$false; toolchain=(& go version); cgo=$env:CGO_ENABLED; platform=[Environment]::OSVersion.ToString(); architecture=[Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString(); source=$snapshots; checks=@(); qualified=$false }
try {
    foreach ($module in @('golem/physics3d','golem/physics3d/box3d')) {
        $log = Join-Path $runPath ($module.Replace('/','-')+'.jsonl')
        Push-Location (Join-Path $engineSnapshot $module)
        try {
            & go test -json ./... 2>&1 | Out-File -LiteralPath $log -Encoding utf8
            $exitCode = $LASTEXITCODE
        } finally { Pop-Location }
        $evidence.checks += [ordered]@{ module=$module; command='go test -json ./...'; exitCode=$exitCode; log=$log }
        if ($exitCode -ne 0) { throw "Tests failed in $module; see $log" }
    }
    $evidence.unitChecksPassed = $true
} finally {
    $evidence.finishedUTC = [DateTime]::UtcNow.ToString('o')
    $evidence | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath (Join-Path $runPath 'manifest.json') -Encoding utf8
}
Write-Output "Local-source unit checks passed. Evidence: $runPath. Workload and release qualification remain separate."
