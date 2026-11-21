package registry

import "testing"

func TestIncarnationLifetimes(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Incarnation(1); ok {
		t.Fatal("unknown entity has an incarnation")
	}
	if err := r.AddWithoutSpawn(&lifecycleTestEntity{id: 1}); err != nil {
		t.Fatal(err)
	}
	first, ok := r.Incarnation(1)
	if !ok || first == 0 {
		t.Fatalf("inserted entity incarnation = %d, %v", first, ok)
	}
	if again, _ := r.Incarnation(1); again != first {
		t.Fatal("incarnation is not stable")
	}

	r.DeleteEntity(1)
	if _, ok := r.Incarnation(1); ok {
		t.Fatal("deleted entity kept its incarnation")
	}
	if err := r.AddWithoutSpawnIn(3, &lifecycleTestEntity{id: 1}); err != nil {
		t.Fatal(err)
	}
	second, ok := r.Incarnation(1)
	if !ok || second == first {
		t.Fatalf("recreated entity incarnation = %d (first %d), %v", second, first, ok)
	}

	// Provisional entities get their incarnation on reservation and keep it
	// through publication.
	if err := r.AddProvisionalIn(2, &lifecycleTestEntity{id: 2}); err != nil {
		t.Fatal(err)
	}
	prepared, ok := r.Incarnation(2)
	if !ok || prepared == first || prepared == second {
		t.Fatalf("provisional incarnation = %d, %v", prepared, ok)
	}
	if err := r.CommitProvisional(2); err != nil {
		t.Fatal(err)
	}
	if committed, _ := r.Incarnation(2); committed != prepared {
		t.Fatalf("commit changed incarnation %d -> %d", prepared, committed)
	}

	if err := r.AddProvisional(&lifecycleTestEntity{id: 4}); err != nil {
		t.Fatal(err)
	}
	r.DiscardProvisional(4)
	if _, ok := r.Incarnation(4); ok {
		t.Fatal("discarded provisional entity kept its incarnation")
	}
	if err := r.AddProvisional(&lifecycleTestEntity{id: 4}); err != nil {
		t.Fatal(err)
	}
	r.DeleteEntity(4)
	if _, ok := r.Incarnation(4); ok {
		t.Fatal("deleted provisional entity kept its incarnation")
	}

	// Replacement gives every entity a new incarnation, also for kept IDs.
	if err := r.ReplaceWithoutHooks([]RestoreEntry{{Entity: &lifecycleTestEntity{id: 1}}, {Entity: &lifecycleTestEntity{id: 5}}}); err != nil {
		t.Fatal(err)
	}
	replaced, ok := r.Incarnation(1)
	if !ok || replaced == second {
		t.Fatalf("replaced entity incarnation = %d (before %d), %v", replaced, second, ok)
	}
	if _, ok := r.Incarnation(2); ok {
		t.Fatal("entity removed by replacement kept its incarnation")
	}
	if inc, ok := r.Incarnation(5); !ok || inc == replaced {
		t.Fatalf("new restored entity incarnation = %d, %v", inc, ok)
	}
}
