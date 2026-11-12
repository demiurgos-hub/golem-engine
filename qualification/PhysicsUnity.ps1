[CmdletBinding()]
param(
    [ValidateSet('Prepare','Test','Generate','All')][string]$Mode = 'All',
    [string]$ValidationRoot = 'D:\Demiurgos\tests\physics-validation',
    [string]$EditorVersion = '6000.3.13f1',
    [string]$WebTransportPackage = 'C:\Demiurgos\webtransport-csharp\artifacts\upm\io.demiurgos.webtransport',
    [string]$RunID = ('unity-' + (Get-Date -Format 'yyyyMMdd-HHmmss-fff'))
)
$ErrorActionPreference = 'Stop'
if ($RunID -notmatch '^[A-Za-z0-9_.-]+$') { throw 'RunID must be one path component.' }
if ($EditorVersion -notmatch '^[0-9]+\.[0-9]+\.[0-9]+[abfp][0-9]+$') { throw 'Specify an exact installed Editor version.' }
$engineRoot = Split-Path -Parent $PSScriptRoot
$validationPath = [IO.Path]::GetFullPath($ValidationRoot).TrimEnd('\','/')
$allowed = [IO.Path]::GetFullPath('D:\Demiurgos\tests\physics-validation')
if ($validationPath -ne $allowed -and -not $validationPath.StartsWith($allowed + '\', [StringComparison]::OrdinalIgnoreCase)) {
    throw "Physics artifacts must stay under $allowed"
}
$project = Join-Path $validationPath 'unity/physics-fixtures'
foreach ($process in @(Get-CimInstance Win32_Process -Filter "Name = 'Unity.exe'")) {
    if ($process.CommandLine -and ($process.CommandLine.Replace('/','\').IndexOf($project.Replace('/','\'), [StringComparison]::OrdinalIgnoreCase) -ge 0)) {
        throw "The fixture Editor is already open (PID $($process.ProcessId)); close it before staging or running batch tests. Other projects are not touched."
    }
}
$packageSource = Join-Path $engineRoot 'golem-unity'
$transportSource = [IO.Path]::GetFullPath($WebTransportPackage)
if (!(Test-Path -LiteralPath (Join-Path $transportSource 'package.json') -PathType Leaf) -or
    !(Get-ChildItem -LiteralPath $transportSource -Recurse -File -Filter '*.dll' | Select-Object -First 1)) {
    throw 'WebTransportPackage must be an existing built UPM package with its managed DLLs; this script does not build a foreign repository.'
}
$run = Join-Path $validationPath "unity/runs/$RunID"
if (Test-Path -LiteralPath $run) { throw "Existing run must not be overwritten: $run" }
$package = Join-Path $run 'package'
$transport = Join-Path $run 'webtransport'
foreach ($path in @($run,$package,$transport,(Join-Path $project 'Assets'),(Join-Path $project 'Packages'),(Join-Path $project 'ProjectSettings'),(Join-Path $validationPath 'tmp'))) {
    New-Item -ItemType Directory -Path $path -Force | Out-Null
}
$env:TEMP = Join-Path $validationPath 'tmp'
$env:TMP = $env:TEMP
$env:GOLEM_PHYSICS_FIXTURE_OUTPUT = Join-Path $run 'reference'
$hashes = @()
foreach ($part in @('Runtime','Editor','Tests','package.json')) {
    $from = Join-Path $packageSource $part
    Copy-Item -LiteralPath $from -Destination $package -Recurse -Force
}
# Keep the source package read-only to Unity: importing incomplete ide/ DLL metadata can delete it.
Copy-Item -Path (Join-Path $transportSource '*') -Destination $transport -Recurse -Force
foreach ($file in Get-ChildItem -LiteralPath $package -File -Recurse) {
    $relative = $file.FullName.Substring($package.Length + 1)
    $hash = (Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($hash -ne (Get-FileHash -LiteralPath (Join-Path $packageSource $relative) -Algorithm SHA256).Hash.ToLowerInvariant()) { throw "Source staging changed $relative" }
    $hashes += [ordered]@{ path=$relative; sha256=$hash }
}
$testFramework = if ($EditorVersion.StartsWith('2021.')) { '1.1.33' } else { '1.6.0' }
$manifest = [ordered]@{
    testables=@('io.demiurgos.golemengine')
    dependencies=[ordered]@{
        'io.demiurgos.golemengine'=('file:' + $package.Replace('\','/'))
        'io.demiurgos.webtransport'=('file:' + $transport.Replace('\','/'))
        'com.unity.test-framework'=$testFramework
        'com.unity.modules.physics'='1.0.0'
        'com.unity.modules.terrain'='1.0.0'
        'com.unity.modules.terrainphysics'='1.0.0'
        'com.unity.modules.jsonserialize'='1.0.0'
        'com.unity.modules.unitywebrequest'='1.0.0'
        'com.unity.modules.unitywebrequestwww'='1.0.0'
    }
}
$utf8 = [Text.UTF8Encoding]::new($false)
[IO.File]::WriteAllText((Join-Path $project 'Packages/manifest.json'),($manifest | ConvertTo-Json -Depth 8),$utf8)
[IO.File]::WriteAllText((Join-Path $project 'ProjectSettings/ProjectVersion.txt'),"m_EditorVersion: $EditorVersion`n",$utf8)
$evidence = [ordered]@{ runID=$RunID; mode=$Mode; startedUTC=[DateTime]::UtcNow.ToString('o'); editorVersion=$EditorVersion; project=$project; sourceRoot=$packageSource; sourceCommit=(& git -C $engineRoot rev-parse HEAD); stagedPackage=$package; source=$hashes; checks=@(); qualified=$false }
try {
    if ($Mode -in @('Test','All')) {
        & unity test $project --mode EditMode --filter GolemPhysics --output (Join-Path $run 'tests.xml') --editor-version $EditorVersion --timeout 600 --json -- -nographics -logFile (Join-Path $run 'tests.log') 2>&1 | Out-File -LiteralPath (Join-Path $run 'tests-cli.json') -Encoding utf8
        $code=$LASTEXITCODE; $evidence.checks += [ordered]@{name='EditMode physics';exitCode=$code}
        if ($code -ne 0) { throw "Unity tests failed; see $run" }
        [xml]$report = Get-Content -LiteralPath (Join-Path $run 'tests.xml') -Raw
        if ([int]$report.'test-run'.failed -ne 0 -or [int]$report.'test-run'.passed -le 0) { throw 'Unity report contains failures or no passing tests.' }
    }
    if ($Mode -in @('Generate','All')) {
        & unity run $project --editor-version $EditorVersion --timeout 600 --log-file (Join-Path $run 'generate.log') --no-tail --json -- -nographics -executeMethod GolemEngine.Unity.Editor.Tests.GolemPhysicsFixtureBuilder.Generate 2>&1 | Out-File -LiteralPath (Join-Path $run 'generate-cli.json') -Encoding utf8
        $code=$LASTEXITCODE; $evidence.checks += [ordered]@{name='Export source and Unity reference observations';exitCode=$code}
        if ($code -ne 0) { throw "Unity generation failed; see $run" }
        foreach ($name in @('unity-physics.source.json','unity-physics.reference.json')) {
            if (!(Test-Path -LiteralPath (Join-Path $env:GOLEM_PHYSICS_FIXTURE_OUTPUT $name) -PathType Leaf)) { throw "Missing fixture output $name" }
        }
    }
    $evidence.requestedChecksPassed=$true
} finally {
    $evidence.finishedUTC=[DateTime]::UtcNow.ToString('o')
    [IO.File]::WriteAllText((Join-Path $run 'manifest.json'),($evidence | ConvertTo-Json -Depth 12),$utf8)
}
Write-Output "Unity $Mode completed. Evidence: $run. Backend equivalence and workload qualification remain separate."
