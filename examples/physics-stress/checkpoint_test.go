package main

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func TestPairedCheckpointRestartsActualPhysicsAndNavigation(t *testing.T) {
	c, _ := counts("correctness")
	o := options{Mode: "correctness", Extent: 64, Out: t.TempDir(), PhysicsWorkers: 1, QueryWorkers: 1, Workers: 1}
	s, err := makeScene(o, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var saved golem.PhysicsCheckpoint
	var captureErr error
	var prior p.BodyState
	s.server.OnTickEnd(func(tick uint64, _ time.Duration) {
		if saved.Version != 0 || tick < 3 {
			return
		}
		prior, captureErr = s.world.Body(s.dynamics[0])
		if captureErr == nil {
			saved, captureErr = s.captureCheckpoint(o)
		}
		cancel()
	})
	if err = s.server.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	if saved.Version == 0 || prior.LinearVelocity.Y >= 0 {
		t.Fatal("checkpoint did not capture a live falling body")
	}
	directory := filepath.Join(t.TempDir(), "checkpoint")
	if err = golem.SavePhysicsCheckpoint(directory, saved); err != nil {
		t.Fatal(err)
	}
	resume := o
	resume.Out, resume.Resume = t.TempDir(), directory
	next, err := makeScene(resume, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer next.world.Close()
	stale := next.dynamics[0]
	if err = next.resumeCheckpoint(resume); err != nil {
		t.Fatal(err)
	}
	if _, err = next.world.Body(stale); !errors.Is(err, p.ErrStale) {
		t.Fatalf("old handle remained usable: %v", err)
	}
	actual, err := next.world.Body(next.dynamics[0])
	if err != nil {
		t.Fatal(err)
	}
	if actual.Pose != prior.Pose || actual.LinearVelocity != prior.LinearVelocity || actual.AngularVelocity != prior.AngularVelocity || actual.ID != prior.ID {
		t.Fatalf("rigid-body state did not survive restart: before=%+v after=%+v", prior, actual)
	}
	if !reflect.DeepEqual(next.projectiles, s.projectiles) {
		t.Fatal("projectile generation or trajectory was reset")
	}
	for i, a := range next.actors {
		if a.position != s.actors[i].position || a.ends != s.actors[i].ends || a.target != s.actors[i].target {
			t.Fatal("actor intent did not survive restart")
		}
		state, ok := next.server.NavigationAgentState(a.ID)
		if !ok || state.Destination != a.ends[a.target] {
			t.Fatalf("navigation intent missing after restart: %+v", state)
		}
	}
	ready, restoring, err := next.server.PhysicsReady()
	if !ready || restoring || err != nil {
		t.Fatalf("restore readiness: %v %v %v", ready, restoring, err)
	}
}
