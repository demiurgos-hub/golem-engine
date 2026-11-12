package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/qualification/worldgen"
)

func TestCapacityExerciseRecordsEveryReconciledMover(t *testing.T) {
	c := worldgen.DefaultConfig()
	c.Family = "capacity"
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
	full := options{Warmup: 2 * time.Minute, Duration: 10 * time.Minute}
	routes, err := capacityPreflight(full, f, worlds)
	if err != nil || len(routes) != 4 {
		t.Fatalf("both profile directions must support the full duration: %+v %v", routes, err)
	}
	for _, route := range routes {
		if route.PhysicalClearance.Violations != 0 || !route.PhysicalClearance.HasMinimum || route.PhysicalClearance.MinimumSeparationMetres <= 0 {
			t.Fatalf("capacity route lacks verified physical clearance: %+v", route)
		}
	}
	o := options{Out: t.TempDir(), Agents: 6, TickRate: 100, Workers: 2, BuildCommits: 2, PathDrainBudget: time.Millisecond, Warmup: 300 * time.Millisecond, Duration: 150 * time.Millisecond, Drain: 50 * time.Millisecond, DrainTimeout: time.Second, BurstInterval: time.Minute}
	m := &measurements{}
	if _, err = exercise(o, f, worlds, m, nil); err != nil {
		t.Fatal(err)
	}
	if m.Displacement.MeasuredTicks == 0 || m.Displacement.MinNonHostMovingAgents != uint64(o.Agents) || m.Displacement.HostMovingAgentTicks != 0 || m.Displacement.InvalidAgentTicks != 0 || m.CapacityStartupAdmissions != uint64(o.Agents) {
		t.Fatalf("startup intents did not yield physical motion: %+v admissions%d", m.Displacement, m.CapacityStartupAdmissions)
	}
	if m.Requests != 0 || m.SubmittedEdits != 0 || m.Arrivals != 0 || m.UnsupportedStates != 0 || m.FailedStates != 0 {
		t.Fatal("stable capacity unexpectedly renewed goals, edited geometry or stopped")
	}
}

func TestLegacyCapacityFixtureFailsPhysicalPreflight(t *testing.T) {
	c := worldgen.DefaultConfig()
	c.Family = "capacity"
	f, err := worldgen.Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	for i := range f.Profiles {
		f.Profiles[i].Profile = f.BodyProfiles[i]
	}
	f.BodyProfiles, f.BakeClearanceMargin = nil, 0
	var worlds []*dynamic.World
	for _, cfg := range f.Profiles {
		world, err := dynamic.New(context.Background(), f.Scene, cfg)
		if err != nil {
			t.Fatal(err)
		}
		worlds = append(worlds, world)
	}
	routes, err := capacityPreflight(options{Warmup: 2 * time.Minute, Duration: 10 * time.Minute}, f, worlds)
	if err == nil || !strings.Contains(err.Error(), "physical clearance") || len(routes) != 4 {
		t.Fatalf("legacy full-run failure was not rejected before timing: %+v %v", routes, err)
	}
	if hit := routes[0].PhysicalClearance.FirstViolation; hit == nil || hit.Obstacle != "capacity-divider-01" || hit.MinimumSeparationMetres >= 0 || hit.Radius != .4 {
		t.Fatalf("captured human second-turn contact missing: %+v", hit)
	}
}
