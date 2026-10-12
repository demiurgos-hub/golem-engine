package registry

import (
	"testing"
	"time"
)

type lifecycleTestEntity struct {
	revisionTestEntity
	id                  int64
	spawn, remove, tick func()
}

func (e *lifecycleTestEntity) EntityID() int64 { return e.id }
func (e *lifecycleTestEntity) OnSpawn() {
	if e.spawn != nil {
		e.spawn()
	}
}
func (e *lifecycleTestEntity) OnRemove() {
	if e.remove != nil {
		e.remove()
	}
}
func (e *lifecycleTestEntity) Tick(float64) {
	if e.tick != nil {
		e.tick()
	}
}

func TestInsertionOwnershipPresenceAndDuplicate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owned bool
		owner int64
	}{
		{"unowned", false, 0}, {"owner zero", true, 0}, {"owned", true, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := NewRegistry()
			e := &lifecycleTestEntity{id: 1}
			var err error
			if tc.owned {
				err = r.AddOwnedWithoutSpawn(e, tc.owner)
			} else {
				err = r.AddWithoutSpawn(e)
			}
			if err != nil {
				t.Fatal(err)
			}
			if sid, owned := r.Owner(1); sid != tc.owner || owned != tc.owned {
				t.Fatalf("owner = %d/%v", sid, owned)
			}
			if err := r.AddOwned(&lifecycleTestEntity{id: 1, spawn: func() { t.Error("duplicate spawn") }}, 99); err == nil {
				t.Fatal("duplicate accepted")
			}
			if got, _ := r.Get(1); got != e {
				t.Fatal("duplicate replaced entity")
			}
			if sid, owned := r.Owner(1); sid != tc.owner || owned != tc.owned {
				t.Fatal("duplicate changed ownership")
			}
			result, err := r.FlushAll()
			if err != nil || len(result.SpawnIDs) != 1 || result.SpawnIDs[0] != 1 {
				t.Fatalf("spawns = %+v, %v", result, err)
			}
		})
	}
}

func TestLifecycleHooksCanReenterRegistry(t *testing.T) {
	r := NewRegistry()
	var spawned, ticked, removed bool
	e := &lifecycleTestEntity{id: 1}
	e.spawn = func() { _, spawned = r.Get(1) }
	e.tick = func() { ticked = true; r.DeleteEntity(1); _ = r.Add(&lifecycleTestEntity{id: 2}) }
	e.remove = func() { _, exists := r.Get(1); removed = !exists }
	done := make(chan struct{})
	go func() { _ = r.Add(e); r.TickAll(1); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("registry hook held mutex")
	}
	if !spawned || !ticked || !removed || r.Len() != 1 {
		t.Fatalf("hooks: %v/%v/%v, len %d", spawned, ticked, removed, r.Len())
	}
}

type revisionTestEntity struct {
	revision uint64
}

func (revisionTestEntity) EntityID() int64              { return 1 }
func (revisionTestEntity) TypeName() string             { return "test" }
func (revisionTestEntity) Position() (float32, float32) { return 0, 0 }
func (revisionTestEntity) IsGlobal() bool               { return false }
func (revisionTestEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (revisionTestEntity) FullUpdate() ([]byte, error)  { return nil, nil }
func (e revisionTestEntity) StateRevision() uint64      { return e.revision }

func TestRemovalRevisionDefaultsToNonZero(t *testing.T) {
	if got := removalRevision(revisionTestEntity{}); got != 1 {
		t.Fatalf("removalRevision = %d, want 1", got)
	}
}

func TestRemovalRevisionAdvancesPastDeletedState(t *testing.T) {
	if got := removalRevision(revisionTestEntity{revision: 7}); got != 8 {
		t.Fatalf("removalRevision = %d, want 8", got)
	}
}

func TestSetOwnerClearRemovesOwnership(t *testing.T) {
	r := NewRegistry()
	e := revisionTestEntity{}
	if err := r.AddOwned(e, 42); err != nil {
		t.Fatalf("AddOwned: %v", err)
	}
	if sid, ok := r.Owner(1); !ok || sid != 42 {
		t.Fatalf("Owner = (%d,%v), want (42,true)", sid, ok)
	}
	if !r.SetOwner(1, 0) {
		t.Fatal("SetOwner(0) failed")
	}
	if sid, ok := r.Owner(1); ok || sid != 0 {
		t.Fatalf("Owner after clear = (%d,%v), want (0,false)", sid, ok)
	}
}
