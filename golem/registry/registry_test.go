package registry

import "testing"

type revisionTestEntity struct{}

func (revisionTestEntity) EntityID() int64              { return 1 }
func (revisionTestEntity) TypeName() string             { return "test" }
func (revisionTestEntity) Position() (float32, float32) { return 0, 0 }
func (revisionTestEntity) IsGlobal() bool               { return false }
func (revisionTestEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (revisionTestEntity) FullUpdate() ([]byte, error)  { return nil, nil }

func TestRemovalRevisionDefaultsToNonZero(t *testing.T) {
	if got := removalRevision(revisionTestEntity{}); got != 1 {
		t.Fatalf("removalRevision = %d, want 1", got)
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
