// Command navigation-stress measures the real Golem navigation service against
// deterministic retained-source fixtures. It never claims missing observations
// passed a gate. Run through qualification/Run-Qualification.ps1 for pinned input.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	adapter "github.com/demiurgos-hub/golem-engine/golem/nav/navmesh"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/artifact"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/geom"
	"github.com/demiurgos-hub/navmesh-go/qualification/worldgen"
)

type options struct {
	CPUProfile, MemProfile                 string
	MinActiveShare                         float64
	Fixture, Out, SourceManifest           string
	Agents, TickRate, Workers, Burst       int
	Warmup, Duration, BurstInterval, Drain time.Duration
	DrainTimeout                           time.Duration
	Churn, Edits                           float64
	Restart                                bool
	BakeOnly                               bool
	BuildCommits                           int
	PathDrainBudget                        time.Duration
}
type actor struct {
	ID                          int64
	PositionValue               surface.Vec3
	tickPosition                surface.Vec3
	hostMotionTick              uint64
	Profile                     int
	motor                       *bodyMotor
	requestAt                   time.Time
	requested                   bool
	requestQuarantined          bool
	lastMotor                   uint64
	lastMode                    golem.NavigationMode
	lastObservedMode            golem.NavigationMode
	pauseStarted                time.Time
	recoveryPending, recovering bool
	fallVelocity                float64
	resumeDestination           surface.Vec3
	resumeHasDestination        bool
}

func (a *actor) EntityID() int64  { return a.ID }
func (a *actor) TypeName() string { return "QualificationActor" }
func (a *actor) Position() (float32, float32) {
	return float32(a.PositionValue.X), float32(a.PositionValue.Z)
}
func (a *actor) Position3D() (float32, float32, float32) {
	return float32(a.PositionValue.X), float32(a.PositionValue.Y), float32(a.PositionValue.Z)
}
func (a *actor) SetPosition3D(x, y, z float32) {
	a.PositionValue = surface.Vec3{X: float64(x), Y: float64(y), Z: float64(z)}
}
func (a *actor) IsGlobal() bool               { return false }
func (a *actor) FlushUpdate() ([]byte, error) { return nil, nil }
func (a *actor) FullUpdate() ([]byte, error)  { return nil, nil }

// A bounded 100-microsecond histogram retains no per-request samples. Values
// above ten seconds have an explicit overflow bin and conservative percentile.
type histogram struct {
	bins               [100002]uint64
	Count              uint64
	Max                time.Duration
	over5ms, over100ms uint64
}

func (h *histogram) add(d time.Duration) {
	if d < 0 {
		return
	}
	i := int(d / (100 * time.Microsecond))
	if i >= len(h.bins)-1 {
		i = len(h.bins) - 1
	}
	h.bins[i]++
	h.Count++
	if d > 5*time.Millisecond {
		h.over5ms++
	}
	if d > 100*time.Millisecond {
		h.over100ms++
	}
	if d > h.Max {
		h.Max = d
	}
}
func (h *histogram) percentile(p float64) float64 {
	if h.Count == 0 {
		return 0
	}
	target := uint64(math.Ceil(float64(h.Count) * p))
	var sum uint64
	for i, n := range h.bins {
		sum += n
		if sum >= target {
			if i == len(h.bins)-1 {
				return float64(h.Max) / float64(time.Millisecond)
			}
			return float64(i+1) / 10
		}
	}
	return 0
}
func (h *histogram) summary() any {
	return map[string]any{"count": h.Count, "p50_ms_upper": h.percentile(.5), "p95_ms_upper": h.percentile(.95), "p99_ms_upper": h.percentile(.99), "max_ms": float64(h.Max) / float64(time.Millisecond), "bin_us": 100, "exact_samples_over_5ms": h.over5ms, "exact_samples_over_100ms": h.over100ms}
}

func (h *histogram) p99Within(limit time.Duration) bool {
	if h.Count == 0 {
		return false
	}
	var exceeded uint64
	switch limit {
	case 5 * time.Millisecond:
		exceeded = h.over5ms
	case 100 * time.Millisecond:
		exceeded = h.over100ms
	default:
		panic("untracked exact threshold")
	}
	return exceeded <= h.Count/100
}

type measurements struct {
	Displacement                                                                                                                                          displacementEvidence
	CapacityStartupAdmissions, CapacityStartupFailures                                                                                                    uint64
	capacityStartupPath                                                                                                                                   histogram
	BaselineLive                                                                                                                                          liveMemoryEvidence
	Drain                                                                                                                                                 drainEvidence
	PostShutdownHeap                                                                                                                                      uint64
	AllocationWindow                                                                                                                                      allocationWindowEvidence
	GCPauses, GCPauseNanoseconds, MaxGCPauseNanoseconds, GCPauseEventsOmitted                                                                             uint64
	RequestOutcomes                                                                                                                                       requestOutcomes
	BuildBusyObservations, BuildBusyFailures                                                                                                              uint64
	AdmittedRequests, FailedRequests, CancelledRequests, SupersededRequests, PendingAtMeasurementEnd, AdmissionEvidenceDropped                            uint64
	pathFailed, pathCancelled, pathSuperseded, pathIncomplete                                                                                             histogram
	HostFallsStarted, HostLandings, HostRecoveryFailures, HostRecoveringAgentTicks, ChurnSuppressedDuringRecovery                                         uint64
	EvidenceWriteFailures                                                                                                                                 uint64
	HostEjections, HostEjectionFailures, EjectionObservationsOmitted                                                                                      uint64
	Ejections                                                                                                                                             []ejectionObservation
	FailureObservations                                                                                                                                   []failureObservation
	FailureObservationsOmitted                                                                                                                            uint64
	BlockedObservations                                                                                                                                   []failureObservation
	BlockedObservationsOmitted                                                                                                                            uint64
	pause                                                                                                                                                 histogram
	AgentTicks, ActiveAgentTicks, EligibleAgentTicks, ArrivalRequests                                                                                     uint64
	DrainRetiredGenerations, DrainOutstandingCandidates                                                                                                   int
	buildCommit                                                                                                                                           histogram
	SchedulingMisses, AgentsNotScheduled, QuarantinedAgentTicks, BuildCommits, BuildFailures                                                              uint64
	tick, nav, path, pathQuarantined, rebuild, motorGap                                                                                                   histogram
	MeasuredTicks, Overruns, Arrivals, FailedStates, UnsupportedStates, MotorCalls, MotorBlocked, TraversalBegins, TraversalCompleted, TraversalCancelled uint64
	Requests, Resolved, Unresolved, RejectedEdits, SubmittedEdits, CompletedEdits, CadenceMisses                                                          uint64
	PeakHeap, PeakRSS, BaseHeap, DrainHeap, AllocBytes, AllocObjects                                                                                      uint64
	MaxRetired, MaxLeases                                                                                                                                 int
	MaxManagedGenerations                                                                                                                                 int
	ScratchGate                                                                                                                                           string
}
type admissionObservation struct {
	EntityID                        int64
	Tick, Generation, WorldRevision uint64
	IssuedAt, AdmittedAt            time.Time
	Elapsed                         time.Duration
	Quarantined, Measured           bool
}
type quarantineCounts struct {
	ObservedQuarantine, NoObservedQuarantine uint64
}
type requestOutcomes struct {
	Admitted, Failed, Cancelled, Superseded, PendingAtMeasurementEnd quarantineCounts
}
type outcomeObservation struct {
	EntityID             int64
	Tick                 uint64
	IssuedAt, ObservedAt time.Time
	Elapsed              time.Duration
	Outcome              string
	Quarantined          bool
	State                *outcomeNavigationState `json:",omitempty"`
}

type outcomeNavigationState struct {
	Mode                      golem.NavigationMode
	Code, Reason              surface.Code
	Error                     string
	Generation, WorldRevision uint64
}

func (o outcomeObservation) withState(state golem.NavigationState) outcomeObservation {
	o.State = &outcomeNavigationState{Mode: state.Mode, Code: state.Status.Code, Reason: state.Status.Reason, Generation: state.Generation, WorldRevision: state.WorldRevision}
	if state.Status.Err != nil {
		o.State.Error = state.Status.Err.Error()
	}
	return o
}

func (m *measurements) requestOutcome(a *actor, outcome string, tick uint64, at time.Time) outcomeObservation {
	var counts *quarantineCounts
	switch outcome {
	case "admitted":
		counts = &m.RequestOutcomes.Admitted
	case "failed":
		counts = &m.RequestOutcomes.Failed
	case "cancelled":
		counts = &m.RequestOutcomes.Cancelled
	case "superseded":
		counts = &m.RequestOutcomes.Superseded
	case "pending_at_measurement_end":
		counts = &m.RequestOutcomes.PendingAtMeasurementEnd
	default:
		panic("unknown request outcome")
	}
	if a.requestQuarantined {
		counts.ObservedQuarantine++
	} else {
		counts.NoObservedQuarantine++
	}
	return outcomeObservation{EntityID: a.ID, Tick: tick, IssuedAt: a.requestAt, ObservedAt: at, Elapsed: at.Sub(a.requestAt), Outcome: outcome, Quarantined: a.requestQuarantined}
}

func (m *measurements) writeRequestOutcome(evidence *json.Encoder, observation outcomeObservation) {
	if err := evidence.Encode(map[string]any{"event": "request_outcome", "observation": observation}); err != nil {
		m.EvidenceWriteFailures++
	}
}

type supportPolicy struct {
	Formula, MotorModel, Reason                         string
	MinimumProfileHeight, MinimumStackedFloorSeparation float64
	Tolerances                                          map[string]float64
}

func fixtureSupportPolicy(f worldgen.Fixture) (supportPolicy, error) {
	policy := supportPolicy{Formula: "CellHeight + DetailSampleMaxError (world units)", MotorModel: "navmesh-supported height plus swept circular-footprint cylinder against exclusion boxes with simultaneous vertical overlap; source-triangle gravity during cancelled-traversal recovery; not a full rigid-body floor solver or agent avoidance", Reason: "Baked height fidelity is explicit. A reconstructed 56.49 mm remesh change at unchanged X/Z exceeded the engine's unchanged 50 mm default.", MinimumProfileHeight: math.Inf(1), MinimumStackedFloorSeparation: 1, Tolerances: make(map[string]float64)}
	// The generated stacked floors are at least one metre apart (the removable
	// bridge at 7m and building floor at 8m); adjacent stair treads are one surface.
	if f.Config.Family != "terrain" && f.Config.Family != "multilevel" && f.Config.Family != "construction" && f.Config.Family != "capacity" {
		return policy, errors.New("support policy requires a known generated family")
	}
	bodies, err := fixtureBodyProfiles(f)
	if err != nil {
		return policy, err
	}
	for _, body := range bodies {
		policy.MinimumProfileHeight = math.Min(policy.MinimumProfileHeight, body.Height)
	}
	bound := math.Min(policy.MinimumProfileHeight, policy.MinimumStackedFloorSeparation) / 2
	for _, cfg := range f.Profiles {
		tolerance := cfg.Settings.CellHeight + cfg.Settings.DetailSampleMaxError
		if math.IsNaN(tolerance) || math.IsInf(tolerance, 0) || tolerance <= 0 || !(tolerance < bound) {
			return policy, fmt.Errorf("support tolerance %.6g must be positive and below half profile-height/floor-separation bound %.6g", tolerance, bound)
		}
		policy.Tolerances[cfg.Profile.ID] = tolerance
	}
	return policy, nil
}

type ejectionObservation struct {
	Tick, Edit         uint64
	EntityID           int64
	Measured, Resolved bool
	Before, After      surface.Vec3
	ClearanceMargin    float64
}
type failureObservation struct {
	Tick                                        uint64
	Measured                                    bool
	EntityID                                    int64
	Profile                                     int
	Mode                                        golem.NavigationMode
	Position, Destination                       surface.Vec3
	Code, Reason                                surface.Code
	Error                                       string
	Quarantined                                 bool
	Generation, WorldRevision, LastPhysicalEdit uint64
	PhysicalOverlaps                            []string
}
type editJob struct{ sequence uint64 }
type editResult struct {
	sequence uint64
	elapsed  time.Duration
	err      error
}
type buildProgress struct {
	Generation uint64
	Attempts   int
	Pending    bool
	Error      string
}

func applyProfileEdits(f worldgen.Fixture, worlds []*dynamic.World, sequence uint64, change dynamic.Changes, ready func() error) error {
	// Quarantine every clearance profile before the host can queue physical
	// activation; an already accepted profile stays conservative on any failure.
	for profile, world := range worlds {
		if world == nil {
			return fmt.Errorf("profile %d world is unavailable", profile)
		}
		if _, err := world.Apply(change); err != nil {
			return fmt.Errorf("profile %d apply: %w", profile, err)
		}
		if f.Config.Family == "construction" && sequence%20 == 0 {
			keys, err := bakeKeys(f, profile)
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				if _, err := world.SetLoaded(keys[:1], sequence%40 == 0); err != nil {
					return err
				}
			}
		}
	}
	return ready()
}

type saved struct {
	Version    int
	ID         string
	Actors     []savedActor
	Navigation golem.NavigationCheckpoint
	Worlds     [][]byte
}
type savedActor struct {
	ID       int64
	Position surface.Vec3
	Profile  int
}

var errGateFailure = errors.New("required 1000-agent qualification gate failed; evidence retained")

func main() {
	if err := mainRun(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errGateFailure) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
func mainRun() error {
	o := options{}
	flag.StringVar(&o.Fixture, "fixture", "", "fixture JSON")
	flag.StringVar(&o.Out, "out", "", "new evidence directory")
	flag.StringVar(&o.SourceManifest, "source-manifest", "", "staged source manifest")
	flag.StringVar(&o.CPUProfile, "cpuprofile", "", "optional CPU profile basename inside evidence directory; profiling timings are diagnostic")
	flag.StringVar(&o.MemProfile, "memprofile", "", "optional heap profile basename inside evidence directory")
	flag.Float64Var(&o.MinActiveShare, "min-active-share", .5, "minimum following/traversing share outside observed quarantine")
	flag.IntVar(&o.Agents, "agents", 100, "active agents")
	flag.IntVar(&o.TickRate, "tick-rate", 20, "simulation ticks per second")
	flag.IntVar(&o.Workers, "workers", 2, "path workers")
	flag.IntVar(&o.BuildCommits, "build-commits-per-tick", 2, "maximum prepared profile publications per tick")
	flag.DurationVar(&o.PathDrainBudget, "path-drain-budget", time.Millisecond, "tick budget for applying path completions")
	flag.IntVar(&o.Burst, "burst", 20, "additional edits per burst")
	flag.DurationVar(&o.BurstInterval, "burst-interval", time.Minute, "edit burst interval")
	flag.DurationVar(&o.Warmup, "warmup", 2*time.Minute, "unmeasured warmup")
	flag.DurationVar(&o.Duration, "duration", 10*time.Minute, "measured duration")
	flag.DurationVar(&o.Drain, "drain", 5*time.Second, "minimum post-load live-server drain observation")
	flag.DurationVar(&o.DrainTimeout, "drain-timeout", 30*time.Second, "maximum post-load wait for live queues, builds and retirement to settle")
	flag.Float64Var(&o.Churn, "churn", .1, "destination changes per agent per second")
	flag.Float64Var(&o.Edits, "edits-per-sec", 2, "world edits per second")
	flag.BoolVar(&o.Restart, "restart", true, "checkpoint and restart after measured run")
	flag.BoolVar(&o.BakeOnly, "bake-only", false, "only verify and retain both initial profile artifacts")
	flag.Parse()
	for _, value := range []float64{o.Churn, o.Edits, o.MinActiveShare} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return errors.New("qualification rates and shares must be finite")
		}
	}
	if o.Fixture == "" || o.Out == "" || o.Agents < 1 || o.Agents > 100000 || o.TickRate < 1 || o.TickRate > 1000 || o.Workers < 1 || o.BuildCommits < 1 || o.PathDrainBudget <= 0 || o.Warmup < 0 || o.Duration <= 0 || o.Drain < 0 || o.DrainTimeout < o.Drain || o.Edits < 0 || o.Churn < 0 || o.Burst < 0 || o.BurstInterval <= 0 {
		return errors.New("invalid qualification arguments")
	}
	if err := os.Mkdir(o.Out, 0755); err != nil {
		return err
	}
	if o.MinActiveShare < 0 || o.MinActiveShare > 1 {
		return errors.New("min-active-share must be within [0,1]")
	}
	for _, p := range []string{o.CPUProfile, o.MemProfile} {
		if p != "" && (filepath.Base(p) != p || p == "." || p == "..") {
			return errors.New("profile output must be a basename inside -out")
		}
	}
	data, err := os.ReadFile(o.Fixture)
	if err != nil {
		return err
	}
	var f worldgen.Fixture
	if err = json.Unmarshal(data, &f); err != nil {
		return err
	}
	if f.Version != 1 || len(f.Profiles) != 2 {
		return errors.New("unsupported fixture; two profiles required")
	}
	bodyProfiles, err := fixtureBodyProfiles(f)
	if err != nil {
		return err
	}
	support, err := fixtureSupportPolicy(f)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(data)
	manifest := map[string]any{"started_utc": time.Now().UTC(), "pid": os.Getpid(), "fixture_sha256": hex.EncodeToString(hash[:]), "options": o, "go_version": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "logical_cpus": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0), "dependency_mode": "unpublished staged candidate archives; not published resolution", "source_manifest": o.SourceManifest}
	manifest["interval_clock"] = map[string]any{"api": "golem.NavigationNow", "elapsed": "same-domain time.Time.Sub; no time.Since/time.Until against this clock", "windows": "QueryPerformanceCounter anchored once to a Go time.Time", "other_platforms": "Go monotonic time", "implementation_identity": "exact helper source and module archive hashes in source manifest", "historical_limit": "Earlier Windows candidates used coarse Go1.26.4 time.Now/time.Since; their phase and boundary estimates are not directly comparable to this clock."}
	manifest["support_policy"] = support
	manifest["profile_dimensions"] = fixtureProfileEvidence(f, bodyProfiles)
	if err = writeJSON(filepath.Join(o.Out, "manifest.json"), manifest); err != nil {
		return err
	}
	if o.SourceManifest != "" {
		b, e := os.ReadFile(o.SourceManifest)
		if e != nil {
			return e
		}
		if e = os.WriteFile(filepath.Join(o.Out, "source-manifest.json"), b, 0644); e != nil {
			return e
		}
	}
	worlds := make([]*dynamic.World, 0, 2)
	initialArtifacts := make(map[string]string)
	for _, cfg := range f.Profiles {
		started := golem.NavigationNow()
		w, e := dynamic.New(context.Background(), f.Scene, cfg)
		if e != nil {
			return fmt.Errorf("initial bake %s: %w", cfg.Profile.ID, e)
		}
		worlds = append(worlds, w)
		fmt.Printf("initial_bake profile=%s elapsed=%s\n", cfg.Profile.ID, golem.NavigationNow().Sub(started))
		bundle, e := w.Snapshot().Bundle()
		if e != nil {
			return e
		}
		encoded, e := artifact.Encode(bundle)
		if e != nil {
			return e
		}
		name := "initial-" + cfg.Profile.ID + ".navmesh"
		if e = os.WriteFile(filepath.Join(o.Out, name), encoded, 0644); e != nil {
			return e
		}
		artifactHash := sha256.Sum256(encoded)
		initialArtifacts[name] = hex.EncodeToString(artifactHash[:])
	}
	if err = writeJSON(filepath.Join(o.Out, "initial-artifacts-sha256.json"), initialArtifacts); err != nil {
		return err
	}
	capacityRoutes, err := capacityPreflight(o, f, worlds)
	if f.Config.Family == "capacity" {
		if writeErr := writeJSON(filepath.Join(o.Out, "capacity-preflight.json"), map[string]any{"routes": capacityRoutes, "query_limits": capacityQueryLimits(), "speed_metres_per_second": agentSpeed, "required_distance_formula": "speed * (warmup + measured duration) * 1.1 + 100 metres", "error": errorText(err), "collision_scope": "static exclusion walls only; agents do not collide with or avoid one another"}); writeErr != nil {
			return writeErr
		}
	}
	if err != nil {
		return err
	}
	if o.BakeOnly {
		return nil
	}
	m := &measurements{ScratchGate: "not_measured: process allocations do not isolate query scratch from owned results"}
	if o.CPUProfile != "" {
		file, e := os.Create(filepath.Join(o.Out, o.CPUProfile))
		if e != nil {
			return e
		}
		if e = pprof.StartCPUProfile(file); e != nil {
			file.Close()
			return e
		}
		defer func() { pprof.StopCPUProfile(); file.Close() }()
	}
	if o.MemProfile != "" {
		defer func() {
			runtime.GC()
			file, e := os.Create(filepath.Join(o.Out, o.MemProfile))
			if e == nil {
				e = pprof.WriteHeapProfile(file)
				file.Close()
			}
			if e != nil {
				fmt.Fprintln(os.Stderr, "heap profile:", e)
			}
		}()
	}
	state, err := exercise(o, f, worlds, m, nil)
	if err != nil {
		_ = writeJSON(filepath.Join(o.Out, "failure.json"), map[string]any{"error": err.Error(), "time": time.Now().UTC()})
		return err
	}
	if o.Restart {
		if err = writeJSON(filepath.Join(o.Out, "checkpoint.json"), state); err != nil {
			return err
		}
		restored := make([]*dynamic.World, 0, 2)
		for _, b := range state.Worlds {
			w, e := dynamic.Restore(context.Background(), b, state.ID)
			if e != nil {
				return e
			}
			restored = append(restored, w)
		}
		restartOpts := o
		restartOpts.Warmup = 0
		restartOpts.Duration = 2 * time.Second
		restartOpts.Drain = 0
		restartOpts.DrainTimeout = 0
		restartOpts.Edits = 0
		restartOpts.Churn = 0
		restartOpts.Burst = 0
		restartOpts.Out = filepath.Join(o.Out, "restart")
		if err = os.Mkdir(restartOpts.Out, 0755); err != nil {
			return err
		}
		if _, err = exercise(restartOpts, f, restored, &measurements{}, state); err != nil {
			return fmt.Errorf("restart: %w", err)
		}
	}
	gates := map[string]any{"nav_tick_p99_le_5ms": gate(m.nav.Count > 0, m.nav.p99Within(5*time.Millisecond)), "path_end_to_end_p99_le_100ms_outside_quarantine": gate(m.path.Count > 0, m.path.p99Within(100*time.Millisecond)), "every_agent_scheduled_each_tick": gate(m.MeasuredTicks > 0, m.SchedulingMisses == 0), "unaffected_following_agent_cadence": gate(m.MotorCalls > 0, m.CadenceMisses == 0), "temporary_scratch_allocations": m.ScratchGate, "retired_generations_bounded": gate(true, m.MaxRetired <= 2)}
	activeShare := 0.0
	if m.EligibleAgentTicks > 0 {
		activeShare = float64(m.ActiveAgentTicks) / float64(m.EligibleAgentTicks)
	}
	absoluteActiveShare, quarantineShare := 0.0, 0.0
	if m.AgentTicks > 0 {
		absoluteActiveShare = float64(m.ActiveAgentTicks) / float64(m.AgentTicks)
		quarantineShare = float64(m.QuarantinedAgentTicks) / float64(m.AgentTicks)
	}
	gates["active_following_or_traversing_share"] = gate(m.EligibleAgentTicks > 0, activeShare >= o.MinActiveShare)
	if f.Config.Family == "capacity" {
		gates["all_agents_move_each_complete_measured_tick"] = gate(m.Displacement.MeasuredTicks > 0, m.Displacement.InvalidAgentTicks == 0 && m.Displacement.MinNonHostMovingAgents == uint64(o.Agents))
		gates["all_startup_routes_admitted_before_measurement"] = gate(true, m.CapacityStartupAdmissions == uint64(o.Agents))
		gates["path_end_to_end_p99_le_100ms_outside_quarantine"] = "not_applicable: static capacity has startup intents only and no measured-window requests; startup burst latency is recorded separately"
	}
	gates["no_failed_or_unsupported_agents"] = gate(m.MeasuredTicks > 0, m.FailedStates == 0 && m.UnsupportedStates == 0)
	gates["host_geometry_activation_resolved"] = gate(m.MeasuredTicks > 0, m.HostEjectionFailures == 0)
	gates["managed_generations_including_candidate_bounded"] = gate(m.MeasuredTicks > 0, m.MaxManagedGenerations <= 3)
	gates["complete_evidence_stream"] = gate(true, m.EvidenceWriteFailures == 0)
	gates["complete_admission_evidence"] = gate(true, m.AdmissionEvidenceDropped == 0)
	gates["host_traversal_recovery"] = gate(m.MeasuredTicks > 0, m.HostRecoveryFailures == 0)
	if f.Config.Family == "construction" && o.Duration >= time.Minute {
		gates["completed_and_cancelled_traversals_exercised"] = gate(true, m.TraversalCompleted > 0 && m.TraversalCancelled > 0)
	}
	gates["drained_retired_generations_and_candidates"] = gate(true, m.Drain.Live.CapturedWhileRunning && m.DrainRetiredGenerations == 0 && m.DrainOutstandingCandidates == 0)
	gates["live_drain_pipeline_settled"] = gate(true, m.Drain.Settled && !m.Drain.TimedOut && m.Drain.Live.settled())
	gates["memory_drain_baseline_envelope"] = gate(m.BaseHeap > 0 && m.DrainHeap > 0, m.BaselineLive.allAgentsLive() && m.Drain.Live.allAgentsLive() && m.Drain.Settled && !m.Drain.TimedOut && m.Drain.Live.settled() && m.DrainHeap <= m.BaseHeap+m.BaseHeap/4+(16<<20))
	failed := false
	for _, v := range gates {
		if v == "fail" {
			failed = true
		}
	}
	report := map[string]any{"finished_utc": time.Now().UTC(), "required_tier": o.Agents == 1000, "known_gate_failure": failed, "qualification": "unqualified until unknown gates and representative content review are resolved", "measurements": m, "tick": m.tick.summary(), "navigation_tick": m.nav.summary(), "path_end_to_end": m.path.summary(), "path_with_quarantine": m.pathQuarantined.summary(), "path_failed_observed": m.pathFailed.summary(), "path_cancelled_observed": m.pathCancelled.summary(), "path_superseded": m.pathSuperseded.summary(), "path_pending_at_measurement_end": m.pathIncomplete.summary(), "source_apply_and_post": m.rebuild.summary(), "build_queue_to_terminal_commit": m.buildCommit.summary(), "motor_cadence": m.motorGap.summary(), "quarantine_pause": m.pause.summary(), "active_following_or_traversing_share": activeShare, "gates": gates, "measurement_limits": []string{"Successful path latency uses per-call issue time and the precise runtime admission timestamp. Failed/cancelled/superseded outcomes remain separate. Gates count exact threshold violations; displayed percentile upper bounds use100microsecond bins.", "Quarantine comes from the actual per-agent service state. All agents must be scheduled; following motors are checked separately.", "Movement budget is one second to expose real navigation cost rather than enforcing the five millisecond acceptance threshold by starvation.", "Process allocations include outputs, instrumentation, rebuilds and runtime work; they do not prove zero scratch allocation.", "RSS is sampled once per second and may miss peaks; heap drain envelope is baseline plus 25 percent plus 16 MiB.", "Checkpoint/restart is after stopped workers and writes one coordinated record; restart probe is additional to measured duration."}}
	report["population"] = map[string]any{
		"description":       "simulated agents with initial destinations and configured round-robin intent renewal; every agent is scheduled at the configured tick rate",
		"configured_agents": o.Agents, "tick_rate_hz": o.TickRate, "intent_renewal_per_agent_per_second": o.Churn,
		"following_or_traversing_agent_ticks": m.ActiveAgentTicks, "all_agent_ticks": m.AgentTicks, "quarantined_agent_ticks": m.QuarantinedAgentTicks,
		"following_or_traversing_fraction_of_all_agent_ticks":          absoluteActiveShare,
		"quarantined_fraction_of_all_agent_ticks":                      quarantineShare,
		"following_or_traversing_fraction_outside_observed_quarantine": activeShare,
		"interpretation": "Following/Traversing are runtime modes, not proof of simultaneous physical motion; the conditional minimum-active-share gate is an additional harness check",
	}
	report["physical_displacement"] = m.Displacement.summary()
	if f.Config.Family == "capacity" {
		report["capacity"] = map[string]any{"routes": capacityRoutes, "query_limits": capacityQueryLimits(), "startup_admissions_before_measurement": m.CapacityStartupAdmissions, "startup_terminal_failures": m.CapacityStartupFailures, "startup_path_end_to_end": m.capacityStartupPath.summary(), "steady_state_path_requests": m.Requests, "interpretation": "Opt-in static geometry, startup goals only, normal4m/s speed. All agents must move each complete measured tick. There are no measured-window path requests; the simultaneous startup burst is a separate latency observation, not the warmed mixed-workload request SLO. Coincident actors share geometry; there is no agent-agent collision or avoidance."}
	}
	report["garbage_collection"] = map[string]any{
		"measured_cycles": m.GCPauses, "pause_nanoseconds": m.GCPauseNanoseconds, "maximum_cycle_pause_nanoseconds": m.MaxGCPauseNanoseconds,
		"pause_wall_fraction": float64(m.GCPauseNanoseconds) / float64(o.Duration), "overwritten_pause_events": m.GCPauseEventsOmitted,
		"interpretation": "Runtime pause-ring observations include cycle end wall timestamps; they do not isolate individual navigation-phase pauses. Windows Go1.26.4 GC pause durations retain the runtime's coarse clock even when navigation uses QPC. CPU fractions in periodic samples are process-lifetime values.",
	}
	report["allocation_counter_coverage"] = "Counters cover the actual baseline-to-measurement-close reads, including the final partial sampling interval and excluding drain work. AllocationWindow records both read brackets and the closing read's delay from the nominal deadline; counters cannot be sampled retrospectively at that deadline. The drain gate measures post-GC live heap, not an RSS return guarantee."
	report["live_memory_coverage"] = "BaselineLive and Drain.Live are captured on the running server with every configured actor registered and navigation attached. Drain waits at least the configured minimum and at most the timeout, requiring two consecutive settled tick ends, no source edit/posts, no queue/worker/completion/build work, and no retired generations. Quarantined pending intents may remain for unloaded columns; modes, destinations and quarantine counts are retained. Forced GC precedes each heap read. A timeout fails the live-memory gate. PostShutdownHeap is separate diagnostic evidence after cancellation and cannot qualify retained-state drainage. Historical candidates read DrainHeap only after shutdown and do not qualify this live-server memory gate. Mode indices: Idle, Pending, Following, Arrived, Blocked, Unsupported, Traversing, Failed."
	if err = writeJSON(filepath.Join(o.Out, "summary.json"), report); err != nil {
		return err
	}
	fmt.Printf("completed evidence=%s measured_ticks=%d nav_p99_upper_ms=%.2f path_p99_upper_ms=%.2f\n", o.Out, m.MeasuredTicks, m.nav.percentile(.99), m.path.percentile(.99))
	if failed && o.Agents == 1000 {
		return errGateFailure
	}
	return nil
}
func gate(measured, pass bool) string {
	if !measured {
		return "unknown"
	}
	if pass {
		return "pass"
	}
	return "fail"
}
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0644)
}

func exercise(o options, f worldgen.Fixture, worlds []*dynamic.World, m *measurements, restore *saved) (*saved, error) {
	bodyProfiles, err := fixtureBodyProfiles(f)
	if err != nil {
		return nil, err
	}
	support, err := fixtureSupportPolicy(f)
	if err != nil {
		return nil, err
	}
	backend, err := adapter.NewDynamic(worlds...)
	if err != nil {
		return nil, err
	}
	srv := golem.NewServer(golem.ServerConfig{TickRate: o.TickRate})
	var actors []*actor
	var measureStart, measureEnd time.Time
	var navSample golem.NavigationTickMetrics
	var postPrepare golem.NavigationStats
	profileRevisions := make([]uint64, len(worlds))
	pendingAdmissions := make([]admissionObservation, 0, o.Agents)
	pendingOutcomes := make([]outcomeObservation, 0, o.Agents)
	cfg := golem.NavigationConfig{PathWorkers: o.Workers, BuildWorkers: 2, BuildCommitsPerTick: o.BuildCommits, PathQueueCapacity: max(o.Agents, 256), CompletionCapacity: max(o.Agents, 256), MovementAgentsPerTick: o.Agents, MovementBudget: time.Second, RebaseAgentsPerTick: 64, RebaseBudget: time.Millisecond, PathResultsPerTick: o.Agents, PathDrainBudget: o.PathDrainBudget, BuildDrainBudget: time.Millisecond, RequiredProfiles: []surface.ProfileID{"human", "large"}}
	if f.Config.Family == "capacity" {
		limits := capacityQueryLimits()
		cfg.Limits = surface.Limits{Nodes: limits.Nodes, PathPolygons: limits.PathPolygons, Corners: limits.Corners}
	}
	if err := writeJSON(filepath.Join(o.Out, "runtime-config.json"), map[string]any{"query_limits": cfg.Limits, "query_limits_zero_mean_recorded_runtime_default": true, "path_workers": cfg.PathWorkers, "build_workers": cfg.BuildWorkers, "build_commits_per_tick": cfg.BuildCommitsPerTick, "path_queue_capacity": cfg.PathQueueCapacity, "completion_capacity": cfg.CompletionCapacity, "movement_agents_per_tick": cfg.MovementAgentsPerTick, "movement_budget_ns": cfg.MovementBudget, "rebase_agents_per_tick": cfg.RebaseAgentsPerTick, "rebase_budget_ns": cfg.RebaseBudget, "build_retry_limit": 3, "path_results_per_tick": cfg.PathResultsPerTick, "path_drain_budget_ns": cfg.PathDrainBudget, "build_drain_budget_ns": cfg.BuildDrainBudget, "required_profiles": cfg.RequiredProfiles, "support_policy": support, "profile_dimensions": fixtureProfileEvidence(f, bodyProfiles), "other_settings": "runtime defaults in recorded candidate source"}); err != nil {
		return nil, err
	}
	measured := false
	cfg.ObservePathAdmission = func(event golem.NavigationPathAdmissionMetrics) {
		index := int(event.EntityID) - 1
		if index < 0 || index >= len(actors) {
			return
		}
		a := actors[index]
		if !a.requested {
			return
		}
		elapsed := event.AdmittedAt.Sub(a.requestAt)
		if f.Config.Family == "capacity" && event.AdmittedAt.Before(measureStart) {
			m.CapacityStartupAdmissions++
			m.capacityStartupPath.add(elapsed)
		}
		inWindow := !a.requestAt.Before(measureStart) && event.AdmittedAt.Before(measureEnd)
		if inWindow {
			if a.requestQuarantined {
				m.pathQuarantined.add(elapsed)
			} else {
				m.path.add(elapsed)
			}
			m.Resolved++
			m.AdmittedRequests++
			m.requestOutcome(a, "admitted", event.Tick, event.AdmittedAt)
		} else if !a.requestAt.Before(measureStart) && a.requestAt.Before(measureEnd) && !event.AdmittedAt.Before(measureEnd) {
			// prepare() can admit a result immediately before OnTick closes the
			// measurement window; it was still outstanding at the exact boundary.
			m.Unresolved++
			m.PendingAtMeasurementEnd++
			m.pathIncomplete.add(measureEnd.Sub(a.requestAt))
			observation := m.requestOutcome(a, "pending_at_measurement_end", event.Tick, measureEnd)
			if len(pendingOutcomes) < cap(pendingOutcomes) {
				pendingOutcomes = append(pendingOutcomes, observation)
			} else {
				m.AdmissionEvidenceDropped++
			}
		}
		observation := admissionObservation{EntityID: a.ID, Tick: event.Tick, Generation: event.Generation, WorldRevision: event.WorldRevision, IssuedAt: a.requestAt, AdmittedAt: event.AdmittedAt, Elapsed: elapsed, Quarantined: a.requestQuarantined, Measured: inWindow}
		if len(pendingAdmissions) < cap(pendingAdmissions) {
			pendingAdmissions = append(pendingAdmissions, observation)
		} else {
			m.AdmissionEvidenceDropped++
		}
		a.requested = false
	}
	cfg.ObserveTick = func(t golem.NavigationTickMetrics) {
		navSample = t
		if measured {
			m.nav.add(t.Prepare + t.Movement + t.Reconcile)
			if t.AgentsScheduled < o.Agents {
				m.SchedulingMisses++
				m.AgentsNotScheduled += uint64(o.Agents - t.AgentsScheduled)
			}
		}
	}
	if err = srv.SetSurfaceNavigation(backend, cfg); err != nil {
		return nil, err
	}
	anchors := make([][]surface.Vec3, len(worlds))
	for i, w := range worlds {
		q, e := w.Snapshot().NewQuery(navigation.Limits{})
		if e != nil {
			return nil, e
		}
		for _, p := range f.Anchors {
			hit := q.Project(context.Background(), p, navigation.ProjectionOptions{Horizontal: 1, Up: 1, Down: 1}, navigation.Filter{})
			if hit.Code == navigation.Complete {
				v := hit.Surface.Position()
				anchors[i] = append(anchors[i], surface.Vec3{X: v.X, Y: v.Y, Z: v.Z})
			}
		}
		if len(anchors[i]) < 2 {
			return nil, fmt.Errorf("profile %d has fewer than two valid anchors", i)
		}
	}
	initialDesired, _, _ := worlds[0].Desired()
	physicalSource, err := f.Scene.Resolve(geom.Vec3{}, true)
	if err != nil {
		return nil, err
	}
	bodies := &collisionWorld{volumes: append([]geom.Volume(nil), initialDesired.Volumes...), triangles: physicalSource.Triangles, index: geom.NewIndex(physicalSource.Triangles), disabled: make(map[string]bool)}
	bodies.rebuildBuckets()
	for _, instance := range initialDesired.Instances {
		bodies.disabled[instance.ID] = instance.Disabled
	}
	actors = make([]*actor, o.Agents)
	for i := range actors {
		profile := i % 2
		a := &actor{ID: int64(i + 1), Profile: profile, PositionValue: anchors[profile][i%len(anchors[profile])]}
		if restore != nil {
			a.PositionValue = restore.Actors[i].Position
			a.Profile = restore.Actors[i].Profile
		}
		a.motor = &bodyMotor{actor: a, metrics: m, tick: func() uint64 { return srv.Tick() }, radius: bodyProfiles[profile].Radius, height: bodyProfiles[profile].Height, bodies: bodies, tickDuration: time.Second / time.Duration(o.TickRate), measured: func() bool { return measured }}
		actors[i] = a
		if err = srv.CreateEntity(a); err != nil {
			return nil, err
		}
		if err = srv.AttachNavigation(a.ID, golem.NavigationAgentConfig{Profile: surface.ProfileID(f.Profiles[profile].Profile.ID), Speed: agentSpeed, Acceleration: 12, StoppingDistance: .2, SupportTolerance: support.Tolerances[f.Profiles[profile].Profile.ID], Projection: surface.ProjectionOptions{Horizontal: 1, Up: .8, Down: .8}, Motor: a.motor, Traversal: &traversal{actor: a, m: m, measured: func() bool { return measured }}}); err != nil {
			return nil, err
		}
		if restore == nil {
			a.requestAt = golem.NavigationNow()
			if err = srv.SetNavigationDestination(a.ID, anchors[profile][(i+len(anchors[profile])/2)%len(anchors[profile])]); err != nil {
				return nil, err
			}
			a.requested = true
		}
	}
	if restore != nil {
		if err = srv.BeginNavigationRestore(); err != nil {
			return nil, err
		}
		if err = srv.RestoreNavigationIntents(restore.Navigation, restore.Navigation.SourceFingerprint); err != nil {
			return nil, err
		}
		if err = srv.CompleteNavigationRestore(nil); err != nil {
			return nil, err
		}
	}
	log, err := os.Create(filepath.Join(o.Out, "samples.jsonl"))
	if err != nil {
		return nil, err
	}
	defer log.Close()
	encoder := json.NewEncoder(log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	jobs := make(chan editJob, 64)
	results := make(chan editResult, 64)
	var physicalPosts atomic.Int64
	var submittedSourceEdits, completedSourceEdits uint64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		physical := make(map[string]geom.Volume)
		for _, v := range initialDesired.Volumes {
			physical[v.ID] = v
		}
		for {
			select {
			case <-ctx.Done():
				return
			case job := <-jobs:
				started := golem.NavigationNow()
				var e error
				change := changes(f, job.sequence)
				e = applyProfileEdits(f, worlds, job.sequence, change, func() error {
					var physicalSnapshot []geom.Volume
					var activations []geom.Volume
					for _, v := range change.Volumes {
						previous, exists := physical[v.ID]
						if !v.Disabled && v.Exclude && (!exists || previous.Disabled || previous.Bounds != v.Bounds) {
							activations = append(activations, v)
						}
						physical[v.ID] = v
					}
					for _, v := range physical {
						physicalSnapshot = append(physicalSnapshot, v)
					}
					sort.Slice(physicalSnapshot, func(i, j int) bool { return physicalSnapshot[i].ID < physicalSnapshot[j].ID })
					physicalPosts.Add(1)
					postErr := srv.Post(func(s *golem.Server) {
						defer physicalPosts.Add(-1)
						bodies.volumes = physicalSnapshot
						bodies.rebuildBuckets()
						bodies.lastEdit = job.sequence
						for _, instance := range change.Instances {
							bodies.disabled[instance.ID] = instance.Disabled
						}
						for _, a := range actors {
							margin := math.Ceil(a.motor.radius/f.Config.CellSize)*f.Config.CellSize + f.Config.CellSize
							affected := false
							for _, v := range activations {
								if overlapsVolume(v, a.PositionValue, margin, a.motor.height) {
									affected = true
									break
								}
							}
							if !affected {
								continue
							}
							after, moved, clear := bodies.dislodge(a.PositionValue, margin, a.motor.height)
							if !moved && clear {
								continue
							}
							before := a.PositionValue
							if clear {
								a.hostMotionTick = s.Tick()
								a.SetPosition3D(float32(after.X), float32(after.Y), float32(after.Z))
								if err := s.ReprojectNavigation(a.ID); err != nil {
									clear = false
									fmt.Fprintf(os.Stderr, "host ejection reproject: %v\n", err)
								}
							}
							if measured {
								if clear {
									m.HostEjections++
								} else {
									m.HostEjectionFailures++
								}
							}
							observation := ejectionObservation{Tick: s.Tick(), Edit: job.sequence, EntityID: a.ID, Measured: measured, Resolved: clear, Before: before, After: a.PositionValue, ClearanceMargin: margin}
							if err := encoder.Encode(map[string]any{"event": "host_ejection", "observation": observation}); err != nil {
								m.EvidenceWriteFailures++
							}
							if len(m.Ejections) < 256 {
								m.Ejections = append(m.Ejections, observation)
							} else {
								m.EjectionObservationsOmitted++
							}
						}
						for profile, target := range worlds {
							world := target
							_, queueErr := s.QueueNavigationBuild(fmt.Sprintf("profile-%d", profile), 1, func(c context.Context) (golem.NavigationBuildResult, error) { return adapter.PrepareBuild(c, world) })
							if queueErr != nil {
								if measured {
									m.RejectedEdits++
								}
								fmt.Fprintf(os.Stderr, "queue build: %v\n", queueErr)
							}
						}
					})
					if postErr != nil {
						physicalPosts.Add(-1)
					}
					return postErr
				})
				select {
				case results <- editResult{job.sequence, golem.NavigationNow().Sub(started), e}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	started := golem.NavigationNow()
	civilStarted := time.Now()
	measureStart = started.Add(o.Warmup)
	measureEnd = measureStart.Add(o.Duration)
	gcCivilStart := civilStarted.Add(o.Warmup)
	gcCivilEnd := gcCivilStart.Add(o.Duration)
	m.Drain.Minimum, m.Drain.Timeout = o.Drain, o.DrainTimeout
	nextSample := started
	nextBurst := started.Add(o.BurstInterval)
	lastChurn, lastEdits := started, started
	var churnDebt, editDebt float64
	var sequence uint64
	cursor := 0
	var baseline runtime.MemStats
	var allocationSamples allocationCounter
	var lastGCCycle uint32
	recordGCPauses := func(stats *runtime.MemStats) {
		m.GCPauseEventsOmitted += forEachGCPause(stats, lastGCCycle, func(observation gcPauseObservation) {
			ended := time.Unix(0, int64(observation.EndUnixNano))
			observation.Measured = !ended.Before(gcCivilStart) && ended.Before(gcCivilEnd)
			if observation.Measured {
				m.GCPauses++
				m.GCPauseNanoseconds += observation.PauseNanosecond
				m.MaxGCPauseNanoseconds = max(m.MaxGCPauseNanoseconds, observation.PauseNanosecond)
			}
			if err := encoder.Encode(map[string]any{"event": "gc_pause", "observation": observation}); err != nil {
				m.EvidenceWriteFailures++
			}
		})
		lastGCCycle = stats.NumGC
	}
	var tickError error
	var tickStarted time.Time
	srv.OnTickStart(func(uint64) {
		tickStarted = golem.NavigationNow()
		for _, a := range actors {
			a.tickPosition = a.PositionValue
		}
	})
	var queues golem.NavigationStats
	completedBuilds := make([]uint64, len(worlds))
	observedBuilds := make([]buildProgress, len(worlds))
	srv.OnTick(func(dt float64, s *golem.Server) {
		now := golem.NavigationNow()
		postPrepare = s.NavigationStats()
		for _, observation := range pendingAdmissions {
			if err := encoder.Encode(map[string]any{"event": "path_admitted", "observation": observation}); err != nil {
				m.EvidenceWriteFailures++
			}
		}
		pendingAdmissions = pendingAdmissions[:0]
		for _, observation := range pendingOutcomes {
			m.writeRequestOutcome(encoder, observation)
		}
		pendingOutcomes = pendingOutcomes[:0]
		if !measured && !now.Before(measureStart) && now.Before(measureEnd) {
			runtime.GC()
			m.BaselineLive = captureLiveState(s, actors, worlds, submittedSourceEdits, completedSourceEdits, physicalPosts.Load())
			m.AllocationWindow.NominalStart, m.AllocationWindow.NominalEnd = measureStart, measureEnd
			m.AllocationWindow.StartReadBefore = golem.NavigationNow()
			runtime.ReadMemStats(&baseline)
			m.AllocationWindow.StartReadAfter = golem.NavigationNow()
			m.BaseHeap = baseline.HeapAlloc
			m.BaselineLive.HeapBytes = baseline.HeapAlloc
			m.BaselineLive.RSSBytes, m.BaselineLive.RSSAvailable = residentBytes()
			allocationSamples.start(baseline.TotalAlloc, baseline.Mallocs)
			measured = true
			for _, a := range actors {
				a.requested = false
				a.lastMotor = 0
				a.pauseStarted = time.Time{}
			}
		}
		if measured && !now.Before(measureEnd) {
			var endCounters runtime.MemStats
			m.AllocationWindow.EndReadBefore = golem.NavigationNow()
			runtime.ReadMemStats(&endCounters)
			m.AllocationWindow.EndReadAfter = golem.NavigationNow()
			m.AllocationWindow.EndReadDelay = m.AllocationWindow.EndReadBefore.Sub(measureEnd)
			bytes, objects := allocationSamples.finish(endCounters.TotalAlloc, endCounters.Mallocs)
			m.AllocationWindow.TailBytes, m.AllocationWindow.TailObjects = bytes, objects
			m.AllocBytes, m.AllocObjects = m.AllocBytes+bytes, m.AllocObjects+objects
			m.PeakHeap = max(m.PeakHeap, endCounters.HeapAlloc)
			recordGCPauses(&endCounters)
			for _, a := range actors {
				if a.requested {
					m.Unresolved++
					m.PendingAtMeasurementEnd++
					m.pathIncomplete.add(measureEnd.Sub(a.requestAt))
					m.writeRequestOutcome(encoder, m.requestOutcome(a, "pending_at_measurement_end", s.Tick(), measureEnd))
					a.requested = false
				}
				if !a.pauseStarted.IsZero() {
					m.pause.add(measureEnd.Sub(a.pauseStarted))
					a.pauseStarted = time.Time{}
				}
			}
			measured = false
		}
		for {
			select {
			case r := <-results:
				completedSourceEdits++
				if measured {
					m.rebuild.add(r.elapsed)
					if r.err != nil {
						m.RejectedEdits++
					} else {
						m.CompletedEdits++
					}
				}
				if r.err != nil && !errors.Is(r.err, dynamic.ErrSuperseded) {
					fmt.Fprintf(os.Stderr, "edit %d: %v\n", r.sequence, r.err)
				}
			default:
				goto drained
			}
		}
	drained:
		for p := range worlds {
			retention := worlds[p].RetentionStats()
			profileRevisions[p] = retention.CommittedRevision
			managed := 1 + retention.RetiredGenerations
			if retention.Building || retention.Prepared {
				managed++
			}
			if managed > m.MaxManagedGenerations {
				m.MaxManagedGenerations = managed
			}
			state, ok := s.NavigationBuildStatus(fmt.Sprintf("profile-%d", p))
			if ok {
				progress := buildProgress{Generation: state.Generation, Attempts: state.Attempts, Pending: state.Pending}
				if state.Err != nil {
					progress.Error = state.Err.Error()
				}
				if progress != observedBuilds[p] {
					observedBuilds[p] = progress
					if state.Err != nil {
						busy := errors.Is(state.Err, dynamic.ErrBusy)
						if measured && busy {
							m.BuildBusyObservations++
							if !state.Pending {
								m.BuildBusyFailures++
							}
						}
						if err := encoder.Encode(map[string]any{"event": "build_error", "tick": s.Tick(), "profile": p, "measured": measured, "progress": progress, "busy": busy, "queued_at": state.QueuedAt, "next_attempt": state.NextAttempt, "completed_at": state.CompletedAt}); err != nil {
							m.EvidenceWriteFailures++
						}
					}
				}
			}
			if ok && !state.Pending && state.Generation != completedBuilds[p] {
				completedBuilds[p] = state.Generation
				if measured && !state.QueuedAt.Before(measureStart) {
					m.buildCommit.add(state.CompletedAt.Sub(state.QueuedAt))
					if state.Committed {
						m.BuildCommits++
					} else {
						m.BuildFailures++
					}
				}
			}
		}
		for _, a := range actors {
			if a.recoveryPending || a.recovering {
				if err := recoverBody(a, s, bodies, f.Config.CellSize, dt, measured, m, encoder); err != nil {
					tickError = err
					cancel()
					return
				}
			}
		}
		if now.Before(measureEnd) {
			churnDebt += now.Sub(lastChurn).Seconds() * o.Churn * float64(o.Agents)
			for churnDebt >= 1 {
				a := actors[cursor%len(actors)]
				cursor++
				if a.recovering || a.recoveryPending {
					if measured {
						m.ChurnSuppressedDuringRecovery++
					}
					churnDebt--
					continue
				}
				p := a.Profile
				if a.requested && measured {
					m.Unresolved++
					m.SupersededRequests++
					m.pathSuperseded.add(golem.NavigationNow().Sub(a.requestAt))
					m.writeRequestOutcome(encoder, m.requestOutcome(a, "superseded", s.Tick(), golem.NavigationNow()))
				}
				destination := anchors[p][(cursor*17+int(sequence))%len(anchors[p])]
				if f.Config.Family == "construction" && cursor%31 == 0 {
					target := f.TraversalEnd
					if cursor%62 == 0 {
						target = f.UnaffectedTraversalEnd
					}
					destination = surface.Vec3{X: target.X, Y: target.Y, Z: target.Z}
				}
				a.requestAt = golem.NavigationNow()
				if e := s.SetNavigationDestination(a.ID, destination); e != nil {
					tickError = e
					cancel()
					return
				}
				a.requested = true
				a.requestQuarantined = false
				if measured {
					m.Requests++
				}
				churnDebt--
			}
			lastChurn = now
			editDebt += now.Sub(lastEdits).Seconds() * o.Edits
			lastEdits = now
			if !now.Before(nextBurst) {
				editDebt += float64(o.Burst)
				nextBurst = nextBurst.Add(o.BurstInterval)
			}
			for editDebt >= 1 {
				sequence++
				select {
				case jobs <- editJob{sequence}:
					submittedSourceEdits++
					if measured {
						m.SubmittedEdits++
					}
				default:
					if measured {
						m.RejectedEdits++
					}
				}
				editDebt--
			}
		}
		for _, a := range actors {
			state, _ := s.NavigationAgentState(a.ID)
			if measured {
				m.AgentTicks++
				if state.Quarantined && a.pauseStarted.IsZero() {
					a.pauseStarted = now
				} else if !state.Quarantined && !a.pauseStarted.IsZero() {
					m.pause.add(now.Sub(a.pauseStarted))
					a.pauseStarted = time.Time{}
				}
				if !state.Quarantined {
					m.EligibleAgentTicks++
					if state.Mode == golem.NavigationFollowing || state.Mode == golem.NavigationTraversing {
						m.ActiveAgentTicks++
					}
				}
			}
			if a.requested {
				if state.Quarantined {
					a.requestQuarantined = true
				}
				if state.Mode != golem.NavigationPending && state.Mode != golem.NavigationIdle {
					if !measured && f.Config.Family == "capacity" && now.Before(measureStart) {
						m.CapacityStartupFailures++
						observation := (outcomeObservation{EntityID: a.ID, Tick: s.Tick(), IssuedAt: a.requestAt, ObservedAt: now, Elapsed: now.Sub(a.requestAt), Outcome: "failed", Quarantined: a.requestQuarantined}).withState(state)
						if err := encoder.Encode(map[string]any{"event": "capacity_startup_terminal", "observation": observation}); err != nil {
							m.EvidenceWriteFailures++
						}
					}
					if measured {
						if state.Status.Code == surface.Cancelled {
							m.CancelledRequests++
							m.pathCancelled.add(golem.NavigationNow().Sub(a.requestAt))
							m.writeRequestOutcome(encoder, m.requestOutcome(a, "cancelled", s.Tick(), golem.NavigationNow()).withState(state))
						} else {
							m.FailedRequests++
							m.pathFailed.add(golem.NavigationNow().Sub(a.requestAt))
							m.writeRequestOutcome(encoder, m.requestOutcome(a, "failed", s.Tick(), golem.NavigationNow()).withState(state))
						}
						m.Resolved++
					}
					a.requested = false
				}
			}
			if measured && state.Quarantined {
				m.QuarantinedAgentTicks++
			}
			if measured && state.Mode != a.lastMode {
				switch state.Mode {
				case golem.NavigationArrived:
					m.Arrivals++
				}
			}
			a.lastMode = state.Mode
			if now.Before(measureEnd) && o.Churn > 0 && state.Mode == golem.NavigationArrived {
				p := a.Profile
				destination := anchors[p][(int(a.ID)+int(s.Tick())*7)%len(anchors[p])]
				a.requestAt = golem.NavigationNow()
				if e := s.SetNavigationDestination(a.ID, destination); e != nil {
					tickError = e
					cancel()
					return
				}
				a.requested = true
				a.requestQuarantined = false
				if measured {
					m.Requests++
					m.ArrivalRequests++
				}
			}
		}
		if !now.Before(nextSample) {
			var mem runtime.MemStats
			runtime.ReadMemStats(&mem)
			recordGCPauses(&mem)
			rss, rssOK := residentBytes()
			if measured {
				if mem.HeapAlloc > m.PeakHeap {
					m.PeakHeap = mem.HeapAlloc
				}
				if rss > m.PeakRSS {
					m.PeakRSS = rss
				}
				bytes, objects := allocationSamples.sample(mem.TotalAlloc, mem.Mallocs)
				m.AllocBytes, m.AllocObjects = m.AllocBytes+bytes, m.AllocObjects+objects
			}
			queues = s.NavigationStats()
			retention := make([]dynamic.RetentionStats, len(worlds))
			for i, w := range worlds {
				retention[i] = w.RetentionStats()
				if retention[i].RetiredGenerations > m.MaxRetired {
					m.MaxRetired = retention[i].RetiredGenerations
				}
				if retention[i].Leases > m.MaxLeases {
					m.MaxLeases = retention[i].Leases
				}
			}
			if e := encoder.Encode(map[string]any{"utc": time.Now().UTC(), "elapsed_s": now.Sub(started).Seconds(), "measured": measured, "tick": s.Tick(), "heap_bytes": mem.HeapAlloc, "rss_bytes": rss, "rss_available": rssOK, "total_alloc_bytes": mem.TotalAlloc, "mallocs": mem.Mallocs, "gc_cycles": mem.NumGC, "gc_pause_total_ns": mem.PauseTotalNs, "gc_cpu_fraction_since_process_start": mem.GCCPUFraction, "queues": queues, "retention": retention}); e != nil {
				tickError = e
				cancel()
			}
			nextSample = now.Add(time.Second)
		}
		_ = dt
	})
	srv.OnTickEnd(func(tick uint64, standardWall time.Duration) {
		preciseEnd := golem.NavigationNow()
		wall := preciseEnd.Sub(tickStarted)
		tickObservedEnd := time.Now().UTC()
		displacement := tickDisplacement{Measured: fullMeasuredTick(tickStarted, preciseEnd, measureStart, measureEnd)}
		for _, a := range actors {
			state, _ := srv.NavigationAgentState(a.ID)
			displacement.observe(a.tickPosition, a.PositionValue, state, a.hostMotionTick == tick)
			if a.requested && state.Quarantined {
				a.requestQuarantined = true
			}
			m.recordTerminalTransition(tick, measured, a, state, bodies)
		}
		m.Displacement.add(displacement)
		if !measured {
			if !preciseEnd.Before(measureEnd) {
				live := captureLiveState(srv, actors, worlds, submittedSourceEdits, completedSourceEdits, physicalPosts.Load())
				if m.Drain.observe(preciseEnd.Sub(measureEnd), live) {
					runtime.GC()
					m.Drain.Live = captureLiveState(srv, actors, worlds, submittedSourceEdits, completedSourceEdits, physicalPosts.Load())
					var drainedMem runtime.MemStats
					runtime.ReadMemStats(&drainedMem)
					m.DrainHeap, m.Drain.Live.HeapBytes = drainedMem.HeapAlloc, drainedMem.HeapAlloc
					m.Drain.Live.RSSBytes, m.Drain.Live.RSSAvailable = residentBytes()
					recordGCPauses(&drainedMem)
					for _, profile := range m.Drain.Live.Profiles {
						m.DrainRetiredGenerations += profile.Retention.RetiredGenerations
						if profile.Retention.Building || profile.Retention.Prepared {
							m.DrainOutstandingCandidates++
						}
					}
					if err := encoder.Encode(map[string]any{"event": "live_memory_drain", "observation": m.Drain}); err != nil {
						m.EvidenceWriteFailures++
					}
					cancel()
				}
			}
			return
		}
		if err := encoder.Encode(map[string]any{"event": "navigation_tick", "utc": time.Now().UTC(), "tick_observed_end": tickObservedEnd, "tick_observed_end_unix_nano": tickObservedEnd.UnixNano(), "tick_wall_ns": wall, "server_reported_standard_tick_wall_ns": standardWall, "measured": true, "metrics": navSample, "physical_displacement": displacement, "profile_revisions": profileRevisions, "queues_after_prepare": postPrepare, "queues_after_movement": srv.NavigationStats()}); err != nil {
			m.EvidenceWriteFailures++
		}
		m.tick.add(wall)
		m.MeasuredTicks++
		if wall > time.Second/time.Duration(o.TickRate) {
			m.Overruns++
		}
		for _, a := range actors {
			state, _ := srv.NavigationAgentState(a.ID)
			if !state.Quarantined && state.Mode == golem.NavigationFollowing && state.Velocity.X*state.Velocity.X+state.Velocity.Z*state.Velocity.Z > .01 && a.lastMotor != tick {
				m.CadenceMisses++
			}
		}
	})
	err = srv.Run(ctx)
	cancel()
	wg.Wait()
	if tickError != nil {
		return nil, tickError
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}
	runtime.GC()
	var drainedMem runtime.MemStats
	runtime.ReadMemStats(&drainedMem)
	recordGCPauses(&drainedMem)
	m.PostShutdownHeap = drainedMem.HeapAlloc
	id := fmt.Sprintf("qualification-%d", time.Now().UnixNano())
	out := &saved{Version: 1, ID: id}
	for _, a := range actors {
		out.Actors = append(out.Actors, savedActor{a.ID, a.PositionValue, a.Profile})
	}
	for _, w := range worlds {
		b, e := w.Checkpoint(id)
		if e != nil {
			return nil, e
		}
		out.Worlds = append(out.Worlds, b)
	}
	scene, _, rev := worlds[0].Desired()
	fingerprint, e := scene.Fingerprint()
	if e != nil {
		return nil, e
	}
	out.Navigation, e = srv.CaptureNavigationCheckpoint(id, fingerprint, rev, out.Worlds[0])
	if e != nil {
		return nil, e
	}
	return out, nil
}

func changes(f worldgen.Fixture, n uint64) dynamic.Changes {
	v := geom.Volume{ID: fmt.Sprintf("edited-blocker-%d", n%4), Shape: "box", Bounds: geom.Bounds{Min: geom.Vec3{X: float64(n%3) - 2, Y: -2, Z: 5}, Max: geom.Vec3{X: float64(n%3) + 2, Y: 4, Z: 7}}, Exclude: true, Disabled: n%8 >= 4}
	c := dynamic.Changes{Volumes: []geom.Volume{v}}
	if n%10 == 0 {
		for _, i := range f.Scene.Instances {
			if i.ID == f.BridgeInstance {
				i.Revision = 0
				i.Disabled = n%20 == 0
				c.Instances = append(c.Instances, i)
			}
		}
	}
	if f.Config.Family == "construction" {
		c.Volumes = append(c.Volumes, geom.Volume{ID: "overlap-a", Shape: "box", Bounds: geom.Bounds{Min: geom.Vec3{X: -3, Y: -1, Z: -2}, Max: geom.Vec3{X: 1, Y: 4, Z: 2}}, Exclude: true, Disabled: n%4 >= 2}, geom.Volume{ID: "overlap-b", Shape: "box", Bounds: geom.Bounds{Min: geom.Vec3{X: -1, Y: -1, Z: -2}, Max: geom.Vec3{X: 3, Y: 4, Z: 2}}, Exclude: true, Disabled: n%4 == 3})
	}
	return c
}

func bakeKeys(f worldgen.Fixture, p int) ([]artifact.TileKey, error) {
	c := f.Profiles[p]
	size := c.Settings.CellSize * float64(c.Settings.TileSize)
	return []artifact.TileKey{{X: int32(math.Floor(-f.Config.HalfExtent / size)), Z: int32(math.Floor(-f.Config.HalfExtent / size))}}, nil
}

type bodyMotor struct {
	actor        *actor
	metrics      *measurements
	tick         func() uint64
	radius       float64
	height       float64
	bodies       *collisionWorld
	tickDuration time.Duration
	measured     func() bool
}

func (m *bodyMotor) MoveNavigation(v golem.NavigationMotion) (surface.Vec3, error) {
	tick := m.tick()
	if m.measured() {
		m.metrics.MotorCalls++
		if m.actor.lastMotor > 0 && m.actor.lastMotor != tick {
			m.metrics.motorGap.add(time.Duration(tick-m.actor.lastMotor) * m.tickDuration)
		}
	}
	m.actor.lastMotor = tick
	// Accepted source blockers become body collision before tick movement,
	// independently of the asynchronous mesh rebuild's completion.
	t := m.bodies.sweepFraction(v, m.radius, m.height)
	if t < 1 {
		if m.measured() {
			m.metrics.MotorBlocked++
		}
		t = math.Max(0, t-.001)
		return surface.Vec3{X: v.From.X + (v.Target.X-v.From.X)*t, Y: v.From.Y + (v.Target.Y-v.From.Y)*t, Z: v.From.Z + (v.Target.Z-v.From.Z)*t}, nil
	}
	return v.Target, nil
}

type collisionWorld struct {
	volumes   []geom.Volume
	buckets   map[collisionCell]uint64
	lastEdit  uint64
	triangles []geom.ResolvedTriangle
	index     *geom.Index
	disabled  map[string]bool
}

func (w *collisionWorld) overlaps(p surface.Vec3, radius, height float64) []string {
	var ids []string
	for _, v := range w.volumes {
		if overlapsVolume(v, p, radius, height) {
			ids = append(ids, v.ID)
		}
	}
	return ids
}

func overlapsVolume(v geom.Volume, p surface.Vec3, radius, height float64) bool {
	return !v.Disabled && v.Exclude && cylinderOverlapsBox(p, radius, height, v.Bounds)
}

// dislodge is host collision policy for geometry spawned around a body. It
// finds the nearest clear axis boundary in the finite blocker set, with a
// voxel-clearance margin supplied by the host. It never projects onto a mesh.
func (w *collisionWorld) dislodge(p surface.Vec3, margin, height float64) (surface.Vec3, bool, bool) {
	inside := func(q surface.Vec3) bool {
		for _, v := range w.volumes {
			if overlapsVolume(v, q, margin, height) {
				return true
			}
		}
		return false
	}
	if !inside(p) {
		return p, false, true
	}
	best, distance := p, math.Inf(1)
	for _, v := range w.volumes {
		if v.Disabled || !v.Exclude || p.Y > v.Bounds.Max.Y || p.Y+height < v.Bounds.Min.Y {
			continue
		}
		for _, q := range [4]surface.Vec3{{X: v.Bounds.Min.X - margin - .01, Y: p.Y, Z: p.Z}, {X: v.Bounds.Max.X + margin + .01, Y: p.Y, Z: p.Z}, {X: p.X, Y: p.Y, Z: v.Bounds.Min.Z - margin - .01}, {X: p.X, Y: p.Y, Z: v.Bounds.Max.Z + margin + .01}} {
			d := (p.X-q.X)*(p.X-q.X) + (p.Z-q.Z)*(p.Z-q.Z)
			if d < distance && !inside(q) {
				best, distance = q, d
			}
		}
	}
	return best, true, !math.IsInf(distance, 1)
}

func (w *collisionWorld) landing(p surface.Vec3, fromY float64) (float64, bool) {
	best, found := math.Inf(-1), false
	box := geom.Bounds{Min: geom.Vec3{X: p.X, Y: p.Y - .01, Z: p.Z}, Max: geom.Vec3{X: p.X, Y: fromY + .02, Z: p.Z}}
	for _, i := range w.index.Query(box) {
		tr := w.triangles[i]
		if w.disabled[tr.ObjectID] {
			continue
		}
		a, b, c := tr.Vertices[0], tr.Vertices[1], tr.Vertices[2]
		normalY := (b.Z-a.Z)*(c.X-a.X) - (b.X-a.X)*(c.Z-a.Z)
		if normalY <= 1e-12 {
			continue
		}
		den := (b.Z-c.Z)*(a.X-c.X) + (c.X-b.X)*(a.Z-c.Z)
		u := ((b.Z-c.Z)*(p.X-c.X) + (c.X-b.X)*(p.Z-c.Z)) / den
		v := ((c.Z-a.Z)*(p.X-c.X) + (a.X-c.X)*(p.Z-c.Z)) / den
		if u < -1e-8 || v < -1e-8 || u+v > 1+1e-8 {
			continue
		}
		y := u*a.Y + v*b.Y + (1-u-v)*c.Y
		if y >= p.Y-.01 && y <= fromY+.02 && y > best {
			best, found = y, true
		}
	}
	return best, found
}

func recoverBody(a *actor, srv *golem.Server, bodies *collisionWorld, cell, dt float64, measured bool, m *measurements, evidence *json.Encoder) error {
	if a.recoveryPending && !a.recovering {
		state, _ := srv.NavigationAgentState(a.ID)
		a.resumeDestination, a.resumeHasDestination = state.Destination, state.HasDestination
		if err := srv.StopNavigation(a.ID); err != nil {
			return err
		}
		a.recovering, a.recoveryPending, a.fallVelocity = true, false, 0
		if measured {
			m.HostFallsStarted++
			if a.requested {
				m.Unresolved++
				m.CancelledRequests++
				m.pathCancelled.add(golem.NavigationNow().Sub(a.requestAt))
				m.writeRequestOutcome(evidence, m.requestOutcome(a, "cancelled", srv.Tick(), golem.NavigationNow()))
			}
		}
		a.requested = false
		if err := evidence.Encode(map[string]any{"event": "host_fall_started", "tick": srv.Tick(), "entity": a.ID, "position": a.PositionValue, "measured": measured}); err != nil {
			m.EvidenceWriteFailures++
		}
	}
	if !a.recovering {
		return nil
	}
	if measured {
		m.HostRecoveringAgentTicks++
	}
	before := a.PositionValue
	a.fallVelocity += 9.81 * dt
	next := before
	next.Y -= a.fallVelocity * dt
	margin := math.Ceil(a.motor.radius/cell)*cell + cell
	next, _, clear := bodies.dislodge(next, margin, a.motor.height)
	if !clear || next.Y < -8 {
		m.HostRecoveryFailures++
		a.recovering = false
		return nil
	}
	landing, landed := bodies.landing(next, before.Y)
	if landed {
		next.Y = landing
	}
	a.hostMotionTick = srv.Tick()
	a.SetPosition3D(float32(next.X), float32(next.Y), float32(next.Z))
	if !landed {
		return nil
	}
	a.recovering, a.recoveryPending = false, false
	if measured {
		m.HostLandings++
	}
	if err := evidence.Encode(map[string]any{"event": "host_landed", "tick": srv.Tick(), "entity": a.ID, "position": a.PositionValue, "measured": measured}); err != nil {
		m.EvidenceWriteFailures++
	}
	if err := srv.ReprojectNavigation(a.ID); err != nil {
		return err
	}
	if a.resumeHasDestination {
		a.requestAt = golem.NavigationNow()
		if err := srv.SetNavigationDestination(a.ID, a.resumeDestination); err != nil {
			return err
		}
		a.requested, a.requestQuarantined = true, false
		if measured {
			m.Requests++
		}
	}
	return nil
}

type traversal struct {
	actor    *actor
	m        *measurements
	measured func() bool
}

func (t *traversal) BeginNavigationTraversal(r golem.NavigationTraversalRequest) (golem.NavigationTraversalAction, error) {
	if t.measured() {
		t.m.TraversalBegins++
	}
	return &action{actor: t.actor, from: r.Link.From, to: r.Link.To, m: t.m, measured: t.measured}, nil
}

type action struct {
	actor    *actor
	from, to surface.Vec3
	elapsed  float64
	m        *measurements
	measured func() bool
	done     bool
}

func (a *action) AdvanceNavigationTraversal(dt float64) (golem.NavigationTraversalUpdate, error) {
	a.elapsed += dt
	t := math.Min(1, a.elapsed)
	if t == 1 && !a.done {
		a.done = true
		if a.measured() {
			a.m.TraversalCompleted++
		}
	}
	return golem.NavigationTraversalUpdate{Position: surface.Vec3{X: a.from.X + (a.to.X-a.from.X)*t, Y: a.from.Y + (a.to.Y-a.from.Y)*t, Z: a.from.Z + (a.to.Z-a.from.Z)*t}, Complete: t == 1}, nil
}
func (a *action) CancelNavigationTraversal(string) {
	if a.actor != nil && !a.done {
		a.actor.recoveryPending = true
	}
	if a.measured() {
		a.m.TraversalCancelled++
	}
}

func (a *action) NavigationTraversalBounds() golem.NavigationTraversalBounds {
	return golem.NavigationTraversalBounds{Min: surface.Vec3{X: math.Min(a.from.X, a.to.X) - .8, Y: math.Min(a.from.Y, a.to.Y), Z: math.Min(a.from.Z, a.to.Z) - .8}, Max: surface.Vec3{X: math.Max(a.from.X, a.to.X) + .8, Y: math.Max(a.from.Y, a.to.Y) + 2.4, Z: math.Max(a.from.Z, a.to.Z) + .8}}
}
