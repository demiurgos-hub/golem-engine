#requires -Version 7.0
param(
    [Parameter(Mandatory=$true)][string]$Lane,
    [string]$ValidationRoot = 'C:\Demiurgos\navmesh-validation\world-test',
    [string]$Unity = "$env:LOCALAPPDATA/Unity/bin/unity.exe",
    [string]$EditorVersion = '6000.3.13f1',
    [string]$WebTransport = 'C:\Demiurgos\webtransport-csharp\artifacts\upm\io.demiurgos.webtransport',
    [switch]$BuildOnly
)
$ErrorActionPreference = 'Stop'
$ValidationRoot = [IO.Path]::GetFullPath($ValidationRoot)
$Lane = (Resolve-Path -LiteralPath $Lane).Path
function Assert-UnderRoot([string]$Path) {
    $full = [IO.Path]::GetFullPath($Path)
    if (-not $full.StartsWith($ValidationRoot.TrimEnd('\','/') + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Qualification path is outside the validation root: $full"
    }
    return $full
}
$null = Assert-UnderRoot $Lane
$settings = Get-Content -LiteralPath (Join-Path $Lane 'environment.json') -Raw | ConvertFrom-Json
foreach ($property in $settings.PSObject.Properties) { [Environment]::SetEnvironmentVariable($property.Name, [string]$property.Value, 'Process') }
$id = Split-Path $Lane -Leaf
$runID = 'unity-' + [DateTime]::UtcNow.ToString('yyyyMMddTHHmmss.fffffffZ') + '-' + [Guid]::NewGuid().ToString('N').Substring(0,8)
$run = Assert-UnderRoot (Join-Path $ValidationRoot "runs\$runID")
$workspace = Assert-UnderRoot (Join-Path $ValidationRoot "workspaces\$runID")
$scratch = Assert-UnderRoot (Join-Path $ValidationRoot "scratch\$runID")
$binary = Assert-UnderRoot (Join-Path $ValidationRoot "bin\$id\windows\$runID")
foreach ($directory in @($run,$workspace,$scratch,$binary)) {
    if (Test-Path -LiteralPath $directory) { throw "Existing qualification content must not be overwritten: $directory" }
    New-Item -ItemType Directory -Path $directory | Out-Null
}
$env:TEMP = $env:TMP = $env:TMPDIR = $env:GOTMPDIR = $scratch
$env:GOWORK = 'off'
$env:CGO_ENABLED = '0'
$env:GOTOOLCHAIN = 'local'
$env:GOENV = 'off'
$env:GOTELEMETRY = 'off'
$env:PYTHONDONTWRITEBYTECODE = '1'
$env:NUGET_PACKAGES = Assert-UnderRoot (Join-Path $ValidationRoot 'cache\nuget')
$env:DOTNET_CLI_HOME = Join-Path $scratch 'dotnet-home'
$env:DOTNET_SKIP_FIRST_TIME_EXPERIENCE = '1'
$env:DOTNET_CLI_TELEMETRY_OPTOUT = '1'
$env:DOTNET_NOLOGO = '1'
$env:GOLEM_NAVIGATION_EXPORT_FIXTURE = Join-Path $run 'scribe-source.json'
foreach ($directory in @($env:GOCACHE,$env:GOMODCACHE,$env:NUGET_PACKAGES,$env:DOTNET_CLI_HOME)) {
    $null = Assert-UnderRoot $directory
    New-Item -ItemType Directory -Force -Path $directory | Out-Null
}
$project = Join-Path $workspace 'unity-authoring'
$package = Join-Path $workspace 'golem-engine\golem-unity'
$dependency = Join-Path $workspace 'unity-webtransport'
$helper = Join-Path $PSScriptRoot 'candidate_inputs.py'
$python = (& python -B -c 'import sys; print(sys.executable)' | Select-Object -Last 1)
if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $python -PathType Leaf)) { throw 'Cannot resolve the actual Python interpreter.' }
$commands = [Collections.Generic.List[object]]::new()
$copies = [Collections.Generic.List[object]]::new()
$failure = $null
$editorExecutable = $null
$baker = Join-Path $binary 'golem-nav-bake.exe'
$beforeModfiles = Join-Path $run 'modfiles-before.json'
$testCounts = @{}
function Get-FileState([string]$Path) {
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return @{path=$Path;exists=$false} }
    $item = Get-Item -LiteralPath $Path
    return @{path=$item.FullName;exists=$true;bytes=$item.Length;sha256=(Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()}
}
function Get-TreeFiles([string]$Directory) {
    @(Get-ChildItem -LiteralPath $Directory -File -Recurse -Force | Sort-Object FullName | ForEach-Object {
        @{path=[IO.Path]::GetRelativePath($Directory,$_.FullName).Replace('\','/');bytes=$_.Length;sha256=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()}
    })
}
function Copy-InputTree([string]$Label,[string]$Source,[string]$Destination) {
    $Source = (Resolve-Path -LiteralPath $Source).Path
    if (Test-Path -LiteralPath $Destination) { throw "Input copy must be new: $Destination" }
    $entries = @(Get-Item -LiteralPath $Source; Get-ChildItem -LiteralPath $Source -Recurse -Force)
    $links = $entries | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint }
    if ($links) { throw "Input trees must contain ordinary files and directories: $Source" }
    $initial = @(Get-TreeFiles $Source)
    New-Item -ItemType Directory -Force -Path (Split-Path $Destination -Parent) | Out-Null
    Copy-Item -LiteralPath $Source -Destination $Destination -Recurse -Force
    $copied = @(Get-TreeFiles $Destination)
    $expected = @{}
    foreach ($item in $initial) { $expected[$item.path] = $item }
    if ($copied.Count -ne $initial.Count) { throw "Copied input file count changed: $Label" }
    foreach ($item in $copied) {
        $original = $expected[$item.path]
        if (-not $original -or $original.sha256 -ne $item.sha256 -or $original.bytes -ne $item.bytes) { throw "Copied input differs: $Label/$($item.path)" }
    }
    $receipt = @{label=$Label;source_directory=$Source;copy_directory=$Destination;file_count=$initial.Count;files=$initial;copy_verified=$true}
    $copies.Add($receipt)
    ConvertTo-Json -InputObject $receipt -Depth 6 | Set-Content -LiteralPath (Join-Path $run "$Label-input-files.json")
}
function Invoke-Check([string]$Label,[string]$Program,[string[]]$Arguments,[string]$Working) {
    $started = [DateTime]::UtcNow
    $resolved = (Get-Command $Program -CommandType Application -ErrorAction Stop).Source
    $code = $null
    $commandError = $null
    Push-Location $Working
    try {
        & $Program @Arguments 2>&1 | Tee-Object -FilePath (Join-Path $run "$Label.log")
        $code = $LASTEXITCODE
        if ($code -ne 0) { throw "$Label failed with exit $code" }
    } catch { $commandError = $_.ToString(); throw } finally {
        Pop-Location
        $commands.Add(@{label=$Label;program=$Program;resolved_executable=$resolved;executable_sha256=(Get-FileState $resolved).sha256;arguments=$Arguments;working_directory=$Working;started_utc=$started.ToString('o');finished_utc=[DateTime]::UtcNow.ToString('o');exit_code=$code;error=$commandError})
        ConvertTo-Json -InputObject @($commands.ToArray()) -Depth 6 | Set-Content -LiteralPath (Join-Path $run 'commands.json')
    }
}
try {
    Invoke-Check 'source-before' $python @('-B',$helper,'verify','--lane',$Lane,'--out',(Join-Path $run 'source-before.json')) $workspace
    $candidateHarness = Join-Path $Lane 'golem-engine\qualification\Run-UnityQualification.ps1'
    if ((Get-FileState $candidateHarness).sha256 -ne (Get-FileState $PSCommandPath).sha256) { throw 'The harness must match the pinned candidate.' }
    if ((Get-FileState (Join-Path $Lane 'golem-engine\qualification\candidate_inputs.py')).sha256 -ne (Get-FileState $helper).sha256) { throw 'The input helper must match the pinned candidate.' }
    Copy-InputTree 'golem-unity' (Join-Path $Lane 'golem-engine\golem-unity') $package
    Copy-InputTree 'webtransport' $WebTransport $dependency
    Copy-InputTree 'unity-assets' (Join-Path $Lane 'golem-engine\qualification\unity\Assets') (Join-Path $project 'Assets')
    # The portable csproj references the same contract fixture relative to its engine root.
    $fixtureRelative = 'golem-engine\golem\nav\navmesh\navbuild\testdata\scribe-source.json'
    $fixture = Join-Path $workspace $fixtureRelative
    New-Item -ItemType Directory -Force -Path (Split-Path $fixture -Parent) | Out-Null
    Copy-Item -LiteralPath (Join-Path $Lane $fixtureRelative) -Destination $fixture
    $fixtureSource = Get-FileState (Join-Path $Lane $fixtureRelative)
    $fixtureCopy = Get-FileState $fixture
    if ($fixtureSource.sha256 -ne $fixtureCopy.sha256) { throw 'Portable fixture copy differs.' }
    @{source=$fixtureSource;copy=$fixtureCopy} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $run 'portable-fixture-input.json')
    New-Item -ItemType Directory -Path (Join-Path $project 'Packages'),(Join-Path $project 'ProjectSettings') | Out-Null
    @{dependencies=@{'io.demiurgos.golemengine'=('file:'+$package.Replace('\','/'));'io.demiurgos.webtransport'=('file:'+$dependency.Replace('\','/'));'com.unity.test-framework'='1.6.0';'com.unity.modules.physics'='1.0.0';'com.unity.modules.physics2d'='1.0.0';'com.unity.modules.unitywebrequest'='1.0.0';'com.unity.modules.jsonserialize'='1.0.0';'com.unity.modules.terrain'='1.0.0';'com.unity.modules.terrainphysics'='1.0.0'};testables=@('io.demiurgos.golemengine')} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $project 'Packages\manifest.json')
    "m_EditorVersion: $EditorVersion" | Set-Content -LiteralPath (Join-Path $project 'ProjectSettings\ProjectVersion.txt')
    Invoke-Check 'modfiles-prepare' $python @('-B',$helper,'prepare','--lane',$Lane,'--scratch',(Join-Path $scratch 'modfiles'),'--module','navmesh=golem-engine/golem/nav/navmesh','--out',$beforeModfiles) $workspace
    $modfiles = Get-Content -LiteralPath $beforeModfiles -Raw | ConvertFrom-Json
    Invoke-Check 'build-baker' 'go' @('build','-mod=mod',('-modfile='+$modfiles.modules.navmesh.modfile.path),'-o',$baker,'./cmd/golem-nav-bake') (Join-Path $Lane 'golem-engine\golem\nav\navmesh')
    if (-not $BuildOnly) {
        Invoke-Check 'installed-editors' $Unity @('editors','--installed','--json') $workspace
        $editors = Get-Content -LiteralPath (Join-Path $run 'installed-editors.log') -Raw | ConvertFrom-Json
        $editor = @($editors.data | Where-Object { $_.version -eq $EditorVersion -and $_.location })
        if (-not $editors.success -or $editor.Count -ne 1 -or -not (Test-Path -LiteralPath $editor[0].location -PathType Leaf)) { throw "Cannot resolve the exact installed Editor: $EditorVersion" }
        $editorExecutable = $editor[0].location
        Invoke-Check 'portable' 'dotnet' @('test',(Join-Path $package 'Tests\Portable\ScribePolicy.Tests.csproj'),'--artifacts-path',(Join-Path $binary 'dotnet'),'--logger','trx;LogFileName=portable.trx','--results-directory',$run) $workspace
        Invoke-Check 'editmode' $Unity @('test',$project,'--mode','EditMode','--filter','GolemEngine.Unity.Editor.Tests','--editor-path',$editorExecutable,'--output',(Join-Path $run 'editmode.xml'),'--timeout','600','--json','--non-interactive','--','-nographics','-logFile',(Join-Path $run 'editor.log')) $workspace
        [xml]$portableResults = Get-Content -LiteralPath (Join-Path $run 'portable.trx') -Raw
        [xml]$editmodeResults = Get-Content -LiteralPath (Join-Path $run 'editmode.xml') -Raw
        $testCounts.portable = @{}
        foreach ($attribute in $portableResults.TestRun.ResultSummary.Counters.Attributes) { $testCounts.portable[$attribute.Name] = $attribute.Value }
        $testCounts.editmode = @{}
        foreach ($attribute in $editmodeResults.DocumentElement.Attributes) { $testCounts.editmode[$attribute.Name] = $attribute.Value }
        if ([int]$testCounts.portable.total -le 0 -or [int]$testCounts.portable.failed -ne 0 -or [int]$testCounts.editmode.total -le 0 -or [int]$testCounts.editmode.failed -ne 0) { throw 'Expected nonempty successful portable and EditMode test results.' }
        Invoke-Check 'bake-emitted-source' $baker @('-in',$env:GOLEM_NAVIGATION_EXPORT_FIXTURE,'-out',(Join-Path $run 'scribe.nav')) $workspace
    }
} catch { $failure = $_.ToString() } finally {
    if (Test-Path -LiteralPath $beforeModfiles) {
        try { Invoke-Check 'modfiles-record' $python @('-B',$helper,'record','--input',$beforeModfiles,'--out',(Join-Path $run 'modfiles-after.json')) $workspace } catch { $failure = "$failure`n$_" }
    }
    try { Invoke-Check 'source-after' $python @('-B',$helper,'verify','--lane',$Lane,'--out',(Join-Path $run 'source-after.json')) $workspace } catch { $failure = "$failure`n$_" }
    $effectiveEnvironment = @{}
    foreach ($name in @('GOWORK','CGO_ENABLED','GOPROXY','GONOSUMDB','GOMODCACHE','GOCACHE','GOTMPDIR','TEMP','TMP','TMPDIR','GOTOOLCHAIN','GOENV','GOFLAGS','GOTELEMETRY','PYTHONDONTWRITEBYTECODE','NUGET_PACKAGES','DOTNET_CLI_HOME','DOTNET_SKIP_FIRST_TIME_EXPERIENCE','DOTNET_CLI_TELEMETRY_OPTOUT','DOTNET_NOLOGO','GOLEM_NAVIGATION_EXPORT_FIXTURE')) { $effectiveEnvironment[$name] = [Environment]::GetEnvironmentVariable($name,'Process') }
    $editorState = if ($editorExecutable) { Get-FileState $editorExecutable } else { $null }
    $status = if ($failure) { 'failed' } elseif ($BuildOnly) { 'build-only' } else { 'completed' }
    @{status=$status;error=$failure;run_id=$runID;candidate=$id;workspace=$workspace;scratch=$scratch;binary_directory=$binary;project=$project;source_manifest=(Get-FileState (Join-Path $Lane 'manifest.json'));environment=$effectiveEnvironment;editor_version=$EditorVersion;editor=$editorState;baker=(Get-FileState $baker);harness=(Get-FileState $PSCommandPath);input_helper=(Get-FileState $helper);copied_inputs=@($copies.ToArray());test_counts=$testCounts;exclusions=$(if ($BuildOnly) { @('Portable tests, Unity Editor discovery/tests, and Scribe export bake were not run. This is build-only evidence.') } else { @() })} | ConvertTo-Json -Depth 9 | Set-Content -LiteralPath (Join-Path $run 'manifest.json')
    ConvertTo-Json -InputObject @(Get-TreeFiles $binary) -Depth 4 | Set-Content -LiteralPath (Join-Path $run 'binary-files.json')
    ConvertTo-Json -InputObject @(Get-TreeFiles $run) -Depth 4 | Set-Content -LiteralPath (Join-Path $run 'files.json')
    Write-Output "Unity evidence: $run"
}
if ($failure) { throw $failure }
