# Navigation qualification — completed local scope with measured limits

Status finalized on 2026-11-12. Implementation and the requested local
**1,000-agent-only** qualification are complete. This does not mean every
original performance target passed. The selected candidate is **181226**, whose Go and
module sources are identical to 173226; only the reduced workload schedule and
documentation changed. The approved capacity-clearance and terrain-accuracy
fixture corrections remain in place. The 173226 candidate's 16 world-generator
gates and four ordinary runner checks pass; all 16 runner modes also passed on
172538 with identical runner sources. The earlier 162949 core-library/adapter
matrices and bounded 39-case C++ oracle remain attributed through source identity,
as do earlier engine and Unity 6 checks. Platform coverage includes Windows and
native Linux at current and documented minimum Go versions; Linux root coverage
is the explicit server package set, excluding Ebiten graphics.

This is an unpublished candidate, **not a general capacity guarantee**. The
corrected 173226 full static capacity
runs record all 1,000 actors physically moving on every measured tick on both
Windows and native Linux: 12,000 and 11,999 ticks over 600 seconds, respectively.
Navigation p99 remains above the target, and both runs have full-tick overruns.
The three 1,000-agent mixed cases and two-hour construction/streaming soak have
completed on both Windows and native Linux, with all required correctness,
path and memory policy gates passing. Their timing, physical-movement and
wall-cadence limits are recorded below. The user excluded future
100-agent and 5,000-agent tiers from this pass; they are neither failed nor
pending work. Earlier evidence and failed runs remain preserved below.
The recorded 5 ms navigation p99 target remains **unmet for both static Capacity
runs and the three Windows mixed cases**. Both soaks and the three Linux mixed
cases meet it within their recorded activity mixes. The user waived that target
as a completion blocker; raw failures remain in the evidence. Publication,
published dependency resolution, native ARM64, Unity 2021.3 and hosted execution
are not claimed. The [final evidence manifest][finalmanifest] indexes the exact
candidate, inputs, executables, commands, results and exclusions.
Follow [the qualification and release procedure](RELEASE.md).

## Completed mixed suites and two-hour soaks

The reduced Windows Full run on 181226 completed all three mixed cases and the
construction/streaming soak. Each mixed case used two minutes warm-up and ten
measured minutes at 1,000 agents. All correctness, path and memory policy gates
pass. The first three cases still fail the raw 5 ms navigation p99 target;
the soak passes that target within its recorded activity mix.

| Windows workload | Measured ticks / duration / average rate | Exact navigation p99 | Exact admitted-path p99 | Whole-tick observation |
| --- | --- | --- | --- | --- |
| [Terrain][fullwindowsterrain] | 11,979 / 600 s / 19.965 Hz | **5.7845 ms — target unmet** | 99.6447 ms | One 1,104.7808 ms whole-tick stall; maximum navigation phase 16.1638 ms. |
| [Multilevel][fullwindowsmultilevel] | 12,000 / 600 s / 20 Hz | **6.4651 ms — target unmet** | 99.7179 ms | No full-tick overruns. |
| [Construction][fullwindowsconstruction] | 12,000 / 600 s / 20 Hz | **5.4787 ms — target unmet** | 99.9610 ms | No full-tick overruns. |
| [Construction/streaming soak][fullwindowssoak] | 144,000 / 7,200 s / 20 Hz | **4.7033 ms — target met** | 99.9506 ms | One overrun, 56.1911 ms. |

The cause of terrain's whole-tick stall is unknown. Its much smaller maximum
navigation phase does not establish a cause elsewhere, and this run does not
demonstrate sustained 20 Hz or perfectly regular 50 ms intervals. Multilevel and
construction recorded no Blocked, Failed, Unsupported or motor-blocked outcomes.

The soak submitted 16,800 edits and committed 28,379 builds while keeping at most
three managed generations. The [completed-build review][fullwindowsbuildreview]
classifies all 14 terminal build failures as `dynamic: candidate superseded`.
Two pending Busy observations recovered; no Busy terminal failures occurred.
Four motor contact clamps were recorded, with no persistent Blocked outcomes;
their individual causes were not captured. All 430 falls had corresponding
landings, with no host recovery failures. Post-GC live heap changed from
30.408 to 32.521 decimal MB, and the live drain settled with all 1,000 actors
still attached.

Mean/minimum physical movers during the soak were 376.445/168, and quarantine
accounted for 60.44% of agent-ticks. Its passing 5 ms navigation p99 therefore
describes this mixed workload, **not 1,000 actors moving continuously**. The
separate static capacity cases below provide the all-mover evidence and retain
their much higher p99 values.

[Windows post-runtime verification][fullwindowsintegrity] confirms all 968 staged
source files and four candidate archives unchanged.

The native Linux Full run completed the same reduced schedule on 181226. All
four measured navigation p99 values are below 5 ms; each case nevertheless has
whole-tick overruns and a measured tick count below the nominal 20 Hz schedule.

| Native Linux workload | Measured ticks / duration / average rate | Exact navigation p99 | Exact admitted-path p99 | Full-tick overruns / maximum |
| --- | --- | --- | --- | --- |
| [Terrain][fulllinuxterrain] | 11,998 / 600 s / 19.996667 Hz | 4.396460 ms | 95.774545 ms | 21 / 93.622566 ms |
| [Multilevel][fulllinuxmultilevel] | 11,996 / 600 s / 19.993333 Hz | 4.898536 ms | 95.755216 ms | 35 / 107.521391 ms |
| [Construction][fulllinuxconstruction] | 11,996 / 600 s / 19.993333 Hz | 4.265516 ms | 98.142118 ms | 32 / 144.830641 ms |
| [Construction/streaming soak][fulllinuxsoak] | 143,934 / 7,200 s / 19.990833 Hz | 3.829215 ms | 98.309301 ms | 226 / 373.994093 ms |

The admitted-path figures measure request-to-admission outside observed quarantine,
not all outcomes or work time alone. The [completed timing review][timingcompletion]
retains exact samples, separately recorded failed/cancelled/superseded outcomes,
policy results, inputs, executable identities and all ten completed cases.

The Linux soak submitted 16,800 edits and committed 28,560 builds, with no build
or Busy failures and at most three managed generations. It recorded no Blocked,
Failed, Unsupported or host recovery failures. Four motor contact clamps had no
persistent Blocked outcome; their individual causes were not captured. All 389
falls had corresponding landings. Post-GC live heap changed from 30.493 to
32.545 decimal MB, and the live drain settled with all 1,000 actors attached.
Mean/minimum physical movers were 368.514/155, with 61.20% of agent-ticks
quarantined. As on Windows, this soak qualifies its observed mixed activity,
not continuous movement by every actor. [Linux post-runtime verification][fulllinuxintegrity]
confirms all 968 staged source files and four candidate archives unchanged.

Passing navigation-phase timing or scheduling each unaffected agent on every
processed tick does not erase the wall-clock deficits and overruns above.
These results do not establish perfect 50 ms cadence. Unknown causes remain
unknown; no single subsystem is blamed for the uncaptured whole-tick stalls.
The [rejected initial affinity launch][rejectedaffinity] is also preserved: its
dedicated-process guard stopped it before workload execution. The corrected
launch used the same candidate/settings; the rejected attempt is not a timing run.

## Full static capacity on Windows and native Linux

The [Windows analysis][capacitywindowspass] and [summary][capacitywindowssummary],
followed by the [native Linux analysis][capacitylinuxpass] and
[summary][capacitylinuxsummary], each record two minutes warm-up and ten measured
minutes on 173226. All 1,000 startup routes were admitted in each run. All 1,000
actors then moved on every complete measured tick, with no stationary ticks,
Blocked, Failed, Unsupported or motor-blocked outcomes. Each run used only the
1,000 startup path jobs, with no retries or arrivals during measurement. Windows
provided the first full-duration all-mover proof; Linux subsequently confirmed
that physical-movement result on its native execution platform.

| Recorded metric | Windows/amd64 | Native Linux/amd64 |
| --- | ---: | ---: |
| Measured ticks / duration / average rate | 12,000 / 600 s / 20 Hz | 11,999 / 600 s / 19.998333 Hz |
| Minimum / mean physical movers | 1,000 / 1,000 | 1,000 / 1,000 |
| Navigation median / exact p99 | 3.3896 / **30.5424 ms — target unmet** | 2.822537 / **32.659239 ms — target unmet** |
| Full-tick overruns / maximum full tick | 8 / 67.371 ms | 24 / 100.013660 ms |
| Post-GC live heap, baseline → drained, decimal MB | 73.616288 → 66.282200 | 73.574640 → 66.228320 |
| Process allocations over the recorded window, decimal GB | 3.433939752 | 3.464670104 |
| Live drain time | 5.0046576 s; settled | 5.01440833 s; settled |

Windows averaged 20 Hz; Linux recorded one fewer tick than the nominal 12,000.
Neither result establishes perfectly regular 50 ms intervals. All 1,000 actors
remained attached and Following at each live drain,
with no retired generations or outstanding candidates. The memory envelope and
all required policy gates pass. The raw 5 ms navigation p99 failure is retained
under the user's waiver; warmed path latency is not applicable to this static
startup-only workload, and scratch allocation remains separately qualified.

[Windows][capacitywindowsintegrity] and [Linux post-runtime verification][capacitylinuxintegrity]
confirm all 968 staged source files and four candidate archives unchanged.
The Windows launcher restored its affinity and environment. This is full-duration physical-movement evidence for
this fixed-world workload with overlapping actors and no agent-agent avoidance.
It does not itself qualify destination/geometry churn, mixed workloads or the
two-hour soaks; those have their own completed results above.

## Selected candidate and fixture checks

The selected reduced-schedule candidate `20261111T181226.520768Z` has manifest
SHA-256 `9fd8fb0483c55f38bbfc45c85898a7d8e6280abe6889d4dc9a02b467f82aabd4`.
Its [stage identity review][reducedidentity] and
[Go/module comparison][reducedgoidentity] establish that all Go and module
bytes are identical to the qualified fixture candidate 173226. The change is
limited to documentation and workload launchers; prior executions retain their
original candidate identities.

Fixture candidate `20261111T173226.584096Z` has manifest SHA-256
`df3892f5200e5295e1fa77915dbd2478efe4fcb3a5c385e9b4caaa5850425a46`.
Capacity retains physical body radii 0.4/0.8 m while using bake radii 0.9/1.3 m.
The fixture records `BodyProfiles` separately and resolves them by profile ID;
positive bake margins cannot silently fall back to enlarged physical bodies.
Preflight checks continuous physical-cylinder clearance against the same static
exclusion boxes, with a regression that rejects the unsafe legacy fixture.
This changes fixture validity and validation; motor size, speed, goals and the
production navigation implementation remain unchanged.

| Evidence | Recorded result |
| --- | --- |
| [172538 runner matrix][marginrunnermatrix] | All 16 ordinary/vet/checkptr/race gates pass on Windows/native Linux, Go 1.26.4 and 1.25.4. |
| [173226 ordinary runner checks][terrainrunnermatrix] | All four platform/toolchain combinations pass at CGO=0. |
| [173226 world-generator matrix][terrainworldgenmatrix] | All 16 gates pass on Windows/native Linux, Go 1.26.4 and library minimum 1.20.14. |
| [Runner identity mapping][terrainrunneridentity] | Runner bytes are identical between 172538 and 173226; earlier diagnostic execution remains attributed to 172538. |
| [Core identity mapping][marginidentity] and [terrain-only delta][terrainidentity] | Runtime core matches 162949 apart from a comment-only Profile clarification; later code changes are confined to fixture generation/tests. |

All linked matrix lanes retain clean before/after source/archive receipts and
test executables. Earlier production-engine, shared-nav and adapter results stay
at their original candidate identities; the fixture changes do not relabel old
executions as new ones.

The [bounded initial-route review][initialclearance] first found two of 98
Complete terrain routes clipping passage walls by about 4.715292 mm. One
[terrain-only simplification change][terrainaccuracy], 1.3 to 0.5 voxels, then
produced 98/98 Complete routes with no physical-clearance violations. Minimum
excess clearance is +0.130330 m for human and +0.302003 m for large. Geometry,
body radii and grid size are unchanged; the capacity-only radius margin remains
separate. The [diagnostic receipt][terrainaccuracyreceipt] retains exact inputs,
artifacts and executable identity. Multilevel's 100 and construction's 102
initial route pairs were also Complete and clear in the bounded review. These
initial-route checks do not cover later destination or geometry churn.

**Gap coverage limit:** all 17 authored 1.1 m gap centerlines block both profiles
under both the original and corrected terrain presets. No human-pass/large-block
behavior was established. The preset preserves that limitation; neither these
checks nor feature presence qualify differentiated passage clearance.

The [173226 archive inspection][archivesselected] passes all four candidate
modules and 17 ordinary-byte canonical fixtures, with clean before/after source
checks. The [181226 archive inspection][archives181226] repeats those checks
successfully for the reduced-schedule candidate. The [website build][websitelatest]
passes 46 pages, including the
generic advice to account for voxel/contour error and validate routes against
actual motor collision. No fixed margin is promised for arbitrary worlds.
Full Windows and native Linux capacity have passed the required policy gates
as recorded above. Both mixed suites and soaks are complete on the
source-identical reduced-schedule candidate, subject to the stated measured limits.

## Full Windows capacity failed physical clearance

The [full 162949 capacity run][fullcapacityfailure] completed its two-minute
warm-up and ten-minute measurement, then failed the required per-tick physical
movement gate. [First-motion-loss evidence][fullcapacitymotionloss] places the
first loss near tick 2,382, about 119 seconds after movement began. All 500
human-profile actors stopped at the same corner; the other 500 continued.
The measured minimum/mean movers were 500/500.473, with no quarantine or host
recovery motion. There were 500 Blocked transitions: 64 captured and 436 omitted,
with captured errors reporting exhaustion of bounded stalled-route replanning.

The [physical replay][humanphysicaldiagnostic] and
[pinned native comparison][humanclearanceverification] establish insufficient
clearance in the route itself. Its minimum clearance is 0.294811 m against a
0.4 m physical body radius. All six coarse/detail output comparisons match the
pinned C++ implementation, and the retained tile is reproduced exactly in the
[oracle results][humanclearanceoracle]. Setting contour simplification to zero
still leaves only 0.390512 m clearance, below the same body radius. The motor
correctly blocks the body; this captured failure is not a port or motor defect.

The raw failed-run navigation p99 is 49.2456 ms, with 11,971 measured ticks,
19.9517 Hz actual cadence and 114 full-tick overruns. Most of that measurement
contains only 500 moving actors, so those timings are **not 1,000-mover capacity
measurements**. Startup admission, generation bounds and live drain passed,
but they do not override the failed movement requirement. All 1,000 actors
remained attached at drain, including the 500 Blocked actors. Source/archive
verification remained clean, and the required-failure policy stopped progression.

The [single margin diagnostic][capacitymargindiagnostic] passes with physical
radii unchanged at 0.4/0.8 m and bake radii 0.9/1.3 m: two additional 0.25 m
cells, or 0.5 m, for this capacity fixture only. All four profile/direction
routes are Complete and exceed the required 3,268 m. Minimum clearance beyond
the physical radius is +0.44 m for human and +0.395010879 m for large. The
[diagnostic receipt][capacitymarginreceipt] pins its input, executable and commands.
Obstacles, motor collision and query limits remain unchanged.

This policy is approved and integrated for the capacity fixture, with bake and
physical radii recorded separately. Its margin does not apply to mixed scenes;
the later terrain-only contour preset is described above. Schema integration
and continuous motor-collision preflight passed the affected checks. This
diagnostic is fixture-validity evidence; the separate full Windows capacity
result is recorded above. Complete path queries alone missed the original mismatch, and
the selected two-cell margin is not a universal guarantee across worlds or settings.

## Latest correction: captured ramp stall reproduced and fixed

Candidate `20261111T162949.004814Z` combines a library ground-endpoint height
correction with the captured real-server regression. Before the correction,
the selected start support was Y=8.196059 while the first ground route corner
at the same XZ was Y=7.955913. The mismatched height made the actual server
stall and exhaust bounded replanning. The correction preserves selected
ground endpoint heights without changing intermediate corners, split traversal
anchors or truncated/partial boundaries.

The [same real-server regression][rampproof] fails against 160444 after
2.46 seconds with zero displacement and Blocked after three replans, then
passes on 162817 in 0.94 seconds with more than 1 m physical progress and
no step larger than 0.3 m. The captured start/goal, two-building 32-tile geometry,
20 Hz tick, 4 m/s speed, acceleration 12, support tolerance 0.2, existing circular
motor and default runtime query limits remain fixed. [Library regressions][endpointgreen]
also pass for captured, negative and large origins, partial boundaries and
split traversal anchors. The [original failing library evidence][endpointred]
is preserved.

These are correctness results, not a mixed-workload qualification pass.
The combined 162949 candidate now passes the affected adapter and stress-runner
ordinary/vet/checkptr/race checks on Windows/native Linux, each at current and
exact minimum Go versions, as recorded below. The obsolete 160444 remainder was
never started. The corrected short multilevel result follows; subsequent
full-duration evidence is recorded above.

## Corrected multilevel short rehearsal

The [162949 multilevel analysis][multilevelcorrected] and
[summary][multilevelcorrectedsummary] cover 10 seconds warm-up and 60 measured
seconds with 1,000 actors. The captured ramp stall did not recur: Blocked,
Failed, Unsupported and motor-blocked counts are all zero.

| Recorded metric | Result |
| --- | ---: |
| Navigation p50 / exact p99 | 3.9586 / **7.4922 ms — target unmet** |
| Successful request-to-admission exact p99 outside observed quarantine | 99.7983 ms — pass |
| Measured ticks / actual tick rate | 1,199 / 19.9833 Hz |
| Full-tick overruns / maximum full tick | 0 / 21.5046 ms |
| Minimum / mean actors with actual displacement | 505 / 616.943 |
| Post-GC live heap, baseline → settled drain, decimal MB | 38.922 → 39.947 — pass |
| Maximum managed / retired generations | 3 / 2 |

All 1,000 actors were scheduled on each measured tick; the separate displacement
counts do not imply all moved continuously. Both live heap samples retained all
1,000 registered, navigation-attached actors, and the running server settled
before the drain sample. The run submitted 140 measured edits and recorded 235
commits. One build terminal failure was an explicitly superseded candidate;
it remains counted rather than being erased. [Post-runtime integrity][multilevelcorrectedintegrity]
verifies all 963 staged files and four candidate archives unchanged.

Timing caveat: another agent reported roughly ten seconds of filesystem
instruction inventory that may have overlapped this rehearsal. It was not
fuzzing or a benchmark, but this result is not presented as a proven uncontended
timing run; its effect is unknown. Full-duration runs are kept quiet. This short
result establishes neither full multilevel duration nor soak qualification.

## Preserved 160444 short runs: capacity movement passes, multilevel stalls

Both runs use candidate `20261111T160444.312786Z`, with 10 seconds warm-up and
60 measured seconds. The test motor now sweeps a circular footprint against
exclusion boxes, correcting the earlier square-expanded-box false positive.
The runner also retains bounded post-admission Blocked evidence. Runtime,
fixtures, goals and requested speed were not changed for this correction.

| Recorded result | Static capacity | Mixed multilevel |
| --- | ---: | ---: |
| Navigation p50 / p99 | 2.6258 / **79.4949 ms — target unmet** | 3.1957 / **6.3911 ms — target unmet** |
| Measured ticks / actual rate | 1,197 / 19.95 Hz | 1,200 / 20 Hz |
| Full-tick overruns | 29 | 0 |
| Minimum / mean actors with actual displacement | **1,000 / 1,000** | 457 / 583.207 |
| Post-admission Blocked transitions | 0 | 179 |

The [capacity analysis][capacitycorrected] and [summary][capacitycorrectedsummary]
record 1,197,000 moving agent-ticks, with no Blocked actors, motor-blocked moves
or retries. This is direct displacement evidence for every complete measured
tick, rather than an inference from scheduled or Following states. It remains
a short static case with routes near 4 km, two synchronized starts, 4 m/s requested
speed and no agent-agent collision or avoidance. There are no measured path
requests after startup, so its mixed-workload path-latency gate is not applicable.
The 79.4949 ms p99 and 29 overruns remain workload-specific performance limits;
this result neither establishes a universal 1,000-agent cost nor qualifies the
long capacity workload or soak.

The [multilevel analysis][multilevelcurrent] records 20 Hz without full-tick
overruns. Its successful request-latency p99 is bounded above by 99.7 ms in the
[summary histogram][multilevelcurrentsummary]. However, 179 post-admission
Blocked transitions occurred: 64 observations were retained and 115 omitted
by the evidence bound. Every retained observation is a human-profile actor
reaching the bounded-replan stall limit on a ramp near Y=8.1; 80 actors remain
Blocked at drain. The captured endpoint defect above is now reproduced and
fixed, but this measured run remains **not qualified**, regardless of the
recorded policy-gate booleans. Complete planned
routes and the earlier connectivity checks did not establish successful actual
movement through these ramps.

Both [capacity][capacitycorrectedintegrity] and
[multilevel post-runtime checks][multilevelcurrentintegrity] verify all 960
staged source files and candidate archives unchanged. The [motor regression
receipt][motorcorrection] records full Windows current ordinary/vet passes and
focused checkptr coverage. Its remaining 14 platform gates were held and are
now superseded by the affected 162949 checks. No full workload suite or
two-hour soak has started.

## Preserved first capacity failure

The [152132 capacity rehearsal][capacityfailed] used 1,000 actors on routes near
4 km long, synchronized from two starts, with only 10 seconds warm-up and
60 measured seconds. It does not qualify sustained capacity or establish a
universal cost for 1,000 agents.

| Recorded metric | Result |
| --- | ---: |
| Navigation p50 / p99 / maximum | 3.9839 / 121.6565 / 131.674 ms |
| Reconcile p99 / Movement p99 | 117.8895 / 4.3991 ms |
| Actual tick rate / measured ticks | 19.5833 Hz / 1,175, versus configured 20 Hz |
| Full-tick overruns / maximum full tick | 53 / 132.0874 ms |
| Minimum / mean actors with actual displacement | 500 / 926.523 |

The [failure analysis][capacityfailureanalysis] shows all 500 large-profile
actors stopped at their first turn, without quarantine or host ejection/fall
motion. An [offline corner replay][capacitycorner] returned Complete for its
navigation queries. Its proposed circular-footprint movement has 1.072445 m
clearance from the actual box, greater than the 0.8 m agent radius, while the
test motor's square-expanded box rejects it. The [probe receipt][capacitycornerreceipt]
pins the artifact, source and executable. This is a test-motor collision
false positive; the earlier unit/platform passes did not cover this corner.
The 160444 motor correction and short rerun above address this false positive.

Large reconciliation spikes already occurred while all 1,000 actors were still
moving, before the later blocking. They are a separate observed performance
limit of this synchronized long-route workload; correcting the motor does not
establish that those spikes are resolved. Final post-admission Blocked errors
were not captured individually in this run. An Unreachable terminal reason is
inferred from runtime code and path-job counts, not directly observed evidence.

[Post-runtime verification][capacityintegrity] confirms all 955 staged source
files and candidate archives remained unchanged. This failed result remains
preserved alongside the later corrected short and full-duration results above.

## Current correctness evidence

Candidate `20261111T162949.004814Z` has manifest SHA-256
`9de19c5e9672ea5d561f6908b1236ffdbf73c34e2fa23dac3194e48c58c62da6`.
The [endpoint qualification index][endpointqualification] records all 16 library
ordinary/vet/checkptr/race gates passing on Windows and native Linux, Go 1.26.4
and minimum 1.20.14, plus all 39 expanded oracle cases. The focused C++ evidence
also distinguishes successful nearest/detail projection from the coarse edge
height; its exact-edge `GetPolyHeight` failure is preserved rather than treated
as a successful height result.

The [affected engine matrix][endpointenginematrix] passes all 32 adapter and
stress-runner gates on the same candidate: both platforms, all four modes,
current Go 1.26.4, adapter minimum 1.25.0 and runner minimum 1.25.4. All six
serialized lane receipts have clean before/after source/archive checks; 36
retained package-test executions pass and their executables are preserved.
The staged input contains 963 source files and four candidate archives.
Ordinary checks use CGO=0; race uses CGO=1 separately. Diagnostic checks skip
only named allocation-assertion children while ordinary checks retain them.

The [extended identity receipt][identitycurrent] verifies all eight recorded
stages from 142257 through 162949. Production code is unchanged through 160444;
the final transition changes only `navmesh-go/navmesh.go` in production, plus
the endpoint regression/fixture and real-server regression. Root runtime,
shared nav and adapter source remain unchanged. Earlier broad checks below
retain their original candidate attribution rather than being relabeled as
new executions.

Production-module qualification uses immutable candidate
`20261111T144303.392360Z` (manifest SHA-256
`1f5232c2a92f5a019779b60075aeac4275ea9aa7d90806fc0f6fb88a7bb00e01`).
The [library platform summary][libraryfinal] records all 16 current/minimum
Windows/Linux ordinary, vet, checkptr and race gates passing. One original
Windows checkptr run failed before execution because the retained-test helper
compared two spellings of the same Windows path; its corrected retry passed,
with the failed record preserved.
All 64 production-engine gates pass on 144303: root, shared nav, adapter and
navigation-server demo, each with ordinary/vet/checkptr/race on both platforms
and current/exact-minimum Go versions. Every lane's before/after source/archive
checks are clean, with exact commands, compiler identities and retained test
executables in the linked records.

| Platform | Current Go 1.26.4 | Exact minimum Go versions |
| --- | --- | --- |
| Windows | [All four production targets pass][enginewincurrent] | [Shared nav/adapter 1.25.0 pass][enginewinnavmin]; [root/demo 1.25.4 pass][enginewinmin] |
| Native Linux | [All four production targets pass][enginelinuxcurrent] | [Shared nav/adapter 1.25.0 pass][enginelinuxnavmin]; [root/demo 1.25.4 pass][enginelinuxmin] |

Windows root checks use `./...`. Linux root checks use the explicit server
package set, excluding Ebiten client graphics and its cgo/system dependencies.
Some linked run-level statuses remain failed because they also preserve the
superseded 144303 runner's allocation-only checkptr failure; all 64 production
target records themselves pass. Ordinary production checks remain CGO=0; race
checks are separate CGO=1 diagnostics.
The [expanded pinned C++ oracle][oraclefinal] passed all 39 cases on 144303
with ordinary Go execution at CGO=0. This is a bounded comparison corpus,
not proof of equivalence for all possible geometry.

[Bounded fuzzing][fuzzfinal] is complete on 145312, Go 1.26.4 and CGO=0:
all eight Windows targets passed their 600-second runs. Seven Linux targets
passed initially; the original `FuzzDecodeNavMeshData` run failed at 600.12 s
with `context deadline exceeded`, without a saved failing input or observed
decoder panic/assertion. That failure is preserved. The exact executable then
passed replay of 66 cached inputs plus three built-in seeds, followed by one
authorized 600-second retry: native exit 0 after 602.11 s, 74,412,098 reported
executions, and no reproducer. A coordinator shutdown/deadline interaction is
an unproven explanation, not an established cause. Execution counts are not
distinct cases, and these bounded runs are not exhaustive verification.
The [162949 fuzz-scope review][endpointfuzzscope] verifies all eight target
source files unchanged and finds none reaches the changed path endpoint code.
No redundant fuzz run was launched. This is a source-reachability assessment,
not new fuzz coverage of the endpoint correction.

Prior corrected-runner checks use candidate `20261111T145312.915367Z`
(manifest SHA-256
`53c08caa3e52639f7d67556b43f4583c0a50dcf967dd7643efc1f8cff3fb6418`).
Relative to 144303, changes are limited to the workload runner/tests, the
multilevel generator/tests and qualification tooling. Runtime, shared nav,
adapter and core library production files are unchanged. Further displacement
metrics or capacity workloads require their own staged checks; they are not
covered by these earlier candidate identities.

All 16 runner checks on 145312 pass: ordinary, vet, checkptr and race on
[Windows current][runnerwincurrent], [Windows minimum][runnerwinmin],
[Linux current][runnerlinuxcurrent] and [Linux minimum][runnerlinuxmin].
Current is Go 1.26.4; this runner's minimum is 1.25.4. Every lane has clean
before/after source/archive checks and retained executables. Original 144303
runner checkptr failures are preserved: an embedded allocation assertion counted
instrumentation overhead. The 145312 test separates that assertion into a named
diagnostic-excluded child, retains all geometry checks, and still enforces the
allocation assertion in ordinary tests.

The previously matrix-qualified capacity/displacement runner is candidate `20261111T150930.846936Z`
(manifest SHA-256
`0d46700b5cd0eebfc24bee980f0ebf95fce07a0d52cb6b5b68c61ba2f7fdf5df`).
All 16 affected runner checks also pass on this candidate: [Windows current][finalrunnerwincurrent],
[Windows minimum][finalrunnerwinmin], [Linux current][finalrunnerlinuxcurrent]
and [Linux minimum][finalrunnerlinuxmin], each ordinary/vet/checkptr/race with
clean before/after integrity checks. The capacity fixture uses explicitly
bounded 4,096-node/2,048-polygon/512-corner limits and 4 m/s requested speed;
mixed-world defaults are unchanged. These checks do not qualify the later
160444 motor changes; the short displacement evidence above comes from its
separate measured run.

The [earlier categorized source identity receipt][identity] verifies actual files and
archives across 142257 → 144303 → 145312 → 150506 → 150930 → 152132. Core library,
runtime, shared nav, adapter and dependency metadata are unchanged throughout.
Every change has its old/new SHA-256 and category recorded. The final
`20261111T152132.296092Z` stage changes only documentation and timing tooling
from 150930; it preserves the qualified Go/module/configuration inputs. The
142257 → 144303 transition only adds this report, so historical timing used the
same production code. Later fixture/runner changes still require new workloads.

| Transition | Changed source categories |
| --- | --- |
| 142257 → 144303 | 1 documentation file |
| 144303 → 145312 | 2 fixture, 3 runner/test, 3 tooling files |
| 145312 → 150506 | 4 documentation, 3 fixture, 6 runner/test, 7 tooling files |
| 150506 → 150930 | 1 documentation, 2 runner/test, 1 tooling file |
| 150930 → 152132 | 3 documentation, 5 tooling files |
| 152132 → 160444 | 3 documentation, 7 runner/test, 1 tooling file; production unchanged |
| 160444 → 162949 | 1 core-library production file, 2 documentation, 1 fixture, 1 runner regression, 1 library test |

The last two rows come from the [extended identity receipt][identitycurrent];
the one library production change is the endpoint-height correction described above.

World-generator checks executed on 150506 pass all 16 platform/toolchain modes:
[Windows current][worldgenwincurrent], [Windows minimum][worldgenwinmin],
[Linux current][worldgenlinuxcurrent], and [Linux minimum][worldgenlinuxmin];
[their identity receipt][worldgenidentity] proves the same seven source/test/
configuration files are present in 150930. Test execution remains attributed
to the stage on which it ran.

[Unity authoring qualification][unityfinal] on 145312 passed 65 portable tests
and 169 actual Unity 6 EditMode tests in editor 6000.3.13f1, with no failures or
skips. The emitted source was baked by the pure-Go tool at CGO=0; immutable
source/archive checks passed before and after. This does not qualify Unity 2021.3.

[Candidate archive inspection][archives] on 145312 passed for all four private
module archives: LICENSE and go.mod are present, proxy and archived go.mod bytes
match, workspace files are absent, and all 15 canonical fixture files contain
ordinary data rather than LFS pointers. Proxy hashes and before/after source
checks passed. Published checksums and public dependency resolution remain
unqualified.
The [final 152132 archive inspection][archivesfinal] also passes for all four
archives and all 16 canonical fixture files, including the capacity configuration,
with clean source/archive checks before and after.
The [162949 archive inspection][archivescurrent] passes for all four current
candidate archives and all 17 canonical fixture files, now including the captured
ramp-boundary regression. Proxy/archive metadata, ordinary fixture bytes and
before/after source integrity all pass; published resolution remains pending.

The [website build][website] passed after documenting the unmet timing target,
the difference between scheduled work and actual movement, and the limits of a
static capacity fixture. A documentation build is not workload qualification.

Ordinary checks retain allocation assertions. Diagnostic race/checkptr lanes
skip only named `*Allocations` children and do not claim allocation performance.
Earlier broad Linux attempts retain the Ebiten/cgo graphics limitation and a
repeated transport ACK test failure. A later [focused ACK retry][ack] passed;
the 144303 explicit server matrix above subsequently passed in all four modes
on current and minimum Go. These results do not qualify Linux client graphics
or published dependency resolution.

## Scoped allocation measurements

The [quiet microbenchmark comparison][microbench] passed on Windows/amd64
Go 1.26.4, with [clean post-run integrity][microbenchintegrity]. It compares
baseline `7019eb57f33b1d8fb91289b3836e45f8593d2e2a` with candidate 150506 on
one small fixed fixture, sequential queries, three 500 ms repetitions, CGO=0.

| Operation | Baseline bytes / allocations per operation | Candidate bytes / allocations per operation |
| --- | ---: | ---: |
| Projection | 16,672 / 2 | 0 / 0 |
| Path | 17,680 / 12 | 344 / 4 |
| Move with visited output | 8,608 / 4 | 80 / 1 |
| MoveWithoutVisited | not compared | 0 / 0 |
| RepairPath | not compared | 344 / 4 |
| Query creation | not compared | 1,152 / 2 |

Path/repair results and visited lists retain independent ownership, so their
result allocations remain intentional. Zero-allocation readings describe warmed
temporary storage for the exercised operations, not query creation, initial
scratch growth, all possible geometry, rebuilding or whole-server allocation.
These measurements establish neither multiplayer capacity nor tick-tail latency.

## Completed scope and separate release gates

Versioned [staging](stage.py), [platform checks](platform_matrix.py),
[Windows workloads](Run-Qualification.ps1) and [native Linux workloads](run_linux.py)
retain the commands and ownership rules used for these executions. Their
availability alone is not evidence; the linked run receipts establish the
completed checks and workloads.

On 2026-11-11, the user narrowed future workload execution to **1,000 agents
only**. The 100-agent and 5,000-agent tiers are excluded by that decision, not
failed or pending qualification. Historical results and exact commands remain
unchanged. Static Capacity, all three 12-minute mixed cases and the two-hour
construction/streaming soak are complete on both Windows/amd64 and native
Linux/amd64. No further run is pending within this selected local workload scope.
The 5 ms target failures, wall-cadence deficits, unknown terrain stall cause and
uncaptured individual motor-clamp causes are retained limitations, not silently
passed gates.

Current/minimum ordinary, vet, checkptr and separate race checks, bounded C++
comparison, ten-minute fuzz targets, quiet microbenchmarks, Unity 6 authoring and
candidate archive checks are recorded above at their actual source identities.
The exact floors are library Go 1.20.14, shared nav/adapter 1.25.0 and
engine/consumers 1.25.4; current is 1.26.4. See the
[Linux toolchain receipt][toolchains] and [compiler receipt][compiler]. The
original Linux fuzz deadline failure and single passing retry remain visible.
Earlier failed fixtures, motor behavior and endpoint handling remain historical
evidence with their focused corrections and affected checks.

The [final source audit][finalsourceaudit] verifies all 475 Go/module files
against 181226 and all 17 canonical archive fixtures against current bytes.
Candidate archives include ten new fixture/configuration files that were still
untracked at audit time; any future release commit must include them. The audit
does not create a commit or establish published resolution. Current runners use
external modfiles/checksums and preserve clean source/archive checks before and
after builds and workloads. Earlier direct `-mod=mod` stages remain diagnostic
history, not immutable-candidate evidence.

Native ARM64 execution, Unity 2021.3 and hosted execution remain separate,
unexecuted release gates. Published module resolution, release commits, tags,
pushes and publication have not occurred. Private candidate archives cannot
substitute for those steps. Follow the separate publication procedure only after
authorization and repeat its clean published-dependency checks then.

## Hardware, measurement and coverage boundaries

The [host record][hardware] identifies an Acer Predator PHN16-73 with Intel
Core Ultra 9 275HX, 24 physical/logical processors and about 33.75 GB RAM,
running Windows 11 Home. The [CPU-set record][topology] reports eight higher
performance-class cores and sixteen efficiency-class cores. GOMAXPROCS counts
Go scheduling slots, not performance cores. This topology snapshot does not
establish which cores executed earlier ticks. The controlled 142257 comparison
has separate affinity receipts; its eight-core run still fails the target.

Windows navigation intervals use `NavigationNow`/QPC and paired `Time.Sub`.
Earlier ordinary-clock Windows measurements can quantize short phases to zero
and are not directly comparable. Runtime GC pause durations remain separately
coarse, and wall-time GC-to-tick correlation is approximate. Rebase time is
already included in movement time. Timing must remain uncontended by builds,
fuzzing, profiling or unrelated benchmarks.

The generator has terrain ramps/stairs/narrow passages, multilevel floors and
cave ceilings, and construction blockers, removable bridges, traversal islands
and streaming schedules. Feature presence is not execution coverage. The recorded
terrain runs recorded no traversal starts; they do not qualify the construction
traversal/recovery or multilevel workloads. The fixture motor follows baked
support with swept exclusion-volume collision and source-triangle falling
recovery, not a complete rigid-body character solver. Its 0.2 m support tolerance
is tied to cell height/detail error; the engine default remains 0.05 m. These
synthetic worlds do not establish arbitrary-world, gameplay-door, avoidance,
cross-region routing or production-map capacity.

An agent being scheduled, or reported as Following/Traversing, does not prove
physical displacement. Existing cadence/activity gates count work and states;
they cannot establish how many agents moved, distance travelled, or achieved
speed. Separate displacement evidence is required before claiming that 1,000
agents actually move at the requested speed throughout a capacity workload.

## Historical paired terrain rehearsals

Both runs use immutable candidate `20261111T142257.063281Z`, Windows/amd64
Go 1.26.4, 1,000 actors at 20 Hz, two clearance profiles, four path workers,
two build workers/commits per tick, 10 seconds warm-up and 60 measured seconds.
Destination churn is 10% per second, with two edits per second and a 20-edit
burst. Line 16 of the [all-core launch receipt][latestlaunchall] and
[8P launch receipt][latestlaunch8p] records GOGC=200 with no GOMEMLIMIT or
GOMAXPROCS override. The child manifests report 24 and 8 scheduling slots,
respectively. Both runs identify executable SHA-256
`e616026ee9f53093cd6d0fd774857892055c7950cd9de45519f58ac84c2eb38b`.

| Recorded result | All cores, run 1423472923753Z | Eight performance cores, run 1425283090170Z |
| --- | ---: | ---: |
| Navigation p50 / p99 | 3.3041 / **5.4527 ms — FAIL** | 3.2169 / **6.4369 ms — FAIL** |
| Successful request-to-admission p99 outside observed quarantine | 99.5716 ms — pass | 99.7310 ms — pass |
| Process allocation bytes over the measured read window | 2,254,069,840 | 2,251,466,264 |
| Post-GC live heap, baseline → drained, decimal MB | 31.65 → 33.29 — pass | 31.34 → 32.97 — pass |
| Sampled peak RSS, decimal MB | 132.29 | 127.04 |
| Maximum managed generations / retired generations | 3 / 2 | 3 / 2 |
| Failed / unsupported agent states | 0 / 0 | 0 / 0 |
| Quarantined agent-ticks | 483,949 / 1,200,000 | 489,202 / 1,200,000 |

The [all-core analysis][latestanalysisall] and [8P analysis][latestanalysis8p]
contain exact nearest-rank percentiles; the [all-core summary][latestsummaryall]
and [8P summary][latestsummary8p] retain threshold counts and every gate.
The recorded correctness, cadence, path-latency and live-heap gates pass in
both runs. Navigation latency fails in both; query scratch allocation is not
measured by these workloads. Neither run qualifies 1,000-agent capacity or
continuous movement by every attached actor.

Source/archive checks before and after both builds passed: 945 source files
and four candidate archives remained unchanged. The [all-core verification][latestsourceall]
and [8P verification][latestsource8p] identify source manifest SHA-256
`5f9c0a5a20258b5b8571b942b2cb0e47253a733153a9ffb3f79605d7a2cf5e01`.
The [all-core affinity receipt][latestaffinityall] retains mask `0xFFFFFF`;
the [8P receipt][latestaffinity8p] records mask `0xC03C03` for the isolated
launcher and its children, then successful restoration to `0xFFFFFF`.
This controlled comparison does not establish that performance-core affinity
meets the 5 ms target.

## Additional short diagnostics and fixture correction

Later short runs on the same 142257 candidate recorded:

| Workload / run | Navigation p99 | Path-latency gate | Other limits |
| --- | ---: | --- | --- |
| [Terrain, GOGC=1000/512MiB, 1431469003512Z][gc1000] | 5.4044 ms — target unmet | 99.8031 ms — pass | Sampled peak Go heap about 339 MB; live drain 31.59 → 33.22 MB. |
| [Multilevel, 1434286924192Z][brokenmultilevel] | 11.0319 ms — target unmet | pass | Activity gate failed; the authored fixture was disconnected. |
| [Construction, GOGC=200, 1437095351735Z][constructionrun] | 5.0533 ms — target unmet | 99.7965 ms — pass | Recorded correctness/cadence/live-heap gates pass; traversal starts, completions and cancellations exercised. |

The GOGC=1000 experiment improved the observed terrain p99 by about 0.9% over
the paired GOGC=200 run while increasing sampled peak heap by about 3.5 times.
It was not selected as a capacity preset. These short measurements still do not
substitute for the full suite or soak.

The [multilevel connectivity diagnostic][connectivity] found 52 mutually
reachable anchor components in each profile: one group of 49 ground anchors
and 51 isolated anchors. All 100 initial anchor-offset requests returned
Partial/Unreachable. Ramps ran beneath uncut floors and lost required headroom;
projection success had not established floor-to-floor connectivity. A new
[regression failed on that fixture][connectivityred]. Candidate
`20261111T145312.915367Z` moves the ramp runs outside the floor footprints,
alternates building faces, and adds landings. [Connectivity checks][connectivityfixed]
now require complete bidirectional ground/floor and adjacent-floor routes for
both profiles. The [generator receipt][anchors] confirms all 100 authored
anchors remain unchanged; the corrected generated source SHA-256 is
`9deed4bc26e71458f54d7fc6548f1917fe4be4595733dd0dec055eff16bf073c`.
The [full 256 m connectivity check][connectivityfull] on 150930 confirms one
anchor component per profile, 100/100 initial offset goals Complete and
307/307 diagnostic paths Complete for each profile. The later 160444 measured
run nevertheless exposed real movement stalls; the endpoint correction and
zero-Blocked 162949 short run above follow that failure. Connectivity alone is
insufficient qualification.

## Historical 134803 terrain rehearsals

Both runs use candidate `20261111T134803.223648Z`, Windows/amd64 Go 1.26.4,
1,000 actors at 20 Hz, two clearance profiles, four path workers, two build
workers/commits per tick, 10 seconds warm-up and 60 measured seconds. Destination
churn is 10% per second, with two edits per second and a 20-edit burst.
The child manifests report GOMAXPROCS=24. The terrain start events on line 7 of
each [200 launch receipt][launch200] and [400 launch receipt][launch400] record
the settings below; both identify executable SHA-256
`815e9404d092c4833cf4a7c74ef79ddc2d6fcffc90e47959ad8baec81e31611b`.

| Recorded result | GOGC=200, no GOMEMLIMIT override | GOGC=400, GOMEMLIMIT=512MiB |
| --- | ---: | ---: |
| Navigation p50 / p99 | 3.5723 / **6.1698 ms — FAIL** | 3.5873 / **6.3460 ms — FAIL** |
| Successful request-to-admission p99 outside observed quarantine | 99.7523 ms — pass | 99.5547 ms — pass |
| Process allocation bytes over the measured read window | 2,243,402,720 | 2,247,362,424 |
| Post-GC live heap, baseline → drained, decimal MB | 32.85 → 33.28 — pass | 31.62 → 33.22 — pass |
| Sampled peak RSS, decimal MB | 118.91 | 206.66 |
| Maximum managed generations / retired generations | 3 / 2 | 3 / 2 |
| Failed / unsupported agent states | 0 / 0 | 0 / 0 |
| Build commits / failures | 236 / 0 | 236 / 0 |
| Scheduling misses / unaffected cadence misses | 0 / 0 | 0 / 0 |

These are exact nearest-rank timing percentiles from the [200 analysis][analysis200]
and [400 analysis][analysis400]. Runner summaries use conservative 100-microsecond
histogram bounds and exact threshold-violation counts. The [200 summary][summary200]
and [400 summary][summary400] retain every gate and terminal request outcome.
About 40.6% and 40.3% of agent-ticks were quarantined; following/traversing share
outside quarantine was about 98.9%. This is 1,000 registered/attached actors,
not evidence that 1,000 actors moved continuously. Superseded and pending
requests are recorded separately from successful latency samples.

The live-memory gate kept all 1,000 actors registered and navigation attached
while the running server drained queues, source edits, candidates and retired
generations. It required two consecutive settled tick ends before forced GC and
the heap read. The acceptance envelope is baseline +25% +16 MiB. This measures
live Go heap, not an RSS-return guarantee. `PostShutdownHeap` is separate
diagnostic data; earlier candidates whose `DrainHeap` was read only after
shutdown cannot qualify the live-state drain gate.

The earlier [132044 terrain rehearsal][earlieralloc] recorded 4.390 GB process
allocations over its short window, versus 2.243 GB above after rebuild/source
reuse work. This is historical diagnostic comparison across different candidates
and measurement code, not a controlled benchmark or proof of zero query-scratch
allocation. Allocation-window read brackets and closing-read delay are retained
in each summary. Higher GOGC did not pass the navigation gate in this pair.

## Implementation history

Pure-Go Recast baking and Detour queries feed regional all-floor quarantine, safe movement on
retained views, bounded fair rebasing, coherent view-scoped safety, strict
unchanged-corridor transfer distinct from repair, independent runtime meshes,
immutable construction/source reuse, and Windows QPC interval timing. Later
source changes also add exact scoped snapshot ownership and fix link metadata
invalidation above source height bounds. The historical 134803 terrain table
predates those changes; the historical 142257 pair includes them. Neither targeted
checks nor short rehearsals replace final qualification of the selected source.
Ordinary production checks use `CGO_ENABLED=0`; a C compiler is used only for
separate race instrumentation and the optional native comparison oracle.

| Evidence | Recorded scope and limits |
| --- | --- |
| [Prepared-source checks, 134533][prepared] | Current full library tests/vet; focused bake/dynamic current, minimum, checkptr and Windows race checks. |
| [Construction sharing, 134112][construction] | Facade/artifact/dynamic ordinary, checkptr, Windows race and vet; [adapter ordinary][constructionadapter]. Includes deep-copy baseline parity, nested ownership, full-column replacement and merged limits. |
| [Surface dependency checks, 135813][dependency] | Current/minimum facade tests, checkptr, Windows race and vet for immutable dependency caching. |
| [Scoped identity/link checks, 140552][scoped] | Facade/artifact/dynamic/bake tests on Go 1.26.4 and 1.20.14, current checkptr/race/vet; [adapter ordinary/race][scopedadapter]. Covers foreign identities, duplicate revisions, lease release, layers, streaming and link changes. |
| [Link-height failing reproduction, 140429][linkred] | Real attached off-mesh link changed Action with `dirty=[]`; preserved before the passing 140552 correction. |
| [Earlier Windows matrix][oldwindows] / [earlier Linux matrix][oldlinux] | Superseded source snapshots, with failures retained. These do not qualify the current candidate. |

[latestlaunchall]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1423472923753Z/progress.jsonl
[latestlaunch8p]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1425283090170Z/progress.jsonl
[latestanalysisall]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1423472923753Z/terrain-1000/per-tick-analysis.json
[latestanalysis8p]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1425283090170Z/terrain-1000/per-tick-analysis.json
[latestsummaryall]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1423472923753Z/terrain-1000/summary.json
[latestsummary8p]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1425283090170Z/terrain-1000/summary.json
[latestsourceall]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1423472923753Z/source-after.json
[latestsource8p]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1425283090170Z/source-after.json
[latestaffinityall]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1423472923753Z/processor-affinity.json
[latestaffinity8p]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1425283090170Z/processor-affinity.json
[gc1000]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1431469003512Z/terrain-1000/summary.json
[brokenmultilevel]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1434286924192Z/multilevel-1000/summary.json
[constructionrun]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T142257.063281Z-rehearsal-20261111T1437095351735Z/construction-1000/summary.json
[connectivity]: C:/Demiurgos/navmesh-validation/world-test/runs/multilevel-connectivity-20261111T1445177633166Z/results.json
[connectivityred]: C:/Demiurgos/navmesh-validation/world-test/runs/multilevel-fixture-regression-20261111T145223.319702Z/original-fixture-connectivity.log
[connectivityfixed]: C:/Demiurgos/navmesh-validation/world-test/runs/multilevel-fixture-fixed-20261111T145312.915367Z/worldgen.log
[anchors]: C:/Demiurgos/navmesh-validation/world-test/runs/multilevel-fixture-fixed-20261111T145312.915367Z/anchor-preservation.json
[libraryfinal]: C:/Demiurgos/navmesh-validation/world-test/runs/final-library-144303-summary/results.json
[oraclefinal]: C:/Demiurgos/navmesh-validation/world-test/runs/final-library-oracle-144303/results.json
[unityfinal]: C:/Demiurgos/navmesh-validation/world-test/runs/unity-20261111T145639.7457027Z-5bff2522/manifest.json
[archives]: C:/Demiurgos/navmesh-validation/world-test/runs/module-archive-inspection-145312-tool/results.json
[runnerwincurrent]: C:/Demiurgos/navmesh-validation/world-test/runs/runner-final-windows-current-145312-20261111T145606858Z/results.json
[runnerwinmin]: C:/Demiurgos/navmesh-validation/world-test/runs/runner-final-windows-minimum-145312-20261111T145606858Z/results.json
[runnerlinuxcurrent]: C:/Demiurgos/navmesh-validation/world-test/runs/runner-final-linux-current-145312-20261111T145606911Z/results.json
[runnerlinuxmin]: C:/Demiurgos/navmesh-validation/world-test/runs/runner-final-linux-minimum-145312-20261111T145606911Z/results.json
[website]: C:/Demiurgos/navmesh-validation/world-test/runs/website-build-20261111T151011.3561314Z/results.json
[connectivityfull]: C:/Demiurgos/navmesh-validation/world-test/runs/multilevel-connectivity-fixed-20261111T150930.846936Z/results.json
[enginewincurrent]: C:/Demiurgos/navmesh-validation/world-test/runs/engine-final-windows-current-144303-20261111T144632899Z/results.json
[enginewinnavmin]: C:/Demiurgos/navmesh-validation/world-test/runs/engine-final-windows-minimum-nav-144303-20261111T144632899Z/results.json
[enginewinmin]: C:/Demiurgos/navmesh-validation/world-test/runs/engine-final-windows-minimum-engine-144303-20261111T144632899Z/results.json
[enginelinuxcurrent]: C:/Demiurgos/navmesh-validation/world-test/runs/engine-final-linux-current-144303-20261111T144632948Z/results.json
[enginelinuxnavmin]: C:/Demiurgos/navmesh-validation/world-test/runs/engine-final-linux-minimum-nav-144303-20261111T144632948Z/results.json
[enginelinuxmin]: C:/Demiurgos/navmesh-validation/world-test/runs/engine-final-linux-minimum-engine-144303-20261111T144632948Z/results.json
[finalrunnerwincurrent]: C:/Demiurgos/navmesh-validation/world-test/runs/runner-final-windows-current-150930-20261111T151216426Z/results.json
[finalrunnerwinmin]: C:/Demiurgos/navmesh-validation/world-test/runs/runner-final-windows-minimum-150930-20261111T151216426Z/results.json
[finalrunnerlinuxcurrent]: C:/Demiurgos/navmesh-validation/world-test/runs/runner-final-linux-current-150930-20261111T151216429Z/results.json
[finalrunnerlinuxmin]: C:/Demiurgos/navmesh-validation/world-test/runs/runner-final-linux-minimum-150930-20261111T151216429Z/results.json
[identity]: C:/Demiurgos/navmesh-validation/world-test/runs/source-identity-142257-through-152132/results.json
[worldgenidentity]: C:/Demiurgos/navmesh-validation/world-test/runs/final-worldgen-150506-identity/results.json
[worldgenwincurrent]: C:/Demiurgos/navmesh-validation/world-test/runs/final-worldgen-150506-windows-current/results.json
[worldgenwinmin]: C:/Demiurgos/navmesh-validation/world-test/runs/final-worldgen-150506-windows-minimum/results.json
[worldgenlinuxcurrent]: C:/Demiurgos/navmesh-validation/world-test/runs/final-worldgen-150506-linux-current/results.json
[worldgenlinuxmin]: C:/Demiurgos/navmesh-validation/world-test/runs/final-worldgen-150506-linux-minimum/results.json
[fuzzfinal]: C:/Demiurgos/navmesh-validation/world-test/runs/final-fuzz-145312-summary/results.json
[microbench]: C:/Demiurgos/navmesh-validation/world-test/runs/final-microbench-150506/results.json
[microbenchintegrity]: C:/Demiurgos/navmesh-validation/world-test/runs/final-microbench-150506-integrity/source-after-run.json
[archivesfinal]: C:/Demiurgos/navmesh-validation/world-test/runs/module-archive-inspection-152132/results.json
[archivescurrent]: C:/Demiurgos/navmesh-validation/world-test/runs/module-archive-inspection-162949/results.json
[endpointqualification]: C:/Demiurgos/navmesh-validation/world-test/runs/endpoint-height-qualification-162949/results.json
[endpointenginematrix]: C:/Demiurgos/navmesh-validation/world-test/runs/endpoint-height-engine-162949-summary/results.json
[endpointfuzzscope]: C:/Demiurgos/navmesh-validation/world-test/runs/endpoint-height-qualification-162949/fuzz-scope.json
[identitycurrent]: C:/Demiurgos/navmesh-validation/world-test/runs/source-identity-142257-through-162949/results.json
[multilevelcorrected]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T162949.004814Z-rehearsal-20261111T1648052504774Z/multilevel-1000/per-tick-analysis.json
[multilevelcorrectedsummary]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T162949.004814Z-rehearsal-20261111T1648052504774Z/multilevel-1000/summary.json
[multilevelcorrectedintegrity]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T162949.004814Z-rehearsal-20261111T1648052504774Z/source-after-runtime.json
[fullcapacityfailure]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T162949.004814Z-capacity-20261111T1652254916635Z/capacity-1000/capacity-failure-analysis.json
[fullcapacitymotionloss]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T162949.004814Z-capacity-20261111T1652254916635Z/capacity-1000/first-motion-loss.json
[humanphysicaldiagnostic]: C:/Demiurgos/navmesh-validation/world-test/runs/capacity-human-diagnostic/result.json
[humanclearanceverification]: C:/Demiurgos/navmesh-validation/world-test/runs/capacity-human-oracle/verification.json
[humanclearanceoracle]: C:/Demiurgos/navmesh-validation/world-test/runs/capacity-human-oracle/result.json
[capacitymargindiagnostic]: C:/Demiurgos/navmesh-validation/world-test/runs/capacity-clearance-margin-diagnostic/result.json
[capacitymarginreceipt]: C:/Demiurgos/navmesh-validation/world-test/runs/capacity-clearance-margin-diagnostic/receipt.json
[marginrunnermatrix]: C:/Demiurgos/navmesh-validation/world-test/runs/capacity-margin-stress-172538-summary/results.json
[terrainrunnermatrix]: C:/Demiurgos/navmesh-validation/world-test/runs/terrain-preset-stress-173226-summary/results.json
[terrainworldgenmatrix]: C:/Demiurgos/navmesh-validation/world-test/runs/terrain-preset-worldgen-173226-summary/results.json
[terrainrunneridentity]: C:/Demiurgos/navmesh-validation/world-test/runs/terrain-preset-identities-173226/identity-map.json
[marginidentity]: C:/Demiurgos/navmesh-validation/world-test/runs/capacity-margin-worldgen-172538-summary/source-identity.json
[terrainidentity]: C:/Demiurgos/navmesh-validation/world-test/runs/terrain-preset-worldgen-173226-summary/source-identity.json
[initialclearance]: C:/Demiurgos/navmesh-validation/world-test/runs/clearance-artifact-diagnostic-172538/input-identities-and-summary.json
[terrainaccuracy]: C:/Demiurgos/navmesh-validation/world-test/runs/terrain-accuracy-050-172538/summary.json
[terrainaccuracyreceipt]: C:/Demiurgos/navmesh-validation/world-test/runs/terrain-accuracy-050-172538/receipt.json
[archivesselected]: C:/Demiurgos/navmesh-validation/world-test/runs/module-archive-inspection-173226/results.json
[websitelatest]: C:/Demiurgos/navmesh-validation/world-test/runs/website-build-20261111T234057.1789371Z/results.json
[capacitywindowspass]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T173226.584096Z-capacity-20261111T1738307342661Z/capacity-1000/per-tick-analysis.json
[capacitywindowssummary]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T173226.584096Z-capacity-20261111T1738307342661Z/capacity-1000/summary.json
[capacitywindowsintegrity]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T173226.584096Z-capacity-20261111T1738307342661Z/source-after-runtime.json
[capacitylinuxpass]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T173226.584096Z-linux-capacity-20261111T175305.303601Z/capacity-1000/per-tick-analysis.json
[capacitylinuxsummary]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T173226.584096Z-linux-capacity-20261111T175305.303601Z/capacity-1000/summary.json
[capacitylinuxintegrity]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T173226.584096Z-linux-capacity-20261111T175305.303601Z/source-after-runtime.json
[fullwindowsterrain]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T181226.520768Z-full-20261111T1813336548741Z/terrain-1000/per-tick-analysis.json
[fullwindowsmultilevel]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T181226.520768Z-full-20261111T1813336548741Z/multilevel-1000/per-tick-analysis.json
[fullwindowsconstruction]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T181226.520768Z-full-20261111T1813336548741Z/construction-1000/per-tick-analysis.json
[fullwindowssoak]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T181226.520768Z-full-20261111T1813336548741Z/soak-construction-1000/per-tick-analysis.json
[fullwindowsbuildreview]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T181226.520768Z-full-20261111T1813336548741Z/soak-construction-1000/completed-build-review.json
[fullwindowsintegrity]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T181226.520768Z-full-20261111T1813336548741Z/source-after-runtime.json
[reducedidentity]: C:/Demiurgos/navmesh-validation/world-test/runs/reduced-suite-identity-181226/identity.json
[reducedgoidentity]: C:/Demiurgos/navmesh-validation/world-test/runs/reduced-suite-identity-181226/go-source-equality.json
[fulllinuxterrain]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T181226.520768Z-linux-full-20261111T205423.879288Z/terrain-1000/per-tick-analysis.json
[fulllinuxmultilevel]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T181226.520768Z-linux-full-20261111T205423.879288Z/multilevel-1000/per-tick-analysis.json
[fulllinuxconstruction]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T181226.520768Z-linux-full-20261111T205423.879288Z/construction-1000/per-tick-analysis.json
[fulllinuxsoak]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T181226.520768Z-linux-full-20261111T205423.879288Z/soak-construction-1000/per-tick-analysis.json
[fulllinuxintegrity]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T181226.520768Z-linux-full-20261111T205423.879288Z/source-after-runtime.json
[archives181226]: C:/Demiurgos/navmesh-validation/world-test/runs/module-archive-inspection-181226/results.json
[finalsourceaudit]: C:/Demiurgos/navmesh-validation/world-test/runs/final-source-audit-20261111T233838.569177Z/results.json
[timingcompletion]: C:/Demiurgos/navmesh-validation/world-test/runs/completed-timing-review-20261111/completion.json
[rejectedaffinity]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T173226.584096Z-capacity-20261111T1738119155123Z/processor-affinity.json
[finalmanifest]: C:/Demiurgos/navmesh-validation/world-test/runs/final-qualification-181226-20261111T233727Z/manifest.json
[rampproof]: C:/Demiurgos/navmesh-validation/world-test/runs/multilevel-server-green-20261111T162817.017519Z/red-green-proof.json
[endpointgreen]: C:/Demiurgos/navmesh-validation/world-test/runs/endpoint-height-green-162817/results.json
[endpointred]: C:/Demiurgos/navmesh-validation/world-test/runs/endpoint-height-red-162653/test.log
[capacitycorrected]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T160444.312786Z-rehearsal-20261111T1610056070718Z/capacity-1000/per-tick-analysis.json
[capacitycorrectedsummary]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T160444.312786Z-rehearsal-20261111T1610056070718Z/capacity-1000/summary.json
[capacitycorrectedintegrity]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T160444.312786Z-rehearsal-20261111T1610056070718Z/source-after-runtime.json
[multilevelcurrent]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T160444.312786Z-rehearsal-20261111T1612232522617Z/multilevel-1000/per-tick-analysis.json
[multilevelcurrentsummary]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T160444.312786Z-rehearsal-20261111T1612232522617Z/multilevel-1000/summary.json
[multilevelcurrentintegrity]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T160444.312786Z-rehearsal-20261111T1612232522617Z/source-after-runtime.json
[motorcorrection]: C:/Demiurgos/navmesh-validation/world-test/runs/circular-motor-tests-20261111T160444.312786Z/receipt.json
[capacityfailed]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T152132.296092Z-rehearsal-20261111T1546472278734Z/capacity-1000/per-tick-analysis.json
[capacityfailureanalysis]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T152132.296092Z-rehearsal-20261111T1546472278734Z/capacity-1000/capacity-failure-analysis.json
[capacitycorner]: C:/Demiurgos/navmesh-validation/world-test/runs/capacity-corner-diagnostic/result.json
[capacitycornerreceipt]: C:/Demiurgos/navmesh-validation/world-test/runs/capacity-corner-diagnostic/receipt.json
[capacityintegrity]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T152132.296092Z-rehearsal-20261111T1546472278734Z/source-after-runtime.json
[launch200]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T134803.223648Z-rehearsal-20261111T1348564337218Z/progress.jsonl
[launch400]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T134803.223648Z-rehearsal-20261111T1351558706702Z/progress.jsonl
[summary200]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T134803.223648Z-rehearsal-20261111T1348564337218Z/terrain-1000/summary.json
[summary400]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T134803.223648Z-rehearsal-20261111T1351558706702Z/terrain-1000/summary.json
[analysis200]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T134803.223648Z-rehearsal-20261111T1348564337218Z/terrain-1000/per-tick-analysis.json
[analysis400]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T134803.223648Z-rehearsal-20261111T1351558706702Z/terrain-1000/per-tick-analysis.json
[earlieralloc]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T132044.287852Z-rehearsal-20261111T1321416211543Z/terrain-1000/summary.json
[prepared]: C:/Demiurgos/navmesh-validation/world-test/runs/prepared-source-20261111T134533.999852Z/results.json
[construction]: C:/Demiurgos/navmesh-validation/world-test/runs/construction-sharing-134112/results.json
[constructionadapter]: C:/Demiurgos/navmesh-validation/world-test/runs/construction-adapter-134112/results.json
[dependency]: C:/Demiurgos/navmesh-validation/world-test/runs/surface-dependency-20261111T135813.559689Z/results.json
[scoped]: C:/Demiurgos/navmesh-validation/world-test/runs/scoped-identity-link-140552/results.json
[scopedadapter]: C:/Demiurgos/navmesh-validation/world-test/runs/scoped-link-adapter-140552/results.json
[linkred]: C:/Demiurgos/navmesh-validation/world-test/runs/link-height-red-140429/test.log
[oldwindows]: C:/Demiurgos/navmesh-validation/world-test/runs/regional-matrix-windows/results.json
[oldlinux]: C:/Demiurgos/navmesh-validation/world-test/runs/regional-matrix-linux/results.json
[ack]: C:/Demiurgos/navmesh-validation/world-test/runs/ack-window-20261111T123514.194358Z-linux-current-retry/results.json
[hardware]: C:/Demiurgos/navmesh-validation/world-test/runs/20261111T134803.223648Z-rehearsal-20261111T1348564337218Z/hardware.json
[topology]: C:/Demiurgos/navmesh-validation/world-test/runs/cpu-topology-20261111T140201.069886Z-20261111T140214.171545Z/result.json
[toolchains]: C:/Demiurgos/navmesh-validation/world-test/runs/linux-minimum-toolchains-20261111T140753Z/results.json
[compiler]: C:/Demiurgos/navmesh-validation/world-test/runs/linux-race-compiler-20261111T124554.612057Z/manifest.json
