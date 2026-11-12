package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/qualification/worldgen"
)

func settledLiveFixture() liveMemoryEvidence {
	return liveMemoryEvidence{ExpectedAgents: 1000, RegisteredEntities: 1000, AttachedAgents: 1000, CapturedWhileRunning: true, Profiles: []liveProfileEvidence{{Retention: dynamic.RetentionStats{CommittedRevision: 7, DesiredRevision: 7, Leases: 1000}}}}
}

func TestLiveDrainRequiresOwnershipAndEveryPipelineStage(t *testing.T) {
	for name, change := range map[string]func(*liveMemoryEvidence){
		"shutdown":              func(e *liveMemoryEvidence) { e.CapturedWhileRunning = false },
		"deleted_actor":         func(e *liveMemoryEvidence) { e.RegisteredEntities-- },
		"detached_navigation":   func(e *liveMemoryEvidence) { e.AttachedAgents-- },
		"source_edit":           func(e *liveMemoryEvidence) { e.SourceEditsSubmitted++ },
		"physical_post":         func(e *liveMemoryEvidence) { e.PhysicalPostsPending++ },
		"path_queue":            func(e *liveMemoryEvidence) { e.Queues.Paths.Queued++ },
		"path_worker":           func(e *liveMemoryEvidence) { e.Queues.Paths.Running++ },
		"path_completion":       func(e *liveMemoryEvidence) { e.Queues.Paths.Completions++ },
		"delayed_build":         func(e *liveMemoryEvidence) { e.Queues.Builds.Delayed++ },
		"build_queue":           func(e *liveMemoryEvidence) { e.Queues.Builds.Queued++ },
		"build_worker":          func(e *liveMemoryEvidence) { e.Queues.Builds.Running++ },
		"build_completion":      func(e *liveMemoryEvidence) { e.Queues.Builds.Completions++ },
		"build_status":          func(e *liveMemoryEvidence) { e.Profiles[0].BuildPending = true },
		"building_candidate":    func(e *liveMemoryEvidence) { e.Profiles[0].Retention.Building = true },
		"prepared_candidate":    func(e *liveMemoryEvidence) { e.Profiles[0].Retention.Prepared = true },
		"retired_lease":         func(e *liveMemoryEvidence) { e.Profiles[0].Retention.RetiredGenerations++ },
		"uncommitted_edit":      func(e *liveMemoryEvidence) { e.Profiles[0].Retention.DesiredRevision++ },
		"unquarantined_pending": func(e *liveMemoryEvidence) { e.UnquarantinedPendingAgents++ },
		"host_recovery":         func(e *liveMemoryEvidence) { e.RecoveringAgents++ },
	} {
		t.Run(name, func(t *testing.T) {
			e := settledLiveFixture()
			change(&e)
			if e.settled() {
				t.Fatal("unsettled or released server qualified as a live drain")
			}
		})
	}
	e := settledLiveFixture()
	e.QuarantinedAgents, e.Modes[golem.NavigationPending] = 1000, 1000
	e.Profiles[0].HasQuarantine = true
	if !e.settled() {
		t.Fatal("permanent unloaded-column quarantine is not pending worker work")
	}
}

func TestLiveDrainHonorsMinimumAndFailsBoundedTimeout(t *testing.T) {
	e := settledLiveFixture()
	d := drainEvidence{Minimum: 5 * time.Second, Timeout: 30 * time.Second}
	if d.observe(time.Second, e) || d.observe(2*time.Second, e) || !d.observe(5*time.Second, e) || !d.Settled || d.TimedOut {
		t.Fatalf("minimum drain not honored: %+v", d)
	}
	d = drainEvidence{Minimum: 5 * time.Second, Timeout: 30 * time.Second}
	e.Queues.Paths.Running = 1
	if d.observe(5*time.Second, e) || d.observe(29*time.Second, e) || !d.observe(30*time.Second, e) || !d.TimedOut || d.Settled {
		t.Fatalf("busy pipeline timeout passed: %+v", d)
	}
}

func TestExerciseCapturesLiveDrainBeforeShutdownAndPreservesCheckpoint(t *testing.T) {
	c := worldgen.DefaultConfig()
	c.Family, c.HalfExtent, c.GridStep, c.TileSize = "construction", 16, 2, 32
	f, err := worldgen.Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	var worlds []*dynamic.World
	for _, profile := range f.Profiles {
		world, err := dynamic.New(context.Background(), f.Scene, profile)
		if err != nil {
			t.Fatal(err)
		}
		worlds = append(worlds, world)
	}
	o := options{Out: t.TempDir(), Agents: 4, TickRate: 100, Workers: 2, BuildCommits: 2, PathDrainBudget: time.Millisecond, Warmup: 100 * time.Millisecond, Duration: 200 * time.Millisecond, Drain: 100 * time.Millisecond, DrainTimeout: 3 * time.Second, BurstInterval: time.Minute}
	m := &measurements{}
	checkpoint, err := exercise(o, f, worlds, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !m.BaselineLive.allAgentsLive() || !m.Drain.Live.allAgentsLive() || !m.Drain.Live.settled() || !m.Drain.Settled || m.Drain.TimedOut || m.Drain.Elapsed < o.Drain {
		t.Fatalf("missing live-server drain: baseline=%+v drain=%+v", m.BaselineLive, m.Drain)
	}
	if m.BaseHeap != m.BaselineLive.HeapBytes || m.DrainHeap != m.Drain.Live.HeapBytes || m.DrainHeap == 0 || m.PostShutdownHeap == 0 {
		t.Fatal("live heap and post-shutdown heap were not recorded separately")
	}
	leases := 0
	for i, profile := range m.Drain.Live.Profiles {
		leases += profile.Retention.Leases
		if worlds[i].RetentionStats().Leases != 0 {
			t.Fatal("shutdown did not release navigation leases")
		}
	}
	if leases == 0 || checkpoint == nil || len(checkpoint.Actors) != o.Agents || len(checkpoint.Worlds) != 2 {
		t.Fatal("capture did not preserve live leases and coordinated checkpoint")
	}
	stationary := uint64(0)
	for _, count := range m.Displacement.StationaryByMode {
		stationary += count
	}
	if m.Displacement.MeasuredTicks == 0 || stationary+m.Displacement.MovingAgentTicks+m.Displacement.InvalidAgentTicks != uint64(o.Agents)*m.Displacement.MeasuredTicks {
		t.Fatal("per-tick final-position evidence omitted an attached actor")
	}
	log, err := os.ReadFile(filepath.Join(o.Out, "samples.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(log), []byte("\n")) {
		if err := json.Unmarshal(line, &raw); err != nil {
			t.Fatal(err)
		}
		if raw["event"] == "live_memory_drain" {
			return
		}
	}
	t.Fatal("live drain evidence event missing")
}
