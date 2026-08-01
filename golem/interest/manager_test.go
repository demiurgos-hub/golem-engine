package interest

import (
	"reflect"
	"sort"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/registry"
)

type testEntity struct {
	id        int64
	x, y      float64
	global    bool
	full      []byte
	flush     []byte
	fullCalls int
}

func (e *testEntity) EntityID() int64              { return e.id }
func (e *testEntity) TypeName() string             { return "test" }
func (e *testEntity) Position() (float32, float32) { return float32(e.x), float32(e.y) }
func (e *testEntity) IsGlobal() bool               { return e.global }
func (e *testEntity) FlushUpdate() ([]byte, error) { return e.flush, nil }
func (e *testEntity) FullUpdate() ([]byte, error) {
	e.fullCalls++
	return e.full, nil
}

type testEntity3D struct {
	testEntity
	z float64
}

func (e *testEntity3D) Position3D() (float32, float32, float32) {
	return float32(e.x), float32(e.y), float32(e.z)
}

func sortedIDs(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func TestComputeDiffsReusesScratchWithoutLeakingState(t *testing.T) {
	reg := registry.NewRegistry()
	anchor := &testEntity{id: 1, x: 0, y: 0}
	stay := &testEntity{id: 2, x: 2, y: 0}
	enterThenExit := &testEntity{id: 3, x: 4, y: 0}
	global := &testEntity{id: 4, x: 100, y: 100, global: true}

	for _, e := range []*testEntity{anchor, stay, enterThenExit, global} {
		if err := reg.Add(e); err != nil {
			t.Fatalf("Add(%d): %v", e.id, err)
		}
	}

	mgr := NewManager(2)
	mgr.AssignFOI(10, anchor.id, 5, 1)

	mgr.UpdateGrid(reg)
	first := mgr.ComputeDiffs()
	diff1 := first[10]
	if got, want := sortedIDs(diff1.Entered), []int64{1, 2, 3, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tick1 entered = %v, want %v", got, want)
	}

	enterThenExit.x = 20
	mgr.UpdateGrid(reg)
	second := mgr.ComputeDiffs()
	diff2 := second[10]
	if diff1 != diff2 {
		t.Fatalf("expected ComputeDiffs to reuse the per-session Diff object")
	}
	if got, want := sortedIDs(diff2.Stayed), []int64{1, 2, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tick2 stayed = %v, want %v", got, want)
	}
	if got, want := sortedIDs(diff2.Exited), []int64{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tick2 exited = %v, want %v", got, want)
	}
	if len(diff2.Entered) != 0 {
		t.Fatalf("tick2 entered leaked prior data: %v", diff2.Entered)
	}
}

func TestQueryIntoReusesBuffer(t *testing.T) {
	grid := NewGrid(2)
	grid.Insert(1, 0, 0)
	grid.Insert(2, 1, 0)
	grid.Insert(3, 10, 10)

	buf := make([]int64, 0, 4)
	first := grid.QueryInto(0, 0, 2, buf)
	if cap(first) != cap(buf) {
		t.Fatalf("QueryInto changed capacity unexpectedly: got %d want %d", cap(first), cap(buf))
	}
	if got, want := sortedIDs(first), []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first query = %v, want %v", got, want)
	}

	second := grid.QueryInto(10, 10, 1, first)
	if cap(second) != cap(buf) {
		t.Fatalf("reused QueryInto capacity unexpectedly: got %d want %d", cap(second), cap(buf))
	}
	if got, want := sortedIDs(second), []int64{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second query = %v, want %v", got, want)
	}
}

func TestComputeDiffsHonorsHysteresisAndGlobals(t *testing.T) {
	reg := registry.NewRegistry()
	anchor := &testEntity{id: 1, x: 0, y: 0}
	edge := &testEntity{id: 2, x: 5.5, y: 0}
	global := &testEntity{id: 3, x: 100, y: 100, global: true}

	for _, e := range []*testEntity{anchor, edge, global} {
		if err := reg.Add(e); err != nil {
			t.Fatalf("Add(%d): %v", e.id, err)
		}
	}

	mgr := NewManager(2)
	mgr.AssignFOI(10, anchor.id, 5, 1)

	mgr.UpdateGrid(reg)
	diff := mgr.ComputeDiffs()[10]
	if got, want := sortedIDs(diff.Entered), []int64{1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tick1 entered = %v, want %v", got, want)
	}

	edge.x = 5.8
	mgr.UpdateGrid(reg)
	diff = mgr.ComputeDiffs()[10]
	if got, want := sortedIDs(diff.Stayed), []int64{1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tick2 stayed = %v, want %v", got, want)
	}

	edge.x = 4.9
	mgr.UpdateGrid(reg)
	diff = mgr.ComputeDiffs()[10]
	if got, want := sortedIDs(diff.Entered), []int64{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tick3 entered = %v, want %v", got, want)
	}

	edge.x = 5.9
	mgr.UpdateGrid(reg)
	diff = mgr.ComputeDiffs()[10]
	if got, want := sortedIDs(diff.Stayed), []int64{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tick4 stayed = %v, want %v", got, want)
	}

	edge.x = 6.2
	mgr.UpdateGrid(reg)
	diff = mgr.ComputeDiffs()[10]
	if got, want := sortedIDs(diff.Exited), []int64{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tick5 exited = %v, want %v", got, want)
	}
	if got, want := sortedIDs(diff.Stayed), []int64{1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tick5 stayed = %v, want %v", got, want)
	}
}

func TestComputeDiffsProjects3DEntitiesOntoXZPlane(t *testing.T) {
	reg := registry.NewRegistry()
	anchor := &testEntity3D{testEntity: testEntity{id: 1, x: 0, y: 0}, z: 0}
	above := &testEntity3D{testEntity: testEntity{id: 2, x: 3, y: 100}, z: 0}
	distantZ := &testEntity3D{testEntity: testEntity{id: 3, x: 0, y: 1}, z: 20}

	for _, e := range []*testEntity3D{anchor, above, distantZ} {
		if err := reg.Add(e); err != nil {
			t.Fatalf("Add(%d): %v", e.id, err)
		}
	}

	mgr := NewManager(2)
	mgr.AssignFOI(10, anchor.id, 5, 1)

	mgr.UpdateGrid(reg)
	diff := mgr.ComputeDiffs()[10]
	if got, want := sortedIDs(diff.Entered), []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("entered = %v, want %v", got, want)
	}
}

func TestComputeDiffsCandidateMarksTrackRepeatedChurn(t *testing.T) {
	reg := registry.NewRegistry()
	anchor := &testEntity{id: 1, x: 0, y: 0}
	if err := reg.Add(anchor); err != nil {
		t.Fatalf("Add(anchor): %v", err)
	}

	mgr := NewManager(2)
	mgr.AssignFOI(10, anchor.id, 5, 1)

	for tick := 0; tick < 6; tick++ {
		id := int64(100 + tick)
		mover := &testEntity{id: id, x: 4, y: 0}
		if err := reg.Add(mover); err != nil {
			t.Fatalf("Add(%d): %v", id, err)
		}

		mgr.UpdateGrid(reg)
		diff := mgr.ComputeDiffs()[10]
		wantEntered := []int64{id}
		if tick == 0 {
			wantEntered = []int64{1, id}
		}
		if got := sortedIDs(diff.Entered); !reflect.DeepEqual(got, wantEntered) {
			t.Fatalf("tick %d entered = %v, want [%d]", tick, got, id)
		}

		reg.DeleteEntity(id)
		mgr.UpdateGrid(reg)
		diff = mgr.ComputeDiffs()[10]
		if got, want := sortedIDs(diff.Exited), []int64{id}; !reflect.DeepEqual(got, want) {
			t.Fatalf("tick %d exited = %v, want [%d]", tick, got, id)
		}
	}
}

func TestComputeDiffsCompactsCandidateMarks(t *testing.T) {
	reg := registry.NewRegistry()
	anchor := &testEntity{id: 1, x: 0, y: 0}
	if err := reg.Add(anchor); err != nil {
		t.Fatalf("Add(anchor): %v", err)
	}

	mgr := NewManager(2)
	mgr.AssignFOI(10, anchor.id, 5, 1)
	scratch := mgr.diffFor(10)

	for i := 0; i < candidateMarksCompactionMinSize+32; i++ {
		scratch.candidateMarks[int64(1000+i)] = 1
	}

	before := len(scratch.candidateMarks)
	if before <= candidateMarksCompactionMinSize {
		t.Fatalf("candidate marks too small before compaction test: got %d", before)
	}

	steady := &testEntity{id: 42, x: 4, y: 0}
	if err := reg.Add(steady); err != nil {
		t.Fatalf("Add(steady): %v", err)
	}

	mgr.UpdateGrid(reg)
	diff := mgr.ComputeDiffs()[10]
	if got, want := sortedIDs(diff.Entered), []int64{1, 42}; !reflect.DeepEqual(got, want) {
		t.Fatalf("steady entered = %v, want %v", got, want)
	}
	after := len(scratch.candidateMarks)
	if after >= before {
		t.Fatalf("candidate marks did not compact: before=%d after=%d", before, after)
	}
	if after > 4 {
		t.Fatalf("candidate marks after compaction = %d, want a small working set", after)
	}
}

func TestComputeDiffsFilteredEnterStayExitAndGlobals(t *testing.T) {
	reg := registry.NewRegistry()
	anchor := &testEntity{id: 1, x: 0, y: 0}
	near := &testEntity{id: 2, x: 2, y: 0}
	global := &testEntity{id: 3, x: 100, y: 100, global: true}
	for _, e := range []*testEntity{anchor, near, global} {
		if err := reg.Add(e); err != nil {
			t.Fatalf("Add(%d): %v", e.id, err)
		}
	}

	mgr := NewManager(2)
	mgr.AssignFOI(10, anchor.id, 5, 1)
	mgr.UpdateGrid(reg)

	allowed := map[int64]bool{1: true, 2: true, 3: true}
	allow := func(_, entityID int64) bool { return allowed[entityID] }

	diff := mgr.ComputeDiffsFiltered(allow)[10]
	if got, want := sortedIDs(diff.Entered), []int64{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("initial entered = %v, want %v", got, want)
	}

	// Membership loss while still in radius forces exit.
	allowed[2] = false
	diff = mgr.ComputeDiffsFiltered(allow)[10]
	if got, want := sortedIDs(diff.Exited), []int64{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("membership-loss exited = %v, want %v", got, want)
	}
	if got, want := sortedIDs(diff.Stayed), []int64{1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("membership-loss stayed = %v, want %v", got, want)
	}

	// Grouped global only for members.
	allowed[3] = false
	diff = mgr.ComputeDiffsFiltered(allow)[10]
	if got, want := sortedIDs(diff.Exited), []int64{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("global membership-loss exited = %v, want %v", got, want)
	}
	if _, ok := mgr.Known(10)[3]; ok {
		t.Fatal("disallowed global must leave known set")
	}

	allowed[2] = true
	allowed[3] = true
	diff = mgr.ComputeDiffsFiltered(allow)[10]
	if got, want := sortedIDs(diff.Entered), []int64{2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("re-allowed entered = %v, want %v", got, want)
	}
}

func TestComputeDiffsDeletedGlobalExitsWithGlobalAnchor(t *testing.T) {
	// FOI anchored on a global entity has no grid position, so only
	// appendGlobalsFiltered runs. Deleted globals must still Exit.
	reg := registry.NewRegistry()
	anchor := &testEntity{id: 1, x: 0, y: 0, global: true}
	extra := &testEntity{id: 2, x: 50, y: 50, global: true}
	for _, e := range []*testEntity{anchor, extra} {
		if err := reg.Add(e); err != nil {
			t.Fatalf("Add(%d): %v", e.id, err)
		}
	}

	mgr := NewManager(2)
	mgr.AssignFOI(10, anchor.id, 5, 1)
	mgr.UpdateGrid(reg)
	diff := mgr.ComputeDiffs()[10]
	if got, want := sortedIDs(diff.Entered), []int64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("initial entered = %v, want %v", got, want)
	}
	if _, ok := mgr.Known(10)[2]; !ok {
		t.Fatal("expected global 2 in known set")
	}

	reg.DeleteEntity(extra.id)
	mgr.UpdateGrid(reg)
	diff = mgr.ComputeDiffs()[10]
	if got, want := sortedIDs(diff.Exited), []int64{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted global exited = %v, want %v", got, want)
	}
	if _, ok := mgr.Known(10)[2]; ok {
		t.Fatal("deleted global must leave known set")
	}
	if len(diff.Exited) != 1 {
		t.Fatalf("expected exactly one Exited entry, got %v", diff.Exited)
	}

	// Second tick must not re-emit the same exit.
	diff = mgr.ComputeDiffs()[10]
	if len(diff.Exited) != 0 {
		t.Fatalf("repeated exit leak: %v", diff.Exited)
	}
}

func TestComputeDiffsFilteredDeletedGlobalExitsWithGlobalAnchor(t *testing.T) {
	reg := registry.NewRegistry()
	anchor := &testEntity{id: 1, x: 0, y: 0, global: true}
	extra := &testEntity{id: 2, x: 50, y: 50, global: true}
	for _, e := range []*testEntity{anchor, extra} {
		if err := reg.Add(e); err != nil {
			t.Fatalf("Add(%d): %v", e.id, err)
		}
	}

	mgr := NewManager(2)
	mgr.AssignFOI(10, anchor.id, 5, 1)
	mgr.UpdateGrid(reg)
	allow := func(_, _ int64) bool { return true }
	_ = mgr.ComputeDiffsFiltered(allow)[10]

	reg.DeleteEntity(extra.id)
	mgr.UpdateGrid(reg)
	diff := mgr.ComputeDiffsFiltered(allow)[10]
	if got, want := sortedIDs(diff.Exited), []int64{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered deleted global exited = %v, want %v", got, want)
	}
	if _, ok := mgr.Known(10)[2]; ok {
		t.Fatal("deleted global must leave known set")
	}
}

func TestComputeDiffsUnfilteredCompatibility(t *testing.T) {
	reg := registry.NewRegistry()
	anchor := &testEntity{id: 1, x: 0, y: 0}
	near := &testEntity{id: 2, x: 2, y: 0}
	global := &testEntity{id: 3, x: 100, y: 100, global: true}
	for _, e := range []*testEntity{anchor, near, global} {
		if err := reg.Add(e); err != nil {
			t.Fatalf("Add(%d): %v", e.id, err)
		}
	}

	mgr := NewManager(2)
	mgr.AssignFOI(10, anchor.id, 5, 1)
	mgr.UpdateGrid(reg)

	unfiltered := mgr.ComputeDiffs()[10]
	mgr2 := NewManager(2)
	mgr2.AssignFOI(10, anchor.id, 5, 1)
	mgr2.UpdateGrid(reg)
	nilFilter := mgr2.ComputeDiffsFiltered(nil)[10]
	if !reflect.DeepEqual(sortedIDs(unfiltered.Entered), sortedIDs(nilFilter.Entered)) {
		t.Fatalf("ComputeDiffs vs nil filter entered: %v vs %v", unfiltered.Entered, nilFilter.Entered)
	}
}
