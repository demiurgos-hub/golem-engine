package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/demiurgos-hub/golem-engine/examples/unity-integration/generated"
	"github.com/demiurgos-hub/golem-engine/golem"
	adapter "github.com/demiurgos-hub/golem-engine/golem/nav/navmesh"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
)

type savedHost struct {
	Version     int           `json:"version"`
	Recipe      string        `json:"recipe"`
	ContentHash string        `json:"content_hash"`
	Entities    []entityState `json:"entities"`
}

func (f *fixture) capture() error {
	state := f.snapshot()
	if state.Mode != "fresh" || !state.MotionPassed || f.checkpointStarted {
		return errors.New("checkpoint requires completed fresh motion, once")
	}
	nav, ok := f.server.NavigationAgentState(3)
	if !ok || !nav.HasDestination || nav.Mode == golem.NavigationArrived {
		return errors.New("guard must have an active intent at capture")
	}
	f.actors[1].SetHealth(13)
	f.server.DeleteEntity(2)
	delete(f.actors, 2)
	if err := f.world.Synchronize(); err != nil {
		return err
	}
	id := fmt.Sprintf("unity-integration-%d", f.server.Tick())
	navigation, err := f.navWorld.Checkpoint(id)
	if err != nil {
		return err
	}
	host := savedHost{Version: 1, Recipe: fixtureRecipe, ContentHash: f.definitions.Hash(), Entities: f.entityStates()}
	physics, err := json.Marshal(host)
	if err != nil {
		return err
	}
	cp, err := f.server.CapturePhysicsCheckpoint(golem.PhysicsCheckpointOptions{ID: id, SchemaFingerprint: generated.SchemaFingerprint, PhysicsFingerprint: digest([]byte(fixtureRecipe)), NavigationFingerprint: digest(navigation), DesiredPhysicsRevision: 1, DesiredNavigationRevision: f.navWorld.Snapshot().Revision(), DesiredPhysicsWorld: physics, DesiredNavigationWorld: navigation})
	if err != nil {
		return err
	}
	f.checkpointStarted = true
	path := filepath.Join(f.options.evidence, "checkpoint")
	f.mu.Lock()
	f.state.CheckpointEntities = host.Entities
	f.state.CheckpointPath = path
	f.state.Phase = "checkpoint_saving"
	f.mu.Unlock()
	f.saves.Add(1)
	go func() {
		defer f.saves.Done()
		err := golem.SavePhysicsCheckpoint(path, cp)
		if err == nil {
			err = writeJSONExclusive(filepath.Join(f.options.evidence, "checkpoint-state.json"), host)
		}
		if err != nil {
			f.fail(err)
			return
		}
		f.mu.Lock()
		f.state.CheckpointReady = true
		f.state.Phase = "checkpoint_ready"
		f.mu.Unlock()
	}()
	return nil
}

func (f *fixture) restore(path string) error {
	cp, err := golem.LoadPhysicsCheckpoint(path)
	if err != nil {
		return err
	}
	var host savedHost
	if err = decodeStrict(cp.DesiredPhysicsWorld, &host); err != nil {
		return err
	}
	if host.Version != 1 || host.Recipe != fixtureRecipe || host.ContentHash != f.definitions.Hash() {
		return errors.New("checkpoint host recipe/content mismatch")
	}
	if cp.Navigation == nil || len(cp.Entities) != 2 || len(host.Entities) != 2 {
		return errors.New("checkpoint expected two surviving entities and navigation")
	}
	actors := map[int64]*monster{}
	err = f.server.RestorePhysicsCheckpoint(cp, golem.PhysicsRestoreOptions{
		SchemaFingerprint: generated.SchemaFingerprint, PhysicsFingerprint: digest([]byte(fixtureRecipe)), NavigationFingerprint: digest(cp.Navigation.DesiredWorld),
		Entity: func(record snapshot.Record) (golem.Entity, error) {
			entity, e := generated.RestoreEntity(record)
			if e != nil {
				return nil, e
			}
			base, ok := entity.(*generated.SyncedMonster)
			if !ok {
				return nil, errors.New("unexpected restored entity type")
			}
			def, ok := f.definitions.LookupMonster(base.DefinitionId())
			if !ok || def.Factory() != "monster" {
				return nil, errors.New("restored definition/factory missing")
			}
			m := &monster{SyncedMonster: base, definition: def}
			actors[m.EntityID()] = m
			return m, nil
		},
		NavigationConfig: func(saved golem.PhysicsNavigationCheckpoint) (golem.NavigationAgentConfig, error) {
			m := actors[saved.EntityID]
			if m == nil || saved.Profile != "human" || !saved.StagedPhysics {
				return golem.NavigationAgentConfig{}, errors.New("restored navigation binding mismatch")
			}
			return agentConfig(m.definition), nil
		},
		PrepareNavigation: func(saved golem.NavigationCheckpoint) (golem.NavigationBuildResult, error) {
			world, e := dynamic.Restore(context.Background(), saved.DesiredWorld, saved.CheckpointID)
			if e != nil {
				return nil, e
			}
			backend, e := adapter.NewDynamic(world)
			if e != nil {
				return nil, e
			}
			return navigationReplacement{func() { f.navWorld = world; f.navBackend.Backend = backend }}, nil
		},
	})
	if err != nil {
		return err
	}
	f.actors = actors
	if actors[1] == nil || actors[3] == nil || actors[2] != nil || actors[1].Health() != 13 || actors[3].Health() != 160 {
		return errors.New("restored survivors/health mismatch")
	}
	actual := f.entityStates()
	for i, want := range host.Entities {
		if actual[i] != want {
			return errors.New("restored entity differs from checkpoint host capture")
		}
		m := actors[want.ID]
		if m.spawns != 0 || m.restores != 1 {
			return errors.New("restore reapplied ordinary spawn lifecycle")
		}
	}
	state, ok := f.server.NavigationAgentState(3)
	if !ok || !state.HasDestination {
		return errors.New("guard navigation intent was not restored")
	}
	f.state.RestoredCheckpointID = cp.CheckpointID
	f.state.CheckpointEntities = host.Entities
	return nil
}

func decodeStrict(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing checkpoint host data")
	}
	return nil
}
