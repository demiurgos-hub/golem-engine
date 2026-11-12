#requires -Version 7.0
param([Parameter(Mandatory)][string]$Out)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'processor_affinity.ps1')
$original = Get-QualificationAffinityEvidence ''
$available = [Convert]::ToUInt64($original.before_hex.Substring(2),16)
$rejected = @()
foreach ($invalid in @('0','0xZZ','-1')) {
    $evidence = Get-QualificationAffinityEvidence $invalid
    try { Set-QualificationProcessorAffinity $evidence $PSCommandPath; throw 'Invalid mask accepted.' } catch {
        if ($_.Exception.Message -eq 'Invalid mask accepted.') { throw }
        $rejected += @{requested=$invalid;error=$_.Exception.Message}
    } finally { Restore-QualificationProcessorAffinity $evidence }
    if ($evidence.attempted) { throw 'Invalid mask changed affinity.' }
}
if ($available -ne [UInt64]::MaxValue) {
    $evidence = Get-QualificationAffinityEvidence '0xFFFFFFFFFFFFFFFF'
    try { Set-QualificationProcessorAffinity $evidence $PSCommandPath; throw 'Unavailable processors accepted.' } catch {
        if ($_.Exception.Message -eq 'Unavailable processors accepted.') { throw }
        $rejected += @{requested='0xFFFFFFFFFFFFFFFF';error=$_.Exception.Message}
    } finally { Restore-QualificationProcessorAffinity $evidence }
    if ($evidence.attempted) { throw 'Unavailable mask changed affinity.' }
}
[UInt64]$single = 1
while (($single -band $available) -eq 0) { $single = $single -shl 1 }
$evidence = Get-QualificationAffinityEvidence ('0x{0:X}' -f $single)
$intentionalFailureObserved = $false
try {
    Set-QualificationProcessorAffinity $evidence $PSCommandPath
    $shell = (Get-Process -Id $PID).Path
    $childMask = (& $shell -NoProfile -Command '[Diagnostics.Process]::GetCurrentProcess().ProcessorAffinity.ToInt64()').Trim()
    if ($LASTEXITCODE -ne 0 -or [UInt64]$childMask -ne $single) { throw 'Child did not inherit the requested affinity.' }
    throw 'intentional qualification failure'
} catch {
    if ($_.Exception.Message -ne 'intentional qualification failure') { throw }
    $intentionalFailureObserved = $true
} finally { Restore-QualificationProcessorAffinity $evidence }
if (-not $intentionalFailureObserved -or -not $evidence.restoration_succeeded -or $evidence.restored_hex -ne $original.before_hex) { throw 'Exception path did not restore affinity.' }
@{result='pass';invalid_masks=$rejected;exception_path=$evidence;child_inherited_mask=$childMask;source_sha256=(Get-FileHash -LiteralPath (Join-Path $PSScriptRoot 'processor_affinity.ps1') -Algorithm SHA256).Hash;test_sha256=(Get-FileHash -LiteralPath $PSCommandPath -Algorithm SHA256).Hash} | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $Out
Write-Output 'Affinity validation, child inheritance and exception restoration PASS'
