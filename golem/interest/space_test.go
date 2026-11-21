package interest

import (
	"reflect"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

func addInSpace(t *testing.T, reg *registry.Registry, space registry.SpaceID, entities ...*testEntity) {
	t.Helper()
	for _, e := range entities {
		if err := reg.Add(e); err != nil {
			t.Fatalf("Add(%d): %v", e.id, err)
		}
		if space != 0 && !reg.SetSpace(e.id, space) {
			t.Fatalf("SetSpace(%d, %d) failed", e.id, space)
		}
	}
}

func assertDiff(t *testing.T, label string, diff *Diff, space registry.SpaceID, entered, stayed, exited []int64) {
	t.Helper()
	if diff == nil {
		t.Fatalf("%s: missing diff", label)
	}
	norm := func(ids []int64) []int64 {
		if len(ids) == 0 {
			return nil
		}
		return sortedIDs(ids)
	}
	if diff.Space != space {
		t.Fatalf("%s: space = %d, want %d", label, diff.Space, space)
	}
	if got := norm(diff.Entered); !reflect.DeepEqual(got, norm(entered)) {
		t.Fatalf("%s: entered = %v, want %v", label, got, entered)
	}
	if got := norm(diff.Stayed); !reflect.DeepEqual(got, norm(stayed)) {
		t.Fatalf("%s: stayed = %v, want %v", label, got, stayed)
	}
	if got := norm(diff.Exited); !reflect.DeepEqual(got, norm(exited)) {
		t.Fatalf("%s: exited = %v, want %v", label, got, exited)
	}
}

func assertKnownInSpace(t *testing.T, reg *registry.Registry, mgr *Manager, sessionID int64, space registry.SpaceID) {
	t.Helper()
	for id := range mgr.Known(sessionID) {
		if got, ok := reg.Space(id); ok && got != space {
			t.Fatalf("session %d knows entity %d in space %d, want only space %d", sessionID, id, got, space)
		}
	}
}

func TestSpacesSameCoordinatesStayIsolated(t *testing.T) {
	reg := registry.NewRegistry()
	addInSpace(t, reg, 0, &testEntity{id: 1}, &testEntity{id: 2, x: 1})
	addInSpace(t, reg, 5, &testEntity{id: 11}, &testEntity{id: 12, x: 1})

	mgr := NewManager(4)
	mgr.AssignFOI(100, 1, 10, 1)
	mgr.AssignFOI(200, 11, 10, 1)
	mgr.UpdateGrid(reg)
	diffs := mgr.ComputeDiffs()
	assertDiff(t, "space 0", diffs[100], 0, []int64{1, 2}, nil, nil)
	assertDiff(t, "space 5", diffs[200], 5, []int64{11, 12}, nil, nil)
	if mgr.SessionSpace(200) != 5 || mgr.SessionSpace(100) != 0 {
		t.Fatalf("session spaces = %d/%d", mgr.SessionSpace(100), mgr.SessionSpace(200))
	}

	mgr.UpdateGrid(reg)
	diffs = mgr.ComputeDiffs()
	assertDiff(t, "space 0 stable", diffs[100], 0, nil, []int64{1, 2}, nil)
	assertDiff(t, "space 5 stable", diffs[200], 5, nil, []int64{11, 12}, nil)
}

func TestSpacesGlobalsArePerSpace(t *testing.T) {
	reg := registry.NewRegistry()
	addInSpace(t, reg, 0, &testEntity{id: 1}, &testEntity{id: 3, x: 500, global: true})
	addInSpace(t, reg, 5, &testEntity{id: 11}, &testEntity{id: 13, x: 500, global: true})

	mgr := NewManager(4)
	mgr.AssignFOI(100, 1, 10, 1)
	mgr.AssignFOI(200, 11, 10, 1)
	mgr.UpdateGrid(reg)
	diffs := mgr.ComputeDiffs()
	assertDiff(t, "space 0", diffs[100], 0, []int64{1, 3}, nil, nil)
	assertDiff(t, "space 5", diffs[200], 5, []int64{11, 13}, nil, nil)
	if !mgr.IsGlobal(3) || !mgr.IsGlobal(13) || mgr.IsGlobal(11) {
		t.Fatal("IsGlobal must cover every space")
	}

	// A global moving between spaces exits the old space and enters the new.
	reg.SetSpace(13, 0)
	mgr.UpdateGrid(reg)
	diffs = mgr.ComputeDiffs()
	assertDiff(t, "space 0 gains global", diffs[100], 0, []int64{13}, []int64{1, 3}, nil)
	assertDiff(t, "space 5 loses global", diffs[200], 5, nil, []int64{11}, []int64{13})

	// Deleting a global of another space emits nothing to space 5 sessions.
	reg.DeleteEntity(13)
	mgr.UpdateGrid(reg)
	diffs = mgr.ComputeDiffs()
	assertDiff(t, "space 0 global deleted", diffs[100], 0, nil, []int64{1, 3}, []int64{13})
	assertDiff(t, "space 5 unaffected", diffs[200], 5, nil, []int64{11}, nil)
}

func TestSpacesAnchorMoveCarriesEntitiesAndExitsTheRest(t *testing.T) {
	reg := registry.NewRegistry()
	anchor := &testEntity{id: 1}
	crate := &testEntity{id: 2, x: 1}
	left := &testEntity{id: 3, x: 2}
	far := &testEntity{id: 4, x: 3}
	addInSpace(t, reg, 0, anchor, crate, left, far, &testEntity{id: 5, x: 900, global: true})
	addInSpace(t, reg, 7, &testEntity{id: 21, x: 1, y: 1}, &testEntity{id: 22, x: 900, global: true})

	mgr := NewManager(4)
	mgr.AssignFOI(100, anchor.id, 10, 1)
	mgr.UpdateGrid(reg)
	assertDiff(t, "initial", mgr.ComputeDiffs()[100], 0, []int64{1, 2, 3, 4, 5}, nil, nil)

	// The anchor and crate move together; the far entity moves too but lands
	// outside the FOI in the new space and exits.
	reg.SetSpace(anchor.id, 7)
	reg.SetSpace(crate.id, 7)
	reg.SetSpace(far.id, 7)
	far.x = 50
	mgr.UpdateGrid(reg)
	diff := mgr.ComputeDiffs()[100]
	assertDiff(t, "moved", diff, 7, []int64{21, 22}, []int64{1, 2}, []int64{3, 4, 5})
	assertKnownInSpace(t, reg, mgr, 100, 7)

	// Returning to space 0 restores the single-space fast path.
	for _, id := range []int64{1, 2, 4, 21, 22} {
		reg.SetSpace(id, 0)
	}
	far.x = 3
	mgr.UpdateGrid(reg)
	diff = mgr.ComputeDiffs()[100]
	assertDiff(t, "back", diff, 0, []int64{3, 4, 5}, []int64{1, 2, 21, 22}, nil)
	if mgr.spaced || len(mgr.sessionSpace) != 0 || len(mgr.grids) != 0 || len(mgr.globalsBySpace) != 0 || len(mgr.entitySpace) != 0 {
		t.Fatalf("space bookkeeping retained: spaced=%v sessions=%v grids=%d globals=%v entities=%v",
			mgr.spaced, mgr.sessionSpace, len(mgr.grids), mgr.globalsBySpace, mgr.entitySpace)
	}
	mgr.UpdateGrid(reg)
	assertDiff(t, "fast path", mgr.ComputeDiffs()[100], 0, nil, []int64{1, 2, 3, 4, 5, 21, 22}, nil)
}

func TestSpacesMissingAnchorKeepsLastSpace(t *testing.T) {
	reg := registry.NewRegistry()
	anchor := &testEntity{id: 1}
	addInSpace(t, reg, 0, &testEntity{id: 9, x: 1})
	addInSpace(t, reg, 5, anchor, &testEntity{id: 2, x: 1}, &testEntity{id: 3, x: 2}, &testEntity{id: 4, global: true})

	mgr := NewManager(4)
	mgr.AssignFOI(100, anchor.id, 10, 1)
	mgr.UpdateGrid(reg)
	assertDiff(t, "initial", mgr.ComputeDiffs()[100], 5, []int64{1, 2, 3, 4}, nil, nil)

	reg.DeleteEntity(anchor.id)
	mgr.UpdateGrid(reg)
	// Globals-only branch: the space is kept and live known locals stay
	// silently; the deleted anchor itself exits.
	assertDiff(t, "anchor missing", mgr.ComputeDiffs()[100], 5, nil, []int64{4}, []int64{1})
	if mgr.SessionSpace(100) != 5 {
		t.Fatalf("session space = %d, want 5", mgr.SessionSpace(100))
	}

	// A known entity leaving the session's space still exits.
	reg.SetSpace(3, 0)
	mgr.UpdateGrid(reg)
	assertDiff(t, "known left space", mgr.ComputeDiffs()[100], 5, nil, []int64{4}, []int64{3})
	assertKnownInSpace(t, reg, mgr, 100, 5)
}

func TestSpacesGlobalAnchorResolvesItsSpace(t *testing.T) {
	reg := registry.NewRegistry()
	addInSpace(t, reg, 0, &testEntity{id: 1, global: true})
	addInSpace(t, reg, 3, &testEntity{id: 10, global: true}, &testEntity{id: 11, global: true}, &testEntity{id: 12})

	mgr := NewManager(4)
	mgr.AssignFOI(100, 10, 10, 1)
	mgr.UpdateGrid(reg)
	assertDiff(t, "global anchor", mgr.ComputeDiffs()[100], 3, []int64{10, 11}, nil, nil)

	reg.SetSpace(10, 0)
	mgr.UpdateGrid(reg)
	assertDiff(t, "global anchor moved", mgr.ComputeDiffs()[100], 0, []int64{1}, []int64{10}, []int64{11})
	assertKnownInSpace(t, reg, mgr, 100, 0)
}

func TestSpacesSetSessionSpaceAppliesUntilAnchorResolves(t *testing.T) {
	reg := registry.NewRegistry()
	addInSpace(t, reg, 4, &testEntity{id: 5, global: true})
	addInSpace(t, reg, 0, &testEntity{id: 6, global: true})

	mgr := NewManager(4)
	mgr.SetSessionSpace(100, 4) // no FOI yet: ignored
	if mgr.SessionSpace(100) != 0 {
		t.Fatal("SetSessionSpace without FOI must be a no-op")
	}
	mgr.AssignFOI(100, 1, 10, 1)
	mgr.SetSessionSpace(100, 4)
	mgr.UpdateGrid(reg)
	assertDiff(t, "unresolved anchor", mgr.ComputeDiffs()[100], 4, []int64{5}, nil, nil)

	addInSpace(t, reg, 0, &testEntity{id: 1})
	mgr.UpdateGrid(reg)
	assertDiff(t, "anchor resolved", mgr.ComputeDiffs()[100], 0, []int64{1, 6}, nil, []int64{5})

	mgr.RemoveFOI(100)
	if mgr.SessionSpace(100) != 0 {
		t.Fatal("RemoveFOI must clear the session space")
	}
}

func BenchmarkManagerComputeDiffsTwoSpaces(b *testing.B) {
	reg := registry.NewRegistry()
	mgr := NewManager(4)
	for i := 0; i < 100; i++ {
		for _, space := range []registry.SpaceID{0, 9} {
			id := int64(i+1) + int64(space)*1000
			if err := reg.Add(&testEntity{id: id, x: float64(i % 10), y: float64(i / 10)}); err != nil {
				b.Fatal(err)
			}
			if space != 0 {
				reg.SetSpace(id, space)
			}
			if i < 25 {
				mgr.AssignFOI(id, id, 10, 2)
			}
		}
	}
	mgr.UpdateGrid(reg)
	mgr.ComputeDiffs()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mgr.UpdateGrid(reg)
		mgr.ComputeDiffs()
	}
}
