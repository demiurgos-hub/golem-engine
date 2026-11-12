// physics-stress runs the actual Golem server with navigation and physics.
// Its synthetic combined-load result is one qualification input, never a claim
// that representative content, navigation churn or other platforms have passed.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

const timeBudget = time.Second

type options struct {
	Mode, Out, SourceManifest        string
	Resume                           string
	Checkpoint                       bool
	ChurnTicks                       uint64
	Extent                           int
	Workers                          int
	PhysicsWorkers, QueryWorkers     int
	Profiles                         bool
	DebugCapture                     bool
	Warmup, Duration, StartupTimeout time.Duration
}

func main() {
	o := options{}
	flag.StringVar(&o.Mode, "mode", "correctness", "correctness, rehearsal, or full (fixed workload counts)")
	flag.StringVar(&o.Out, "out", "", "new output directory in the validation workspace")
	flag.StringVar(&o.SourceManifest, "source-manifest", "", "exact staged source manifest; required for full mode")
	flag.StringVar(&o.Resume, "resume", "", "verified paired checkpoint directory to restore before starting workers")
	flag.BoolVar(&o.Checkpoint, "checkpoint", false, "capture complete paired physics/navigation/projectile state at shutdown")
	flag.Uint64Var(&o.ChurnTicks, "churn-ticks", 0, "attempt a door create/replace/unload every N ticks, after the preceding publication completes; zero disables")
	flag.IntVar(&o.Extent, "extent", 0, "world side length: full mode requires 768 or 2304 metres")
	flag.IntVar(&o.Workers, "workers", 4, "navigation path workers")
	flag.IntVar(&o.PhysicsWorkers, "physics-workers", 6, "rigid-body solver workers")
	flag.IntVar(&o.QueryWorkers, "query-workers", 6, "bounded gameplay query workers")
	flag.BoolVar(&o.Profiles, "profiles", false, "record CPU and retained heap profiles; includes warmup, affects timing")
	flag.BoolVar(&o.DebugCapture, "debug-capture", false, "write bounded accepted/candidate geometry on failure; diagnostic only")
	flag.DurationVar(&o.Warmup, "warmup", 2*time.Second, "minimum warmup after server starts")
	flag.DurationVar(&o.Duration, "duration", 2*time.Second, "measurement interval; selected delivery uses a short full-size Windows run")
	flag.DurationVar(&o.StartupTimeout, "startup-timeout", 2*time.Minute, "maximum wait for all initial routes")
	flag.Parse()
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type sample struct {
	Tick                                                                       uint64
	Measured                                                                   bool
	FullTickNS, ServerWallNS, CadenceNS, PhysicsNS, SolveNS, NavigationNS      int64
	Scheduled, Following, Moving, AwakeDynamic, Projectiles, Queries           int
	Accepted                                                                   bool
	Simulated                                                                  float32
	Substeps                                                                   int
	Intervals, Microsteps                                                      int
	MotionPairTests, MotionPathRefinements                                     int
	CCDCandidatePairs, CCDPairTests, CCDPredictiveContacts                     int
	TOIContinuationCandidates, TOIContinuationPairTests, TOIContinuationEvents int
	TOIContinuationIslandBodies, TOIContinuationContactRows                    int
	Bodies, Colliders, Shapes                                                  int
	Error                                                                      string `json:",omitempty"`
}

type results struct {
	Qualified                                                                          bool
	Mode                                                                               string
	Workload                                                                           workload
	PhysicsWorkers, QueryWorkers                                                       int
	Samples, MeasuredSamples                                                           uint64
	FullTick, Cadence, Physics, Solve, Navigation                                      histogram
	StartupRouteAdmission, RenewedRouteAdmission                                       histogram
	AutomaticRouteAdmissions, UnmeasuredStartupAdmissions                              uint64
	MinMoving, MinFollowing, MinScheduled, MinAwakeDynamic, MinProjectiles, MinQueries int
	MovementDeficitTicks, CadenceMisses, RejectedFrames, QueryOverflows                uint64
	ProjectileSweeps, ProjectileHits, GameplayQueries                                  uint64
	// Native CCD and TOI continuation totals cover measured attempted frames,
	// including rejected frames. Participant/row totals count work, not peaks.
	CCDCandidatePairs, CCDPairTests, CCDPredictiveContacts                     uint64
	TOIContinuationCandidates, TOIContinuationPairTests, TOIContinuationEvents uint64
	TOIContinuationIslandBodies, TOIContinuationContactRows                    uint64
	StartupNS, MeasurementWallNS                                               int64
	CheckpointCaptureNS, CheckpointWriteNS                                     int64
	CheckpointID                                                               string
	WorstObservedTickNS, MeasurementOverrunNS                                  int64
	Churn                                                                      churnEvidence
	TotalAllocBytes, Mallocs                                                   uint64
	PeakHeapBytes, PeakRSSBytes                                                uint64
	StartMemory, EndMemory, AfterCloseGC                                       runtime.MemStats
	MinimumActorTravel                                                         float32
	Gates                                                                      map[string]bool
	RequiredGates                                                              []string
	ExcludedChecks                                                             []string
	Missing                                                                    []string
	Failure                                                                    string `json:",omitempty"`
}

func run(o options) (runErr error) {
	c, err := counts(o.Mode)
	if err != nil {
		return err
	}
	if o.Extent == 0 {
		o.Extent = 64
		if o.Mode == "full" {
			o.Extent = 768
		} else if o.Mode == "rehearsal" {
			o.Extent = 256
		}
	}
	if o.Out == "" || o.Duration <= 0 || o.Warmup < 0 || o.StartupTimeout <= o.Warmup || o.Workers < 1 || o.PhysicsWorkers < 1 || o.PhysicsWorkers > 64 || o.QueryWorkers < 1 || o.QueryWorkers > 64 || o.Extent < 64 || o.Extent > 2304 {
		return errors.New("invalid options")
	}
	if o.Mode == "full" && (o.Extent != 768 && o.Extent != 2304 || o.SourceManifest == "") {
		return errors.New("full requires extent 768 or 2304 and exact source manifest")
	}
	if _, err = os.Stat(o.Out); err == nil {
		return errors.New("output directory already exists; preserve completed runs")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(o.Out, 0755); err != nil {
		return err
	}
	required := []string{"all-moving-every-complete-tick", "awake-dynamic-count", "query-projectile-counts", "accepted-frames"}
	if o.Mode == "full" {
		required = append(required, "full-counts", "full-tick-p99-below-50ms")
	}
	if o.Checkpoint {
		required = append(required, "checkpoint-written")
	}
	if o.Resume != "" {
		required = append(required, "checkpoint-restored")
	}
	r := results{Mode: o.Mode, Workload: c, MinMoving: c.Characters, MinFollowing: c.Characters, MinScheduled: c.Characters, MinAwakeDynamic: c.DynamicBodies, MinProjectiles: c.Projectiles, MinQueries: c.Queries, Gates: map[string]bool{}, RequiredGates: required,
		ExcludedChecks: []string{"two-hour-duration", "two-hour-churn-restart-soak", "extended race/geometry/joint/mass/concurrency matrices", "duplicate full-size Linux performance run", "exhaustive content/collision parity"},
		Missing:        []string{"aggregate same-source four-gate delivery evidence", "published module resolution (separate release prerequisite)"}}
	r.PhysicsWorkers, r.QueryWorkers = o.PhysicsWorkers, o.QueryWorkers
	defer func() {
		if runErr != nil {
			r.Failure = runErr.Error()
		}
		if e := writeJSON(filepath.Join(o.Out, "results.json"), r); runErr == nil && e != nil {
			runErr = e
		}
	}()
	manifest := map[string]any{"version": 1, "startedUTC": time.Now().UTC(), "options": o, "workload": c, "goVersion": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "cpus": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0), "precision": "float32 physics/actor state; navigation existing double API only at boundary", "clock": "golem.NavigationNow; same-domain Sub", "tickRate": 20, "substeps": 4, "qualified": false, "scope": "single workload component of the selected four-gate delivery: focused collision regressions, same-source engine integration, short full-size Windows capacity and native Linux build/simulation/save/restore smoke", "requiredGates": r.RequiredGates, "excludedChecks": r.ExcludedChecks, "measurement": "full tick from first OnTickStart to first OnTickEnd; retain every frame starting in the requested measurement window, including its complete overrun; sample output excluded, start-to-start cadence includes observer cost", "geometry": "unique physical triangles; coarser analytically identical navigation plane; static field excluded from navigation", "queryMix": "25% ray, 25% sphere overlap, 25% box sweep, 25% capsule sweep, plus separate projectile sweeps", "sleep": "all requested dynamic and driven bodies explicitly disable sleep", "nativeExecution": "GOOS/GOARCH identify executable target; runner must attest native host, not emulation"}
	if info, ok := debug.ReadBuildInfo(); ok {
		manifest["buildInfo"] = info
	}
	manifest["admissionMeasurement"] = "startup: first OnTickStart to first route admission, excluding scene construction; renewed: explicitly requested matching generation only; automatic admissions counted separately with unknown duration"
	if o.SourceManifest != "" {
		data, e := os.ReadFile(o.SourceManifest)
		if e != nil {
			return e
		}
		manifest["sourceManifestSHA256"] = hash(data)
		if e = writeBytes(filepath.Join(o.Out, "source-manifest.json"), data); e != nil {
			return e
		}
	}
	if err = writeJSON(filepath.Join(o.Out, "manifest.json"), manifest); err != nil {
		return err
	}
	started := golem.NavigationNow()
	var nav golem.NavigationTickMetrics
	s, err := makeScene(o, c, func(m golem.NavigationTickMetrics) { nav = m })
	if err != nil {
		return err
	}
	if o.Resume != "" {
		if err = s.resumeCheckpoint(o); err != nil {
			_ = s.world.Close()
			return fmt.Errorf("restart: %w", err)
		}
	}
	if o.Profiles {
		file, e := os.Create(filepath.Join(o.Out, "cpu.pprof"))
		if e != nil {
			return e
		}
		if e = pprof.StartCPUProfile(file); e != nil {
			file.Close()
			return e
		}
		defer func() { pprof.StopCPUProfile(); file.Close() }()
	}
	f, err := os.Create(filepath.Join(o.Out, "samples.jsonl"))
	if err != nil {
		_ = s.world.Close()
		return err
	}
	defer f.Close()
	buffer := bufio.NewWriterSize(f, 256<<10)
	defer buffer.Flush()
	enc := json.NewEncoder(buffer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var physics golem.PhysicsFrameMetrics
	if err = s.server.ObservePhysicsFrames(func(m golem.PhysicsFrameMetrics) { physics = m }); err != nil {
		return err
	}
	var tickStart, lastStart, measureStart time.Time
	var cadence time.Duration
	serverStart := golem.NavigationNow()
	var loopErr error
	var captured *golem.PhysicsCheckpoint
	finished := false
	measuring := false
	tickMeasured := false
	var queryCount, projectileCount, projectileHits int
	s.server.OnTickStart(func(tick uint64) {
		tickStart = golem.NavigationNow()
		if s.admissionStarted.IsZero() {
			s.beginAdmissionMeasurement(tickStart)
		}
		cadence = 0
		if !lastStart.IsZero() {
			cadence = tickStart.Sub(lastStart)
		}
		lastStart = tickStart
		allAdmitted := true
		for _, a := range s.actors {
			a.before = a.position
			allAdmitted = allAdmitted && a.admitted
		}
		if !measuring && allAdmitted && tickStart.Sub(serverStart) >= o.Warmup {
			measuring = true
			measureStart = tickStart
			runtime.ReadMemStats(&r.StartMemory)
		}
		if !measuring && tickStart.Sub(serverStart) > o.StartupTimeout {
			loopErr = errors.New("startup routes not all admitted")
			cancel()
		}
		tickMeasured = measuring && tickStart.Sub(measureStart) < o.Duration
		queryCount = 0
		projectileCount = 0
		projectileHits = 0
	})
	s.server.OnTick(func(dt float64, _ *golem.Server) {
		if loopErr != nil {
			return
		}
		if err := s.pollChurn(o.Out); err != nil {
			loopErr = err
			cancel()
			return
		}
		if o.ChurnTicks > 0 && tickMeasured && s.server.Tick()%o.ChurnTicks == 0 && s.churn.PendingGeneration == 0 {
			if err := s.beginChurn(o); err != nil {
				loopErr = err
				cancel()
				return
			}
		}
		for _, a := range s.actors {
			state, ok := s.server.NavigationAgentState(a.ID)
			if !ok {
				loopErr = errors.New("lost navigation attachment")
				cancel()
				return
			}
			if state.Mode == golem.NavigationArrived {
				a.target = 1 - a.target
				if e := s.requestDestination(a); e != nil {
					loopErr = e
					cancel()
					return
				}
			}
		}
		queryCount, err = s.gameplayQueries()
		if err != nil {
			loopErr = err
			cancel()
			return
		}
		projectileCount, projectileHits, err = s.sweepProjectiles(float32(dt), float32(o.Extent/2))
		if err != nil {
			loopErr = err
			cancel()
			return
		}
	})
	s.server.OnTickEnd(func(tick uint64, wall time.Duration) {
		if finished {
			return
		}
		now := golem.NavigationNow()
		full := now.Sub(tickStart)
		// Admit by start time, then retain the entire final frame even if it is
		// slow enough to cross the requested duration. Censoring that frame would
		// hide precisely the tail latency this workload is intended to expose.
		complete := tickMeasured
		r.WorstObservedTickNS = max(r.WorstObservedTickNS, int64(full))
		v := sample{Tick: tick, Measured: tickMeasured, FullTickNS: int64(full), ServerWallNS: int64(wall), CadenceNS: int64(cadence), PhysicsNS: int64(physics.Duration), SolveNS: int64(physics.Solve), NavigationNS: int64(nav.Prepare + nav.Movement + nav.Reconcile), Scheduled: nav.AgentsScheduled, Projectiles: projectileCount, Queries: queryCount, Accepted: physics.Accepted, Simulated: physics.Elapsed, Substeps: physics.Substeps, Bodies: physics.World.Bodies, Colliders: physics.World.Colliders, Shapes: physics.World.Shapes}
		v.Intervals, v.Microsteps, v.MotionPairTests = physics.Intervals, physics.Microsteps, physics.MotionPairTests
		v.MotionPathRefinements = physics.MotionPathRefinements
		v.CCDCandidatePairs, v.CCDPairTests, v.CCDPredictiveContacts = physics.CCDCandidatePairs, physics.CCDPairTests, physics.CCDPredictiveContacts
		v.TOIContinuationCandidates, v.TOIContinuationPairTests, v.TOIContinuationEvents = physics.TOIContinuationCandidates, physics.TOIContinuationPairTests, physics.TOIContinuationEvents
		v.TOIContinuationIslandBodies, v.TOIContinuationContactRows = physics.TOIContinuationIslandBodies, physics.TOIContinuationContactRows
		v.Measured = complete
		for _, a := range s.actors {
			state, _ := s.server.NavigationAgentState(a.ID)
			if state.Mode == golem.NavigationFollowing {
				v.Following++
			}
			delta := a.position.Sub(a.before)
			distance := p.Vec3{X: delta.X, Z: delta.Z}.Length()
			if distance > .0001 {
				v.Moving++
			}
			if complete {
				a.travel += distance
			}
		}
		for _, b := range s.dynamics {
			state, e := s.world.Body(b)
			if e != nil {
				loopErr = e
				cancel()
				break
			}
			if state.Awake {
				v.AwakeDynamic++
			}
		}
		if physics.Err != nil {
			v.Error = physics.Err.Error()
		}
		r.Samples++
		if complete {
			r.GameplayQueries += uint64(queryCount)
			r.ProjectileSweeps += uint64(projectileCount)
			r.ProjectileHits += uint64(projectileHits)
			r.CCDCandidatePairs += uint64(v.CCDCandidatePairs)
			r.CCDPairTests += uint64(v.CCDPairTests)
			r.CCDPredictiveContacts += uint64(v.CCDPredictiveContacts)
			r.TOIContinuationCandidates += uint64(v.TOIContinuationCandidates)
			r.TOIContinuationPairTests += uint64(v.TOIContinuationPairTests)
			r.TOIContinuationEvents += uint64(v.TOIContinuationEvents)
			r.TOIContinuationIslandBodies += uint64(v.TOIContinuationIslandBodies)
			r.TOIContinuationContactRows += uint64(v.TOIContinuationContactRows)
			r.MeasuredSamples++
			r.FullTick.add(full)
			r.Cadence.add(cadence)
			r.Physics.add(physics.Duration)
			r.Solve.add(physics.Solve)
			r.Navigation.add(time.Duration(v.NavigationNS))
			r.MinMoving = min(r.MinMoving, v.Moving)
			r.MinFollowing = min(r.MinFollowing, v.Following)
			r.MinScheduled = min(r.MinScheduled, v.Scheduled)
			r.MinAwakeDynamic = min(r.MinAwakeDynamic, v.AwakeDynamic)
			r.MinProjectiles = min(r.MinProjectiles, v.Projectiles)
			r.MinQueries = min(r.MinQueries, v.Queries)
			if v.Moving < c.Characters {
				r.MovementDeficitTicks++
			}
			if cadence > 55*time.Millisecond {
				r.CadenceMisses++
			}
			if !v.Accepted {
				r.RejectedFrames++
			}
			r.QueryOverflows = physics.World.Overflows
		}
		if tick%20 == 0 {
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			r.PeakHeapBytes = max(r.PeakHeapBytes, memory.HeapAlloc)
			rss, _ := residentBytes()
			r.PeakRSSBytes = max(r.PeakRSSBytes, rss)
		}
		if e := enc.Encode(v); e != nil {
			loopErr = e
			cancel()
		}
		if measuring && now.Sub(measureStart) >= o.Duration {
			if r.MeasurementWallNS == 0 {
				r.MeasurementWallNS = int64(now.Sub(measureStart))
				r.MeasurementOverrunNS = max(0, r.MeasurementWallNS-int64(o.Duration))
				runtime.ReadMemStats(&r.EndMemory)
			}
			if s.churn.PendingGeneration != 0 {
				if now.Sub(measureStart)-time.Duration(r.MeasurementWallNS) < time.Minute {
					return
				}
				loopErr = errors.New("geometry did not drain within one minute")
				finished = true
				cancel()
				return
			}
			if o.Checkpoint {
				captureStart := golem.NavigationNow()
				checkpoint, captureErr := s.captureCheckpoint(o)
				if captureErr != nil {
					loopErr = fmt.Errorf("checkpoint: %w", captureErr)
				} else {
					captured = &checkpoint
				}
				r.CheckpointCaptureNS = int64(golem.NavigationNow().Sub(captureStart))
			}
			finished = true
			cancel()
		}
	})
	r.StartupNS = int64(golem.NavigationNow().Sub(started))
	err = s.server.Run(ctx)
	r.Churn = s.churn
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	if loopErr != nil {
		return loopErr
	}
	if captured != nil {
		writeStart := golem.NavigationNow()
		if err = golem.SavePhysicsCheckpoint(filepath.Join(o.Out, "checkpoint"), *captured); err != nil {
			return err
		}
		r.CheckpointWriteNS = int64(golem.NavigationNow().Sub(writeStart))
		r.CheckpointID = captured.CheckpointID
		captured = nil
	}
	runtime.GC()
	r.StartupRouteAdmission = s.admissionLatency
	r.RenewedRouteAdmission = s.renewalLatency
	r.AutomaticRouteAdmissions, r.UnmeasuredStartupAdmissions = s.automaticAdmissions, s.unmeasuredStartupAdmissions
	runtime.ReadMemStats(&r.AfterCloseGC)
	if o.Profiles {
		file, e := os.Create(filepath.Join(o.Out, "after-close-heap.pprof"))
		if e != nil {
			return e
		}
		e = pprof.WriteHeapProfile(file)
		file.Close()
		if e != nil {
			return e
		}
	}
	r.TotalAllocBytes = r.EndMemory.TotalAlloc - r.StartMemory.TotalAlloc
	r.Mallocs = r.EndMemory.Mallocs - r.StartMemory.Mallocs
	r.MinimumActorTravel = s.actors[0].travel
	for _, a := range s.actors {
		r.MinimumActorTravel = min(r.MinimumActorTravel, a.travel)
	}
	r.Gates["full-counts"] = o.Mode == "full"
	r.Gates["all-moving-every-complete-tick"] = r.MeasuredSamples > 0 && r.MinMoving == c.Characters && r.MinFollowing == c.Characters && r.MinScheduled == c.Characters
	r.Gates["awake-dynamic-count"] = r.MeasuredSamples > 0 && r.MinAwakeDynamic == c.DynamicBodies
	r.Gates["query-projectile-counts"] = r.MeasuredSamples > 0 && r.MinQueries == c.Queries && r.MinProjectiles == c.Projectiles
	r.Gates["full-tick-p99-below-50ms"] = r.MeasuredSamples > 0 && r.FullTick.percentile(.99) < 50*time.Millisecond
	r.Gates["two-hour-duration"] = o.Duration >= 2*time.Hour && time.Duration(r.MeasurementWallNS) >= 2*time.Hour
	// Preserve these observations, but neither soak gate is required by the
	// selected delivery scope. Wall time alone cannot establish restart continuity.
	r.Gates["two-hour-churn-restart-soak"] = false
	r.Gates["checkpoint-written"] = r.CheckpointID != ""
	r.Gates["checkpoint-restored"] = o.Resume != ""
	r.Gates["accepted-frames"] = r.MeasuredSamples > 0 && r.RejectedFrames == 0
	if r.MeasuredSamples == 0 {
		return errors.New("no complete measured tick")
	}
	fmt.Printf("mode=%s ticks=%d moving_min=%d p99=%s qualified=false\n", o.Mode, r.MeasuredSamples, r.MinMoving, r.FullTick.percentile(.99))
	return nil
}

func hash(data []byte) string                   { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func writeBytes(path string, data []byte) error { return os.WriteFile(path, data, 0644) }
func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeBytes(path, append(data, '\n'))
}
