package registry

import "testing"

func TestReplaceWithoutHooksIsAtomicAndPreservesOwnerAndLifecycleFlush(t *testing.T) {
	r := NewRegistry()
	old := &lifecycleTestEntity{id: 1, remove: func() { t.Error("ordinary removal during restore") }}
	if err := r.AddOwnedWithoutSpawn(old, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := r.FlushAll(); err != nil {
		t.Fatal(err)
	}
	fresh := &lifecycleTestEntity{id: 2, spawn: func() { t.Error("ordinary spawn during restore") }}
	for _, entries := range [][]RestoreEntry{
		{{Entity: fresh}, {Entity: fresh}},
		{{Entity: fresh}, {}},
		{{Entity: &lifecycleTestEntity{}}},
	} {
		if err := r.ReplaceWithoutHooks(entries); err == nil {
			t.Fatal("invalid restore accepted")
		}
		if got, _ := r.Get(1); got != old || r.Len() != 1 {
			t.Fatal("invalid restore changed accepted entities")
		}
		if owner, ok := r.Owner(1); !ok || owner != 7 {
			t.Fatal("invalid restore changed ownership")
		}
	}
	if err := r.ReplaceWithoutHooks([]RestoreEntry{{Entity: fresh, Owner: 8}}); err != nil {
		t.Fatal(err)
	}
	if owner, ok := r.Owner(2); !ok || owner != 8 {
		t.Fatal("restored ownership missing")
	}
	flush, err := r.FlushAll()
	if err != nil || len(flush.SpawnIDs) != 1 || flush.SpawnIDs[0] != 2 || len(flush.Removals) != 1 || flush.Removals[0] != 1 {
		t.Fatalf("replacement lifecycle flush: %+v, %v", flush, err)
	}
}
