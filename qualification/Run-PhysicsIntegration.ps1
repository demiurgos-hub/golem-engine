#requires -Version 7.0
# Reuses a committed Run-PhysicsStress -BuildOnly capture and saved Unity data.
# This executes only the selected backend integration gate, not a workload.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$SourceRun,
    [Parameter(Mandatory)][string]$RunID,
    [string]$ValidationRoot = 'D:\Demiurgos\tests\physics-validation',
    [string]$UnityDynamics = 'D:\Demiurgos\tests\physics-validation\unity\runs\dynamics-20261112-03\reference'
)
$ErrorActionPreference = 'Stop'
foreach ($id in @($SourceRun,$RunID)) {
    if ($id -notmatch '^[A-Za-z0-9][A-Za-z0-9_.-]+$') { throw 'Run IDs must be single safe components.' }
}
$root = [IO.Path]::GetFullPath($ValidationRoot).TrimEnd('\','/')
if ($root -ne 'D:\Demiurgos\tests\physics-validation') { throw 'Use the physics validation workspace.' }
$sourcePath = Join-Path $root "runs/$SourceRun/source-manifest.json"
$runnerPath = Join-Path $root "runs/$SourceRun/runner-manifest.json"
$source = Get-Content -LiteralPath $sourcePath -Raw | ConvertFrom-Json
$runner = Get-Content -LiteralPath $runnerPath -Raw | ConvertFrom-Json
if (-not $runner.buildOnly -or @($source.source | Where-Object { -not $_.committedSources }).Count) { throw 'Requires a committed-source BuildOnly capture.' }
if (@($source.source).Count -ne 3 -or (@($source.source.name | Sort-Object) -join ',') -ne 'box3d-go,engine,navmesh-go') { throw 'Requires exactly one engine, Box3D and navigation snapshot.' }
if ($runner.cgo -ne '0' -or $runner.goamd64 -ne 'v1') { throw 'Requires the frozen CGO-disabled GOAMD64=v1 build configuration.' }
$workspace = Join-Path $root "workspaces/$SourceRun"
$run = Join-Path $root "runs/$RunID"
if (Test-Path -LiteralPath $run) { throw 'Existing evidence must not be overwritten.' }
New-Item -ItemType Directory -Path $run,(Join-Path $run 'dynamics') | Out-Null
foreach ($entry in @{'GOCACHE'='cache/gocache';'GOMODCACHE'='cache/gomod';'GOPATH'='cache/gopath';'GOTMPDIR'='tmp';'NPM_CONFIG_CACHE'='cache/npm';'DOTNET_CLI_HOME'='cache/dotnet-cli'}.GetEnumerator()) {
    $path = Join-Path $root $entry.Value
    New-Item -ItemType Directory -Path $path -Force | Out-Null
    Set-Item -LiteralPath "Env:$($entry.Key)" -Value $path
}
$env:TEMP=$env:GOTMPDIR; $env:TMP=$env:GOTMPDIR
$env:GOTOOLCHAIN='go1.27.1'; $env:GOENV='off'; $env:CGO_ENABLED='0'; $env:GOAMD64='v1'; $env:GOFLAGS='-mod=readonly'; $env:GOOS='windows'; $env:GOARCH='amd64'
$experiment=[string]$runner.goexperiment
if ($experiment -notin @('','simd')) { throw 'Unsupported candidate GOEXPERIMENT.' }
$env:GOEXPERIMENT=$experiment
if ([Runtime.InteropServices.RuntimeInformation]::OSArchitecture -ne 'X64' -or [Runtime.InteropServices.RuntimeInformation]::ProcessArchitecture -ne 'X64') { throw 'Native Windows x64 process required.' }
if (-not $runner.PSObject.Properties['buildTags']) { throw 'The source runner must record its exact buildTags.' }
$tagNames = @($runner.buildTags)
if (@($tagNames | Where-Object { $_ -notin @('box3d_simd','box3d_noassert') }).Count -or @($tagNames | Select-Object -Unique).Count -ne $tagNames.Count) { throw 'Unsupported or repeated candidate build tag.' }
if (($tagNames -contains 'box3d_simd') -ne ($experiment -eq 'simd') -or ($tagNames -contains 'box3d_noassert') -ne [bool]$runner.nativeReleaseRequested) { throw 'Candidate build tags disagree with recorded experiment/release settings.' }
$tags = if ($tagNames.Count) { @('-tags',($tagNames -join ',')) } else { @() }
Remove-Item Env:GOLEM_PHYSICS_CATALOG_DIR,Env:GOLEM_PHYSICS_UNITY_REFERENCE_DIR -ErrorAction SilentlyContinue
$engine = Join-Path $workspace 'engine'
$modules = @(
    @{Name='github.com/demiurgos-hub/golem-engine';Version='v0.4.0';Path=$engine},
    @{Name='github.com/demiurgos-hub/golem-engine/golem/collision';Version='v0.3.1-0.20261013153120-49b2e72c189a';Path=(Join-Path $engine 'golem/collision')},
    @{Name='github.com/demiurgos-hub/golem-engine/golem/nav';Version='v0.4.0';Path=(Join-Path $engine 'golem/nav')},
    @{Name='github.com/demiurgos-hub/golem-engine/golem/nav/navmesh';Version='v0.1.0';Path=(Join-Path $engine 'golem/nav/navmesh')},
    @{Name='github.com/demiurgos-hub/golem-engine/golem/physics3d';Version='v0.0.0';Path=(Join-Path $engine 'golem/physics3d')},
    @{Name='github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d';Version='v0.0.0';Path=(Join-Path $engine 'golem/physics3d/box3d')},
    @{Name='github.com/gataengine/box3d-go';Version='v0.0.0-20261019212002-2a00e926a8d9';Path=(Join-Path $workspace 'box3d-go')},
    @{Name='github.com/demiurgos-hub/navmesh-go';Version='v0.1.0';Path=(Join-Path $workspace 'navmesh-go')}
)
$testModules = @('golem/collision','golem/collision/cp','golem/collision/resolv','golem/nav','golem/nav/kelindar','golem/nav/pathing','golem/nav/navmesh','golem/physics3d','golem/physics3d/box3d','examples/navigation-server','examples/navigation-stress')
$allPaths = @($modules | ForEach-Object { $_.Path }) + @($testModules | ForEach-Object { Join-Path $engine $_ }) + @(Join-Path $engine 'examples/physics-stress')
$work = "go 1.27.1`nuse (`n" + ((@($allPaths | Select-Object -Unique) | ForEach-Object { '  ' + (ConvertTo-Json -InputObject $_ -Compress) }) -join "`n") + "`n)`nreplace (`n"
$work += (($modules | ForEach-Object { '  ' + $_.Name + ' ' + $_.Version + ' => ' + (ConvertTo-Json -InputObject $_.Path -Compress) }) -join "`n") + "`n)`n"
$env:GOWORK = Join-Path $run 'go.work'
Set-Content -LiteralPath $env:GOWORK -Value $work -Encoding utf8NoBOM
$env:GOLEM_PHYSICS_UNITY_DYNAMICS_DIR=[IO.Path]::GetFullPath($UnityDynamics)
$env:GOLEM_PHYSICS_UNITY_DYNAMICS_OUTPUT=Join-Path $run 'dynamics'
$env:GOLEM_PHYSICS_SOURCE_MANIFEST=$sourcePath
$evidence = [ordered]@{version=1;gate='selected-delivery-engine-integration';startedUTC=[DateTime]::UtcNow.ToString('o');sourceRun=$SourceRun;sourceManifest=$sourcePath;sourceManifestSHA256=(Get-FileHash -LiteralPath $sourcePath -Algorithm SHA256).Hash;scriptSHA256=(Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash;workspaceSHA256=(Get-FileHash -LiteralPath $env:GOWORK -Algorithm SHA256).Hash;environment=@{GOTOOLCHAIN=$env:GOTOOLCHAIN;CGO_ENABLED=$env:CGO_ENABLED;GOEXPERIMENT=$env:GOEXPERIMENT;GOAMD64=$env:GOAMD64;GOWORK=$env:GOWORK;GOFLAGS=$env:GOFLAGS;GOCACHE=$env:GOCACHE;GOMODCACHE=$env:GOMODCACHE;GOTMPDIR=$env:GOTMPDIR;NPM_CONFIG_CACHE=$env:NPM_CONFIG_CACHE;DOTNET_CLI_HOME=$env:DOTNET_CLI_HOME};checks=@();qualified=$false;publishedResolution=$false;scope='One Windows backend integration run; no native matrix, front-end package, new Unity capture or workload.'}
$evidence.sourceCommits = @($source.source | ForEach-Object { [ordered]@{name=$_.name;commit=$_.commit} })
$evidence.buildTags = $tagNames
$evidence.environment.GOPATH = $env:GOPATH
$evidence.sourceRunnerSHA256 = (Get-FileHash -LiteralPath $runnerPath -Algorithm SHA256).Hash
$evidence.excluded = @('extended native/race matrices','two-hour soak','ARM64','Unity2021','Ebiten/graphics','duplicate full-size Linux performance')
function Save-Evidence { $evidence | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath (Join-Path $run 'manifest.json') -Encoding utf8NoBOM }
function Verify-Sources {
    $count=0
    foreach ($item in $source.source) {
        if ($item.name -notin @('engine','box3d-go','navmesh-go')) { throw 'Unknown source snapshot.' }
        $base=[IO.Path]::GetFullPath((Join-Path $workspace $item.name)).TrimEnd('\')
        $expected=@{}
        foreach ($file in $item.files) {
            $path=[IO.Path]::GetFullPath((Join-Path $base $file.path))
            if (-not $path.StartsWith($base+'\',[StringComparison]::OrdinalIgnoreCase)) { throw 'Source path escaped snapshot.' }
            if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash -ine $file.sha256) { throw "Source changed: $path" }
            $expected[$path]=$true; $count++
        }
        foreach ($file in Get-ChildItem -LiteralPath $base -File -Recurse -Filter '*.go') { if (-not $expected.ContainsKey($file.FullName)) { throw "Unrecorded Go source: $($file.FullName)" } }
    }
    return $count
}
function Read-TestLog([string]$path) {
    $events=@(Get-Content -LiteralPath $path | Where-Object { $_.StartsWith('{') } | ForEach-Object { $_ | ConvertFrom-Json })
    $passes=@($events | Where-Object { $_.Action -eq 'pass' -and $_.Test } | ForEach-Object { [ordered]@{package=$_.Package;test=$_.Test} })
    $skips=@($events | Where-Object { $_.Action -eq 'skip' -and $_.Test } | ForEach-Object { [ordered]@{package=$_.Package;test=$_.Test} })
    return @{Passed=$passes;Skipped=$skips;Failures=@($events | Where-Object {$_.Action -eq 'fail'})}
}
function Run-Tests([string]$name,[string]$directory,[string[]]$packages,[string]$requiredTest='') {
    $log=Join-Path $run ($name+'.jsonl')
    $arguments=@('test')+$tags+@('-count=1','-json')+$packages
    $check=[ordered]@{name=$name;cwd=$directory;command='go';arguments=$arguments;startedUTC=[DateTime]::UtcNow.ToString('o');log=$log}
    $evidence.checks+=,$check
    Push-Location $directory
    try { & go @arguments 2>&1 | Out-File -LiteralPath $log -Encoding utf8; $check.exitCode=$LASTEXITCODE } finally { Pop-Location }
    $parsed=Read-TestLog $log
    $check.logSHA256=(Get-FileHash -LiteralPath $log -Algorithm SHA256).Hash
    $check.passedTests=$parsed.Passed.Count; $check.skippedTests=$parsed.Skipped; $check.finishedUTC=[DateTime]::UtcNow.ToString('o')
    Save-Evidence
    if ($check.exitCode -ne 0 -or $parsed.Failures.Count -or -not $parsed.Passed.Count) { throw "$name failed or executed no tests; see $log" }
    if ($requiredTest -and -not @($parsed.Passed | Where-Object {$_.test -eq $requiredTest}).Count) { throw "Required test did not pass: $requiredTest" }
}
try {
    $evidence.sourceFilesBefore=Verify-Sources
    $evidence.toolchain=(& go version)
    if ($LASTEXITCODE -ne 0 -or $evidence.toolchain -notmatch 'go1\.27\.1 windows/amd64$') { throw 'Expected native Windows x64 Go 1.27.1.' }
    $evidence.unityReference=@('dynamics.source.json','dynamics.reference.json') | ForEach-Object { $path=Join-Path $env:GOLEM_PHYSICS_UNITY_DYNAMICS_DIR $_; [ordered]@{path=$path;sha256=(Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash} }
    $harnessLog=Join-Path $root "runs/$SourceRun/tests.jsonl"
    $harness=Read-TestLog $harnessLog
    if ($harness.Failures.Count -or -not $harness.Passed.Count) { throw 'Frozen BuildOnly harness tests did not pass real tests.' }
    $evidence.reusedHarness=[ordered]@{log=$harnessLog;sha256=(Get-FileHash -LiteralPath $harnessLog -Algorithm SHA256).Hash;passedTests=$harness.Passed.Count;skippedTests=$harness.Skipped}
    Run-Tests 'root-backend' $engine @('./cmd/...','./schema/...','./codegen/...','./golem/...','./internal/...')
    foreach ($module in $testModules) {
        $required = if ($module -eq 'golem/physics3d/box3d') {'TestUnityDynamicsReference'} else {''}
        Run-Tests ($module.Replace('/','-')) (Join-Path $engine $module) @('./...') $required
    }
    $comparison=Get-Content -LiteralPath (Join-Path $run 'dynamics/dynamics-go-comparison.json') -Raw | ConvertFrom-Json
    if (-not $comparison.Passed -or $comparison.SourceManifestSHA256 -ine $evidence.sourceManifestSHA256) { throw 'Unity replay report failed or used another source snapshot.' }
    $owners=@('bounce','force','freefall','hinge','impulse','push-heavy','push-light','stack-0','stack-1','stack-2','stack-3','stack-4','torque')
    if (@($comparison.Comparisons).Count -ne 13 -or (@($comparison.Comparisons.Owner | Sort-Object) -join ',') -ne ($owners -join ',') -or @($comparison.Comparisons | Where-Object { -not $_.Passed }).Count) { throw 'Saved Unity replay must pass all thirteen required cases.' }
    foreach ($inputFile in $evidence.unityReference) {
        if ((Get-FileHash -LiteralPath $inputFile.path -Algorithm SHA256).Hash -ine $inputFile.sha256) { throw 'Saved Unity input changed during replay.' }
    }
    if ($comparison.SourceHash -ine $evidence.unityReference[0].sha256 -or $comparison.ReferenceHash -ine $evidence.unityReference[1].sha256) { throw 'Unity comparison did not consume the recorded source/reference bytes.' }
    $evidence.unityComparison=[ordered]@{path=(Join-Path $run 'dynamics/dynamics-go-comparison.json');sha256=(Get-FileHash -LiteralPath (Join-Path $run 'dynamics/dynamics-go-comparison.json') -Algorithm SHA256).Hash;cases=13}
    $evidence.integrationPassed=$true
} catch {
    $evidence.integrationPassed=$false; $evidence.error=$_.Exception.Message
    throw
} finally {
    try { $evidence.sourceFilesAfter=Verify-Sources } catch { $evidence.integrationPassed=$false; $evidence.sourceIntegrityError=$_.Exception.Message; throw } finally {
        $evidence.finishedUTC=[DateTime]::UtcNow.ToString('o'); Save-Evidence
    }
}
Write-Output "Integration checks passed: $run. Aggregate delivery and published resolution remain separate."
