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
