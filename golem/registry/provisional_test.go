package registry

import "testing"

func TestProvisionalVisibilityAndLifecycle(t *testing.T) {
	r := NewRegistry()
	spawned, removed, ticked := 0, 0, 0
	e := &lifecycleTestEntity{id: 7, spawn: func() { spawned++ }, remove: func() { removed++ }, tick: func() { ticked++ }}
	if err := r.AddProvisional(e, 41); err != nil {
		t.Fatal(err)
	}
	if got, ok := r.Get(7); !ok || got != e {
		t.Fatal("setup lookup unavailable")
	}
	if owner, ok := r.Owner(7); !ok || owner != 41 {
		t.Fatal("setup owner unavailable")
	}
	if r.Len() != 0 || len(r.All()) != 0 {
		t.Fatal("provisional entity was enumerated")
	}
	r.TickAll(1)
	updates, err := r.SnapshotAll()
	if err != nil || len(updates) != 0 {
		t.Fatalf("provisional snapshot: %v, %v", updates, err)
	}
	flush, err := r.FlushAll()
	if err != nil || len(flush.Spawns)+len(flush.Deltas)+len(flush.Removals) != 0 || spawned+removed+ticked != 0 {
		t.Fatalf("provisional lifecycle leaked: %+v, %v", flush, err)
	}
	if err := r.AddWithoutSpawn(&lifecycleTestEntity{id: 7}); err == nil {
		t.Fatal("ordinary insertion replaced reserved identity")
	}
	if err := r.CommitProvisional(7); err != nil {
		t.Fatal(err)
	}
	if spawned != 0 || r.Len() != 1 {
		t.Fatal("commit called hook or failed publication")
	}
	NotifySpawn(e)
	r.TickAll(1)
	flush, err = r.FlushAll()
	if err != nil || len(flush.Spawns) != 1 || flush.SpawnIDs[0] != 7 || spawned != 1 || ticked != 1 {
		t.Fatalf("published lifecycle: %+v, %v", flush, err)
	}
	r.DiscardProvisional(7)
	if r.Len() != 1 {
		t.Fatal("discard removed a published entity")
	}
}

func TestProvisionalCommitFailureAndDiscardAreAtomic(t *testing.T) {
	r := NewRegistry()
	for _, id := range []int64{3, 4} {
		if err := r.AddProvisional(&lifecycleTestEntity{id: id, remove: func() { t.Error("rollback removal hook") }}, 12); err != nil {
			t.Fatal(err)
		}
	}
	for _, ids := range [][]int64{{3, 9}, {3, 3}} {
		if err := r.CommitProvisional(ids...); err == nil || r.Len() != 0 {
			t.Fatal("invalid batch partially published")
		}
	}
	if err := r.ReplaceWithoutHooks(nil); err == nil {
		t.Fatal("replacement admitted during provisional setup")
	}
	r.DiscardProvisional(3)
	r.DeleteEntity(4)
	for _, id := range []int64{3, 4} {
		if _, ok := r.Get(id); ok {
			t.Fatal("discard retained entity")
		}
		if _, ok := r.Owner(id); ok {
			t.Fatal("discard retained owner")
		}
	}
	flush, err := r.FlushAll()
	if err != nil || len(flush.Spawns)+len(flush.Deltas)+len(flush.Removals) != 0 {
		t.Fatalf("rollback emitted lifecycle: %+v, %v", flush, err)
	}
}
