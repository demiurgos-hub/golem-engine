package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
)

func fixtureOptions(t *testing.T) options {
	t.Helper()
	return options{content: filepath.Join("generated", "ContentDefinitions.json"), evidence: t.TempDir()}
}

func startTestFixture(t *testing.T, f *fixture) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.run(ctx) }()
	if err := f.start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	return func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("fixture shutdown timed out")
		}
	}
}

func waitFixture(t *testing.T, f *fixture, condition func(fixtureState) bool) fixtureState {
	t.Helper()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		state := f.snapshot()
		if state.Error != "" {
			t.Fatalf("fixture error: %s", state.Error)
		}
		if condition(state) {
			return state
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ := json.Marshal(f.snapshot())
	t.Fatalf("fixture condition timed out: %s", data)
	return fixtureState{}
}

func TestContentPhysicalNavigationAndPairedRestart(t *testing.T) {
	o := fixtureOptions(t)
	f, err := newFixture(o)
	if err != nil {
		t.Fatal(err)
	}
	initial := f.snapshot()
	if len(initial.Entities) != 3 || initial.Entities[0].Health != 80 || initial.Entities[2].Health != 160 || !initial.QueryPassed || !initial.PoseEqualityPassed {
		t.Fatalf("initial: %+v", initial)
	}
	stop := startTestFixture(t, f)
	stopped := false
	defer func() {
		if !stopped {
			stop()
		}
	}()
	waitFixture(t, f, func(s fixtureState) bool { return s.MotionPassed })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err = f.post(ctx, f.capture); err != nil {
		t.Fatal(err)
	}
	saved := waitFixture(t, f, func(s fixtureState) bool { return s.CheckpointReady })
	stop()
	stopped = true
	if len(saved.CheckpointEntities) != 2 || saved.CheckpointEntities[0].ID != 1 || saved.CheckpointEntities[0].Health != 13 || saved.CheckpointEntities[1].ID != 3 {
		t.Fatalf("saved: %+v", saved)
	}
	resumeOptions := fixtureOptions(t)
	resumeOptions.resume = saved.CheckpointPath
	resumed, err := newFixture(resumeOptions)
	if err != nil {
		t.Fatal(err)
	}
	before := resumed.snapshot()
	if before.MotionPassed || before.RestoredCheckpointID == "" || !reflect.DeepEqual(before.InitialEntities, saved.CheckpointEntities) {
		t.Fatalf("restored initial state: %+v", before)
	}
	stopResumed := startTestFixture(t, resumed)
	defer stopResumed()
	continued := waitFixture(t, resumed, func(s fixtureState) bool { return s.MotionPassed })
	if len(continued.Entities) != 2 || continued.Entities[0].Health != 13 || !continued.PoseEqualityPassed || !continued.QueryPassed {
		t.Fatalf("continued: %+v", continued)
	}
	for _, actor := range resumed.actors {
		if actor.spawns != 0 || actor.restores != 1 {
			t.Fatal("restore lifecycle reinitialized actor")
		}
	}

	checkpoint, err := golem.LoadPhysicsCheckpoint(saved.CheckpointPath)
	if err != nil {
		t.Fatal(err)
	}
	var host savedHost
	if err = decodeStrict(checkpoint.DesiredPhysicsWorld, &host); err != nil {
		t.Fatal(err)
	}
	host.ContentHash = strings.Repeat("0", 64)
	checkpoint.DesiredPhysicsWorld, _ = json.Marshal(host)
	wrong := t.TempDir()
	if err = golem.SavePhysicsCheckpoint(wrong, checkpoint); err != nil {
		t.Fatal(err)
	}
	badOptions := fixtureOptions(t)
	badOptions.resume = wrong
	if bad, e := newFixture(badOptions); e == nil {
		bad.dispose()
		t.Fatal("accepted wrong content pin")
	}

	corrupt := t.TempDir()
	if err = os.WriteFile(filepath.Join(corrupt, "physics-checkpoint.json"), []byte(`{"Version":1,"CheckpointID":"bad","File":"missing","SHA256":"bad"}`), 0600); err != nil {
		t.Fatal(err)
	}
	badOptions = fixtureOptions(t)
	badOptions.resume = corrupt
	if bad, e := newFixture(badOptions); e == nil {
		bad.dispose()
		t.Fatal("accepted corrupt checkpoint")
	}
}

func TestUnfinalizedContentFailsBeforeWorldConstruction(t *testing.T) {
	o := fixtureOptions(t)
	o.content = filepath.Join("content", "demo.bundle.json")
	if f, err := newFixture(o); err == nil {
		f.dispose()
		t.Fatal("authoring source accepted as finalized content")
	}
}
