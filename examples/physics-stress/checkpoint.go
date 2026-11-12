package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	adapter "github.com/demiurgos-hub/golem-engine/golem/nav/navmesh"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
)

const checkpointSchema = "physics-stress-actor-v1"

type savedPhysicsActor struct {
	Position p.Vec3
	Ends     [2]surface.Vec3
	Target   int
}

type savedProjectile struct {
	Position, Velocity p.Vec3
	Generation         uint64
}

type savedHost struct {
	Version, Extent int
	Counts          workload
	Projectiles     []savedProjectile
	ChurnSequence   uint64
}

// This host-owned publication seam replaces all clearance profiles together.
// No worker is started until startup restoration has completed.
type replaceableNavigation struct{ *adapter.Backend }
type navigationReplacement struct{ commit func() }

func (r navigationReplacement) Commit() error { r.commit(); return nil }
func (r navigationReplacement) Discard()      {}

func navigationAgentConfig(profile surface.ProfileID) golem.NavigationAgentConfig {
	return golem.NavigationAgentConfig{Profile: profile, Speed: 3, Acceleration: 12, StoppingDistance: .2, SupportTolerance: .2, Projection: surface.ProjectionOptions{Horizontal: 1, Up: .4, Down: .4}, StagedPhysics: true}
}

func recipeFingerprint(c workload, extent int) string {
	data, _ := json.Marshal(struct {
		Version, Extent int
		Counts          workload
	}{1, extent, c})
	return hash(data)
}

func decodeStrict(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("checkpoint has trailing data")
	}
	return nil
}

func (s *scene) captureCheckpoint(o options) (golem.PhysicsCheckpoint, error) {
	id := fmt.Sprintf("physics-stress-%d-%d", time.Now().UTC().UnixNano(), s.server.Tick())
	worlds := make([][]byte, len(s.navWorlds))
	revision := uint64(1)
	for i, world := range s.navWorlds {
		if world.Pending() {
			return golem.PhysicsCheckpoint{}, p.ErrBusy
		}
		var err error
		worlds[i], err = world.Checkpoint(id)
		if err != nil {
			return golem.PhysicsCheckpoint{}, err
		}
		revision = max(revision, world.Snapshot().Revision())
	}
	navigation, err := json.Marshal(worlds)
	if err != nil {
		return golem.PhysicsCheckpoint{}, err
	}
	host := savedHost{Version: 1, Counts: s.counts, Extent: o.Extent, Projectiles: make([]savedProjectile, len(s.projectiles)), ChurnSequence: s.churn.Sequence}
	for i, projectile := range s.projectiles {
		host.Projectiles[i] = savedProjectile{projectile.position, projectile.velocity, projectile.generation}
	}
	physics, err := json.Marshal(host)
	if err != nil {
		return golem.PhysicsCheckpoint{}, err
	}
	return s.server.CapturePhysicsCheckpoint(golem.PhysicsCheckpointOptions{ID: id, SchemaFingerprint: checkpointSchema,
		PhysicsFingerprint: recipeFingerprint(s.counts, o.Extent), NavigationFingerprint: hash(navigation),
		DesiredPhysicsRevision: revision, DesiredNavigationRevision: revision, DesiredPhysicsWorld: physics, DesiredNavigationWorld: navigation})
}

func (s *scene) resumeCheckpoint(o options) error {
	started := time.Now()
	checkpoint, err := golem.LoadPhysicsCheckpoint(o.Resume)
	if err != nil {
		return err
	}
	var host savedHost
	if err = decodeStrict(checkpoint.DesiredPhysicsWorld, &host); err != nil {
		return err
	}
	if host.Version != 1 || host.Counts != s.counts || host.Extent != o.Extent || len(host.Projectiles) != s.counts.Projectiles || len(checkpoint.Entities) != s.counts.Characters || checkpoint.Navigation == nil {
		return fmt.Errorf("checkpoint workload does not match requested workload")
	}
	for _, saved := range host.Projectiles {
		if !saved.Position.InBounds() || !saved.Velocity.Valid() {
			return p.ErrInvalid
		}
	}
	actors := make([]*actor, s.counts.Characters)
	err = s.server.RestorePhysicsCheckpoint(checkpoint, golem.PhysicsRestoreOptions{
		SchemaFingerprint: checkpointSchema, PhysicsFingerprint: recipeFingerprint(s.counts, o.Extent), NavigationFingerprint: hash(checkpoint.Navigation.DesiredWorld),
		Entity: func(record snapshot.Record) (golem.Entity, error) {
			if record.TypeName != "PhysicsQualificationActor" || record.EntityID < 1 || record.EntityID > int64(len(actors)) {
				return nil, p.ErrInvalid
			}
			var saved savedPhysicsActor
			if err := decodeStrict(record.StateData, &saved); err != nil {
				return nil, err
			}
			if saved.Target < 0 || saved.Target > 1 || !saved.Position.InBounds() {
				return nil, p.ErrInvalid
			}
			a := &actor{ID: record.EntityID, position: saved.Position, ends: saved.Ends, target: saved.Target, requestAt: golem.NavigationNow()}
			actors[record.EntityID-1] = a
			return a, nil
		},
		NavigationConfig: func(saved golem.PhysicsNavigationCheckpoint) (golem.NavigationAgentConfig, error) {
			if !saved.StagedPhysics || saved.Profile != "human" && saved.Profile != "large" {
				return golem.NavigationAgentConfig{}, p.ErrInvalid
			}
			return navigationAgentConfig(saved.Profile), nil
		},
		PrepareNavigation: func(saved golem.NavigationCheckpoint) (golem.NavigationBuildResult, error) {
			var captures [][]byte
			if err := decodeStrict(saved.DesiredWorld, &captures); err != nil {
				return nil, err
			}
			if len(captures) != 2 {
				return nil, p.ErrInvalid
			}
			worlds := make([]*dynamic.World, len(captures))
			for i, capture := range captures {
				var err error
				worlds[i], err = dynamic.Restore(context.Background(), capture, saved.CheckpointID)
				if err != nil {
					return nil, err
				}
			}
			backend, err := adapter.NewDynamic(worlds...)
			if err != nil {
				return nil, err
			}
			return navigationReplacement{func() { s.navBackend.Backend, s.navWorlds = backend, worlds }}, nil
		},
	})
	if err != nil {
		return err
	}
	for i, a := range actors {
		state, ok := s.restoredBodies[p.ContentID(fmt.Sprintf("character/%d", i))]
		if a == nil || !ok || state.Anchor.Position != a.position {
			return fmt.Errorf("restored actor %d binding mismatch", i)
		}
		a.body = state.Handle
	}
	for i := range s.dynamics {
		state, ok := s.restoredBodies[p.ContentID(fmt.Sprintf("dynamic/%d", i))]
		if !ok {
			return fmt.Errorf("restored dynamic body %d missing", i)
		}
		s.dynamics[i] = state.Handle
	}
	s.actors = actors
	s.churn.Sequence = host.ChurnSequence
	for i, saved := range host.Projectiles {
		s.projectiles[i] = projectile{saved.Position, saved.Velocity, saved.Generation}
	}
	s.restoredBodies = nil
	return writeJSON(filepath.Join(o.Out, "restart.json"), map[string]any{"checkpointID": checkpoint.CheckpointID, "tick": checkpoint.Tick, "durationNS": time.Since(started), "actors": len(actors), "dynamicBodies": len(s.dynamics), "projectiles": len(s.projectiles), "source": o.Resume, "ready": true, "scope": "complete paired restore before workers and simulation; startup construction separately recorded"})
}
