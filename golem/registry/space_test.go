package registry

import (
	"reflect"
	"sort"
	"testing"
)

func spaceOf(t *testing.T, r *Registry, id int64) SpaceID {
	t.Helper()
	space, ok := r.Space(id)
	if !ok {
		t.Fatalf("entity %d has no space", id)
	}
	return space
}

func flushMoves(t *testing.T, r *Registry) []SpaceMove {
	t.Helper()
	flush, err := r.FlushAll()
	if err != nil {
		t.Fatal(err)
	}
	return flush.Moves
}

func TestSpaceInsertDeleteAndCount(t *testing.T) {
	r := NewRegistry()
	if r.SpacesInUse() {
		t.Fatal("empty registry reports spaces in use")
	}
	if _, ok := r.Space(1); ok {
		t.Fatal("unknown entity resolved a space")
	}
	if err := r.AddWithoutSpawn(&lifecycleTestEntity{id: 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.AddWithoutSpawnIn(0, &lifecycleTestEntity{id: 2}); err != nil {
		t.Fatal(err)
	}
	if r.SpacesInUse() || len(r.spaces) != 0 {
		t.Fatal("space 0 entities stored space entries")
	}
	if err := r.AddWithoutSpawnIn(3, &lifecycleTestEntity{id: 3}); err != nil {
		t.Fatal(err)
	}
	if err := r.AddOwnedWithoutSpawnIn(4, &lifecycleTestEntity{id: 4}, 9); err != nil {
		t.Fatal(err)
	}
	if err := r.AddWithoutSpawnIn(5, &lifecycleTestEntity{id: 3}); err == nil {
		t.Fatal("duplicate insertion accepted")
	}
	if spaceOf(t, r, 1) != 0 || spaceOf(t, r, 3) != 3 || spaceOf(t, r, 4) != 4 {
		t.Fatal("inserted spaces are wrong")
	}
	if owner, ok := r.Owner(4); !ok || owner != 9 {
		t.Fatal("owned space insertion lost its owner")
	}
	if !r.SpacesInUse() {
		t.Fatal("non-zero spaces not reported in use")
	}
	flush, err := r.FlushAll()
	if err != nil || len(flush.SpawnIDs) != 4 || flush.Moves != nil {
		t.Fatalf("insert flush: %+v, %v", flush, err)
	}
	r.DeleteEntity(3)
	if _, ok := r.Space(3); ok || !r.SpacesInUse() {
		t.Fatal("deleting one spaced entity left it or cleared the count")
	}
	r.DeleteEntity(4)
	if r.SpacesInUse() || len(r.spaces) != 0 {
		t.Fatal("deleting every spaced entity left space entries")
	}
}

func TestSpaceProvisionalLifecycle(t *testing.T) {
	r := NewRegistry()
	if err := r.AddProvisionalIn(6, &lifecycleTestEntity{id: 1}, 4); err != nil {
		t.Fatal(err)
	}
	if spaceOf(t, r, 1) != 6 || !r.SpacesInUse() {
		t.Fatal("provisional space not resolved")
	}
	if r.SetSpace(1, 2) {
		t.Fatal("SetSpace moved a provisional entity")
	}
	if got := r.AppendSpaced(nil); len(got) != 0 {
		t.Fatalf("provisional entity enumerated: %v", got)
	}
	r.DiscardProvisional(1)
	if _, ok := r.Space(1); ok || r.SpacesInUse() {
		t.Fatal("discard left a space entry")
	}

	if err := r.AddProvisionalIn(6, &lifecycleTestEntity{id: 2}); err != nil {
		t.Fatal(err)
	}
	r.DeleteEntity(2)
	if r.SpacesInUse() {
		t.Fatal("provisional delete left a space entry")
	}

	if err := r.AddProvisionalIn(7, &lifecycleTestEntity{id: 3}); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitProvisional(3); err != nil {
		t.Fatal(err)
	}
	if spaceOf(t, r, 3) != 7 {
		t.Fatal("commit lost the provisional space")
	}
	flush, err := r.FlushAll()
	if err != nil || len(flush.SpawnIDs) != 1 || flush.Moves != nil {
		t.Fatalf("commit flush: %+v, %v", flush, err)
	}
}

func TestSpaceMoveRecordingAndCoalescing(t *testing.T) {
	r := NewRegistry()
	for id := int64(1); id <= 4; id++ {
		if err := r.AddWithoutSpawn(&lifecycleTestEntity{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	flushMoves(t, r)

	if r.SetSpace(99, 1) {
		t.Fatal("SetSpace accepted an unknown entity")
	}
	if !r.SetSpace(1, 0) {
		t.Fatal("same-space SetSpace failed")
	}
	if moves := flushMoves(t, r); moves != nil {
		t.Fatalf("same-space SetSpace recorded %v", moves)
	}

	// Coalesced to the first From and last To, in first-move order.
	r.SetSpace(2, 5)
	r.SetSpace(1, 3)
	r.SetSpace(2, 6)
	// Net no-op moves are omitted.
	r.SetSpace(3, 4)
	r.SetSpace(3, 0)
	// Removed in the same window: the removal supersedes the move.
	r.SetSpace(4, 8)
	r.DeleteEntity(4)
	if spaceOf(t, r, 2) != 6 || spaceOf(t, r, 3) != 0 {
		t.Fatal("SetSpace did not apply immediately")
	}
	flush, err := r.FlushAll()
	if err != nil {
		t.Fatal(err)
	}
	want := []SpaceMove{{ID: 2, From: 0, To: 6}, {ID: 1, From: 0, To: 3}}
	if !reflect.DeepEqual(flush.Moves, want) {
		t.Fatalf("moves = %v, want %v", flush.Moves, want)
	}
	if !reflect.DeepEqual(flush.Removals, []int64{4}) {
		t.Fatalf("removals = %v", flush.Removals)
	}
	if moves := flushMoves(t, r); moves != nil {
		t.Fatalf("FlushAll did not clear moves: %v", moves)
	}

	r.SetSpace(1, 0)
	if moves := flushMoves(t, r); !reflect.DeepEqual(moves, []SpaceMove{{ID: 1, From: 3, To: 0}}) {
		t.Fatalf("return move = %v", moves)
	}
	if spaceOf(t, r, 2) != 6 || !r.SpacesInUse() {
		t.Fatal("remaining spaced entity lost")
	}
	r.SetSpace(2, 0)
	if r.SpacesInUse() {
		t.Fatal("moving every entity back to space 0 kept spaces in use")
	}
}

func TestSpaceMoveOmittedForSameWindowSpawn(t *testing.T) {
	r := NewRegistry()
	if err := r.AddWithoutSpawnIn(2, &lifecycleTestEntity{id: 1}); err != nil {
		t.Fatal(err)
	}
	r.SetSpace(1, 5)
	flush, err := r.FlushAll()
	if err != nil || len(flush.SpawnIDs) != 1 || flush.Moves != nil {
		t.Fatalf("spawn window flush: %+v, %v", flush, err)
	}
	if spaceOf(t, r, 1) != 5 {
		t.Fatal("spawned entity not in its final space")
	}

	// A move before deletion and re-insertion of the same ID does not leak.
	r.SetSpace(1, 6)
	r.DeleteEntity(1)
	if err := r.AddWithoutSpawn(&lifecycleTestEntity{id: 1}); err != nil {
		t.Fatal(err)
	}
	r.SetSpace(1, 7)
	flush, err = r.FlushAll()
	if err != nil || flush.Moves != nil || spaceOf(t, r, 1) != 7 {
		t.Fatalf("reinsert flush: %+v, %v", flush, err)
	}
}

func TestAppendSpaced(t *testing.T) {
	r := NewRegistry()
	if err := r.AddWithoutSpawn(&lifecycleTestEntity{id: 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.AddWithoutSpawnIn(4, &lifecycleTestEntity{id: 2}); err != nil {
		t.Fatal(err)
	}
	prefix := []SpacedEntity{{Space: 99}}
	got := r.AppendSpaced(prefix)
	if len(got) != 3 || got[0].Space != 99 {
		t.Fatalf("AppendSpaced did not extend dst: %v", got)
	}
	rest := got[1:]
	sort.Slice(rest, func(i, j int) bool { return rest[i].Entity.EntityID() < rest[j].Entity.EntityID() })
	if rest[0].Entity.EntityID() != 1 || rest[0].Space != 0 || rest[1].Entity.EntityID() != 2 || rest[1].Space != 4 {
		t.Fatalf("AppendSpaced = %v", rest)
	}
}

func TestReplaceWithoutHooksSwapsSpaces(t *testing.T) {
	r := NewRegistry()
	if err := r.AddWithoutSpawnIn(3, &lifecycleTestEntity{id: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.FlushAll(); err != nil {
		t.Fatal(err)
	}
	r.SetSpace(1, 4)
	invalid := []RestoreEntry{{Entity: &lifecycleTestEntity{id: 2}, Space: 9}, {}}
	if err := r.ReplaceWithoutHooks(invalid); err == nil {
		t.Fatal("invalid restore accepted")
	}
	if spaceOf(t, r, 1) != 4 {
		t.Fatal("invalid restore changed spaces")
	}
	if err := r.ReplaceWithoutHooks([]RestoreEntry{
		{Entity: &lifecycleTestEntity{id: 1}},
		{Entity: &lifecycleTestEntity{id: 2}, Owner: 5, Space: 9},
	}); err != nil {
		t.Fatal(err)
	}
	if spaceOf(t, r, 1) != 0 || spaceOf(t, r, 2) != 9 || !r.SpacesInUse() || len(r.spaces) != 1 {
		t.Fatal("replacement spaces were not swapped")
	}
	if owner, ok := r.Owner(2); !ok || owner != 5 {
		t.Fatal("replacement owner lost")
	}
	flush, err := r.FlushAll()
	if err != nil || flush.Moves != nil || len(flush.SpawnIDs) != 2 {
		t.Fatalf("replacement flush: %+v, %v", flush, err)
	}
	if err := r.ReplaceWithoutHooks(nil); err != nil || r.SpacesInUse() {
		t.Fatalf("empty replacement kept spaces: %v", err)
	}
}
