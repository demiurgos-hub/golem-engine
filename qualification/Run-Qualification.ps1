#requires -Version 7.0
param(
    [ValidateSet('Smoke','Rehearsal','Suite','Soak','Full','Capacity','BuildOnly','BakeOnly')][string]$Mode = 'Smoke',
    [string]$ValidationRoot = 'C:\Demiurgos\navmesh-validation\world-test',
    [string]$Library = 'C:\Demiurgos\navmesh-go',
    [string]$Engine = 'C:\Demiurgos\golem-engine',
    [string]$Lane = '',
    [int]$SoakAgents = 1000,
    [ValidateRange(1,100000)][int]$RehearsalAgents = 1000,
    [ValidateRange(1,256)][int]$Workers = 2,
    [ValidateRange(1,1000)][int]$BuildCommitsPerTick = 2,
    [string]$PathDrainBudget = '1ms',
    [string]$ProcessorAffinityMask = '',
    [ValidateSet('terrain','multilevel','construction','capacity')][string]$RehearsalFamily = 'terrain',
    [switch]$StopOnRequiredFailure,
    [switch]$Profile
)
$ErrorActionPreference = 'Stop'
if ($Profile -and $Mode -ne 'Rehearsal') { throw 'Profiling is reserved for Rehearsal; final timings must run unprofiled.' }
if (-not $Lane) {
    $Lane = (& python (Join-Path $PSScriptRoot 'stage.py') --root $ValidationRoot --library $Library --engine $Engine | Select-Object -Last 1)
    if ($LASTEXITCODE -ne 0) { throw 'Qualification source staging failed.' }
}
$settings = Get-Content -LiteralPath (Join-Path $Lane 'environment.json') -Raw | ConvertFrom-Json
foreach ($property in $settings.PSObject.Properties) { [Environment]::SetEnvironmentVariable($property.Name, [string]$property.Value, 'Process') }
$id = Split-Path $Lane -Leaf
$runID = $id + '-' + $Mode.ToLowerInvariant() + '-' + [DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffffffZ')
$binary = Join-Path $ValidationRoot ('bin\' + $id + '\windows\' + $runID)
$run = Join-Path $ValidationRoot ('runs\' + $runID)
$scratch = Join-Path $ValidationRoot ('scratch\' + $runID)
New-Item -ItemType Directory -Path $binary,$run,$scratch -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $Lane 'manifest.json') -Destination (Join-Path $run 'source-manifest.json')
$primaryCases = switch ($Mode) { 'Smoke' {1}; 'Rehearsal' {1}; 'Suite' {3}; 'Soak' {1}; 'Full' {4}; 'Capacity' {1}; default {0} }
. (Join-Path $PSScriptRoot 'processor_affinity.ps1')
$affinity = Get-QualificationAffinityEvidence $ProcessorAffinityMask
$execution = $null
try {
Set-QualificationProcessorAffinity $affinity $PSCommandPath
Add-Content -LiteralPath (Join-Path $run 'progress.jsonl') -Value (@{event='processor_affinity';utc=[DateTime]::UtcNow.ToString('o');settings=$affinity} | ConvertTo-Json -Depth 6 -Compress)
$execution = @{
    script_path = $PSCommandPath
    script_sha256 = (Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash.ToLowerInvariant()
    staged_script_sha256 = (Get-FileHash -LiteralPath (Join-Path $Lane 'golem-engine\qualification\Run-Qualification.ps1') -Algorithm SHA256).Hash.ToLowerInvariant()
    staging_script_sha256 = (Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'stage.py') -Algorithm SHA256).Hash.ToLowerInvariant()
    environment = $settings
    inherited_go_settings = (& go env -json | ConvertFrom-Json)
    powershell_version = $PSVersionTable.PSVersion.ToString()
    lane = $Lane
    navigation_clock_sources = @(Get-ChildItem -LiteralPath (Join-Path $Lane 'golem-engine\golem') -Filter 'navigation_clock*.go' | Where-Object { $_.Name -notlike '*_test.go' } | ForEach-Object { @{path=$_.FullName;sha256=(Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()} })
    mode = $Mode
    stop_on_required_failure = [bool]$StopOnRequiredFailure
    gate_policy_sha256 = (Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'gate_policy.py') -Algorithm SHA256).Hash.ToLowerInvariant()
    processor_affinity = $affinity
    processor_affinity_helper_sha256 = (Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'processor_affinity.ps1') -Algorithm SHA256).Hash.ToLowerInvariant()
    stress_runtime_cpu = @()
    immutable_input_helper_sha256 = (Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'candidate_inputs.py') -Algorithm SHA256).Hash.ToLowerInvariant()
    source_verification = @{before=(Join-Path $run 'source-before.json');after=(Join-Path $run 'source-after.json');after_runtime=(Join-Path $run 'source-after-runtime.json')}
    external_module_inputs = @{before=(Join-Path $run 'module-inputs-before.json');after=(Join-Path $run 'module-inputs-after.json')}
    scheduling = @{path_workers=$Workers;build_workers=2;build_commits_per_tick=$BuildCommitsPerTick;path_drain_budget=$PathDrainBudget}
    planned_workload_minimum_seconds_excluding_build_bake_and_overheads = switch ($Mode) { 'Smoke' {17}; 'Rehearsal' {77}; 'Suite' {2181}; 'Soak' {7327}; 'Full' {9508}; 'Capacity' {727}; default {0} }
    live_drain = @{minimum_seconds_per_case=5;timeout_seconds_per_case=30;maximum_additional_seconds_per_case=25;maximum_additional_seconds_total=(25 * $primaryCases);restart_probe_extra_drain_seconds=0;interpretation='Minimum duration includes configured drain and restart. Unsettled live work can extend each primary case by at most 25 seconds, plus tick/capture overhead; timeout fails the gate.'}
}
$execution | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath (Join-Path $run 'execution.json')
$script:GateFailure = $false
function Invoke-Logged([string]$Working, [string]$Label, [string]$Executable, [string[]]$Arguments) {
    $log = Join-Path $run ($Label + '.log')
    $started = [DateTime]::UtcNow
    $resolved = (Get-Command $Executable -CommandType Application -ErrorAction Stop).Source
    $environment = @{}
    foreach ($property in $settings.PSObject.Properties) { $environment[$property.Name] = [Environment]::GetEnvironmentVariable($property.Name, 'Process') }
    foreach ($name in @('PATH','GOMAXPROCS','GOGC','GOMEMLIMIT','GOFLAGS','GOTOOLCHAIN','GOENV')) { $environment[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
    Add-Content -LiteralPath (Join-Path $run 'progress.jsonl') -Value (@{event='start';label=$Label;utc=$started.ToString('o');command=$Executable;resolved_executable=$resolved;executable_sha256=(Get-FileHash -LiteralPath $resolved -Algorithm SHA256).Hash.ToLowerInvariant();working_directory=$Working;environment=$environment;arguments=$Arguments;launcher_affinity=$affinity.effective_hex} | ConvertTo-Json -Depth 10 -Compress)
    Push-Location $Working
    try { & $Executable @Arguments 2>&1 | Tee-Object -FilePath $log; $result = $LASTEXITCODE } finally { Pop-Location }
    Add-Content -LiteralPath (Join-Path $run 'progress.jsonl') -Value (@{event='finish';label=$Label;utc=[DateTime]::UtcNow.ToString('o');exit_code=$result;seconds=([DateTime]::UtcNow-$started).TotalSeconds} | ConvertTo-Json -Compress)
    $outIndex = [Array]::IndexOf($Arguments, '-out')
    if ($outIndex -ge 0 -and $outIndex+1 -lt $Arguments.Length) {
        $manifestPath = Join-Path $Arguments[$outIndex+1] 'manifest.json'
        if (Test-Path -LiteralPath $manifestPath) {
            $stress = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
            if ($stress.PSObject.Properties.Name -contains 'gomaxprocs') {
                $cpu = @{label=$Label;pid=$stress.pid;logical_cpus=$stress.logical_cpus;gomaxprocs=$stress.gomaxprocs;manifest=$manifestPath;launcher_affinity=$affinity.effective_hex}
                $execution.stress_runtime_cpu += $cpu
                Add-Content -LiteralPath (Join-Path $run 'progress.jsonl') -Value (@{event='stress_runtime_cpu';observed=$cpu} | ConvertTo-Json -Depth 6 -Compress)
            }
        }
        $summaryPath = Join-Path $Arguments[$outIndex+1] 'summary.json'
        if ($StopOnRequiredFailure -and ([Array]::IndexOf($Arguments, '-agents') -ge 0) -and ([Array]::IndexOf($Arguments, '-bake-only') -lt 0) -and -not (Test-Path -LiteralPath $summaryPath)) {
            throw "Required runtime summary is missing for '$Label': $summaryPath"
        }
        if ($StopOnRequiredFailure -and (Test-Path -LiteralPath $summaryPath)) {
            Invoke-Logged $Lane ($Label + '-gate-policy') $pythonExecutable @('-B',(Join-Path $PSScriptRoot 'gate_policy.py'),'--summary',$summaryPath,'--out',(Join-Path $Arguments[$outIndex+1] 'gate-policy.json'))
        }
    }
    if ($result -eq 2) { $script:GateFailure = $true; Write-Warning "Raw gates failed for '$Label'; continuing only where the selected stop policy permits." }
    elseif ($result -ne 0) { throw "Qualification step '$Label' failed with exit $result; see $log" }
}
$inputHelper = Join-Path $PSScriptRoot 'candidate_inputs.py'
$pythonExecutable = (& python -c 'import sys; print(sys.executable)' | Select-Object -Last 1)
if ($LASTEXITCODE -ne 0 -or -not $pythonExecutable) { throw 'Unable to resolve the actual Python executable.' }
$modBefore = Join-Path $run 'module-inputs-before.json'
Invoke-Logged $Lane 'verify-source-before' $pythonExecutable @('-B',$inputHelper,'verify','--lane',$Lane,'--out',(Join-Path $run 'source-before.json'))
Invoke-Logged $Lane 'prepare-module-inputs' $pythonExecutable @('-B',$inputHelper,'prepare','--lane',$Lane,'--scratch',(Join-Path $scratch 'modfiles'),'--out',$modBefore)
$moduleInputs = Get-Content -LiteralPath $modBefore -Raw | ConvertFrom-Json
try {
    Invoke-Logged (Join-Path $Lane 'navmesh-go') 'build-worldgen' 'go' @('build','-mod=mod',('-modfile=' + $moduleInputs.modules.worldgen.modfile.path),'-o',(Join-Path $binary 'worldgen.exe'),'./cmd/navmesh-worldgen')
    Invoke-Logged (Join-Path $Lane 'golem-engine\examples\navigation-stress') 'build-stress' 'go' @('build','-mod=mod',('-modfile=' + $moduleInputs.modules.stress.modfile.path),'-o',(Join-Path $binary 'stress.exe'),'.')
} finally {
    try { Invoke-Logged $Lane 'record-module-inputs' $pythonExecutable @('-B',$inputHelper,'record','--input',$modBefore,'--out',(Join-Path $run 'module-inputs-after.json')) } finally {
        Invoke-Logged $Lane 'verify-source-after' $pythonExecutable @('-B',$inputHelper,'verify','--lane',$Lane,'--out',(Join-Path $run 'source-after.json'))
    }
}
& go version | Set-Content -LiteralPath (Join-Path $run 'go-version.txt')
$hardware = @{
    cpu = @(Get-CimInstance Win32_Processor | Select-Object Name,NumberOfCores,NumberOfLogicalProcessors)
    system = Get-CimInstance Win32_ComputerSystem | Select-Object Manufacturer,Model,TotalPhysicalMemory
    operating_system = Get-CimInstance Win32_OperatingSystem | Select-Object Caption,Version,OSArchitecture,TotalVisibleMemorySize
}
$hardware | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $run 'hardware.json')
if ($Mode -eq 'BuildOnly') { Write-Output "Binaries: $binary"; exit 0 }
$families = if ($Mode -eq 'Capacity') { @('capacity') } elseif ($Mode -eq 'Smoke') { @('smoke') } elseif ($Mode -eq 'Soak') { @('construction') } elseif ($Mode -eq 'Rehearsal' -or $Mode -eq 'BakeOnly') { @($RehearsalFamily) } else { @('terrain','multilevel','construction') }
foreach ($family in $families) {
    $world = Join-Path $ValidationRoot ('worlds\' + $runID + '-' + $family)
    Invoke-Logged $Lane ('generate-' + $family) (Join-Path $binary 'worldgen.exe') @('-config',(Join-Path $Lane ('navmesh-go\qualification\configs\' + $family + '.json')),'-out',$world)
    $counts = if ($Mode -eq 'Capacity') { @(1000) } elseif ($Mode -eq 'Smoke') { @(10) } elseif ($Mode -eq 'BakeOnly') { @(1) } elseif ($Mode -eq 'Soak') { @($SoakAgents) } elseif ($Mode -eq 'Rehearsal') { @($RehearsalAgents) } else { @(1000) }
    foreach ($count in $counts) {
        $warmup = if ($Mode -eq 'Smoke') {'2s'} elseif ($Mode -eq 'Rehearsal') {'10s'} else {'2m'}
        $duration = if ($Mode -eq 'Smoke') {'8s'} elseif ($Mode -eq 'Soak') {'2h'} elseif ($Mode -eq 'Rehearsal') {'60s'} else {'10m'}
        $case = "$family-$count"
        $arguments = @('-fixture',(Join-Path $world 'fixture.json'),'-agents',"$count",'-warmup',$warmup,'-duration',$duration,'-out',(Join-Path $run $case),'-source-manifest',(Join-Path $run 'source-manifest.json'))
        if ($Profile) { $arguments += @('-cpuprofile','cpu.pprof','-memprofile','heap.pprof') }
        $arguments += @('-workers',"$Workers",'-build-commits-per-tick',"$BuildCommitsPerTick",'-path-drain-budget',$PathDrainBudget)
        if ($Mode -eq 'BakeOnly') { $arguments += '-bake-only' }
        if ($family -eq 'capacity') { $arguments += @('-churn','0','-edits-per-sec','0','-burst','0') }
        Invoke-Logged $Lane $case (Join-Path $binary 'stress.exe') $arguments
    }
}
if ($Mode -eq 'Full') {
    Invoke-Logged $Lane ('soak-construction-' + $SoakAgents) (Join-Path $binary 'stress.exe') @('-fixture',(Join-Path $world 'fixture.json'),'-agents',"$SoakAgents",'-warmup','2m','-duration','2h','-out',(Join-Path $run ('soak-construction-' + $SoakAgents)),'-source-manifest',(Join-Path $run 'source-manifest.json'),'-workers',"$Workers",'-build-commits-per-tick',"$BuildCommitsPerTick",'-path-drain-budget',$PathDrainBudget)
}
Write-Output "Evidence: $run"
if ($script:GateFailure) { exit 2 }
} catch {
    $affinity.launcher_error = $_.Exception.Message
    throw
} finally {
    try {
        if ($pythonExecutable -and $inputHelper) {
            Invoke-Logged $Lane 'verify-source-after-runtime' $pythonExecutable @('-B',$inputHelper,'verify','--lane',$Lane,'--out',(Join-Path $run 'source-after-runtime.json'))
        }
    } finally {
      try { Restore-QualificationProcessorAffinity $affinity } finally {
        $affinity.finished_utc = [DateTime]::UtcNow.ToString('o')
        $affinity | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $run 'processor-affinity.json')
        Add-Content -LiteralPath (Join-Path $run 'progress.jsonl') -Value (@{event='processor_affinity_restored';settings=$affinity} | ConvertTo-Json -Depth 6 -Compress)
        if ($null -ne $execution) { $execution | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath (Join-Path $run 'execution.json') }
      }
    }
}
