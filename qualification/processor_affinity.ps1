#requires -Version 7.0

function Get-QualificationAffinityEvidence([string]$RequestedMask) {
    $process = [Diagnostics.Process]::GetCurrentProcess()
    $before = [BitConverter]::ToUInt64([BitConverter]::GetBytes($process.ProcessorAffinity.ToInt64()), 0)
    return @{process_id=$PID;requested=$RequestedMask;before_hex=('0x{0:X}' -f $before);effective_hex=('0x{0:X}' -f $before);attempted=$false;applied=$false;scope='Current isolated launcher process and inherited children only; no power, priority or system policy changes.'}
}

function Set-QualificationProcessorAffinity([hashtable]$Evidence, [string]$LauncherPath) {
    if (-not $Evidence.requested) { return }
    if ([IntPtr]::Size -ne 8) { throw 'ProcessorAffinityMask requires 64-bit PowerShell.' }
    $commandLine = [Environment]::GetCommandLineArgs()
    $fileIndex = -1
    for ($i=0; $i -lt $commandLine.Length; $i++) { if ($commandLine[$i] -ieq '-File') { $fileIndex=$i; break } }
    if ($fileIndex -lt 0 -or $fileIndex+1 -ge $commandLine.Length -or [IO.Path]::GetFullPath($commandLine[$fileIndex+1]) -ine [IO.Path]::GetFullPath($LauncherPath)) {
        throw 'ProcessorAffinityMask requires a dedicated pwsh -File invocation of this launcher.'
    }
    if (-not ('GolemQualification.NativeCpuGroups' -as [type])) {
        Add-Type -TypeDefinition 'using System.Runtime.InteropServices; namespace GolemQualification { public static class NativeCpuGroups { [DllImport("kernel32.dll")] public static extern ushort GetActiveProcessorGroupCount(); } }'
    }
    $Evidence.processor_groups = [GolemQualification.NativeCpuGroups]::GetActiveProcessorGroupCount()
    if ($Evidence.processor_groups -ne 1) { throw 'ProcessorAffinityMask supports exactly one active processor group; multi-group affinity is not qualified.' }
    $text = $Evidence.requested.Trim()
    $style = [Globalization.NumberStyles]::None
    if ($text.StartsWith('0x', [StringComparison]::OrdinalIgnoreCase)) { $text=$text.Substring(2); $style=[Globalization.NumberStyles]::AllowHexSpecifier }
    [UInt64]$requested = 0
    if (-not [UInt64]::TryParse($text, $style, [Globalization.CultureInfo]::InvariantCulture, [ref]$requested) -or $requested -eq 0) { throw 'ProcessorAffinityMask must be a nonzero unsigned decimal or 0x-prefixed hexadecimal mask.' }
    $available = [Convert]::ToUInt64($Evidence.before_hex.Substring(2), 16)
    if (($requested -band $available) -ne $requested) { throw 'ProcessorAffinityMask must be a subset of the launcher original available affinity mask.' }
    $Evidence.requested_hex = '0x{0:X}' -f $requested
    $Evidence.attempted = $true
    $process = [Diagnostics.Process]::GetCurrentProcess()
    $process.ProcessorAffinity = [IntPtr]([BitConverter]::ToInt64([BitConverter]::GetBytes($requested), 0))
    $process.Refresh()
    $effective = [BitConverter]::ToUInt64([BitConverter]::GetBytes($process.ProcessorAffinity.ToInt64()), 0)
    $Evidence.effective_hex = '0x{0:X}' -f $effective
    $Evidence.applied = $true
    if ($effective -ne $requested) { throw 'Processor affinity readback differs from the requested mask.' }
}

function Restore-QualificationProcessorAffinity([hashtable]$Evidence) {
    $process = [Diagnostics.Process]::GetCurrentProcess()
    $before = [Convert]::ToUInt64($Evidence.before_hex.Substring(2), 16)
    if ($Evidence.attempted) { $process.ProcessorAffinity = [IntPtr]([BitConverter]::ToInt64([BitConverter]::GetBytes($before), 0)) }
    $process.Refresh()
    $restored = [BitConverter]::ToUInt64([BitConverter]::GetBytes($process.ProcessorAffinity.ToInt64()), 0)
    $Evidence.restored_hex = '0x{0:X}' -f $restored
    $Evidence.restoration_succeeded = $restored -eq $before
    if (-not $Evidence.restoration_succeeded) { throw 'Launcher processor affinity was not restored to its original mask.' }
}
