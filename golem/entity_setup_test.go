package golem

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/snapshot"
)

type setupTestEntity struct {
	snapshotTestEntity
	srv                 *Server
	spawn, remove, tick func()
}

func (e *setupTestEntity) BindServer(s *Server) { e.srv = s }
func (e *setupTestEntity) OnSpawn() {
	if e.spawn != nil {
		e.spawn()
	}
}
func (e *setupTestEntity) OnRemove() {
	if e.remove != nil {
		e.remove()
	}
}
func (e *setupTestEntity) Tick(float64) {
	if e.tick != nil {
		e.tick()
	}
}

func TestCreateEntityWithSetupPublishesOnlyAfterSuccess(t *testing.T) {
	s := NewServer(ServerConfig{})
	e := &setupTestEntity{snapshotTestEntity: snapshotTestEntity{typeName: "Monster", state: []byte{1}}}
	spawned, ticked, rolledBack := 0, 0, false
	e.tick = func() { ticked++ }
	e.spawn = func() {
		spawned++
		if e.srv != s || s.Len() != 1 || e.state[0] != 9 {
			t.Error("spawn ran before complete setup")
		}
	}
	err := s.CreateEntityWithSetup(e, func(got Entity) (func() error, error) {
		if got != e || e.srv != s || e.id == 0 {
			t.Fatal("setup lacks bound entity identity")
		}
		if pending, ok := s.Get(e.id); !ok || pending != e {
			t.Fatal("setup entity unavailable")
		}
		if owner, ok := s.Owner(e.id); !ok || owner != 17 {
			t.Fatal("setup owner unavailable")
		}
		if s.Len() != 0 || len(s.All()) != 0 {
			t.Fatal("setup entity enumerated")
		}
		s.reg.TickAll(1)
		if ticked != 0 || spawned != 0 {
			t.Fatal("setup entity received lifecycle callbacks")
		}
		if states, err := s.SnapshotAll(); err != nil || len(states) != 0 {
			t.Fatalf("snapshot leaked setup: %v, %v", states, err)
		}
		path := filepath.Join(t.TempDir(), "during-setup.snap")
		if err := <-s.SaveSnapshot("fp", path); err != nil {
			t.Fatal(err)
		}
		if records, err := snapshot.Load(path, "fp"); err != nil || len(records) != 0 {
			t.Fatalf("saved setup entity: %v, %v", records, err)
		}
		if flush, err := s.reg.FlushAll(); err != nil || len(flush.Spawns)+len(flush.Deltas)+len(flush.Removals) != 0 {
			t.Fatalf("flush leaked setup: %+v, %v", flush, err)
		}
		e.state[0] = 9
		return func() error { rolledBack = true; return nil }, nil
	}, 17)
	if err != nil || spawned != 1 || rolledBack {
		t.Fatalf("publication: spawn=%d rollback=%v err=%v", spawned, rolledBack, err)
	}
	flush, err := s.reg.FlushAll()
	if err != nil || len(flush.Spawns) != 1 || flush.Spawns[0][0] != 9 {
		t.Fatalf("spawn state: %+v, %v", flush, err)
	}
}

func TestCreateEntityWithSetupFailureReleasesAttachments(t *testing.T) {
	s := NewServer(ServerConfig{})
	w := &physicsTestWorld{}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{}); err != nil {
		t.Fatal(err)
	}
	e := &navigationTestEntity{id: 8}
	failure, cleanupFailure := errors.New("configuration rejected"), errors.New("caller cleanup failed")
	cleaned := 0
	err := s.CreateEntityWithSetup(e, func(Entity) (func() error, error) {
		physicsTestAttach(t, s, w, 8, p.FreeDynamic, PhysicsAttachmentConfig{})
		if err := s.AttachNavigation(8, NavigationAgentConfig{StagedPhysics: true, Profile: "human", Speed: 2, Projection: surface.ProjectionOptions{Horizontal: 1, Up: 1, Down: 1}}); err != nil {
			t.Fatal(err)
		}
		return func() error { cleaned++; return cleanupFailure }, failure
	}, 17)
	if !errors.Is(err, failure) || !errors.Is(err, cleanupFailure) || cleaned != 1 {
		t.Fatalf("cleanup error: %v, calls %d", err, cleaned)
	}
	if _, ok := s.Get(8); ok {
		t.Fatal("failed entity retained")
	}
	if _, ok := s.Owner(8); ok {
		t.Fatal("failed owner retained")
	}
	if len(w.bodies) != 0 || len(w.destroyed) != 1 || len(s.physics.agents)+len(s.navigation.agents) != 0 {
		t.Fatal("failed attachments retained")
	}
	flush, err := s.reg.FlushAll()
	if err != nil || len(flush.Spawns)+len(flush.Deltas)+len(flush.Removals) != 0 {
		t.Fatalf("failed creation replicated: %+v, %v", flush, err)
	}
}

func TestCreateEntityWithSetupRollbackAndPanicHaveNoLifecycleEffects(t *testing.T) {
	for _, panics := range []bool{false, true} {
		s := NewServer(ServerConfig{})
		backend := newRecordingBackend2D()
		s.SetCollisionLayers(NewCollisionLayers().Bind(backend).Define("Player"))
		e := &avatarColliderLifecycleEntity{}
		e.onSpawn = func() { t.Error("failed setup spawned") }
		e.onRemove = func() { t.Error("failed setup removed") }
		failure := errors.New("setup failed")
		func() {
			defer func() {
				if got := recover(); panics && got != failure || !panics && got != nil {
					t.Errorf("panic changed: %v", got)
				}
			}()
			err := s.CreateEntityWithSetup(e, func(Entity) (func() error, error) {
				if _, ok := backend.added[e.id]; !ok {
					t.Fatal("schema collider missing during setup")
				}
				if panics {
					panic(failure)
				}
				return nil, failure
			})
			if panics || !errors.Is(err, failure) {
				t.Errorf("setup failure: %v", err)
			}
		}()
		if len(backend.added) != 0 || s.Len() != 0 {
			t.Fatal("rollback retained resources")
		}
		if _, ok := s.Get(e.id); ok {
			t.Fatal("rollback retained provisional lookup")
		}
		flush, err := s.reg.FlushAll()
		if err != nil || len(flush.Spawns)+len(flush.Deltas)+len(flush.Removals) != 0 {
			t.Fatalf("rollback output: %+v, %v", flush, err)
		}
	}
}

func TestCreateEntityWithSetupRejectsPhysicsCallbacksBeforeMutation(t *testing.T) {
	s := NewServer(ServerConfig{})
	w := &physicsTestWorld{}
	if err := s.SetPhysicsWorld(w); err != nil {
		t.Fatal(err)
	}
	s.physics.dispatching = true
	e := &setupTestEntity{}
	if err := s.CreateEntityWithSetup(e, nil); !errors.Is(err, p.ErrBusy) {
		t.Fatalf("callback admission: %v", err)
	}
	if e.id != 0 || e.srv != nil || s.Len() != 0 {
		t.Fatal("rejected setup mutated entity")
	}
}

func TestCreateEntityWithSetupChangedIdentityRollsBackOriginalResources(t *testing.T) {
	s := NewServer(ServerConfig{})
	backend := newRecordingBackend2D()
	s.SetCollisionLayers(NewCollisionLayers().Bind(backend).Define("Player"))
	e := &avatarColliderLifecycleEntity{}
	var originalID int64
	err := s.CreateEntityWithSetup(e, func(Entity) (func() error, error) {
		originalID = e.id
		e.id = 99
		return nil, nil
	})
	if err == nil || len(backend.added) != 0 || s.Len() != 0 {
		t.Fatalf("changed identity retained resources: %v", err)
	}
	if _, ok := s.Get(originalID); ok {
		t.Fatal("changed identity retained original provisional entry")
	}
	flush, err := s.reg.FlushAll()
	if err != nil || len(flush.Spawns)+len(flush.Deltas)+len(flush.Removals) != 0 {
		t.Fatalf("changed identity emitted lifecycle: %+v, %v", flush, err)
	}
}
