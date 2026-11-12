[CmdletBinding()]
param(
    [Parameter(Mandatory=$true)][string]$Project,
    [Parameter(Mandatory=$true)][string]$Inputs,
    [Parameter(Mandatory=$true)][string]$Output,
    [int]$BatchStart = 0,
    [int]$BatchCount = 13,
    [string]$EditorVersion = '6000.3.13f1'
)
$ErrorActionPreference = 'Stop'
$allowed = [IO.Path]::GetFullPath('D:/Demiurgos/tests/physics-validation').TrimEnd('\','/')
foreach ($value in @($Project,$Inputs,$Output)) {
    $absolute = [IO.Path]::GetFullPath($value)
    if (!$absolute.StartsWith($allowed + '\',[StringComparison]::OrdinalIgnoreCase)) { throw 'Catalog execution must stay inside the D physics validation workspace.' }
}
if ($BatchStart -lt 0 -or $BatchCount -lt 1 -or $BatchCount -gt 64) { throw 'Invalid bounded batch selection.' }
if (Test-Path -LiteralPath $Output) { throw 'Preserve existing catalog run outputs.' }
foreach ($process in @(Get-CimInstance Win32_Process -Filter "Name = 'Unity.exe'")) {
    if ($process.CommandLine -and $process.CommandLine.Replace('/','\').IndexOf($Project.Replace('/','\'),[StringComparison]::OrdinalIgnoreCase) -ge 0) { throw 'The copied fixture project is already open.' }
}
New-Item -ItemType Directory -Path $Output -Force | Out-Null
$env:TEMP = Join-Path $Output 'tmp'; $env:TMP = $env:TEMP
New-Item -ItemType Directory -Path $env:TEMP -Force | Out-Null
$manifest = [ordered]@{version=1;scope='Resolved copied catalog/state and query reference capture; no gameplay, dynamics or capacity qualification';project=[IO.Path]::GetFullPath($Project);editorVersion=$EditorVersion;startedUTC=[DateTime]::UtcNow.ToString('o');batches=@();qualified=$false}
$packageManifest = Get-Content -LiteralPath (Join-Path $Project 'Packages/manifest.json') -Raw | ConvertFrom-Json
$packageReference = $packageManifest.dependencies.'io.demiurgos.golemengine'
if (!$packageReference.StartsWith('file:')) { throw 'Use an exact staged local engine package.' }
$packagePath = [IO.Path]::GetFullPath($packageReference.Substring(5))
if (!$packagePath.StartsWith($allowed + '\',[StringComparison]::OrdinalIgnoreCase)) { throw 'Staged engine package must remain in D validation workspace.' }
$manifest.package = $packagePath
$manifest.packageSource = @(Get-ChildItem -LiteralPath $packagePath -Recurse -File | Sort-Object FullName | ForEach-Object { [ordered]@{path=$_.FullName.Substring($packagePath.Length+1);sha256=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()} })
$failed = $false
try {
    for ($batch = $BatchStart; $batch -lt $BatchStart + $BatchCount; $batch++) {
        $name = 'batch-{0:D2}' -f $batch
        $inputPath = Join-Path $Inputs ($name + '.json')
        if (!(Test-Path -LiteralPath $inputPath -PathType Leaf)) { throw "Missing selected batch $inputPath" }
        $inputData = Get-Content -LiteralPath $inputPath -Raw | ConvertFrom-Json
        if ($inputData.Prefabs.Count -lt 1 -or $inputData.Prefabs.Count -gt 16) { throw 'Batch must contain one to sixteen roots.' }
        $env:GOLEM_PHYSICS_CATALOG_INPUT = [IO.Path]::GetFullPath($inputPath)
        $env:GOLEM_PHYSICS_FIXTURE_OUTPUT = Join-Path $Output $name
        $started = [DateTime]::UtcNow.ToString('o')
        & unity run $Project --editor-version $EditorVersion --timeout 600 --log-file (Join-Path $Output ($name + '.log')) --no-tail --json -- -nographics -executeMethod GolemEngine.Unity.Editor.Tests.GolemPhysicsCatalogFixture.Generate 2>&1 | Out-File -LiteralPath (Join-Path $Output ($name + '-cli.json')) -Encoding utf8
        $code = $LASTEXITCODE
        $resultPath = Join-Path $env:GOLEM_PHYSICS_FIXTURE_OUTPUT 'catalog-result.json'
        $record = [ordered]@{batch=$batch;input=$env:GOLEM_PHYSICS_CATALOG_INPUT;inputSHA256=(Get-FileHash -LiteralPath $inputPath -Algorithm SHA256).Hash.ToLowerInvariant();roots=$inputData.Prefabs.Count;startedUTC=$started;finishedUTC=[DateTime]::UtcNow.ToString('o');exitCode=$code;result=$resultPath;resultSHA256=$null}
        if (Test-Path -LiteralPath $resultPath) { $record.resultSHA256=(Get-FileHash -LiteralPath $resultPath -Algorithm SHA256).Hash.ToLowerInvariant() }
        if ($code -ne 0 -or !$record.resultSHA256) { $failed=$true }
        $manifest.batches += $record
        Write-Output "$name roots=$($inputData.Prefabs.Count) exit=$code result=$resultPath"
    }
} finally {
    $manifest.packageUnchanged = $true
    foreach ($file in $manifest.packageSource) {
        if ((Get-FileHash -LiteralPath (Join-Path $packagePath $file.path) -Algorithm SHA256).Hash.ToLowerInvariant() -ne $file.sha256) { $manifest.packageUnchanged=$false; $failed=$true }
    }
    $manifest.finishedUTC=[DateTime]::UtcNow.ToString('o')
    $manifest.requestedBatchesPassed=!$failed -and $manifest.batches.Count -eq $BatchCount
    [IO.File]::WriteAllText((Join-Path $Output 'manifest.json'),($manifest | ConvertTo-Json -Depth 12),[Text.UTF8Encoding]::new($false))
}
if ($failed) { throw 'One or more catalog batches retained explicit failures; inspect their result manifests.' }
