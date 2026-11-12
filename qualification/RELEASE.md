# Navigation candidate qualification and publication

This procedure prepares an unpublished candidate. It does not create a remote,
tag, push, publish a release, or establish that the intended versions are public.

## Preserve an exact candidate

From the engine checkout, run `python qualification/stage.py`. The script copies
tracked and untracked, non-ignored source files, records their byte hashes and
Git heads/status, and creates private module archives. It leaves repository
module manifests and checksums unchanged. Inspect `manifest.json` and keep the
printed workspace path with the results.

Windows and Linux workload builds copy module manifests and any existing sums to
per-run external `.mod`/`.sum` files under `scratch`, then pass `-modfile` to Go.
Candidate dependency checksums may change only those external files. Receipts
record their paths and hashes before and after building. Every originally
manifested source file and candidate module archive is verified before and after
the builds, then again in a final check after runtime completion or failure;
additional source-tree files are rejected too. A mismatch stops the
run. Do not repair an older mutated stage or overwrite its evidence: stage fresh
source. Earlier direct `-mod=mod` builds remain historical diagnostics, not proof
of an immutable final candidate.

The default workspace is `C:/Demiurgos/navmesh-validation/world-test`:

| Directory | Purpose |
| --- | --- |
| `worlds` | Generated source/configuration and baked artifacts |
| `workspaces` | Exact source copies, candidate proxies and consumer checks |
| `bin` | Candidate/platform executables |
| `scratch` | Temporary and child-test working files |
| `cache` | Build, module and toolchain caches |
| `runs` | Commands, manifests, samples, profiles and failures |

Set the process environment from the candidate's `environment.json` for every
check. `GOWORK=off` and `CGO_ENABLED=0` are ordinary build requirements. `TEMP`,
`TMP`, `GOTMPDIR`, `GOCACHE` and `GOMODCACHE` stay in this workspace. For WSL,
translate the paths to `/mnt/c/Demiurgos/navmesh-validation/world-test`, set
`TMPDIR` too, and use a Linux build cache. A race lane separately enables cgo
only for instrumentation. Do not copy candidate sums into the source checkout.

## Run checks and workloads

`pwsh -NoProfile -File qualification/Run-Qualification.ps1 -Mode Smoke` stages and executes a short
integration rehearsal. `-Mode Suite` runs terrain, multilevel and construction
at 1,000 agents each, with two minutes of warm-up and ten measured minutes.
`-Mode Full` adds the two-hour construction/streaming soak at 1,000 agents.
The user removed the 100-agent and 5,000-agent workloads from qualification;
existing rehearsal/soak count controls and the runner's `-agents` option remain
available for explicit diagnostics. Supply `-Lane` to
use an already captured candidate. Each invocation has a distinct run directory.
`Suite` has three primary cases and a minimum of 2,181 seconds (36m 21s);
`Full` has four primary cases and a minimum of 9,508 seconds (2h 38m 28s).
These include the configured drain and restart probes, excluding build, bake
and capture overhead. Unsettled live drainage can add at most 75 seconds to
`Suite` or 100 seconds to `Full` before the drain timeout fails qualification.
For dependent final qualification, pass `-StopOnRequiredFailure` on Windows or
`--stop-on-required-failure` on Linux. The shared policy preserves raw summaries
and exit codes while stopping before later cases on required failures, missing
gates or unknown observations. The recorded 5 ms failure does not block. Integrity, evidence,
host recovery, generation and live-drain failures still stop. Scratch allocation
requires separate qualification; only the static capacity workload's warmed
path-latency gate is not applicable. Each evaluated case retains `gate-policy.json`.
Windows launchers require PowerShell 7 (`pwsh`); Windows PowerShell 5 treats
ordinary native dependency-download stderr as a terminating pipeline error.
Record logical processor topology separately from `GOMAXPROCS`: the latter
counts Go scheduler execution slots, not hardware performance cores. On Windows,
`python qualification/cpu_topology.py --lane <candidate>` reads CPU-set
`EfficiencyClass`, process affinity/default CPU sets, priority, power-throttling
policy and the active power plan,
retaining source/executable hashes without changing any settings. A topology
snapshot does not establish which core executed an earlier workload tick.

An optional `-ProcessorAffinityMask <mask>` restricts only the dedicated Windows
launcher process and its inherited children. Invoke it through `pwsh -File`;
interactive/dot-sourced use and multiple processor groups are rejected. The
nonzero hexadecimal or decimal mask must be a subset of the launcher's original
affinity. `execution.json`, `progress.jsonl` and `processor-affinity.json` retain
the original, requested, effective and restored masks. Restoration runs on normal
completion, gate failure and exceptions. Actual stress `logical_cpus` and
`gomaxprocs` values come from each child manifest, not from mask assumptions.

Derive a replay mask from the recorded CPU-set topology for that host. For the
recorded 275HX snapshot, group-zero indices `0,1,10,11,12,13,22,23` produce
`0xC03C03` by summing their individual bits; do not reuse that mapping blindly on
another machine. A corresponding controlled rehearsal command is:

```powershell
$candidate = 'C:\Demiurgos\navmesh-validation\world-test\workspaces\<candidate-id>'
$previousGC = [Environment]::GetEnvironmentVariable('GOGC', 'Process')
try {
    $env:GOGC = '200'
    pwsh -NoProfile -File qualification/Run-Qualification.ps1 -Lane $candidate -Mode Rehearsal -RehearsalAgents 1000 -RehearsalFamily terrain -Workers 4 -PathDrainBudget 2ms -ProcessorAffinityMask 0xC03C03
} finally {
    [Environment]::SetEnvironmentVariable('GOGC', $previousGC, 'Process')
}
```

Set/restore GC and other environment overrides in the caller's dedicated process
and retain their receipt. Omit the affinity argument for the all-available-core
comparison; use the same candidate, GC settings, workers and workload. Do not
change the machine's power plan, another process's affinity, or acceptance gates.

The 1,000-agent navigation p99 target is 5 ms for the group's combined tick
contribution. At the user's direction on 2026-11-11, preserve its measured failure
as a reported limit rather than a completion blocker; do not tune or lower
movement cadence merely to reach that number. Leave the numeric gate and raw
failure in the evidence. Required gates remain request-to-admission
p99 at most 100 ms outside observed quarantine, full unaffected-agent cadence,
bounded managed generations, and retained memory returning to its recorded
steady-state envelope after churn drains. Separately execute warm query
allocation tests: process-wide allocations cannot prove scratch allocation
behavior. Owned result storage is measured separately. Only the 1,000-agent
cases are part of the required workload scope.

Destination orders and movement ticks are different events. A destination can
last for hundreds of ticks; following an existing route normally uses local
movement queries rather than a fresh full path search. Scheduled or Following
counts do not prove physical displacement. Report quarantined and stationary
agents separately, and use a separate stable-geometry workload when claiming
that all 1,000 agents moved simultaneously throughout the measured interval.
Also report `MeasuredTicks / Duration`, full-tick overruns and maximum duration:
scheduling every agent on each tick does not prove the whole loop sustained
20 Hz. The analyzer reports these raw values without imposing a new tolerance.

Run the separate static qualification with
`pwsh -File qualification/Run-Qualification.ps1 -Mode Capacity -Lane <candidate>`
on Windows or `python -B qualification/run_linux.py --mode Capacity --lane <candidate>`
on native Linux/WSL. This adds no cases to `Full`: it is an independent 1,000-agent
run with two minutes of warmup, ten measured minutes, no destination or geometry
churn, and the normal 4 m/s speed. Sixteen serpentine lanes fit inside the 256 m
region. Preflight checks complete paths in both directions for both profiles and
requires actual funnel length at least `4 * (warmup + duration) * 1.1 + 100` metres.
It also checks every funnel segment continuously against the original physical
cylinder and exclusion boxes, including simultaneous vertical overlap. All four
route records retain the minimum signed separation and any offending segment,
obstacle and contact fraction. A physical-clearance failure stops before timing.
Capacity uses an explicit bake-radius margin of `2 * CellSize` (0.5 m with the
canonical settings): baked radii are 0.9/1.3 m, while physical body radii remain
0.4/0.8 m. Wall geometry, body height, speed and motor policy are unchanged.
This is fixture-specific conservative baking, not a universal clearance bound.
The failed full run demonstrated a 0.294811 m funnel-to-wall distance for a
0.4 m body; its tile matched pinned upstream Recast exactly. Keep that failure
and the negative preflight regression. This radius margin is not applied to mixed
fixtures. Optional `BodyProfiles` maps physical dimensions by profile ID;
legacy Version1 fixtures fall back to baked dimensions only when body metadata
is absent and `BakeClearanceMargin` is zero. Manifests and runtime configuration
record both sets of dimensions and the explicit margin.

Terrain uses `MaxSimplificationError=0.5` voxels; the other families retain 1.3.
The bounded static-body diagnostic found two original terrain routes with
4.715 mm penetration at 1.3. The single 0.5 candidate preserved all 98 initial
cohort routes as Complete and cleared every checked segment, with minimum excess
clearance of 0.130330 m for human and 0.302003 m for large bodies. Source geometry,
anchors, grid, body dimensions and bake radii are unchanged. These checks cover
initial assignments, not every later destination or geometry edit. The authored
1.1 m gaps block both profiles in both presets; do not claim human-pass/large-block
coverage from their presence.

The long corridor explicitly uses 4,096 query nodes, 2,048 path polygons and
512 corners in both preflight and server configuration. The ordinary 512-polygon
server default correctly returned `Partial/ResourceExhausted` for these routes;
mixed workloads retain their existing defaults. This bounded input-size setting
does not change runtime defaults or acceptance thresholds.
Starts and goals are inset from walls; agents share the two endpoints and may
overlap because the motor resolves static exclusion walls only, without
agent-agent collision or avoidance. Startup burst latency is recorded separately;
the warmed path-latency gate is not applicable because there are no
measured-window path requests. Every startup route must be admitted before
measurement. The mixed-workload path-latency gate is unchanged.

The test motor sweeps the physical body's circular footprint against box faces and
rounded corner clearance, intersecting contact with the body's vertical motion
interval. Existing penetration may move strictly toward its nearest escape
boundary. Overlap and host ejection use the same circular footprint. This is
static collision policy, not a complete rigid-body or sliding contact solver.
Earlier capacity evidence used square-expanded corner bounds and falsely
blocked the large circular body on a clear corner; preserve that failed run.
The correction does not change route geometry, speed, goals, runtime navigation
or its recorded long-corridor reconciliation cost.

Post-reconciliation terminal transitions retain the first 64 Blocked records
with mode, code, reason, error, generation, profile, body position and destination,
plus an omitted count. Repeated ticks in the same mode do not append records.
Blocked outcomes remain separate from Failed/Unsupported gates; historical
blocked causes without these records must be described as inferences.

`Capacity` records a minimum 727 seconds including the live drain and restart
probe, plus build/bake and capture overhead; unsettled drainage can add at most
25 seconds. `Rehearsal -RehearsalFamily capacity` (Linux `--family capacity`)
is only a shorter diagnostic. It cannot substitute for the full duration.
The capacity gate requires all configured agents to have net displacement above
`0.00001` metres in every complete measured tick, excluding host ejection/fall
motion. Positions are sampled at the same tick's start and final reconciliation;
ticks crossing a measurement boundary are excluded from displacement statistics.
Reports retain minimum/mean movers and stationary counts by mode separately
from Following/Traversing counts. Historical reports without these fields do
not establish simultaneous physical movement.

Inspect a staged candidate's archive metadata, licenses and canonical fixture
bytes with `python -B qualification/inspect_archives.py --lane <candidate> --out
<validation-root>/runs/<new-id>/archives.json`. This rejects LFS pointers in
canonical inputs and records every private proxy archive hash; it does not
publish or qualify resolution of released versions.

Read `BaselineLive` and `Drain.Live` alongside heap values. Both snapshots must
retain every configured entity and navigation attachment. After measured churn
stops, the running server waits at least five seconds, extending to a recorded
30-second timeout if necessary. Two consecutive tick ends must show no source
edit/post, path/build queue, worker, completion, delayed build, candidate or retired
generation; desired and committed revisions must match. Permanently quarantined
intents for unloaded columns may remain and are counted separately. Forced GC
precedes the heap reads, and the unchanged envelope is baseline plus 25 percent
plus 16 MiB. Timeout or missing live attachments fails qualification. The
execution manifest records minimum planned duration and the maximum possible
drain extension; tick/capture and bake/build overhead are additional.

`PostShutdownHeap` is diagnostic only. Earlier candidates captured `DrainHeap`
after cancellation released navigation leases and scratch; those historical
values do **not** qualify live-server retained-memory drainage. Preserve them
unchanged. `AllocationWindow` records actual counter-read brackets and the final
partial interval so post-measurement drain allocations are not silently counted
as measured allocations. Its closing-read delay identifies tick-boundary sampling
limits rather than pretending counters were read retrospectively at a deadline.

Do not run fuzzing, other benchmarks or builds during final timing runs. Keep
failed observations; fix the source, capture a new candidate and rerun affected
gates. A short smoke is never a substitute for the required durations.

Use `golem.NavigationNow` for navigation intervals and paired request/admission
timestamps, including the full-tick duration. On Windows it uses the
high-resolution performance counter. Subtract readings from that clock directly;
never mix them with `time.Since`, `time.Until` or ordinary `time.Now` intervals.
Keep civil timestamps separate. Runtime GC wall-time correlation is approximate.
Older Windows measurements using the coarse ordinary clock are historical
evidence and are not directly comparable to these intervals. Record the exact
clock/source candidate; no earlier rehearsal establishes the current gates.

Run ordinary tests, vet and checkptr in the library, root engine, shared nav,
adapter, navigation-server and navigation-stress modules explicitly. Nested
modules are not included by the root `./...`. Qualify declared floors separately:
library Go 1.20.14, shared nav/adapter Go 1.25.0, engine/consumers Go 1.25.4, and
current Go 1.26.4. Record compilation platform separately from execution platform
when Windows Go cross-compiles executables run natively through Ubuntu/WSL.

Run race checks as a separate lane. Run the pinned C++ oracle against the expanded
geometry corpus; production runtime and baking remain cgo-disabled. Fuzz every
listed target for ten minutes with bounded workers and retain failure inputs.

`pwsh -NoProfile -File qualification/Run-UnityQualification.ps1 -Lane <candidate>` creates an isolated
Unity 6 authoring project, runs portable and real Editor Scribe tests, and feeds
actual exported JSON to the Go baker. Keep the XML/TRX, source output and artifact
hashes. A successful Unity 6 run does not qualify Unity 2021.3.

The final evidence index must name each candidate, executable and input hash,
exact command/environment, result, excluded check, and platform. Native ARM64,
Unity 2021.3 and hosted execution remain pending until actually executed.

## Later publication, after separate authorization

1. Select and review the exact qualified source commits. Confirm the navmesh-go
   repository destination and visibility; this preparation creates no remote.
2. Publish navmesh-go `v0.1.0` and shared nav `golem/nav/v0.4.0`. They are independent
   prerequisites. Verify the tagged module archives contain canonical fixtures
   as ordinary bytes rather than LFS pointers.
3. In a clean adapter checkout, remove all candidate proxy/override settings.
   Run `GOWORK=off go mod tidy`, tests and vet against those genuinely published
   versions. Commit the resulting real checksums, then publish
   `golem/nav/navmesh/v0.1.0`.
4. Resolve the published shared nav from a clean root checkout. Run ordinary
   tidy/tests/vet, retain real checksums, then publish engine root `v0.4.0`.
5. Repeat standalone navigation-server and navigation-stress checks with
   `GOWORK=off`, no local replacements, a fresh module cache and the published
   proxy. Capture `go list -m -json all`, module sums and smoke results. Update
   documentation's installation guidance only after this resolution succeeds.

Candidate archives and their checksums establish reproducibility of local source
qualification. They never substitute for steps 2–5 or justify publication claims.
