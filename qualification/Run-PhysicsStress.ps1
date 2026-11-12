[CmdletBinding()]
param(
    [ValidateSet('correctness','rehearsal','full')][string]$Mode = 'correctness',
    [string]$ValidationRoot = 'D:\Demiurgos\tests\physics-validation',
    [string]$ForkRoot = 'C:\Demiurgos\box3d-go',
    [string]$NavigationRoot = 'C:\Demiurgos\navmesh-go',
    [string]$RunID = ('stress-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff')),
    [ValidateSet(64,256,768,2304)][int]$Extent = 768,
    [string]$Duration = '2s',
    [string]$Warmup = '2s',
    [ValidateRange(1,64)][int]$PhysicsWorkers = 6,
    [ValidateRange(1,64)][int]$QueryWorkers = 6,
    [switch]$SIMD,
    [switch]$Profiles,
    [switch]$DebugCapture,
    [switch]$BuildOnly
)
$ErrorActionPreference = 'Stop'
if ($RunID -notmatch '^[A-Za-z0-9_.-]+$') { throw 'RunID must be one path component.' }
$engineRoot = Split-Path -Parent $PSScriptRoot
$validationPath = [IO.Path]::GetFullPath($ValidationRoot)
$runPath = Join-Path $validationPath "runs/$RunID"
$workspacePath = Join-Path $validationPath "workspaces/$RunID"
foreach ($path in @($runPath,$workspacePath)) {
    if (Test-Path -LiteralPath $path) { throw "Existing evidence must not be overwritten: $path" }
    New-Item -ItemType Directory -Path $path | Out-Null
}
foreach ($name in @('cache/gocache','cache/gomod','tmp')) { New-Item -ItemType Directory -Path (Join-Path $validationPath $name) -Force | Out-Null }
$env:GOCACHE = Join-Path $validationPath 'cache/gocache'
$env:GOMODCACHE = Join-Path $validationPath 'cache/gomod'
$env:GOTMPDIR = Join-Path $validationPath 'tmp'
$env:TEMP = $env:GOTMPDIR
$env:TMP = $env:GOTMPDIR
$env:GOTOOLCHAIN = 'go1.27.1'
$env:CGO_ENABLED = '0'
$env:GOEXPERIMENT = if ($SIMD) { 'simd' } else { '' }
$buildTags = if ($SIMD) { @('-tags','box3d_simd') } else { @() }
$tagNames = @()
if ($SIMD) { $tagNames = @('box3d_simd') }
$snapshots = @()
foreach ($source in @(@{Name='engine';Root=$engineRoot},@{Name='box3d-go';Root=$ForkRoot},@{Name='navmesh-go';Root=$NavigationRoot})) {
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
        $hashes += [ordered]@{path=$relative;sha256=(Get-FileHash -LiteralPath $to -Algorithm SHA256).Hash.ToLowerInvariant()}
    }
    $snapshots += [ordered]@{name=$source.Name;origin=$source.Root;commit=(& git -C $source.Root rev-parse HEAD);status=@(& git -C $source.Root status --porcelain);files=$hashes}
}
$engineSnapshot = Join-Path $workspacePath 'engine'
$forkSnapshot = Join-Path $workspacePath 'box3d-go'
$navSnapshot = Join-Path $workspacePath 'navmesh-go'
$env:GOWORK = Join-Path $workspacePath 'go.work'
$modulePaths = @(
    @{Module='github.com/demiurgos-hub/golem-engine';Version='v0.4.0';Path=$engineSnapshot},
    @{Module='github.com/demiurgos-hub/golem-engine/golem/collision';Version='v0.3.1-0.20261013153120-49b2e72c189a';Path=(Join-Path $engineSnapshot 'golem/collision')},
    @{Module='github.com/demiurgos-hub/golem-engine/golem/nav';Version='v0.4.0';Path=(Join-Path $engineSnapshot 'golem/nav')},
    @{Module='github.com/demiurgos-hub/golem-engine/golem/nav/navmesh';Version='v0.1.0';Path=(Join-Path $engineSnapshot 'golem/nav/navmesh')},
    @{Module='github.com/demiurgos-hub/golem-engine/golem/physics3d';Version='v0.0.0';Path=(Join-Path $engineSnapshot 'golem/physics3d')},
    @{Module='github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d';Version='v0.0.0';Path=(Join-Path $engineSnapshot 'golem/physics3d/box3d')},
    @{Module='github.com/gataengine/box3d-go';Version='v0.0.0-20261019212002-2a00e926a8d9';Path=$forkSnapshot},
    @{Module='github.com/demiurgos-hub/navmesh-go';Version='v0.1.0';Path=$navSnapshot}
)
$stressModule = Join-Path $engineSnapshot 'examples/physics-stress'
$paths = @($modulePaths | ForEach-Object { $_.Path }) + @($stressModule)
$workText = "go 1.27.1`nuse (`n" + (($paths | ForEach-Object { '  ' + (ConvertTo-Json -InputObject $_ -Compress) }) -join "`n") + "`n)`nreplace (`n"
$workText += (($modulePaths | ForEach-Object { '  ' + $_.Module + ' ' + $_.Version + ' => ' + (ConvertTo-Json -InputObject $_.Path -Compress) }) -join "`n") + "`n)`n"
[IO.File]::WriteAllText($env:GOWORK,$workText,[Text.UTF8Encoding]::new($false))
$sourceManifest = Join-Path $runPath 'source-manifest.json'
[ordered]@{version=1;source=$snapshots;workspaceSHA256=(Get-FileHash -LiteralPath $env:GOWORK -Algorithm SHA256).Hash.ToLowerInvariant();publishedResolution=$false} | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $sourceManifest -Encoding utf8
$executable = Join-Path $runPath 'physics-stress.exe'
$record = [ordered]@{runID=$RunID;startedUTC=[DateTime]::UtcNow.ToString('o');mode=$Mode;extent=$Extent;duration=$Duration;warmup=$Warmup;buildOnly=[bool]$BuildOnly;toolchain=(& go version);cgo=$env:CGO_ENABLED;goexperiment=$env:GOEXPERIMENT;buildTags=$tagNames;goamd64=(& go env GOAMD64);simdRequested=[bool]$SIMD;nativeHost=[Runtime.InteropServices.RuntimeInformation]::OSDescription;hostArchitecture=[Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString();processArchitecture=[Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture.ToString();sourceManifest=$sourceManifest;qualified=$false}
try {
    Push-Location $stressModule
    try {
        & go test @buildTags -json ./... 2>&1 | Out-File -LiteralPath (Join-Path $runPath 'tests.jsonl') -Encoding utf8
        if ($LASTEXITCODE -ne 0) { throw 'Harness checks failed.' }
        & go build @buildTags -trimpath -o $executable . 2>&1 | Out-File -LiteralPath (Join-Path $runPath 'build.log') -Encoding utf8
        if ($LASTEXITCODE -ne 0) { throw 'Harness build failed.' }
    } finally { Pop-Location }
    $record.executableSHA256 = (Get-FileHash -LiteralPath $executable -Algorithm SHA256).Hash.ToLowerInvariant()
    if (-not $BuildOnly) {
        $arguments = @('-mode',$Mode,'-extent',"$Extent",'-duration',$Duration,'-warmup',$Warmup,'-physics-workers',"$PhysicsWorkers",'-query-workers',"$QueryWorkers",'-source-manifest',$sourceManifest,'-out',(Join-Path $runPath 'workload'))
        if ($Profiles) { $arguments += '-profiles' }
        if ($DebugCapture) { $arguments += '-debug-capture' }
        $record.arguments = $arguments
        & $executable @arguments 2>&1 | Out-File -LiteralPath (Join-Path $runPath 'workload.log') -Encoding utf8
        $record.exitCode = $LASTEXITCODE
        if ($LASTEXITCODE -ne 0) { throw "Workload failed; see $runPath" }
    }
} finally {
    $record.finishedUTC = [DateTime]::UtcNow.ToString('o')
    $record | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath (Join-Path $runPath 'runner-manifest.json') -Encoding utf8
}
Write-Output "Recorded $Mode physics workload: $runPath. This run does not qualify missing release gates."
