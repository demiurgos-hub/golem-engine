package collision3d

import "testing"

func TestSimpleBackendDetectsSphereSphereContact(t *testing.T) {
	b := NewSimpleBackend()
	b.Add(1, Sphere{R: 1}, 1, 1, false)
	b.Add(2, Sphere{R: 1}, 1, 1, false)
	b.Update(1, 0, 0, 0)
	b.Update(2, 1.5, 0, 0)

	contacts := b.Step(1.0 / 20.0)
	if len(contacts) != 1 {
		t.Fatalf("contacts = %d, want 1", len(contacts))
	}
	c := contacts[0]
	if c.A != 1 || c.B != 2 {
		t.Fatalf("contact pair = %d,%d; want 1,2", c.A, c.B)
	}
	if c.Depth <= 0 {
		t.Fatalf("Depth = %g, want positive", c.Depth)
	}
	if c.Normal.X >= 0 {
		t.Fatalf("Normal.X = %g, want push direction away from B", c.Normal.X)
	}
}

func TestSimpleBackendOverlapSphereFindsAABB(t *testing.T) {
	b := NewSimpleBackend()
	b.Add(1, AABB{W: 2, H: 2, D: 2}, 1, 1, false)
	b.Update(1, 0, 0, 0)

	got := b.OverlapSphere(1.5, 0, 0, 0.75, 1)
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("OverlapSphere = %v, want [1]", got)
	}
}

func TestSimpleBackendRaycastAllSortsHits(t *testing.T) {
	b := NewSimpleBackend()
	b.Add(1, Sphere{R: 0.5}, 1, 1, false)
	b.Add(2, Sphere{R: 0.5}, 1, 1, false)
	b.Update(1, 4, 0, 0)
	b.Update(2, 2, 0, 0)

	hits := b.RaycastAll(Vec3{}, Vec3{X: 10}, 1)
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(hits))
	}
	if hits[0].EntityID != 2 || hits[1].EntityID != 1 {
		t.Fatalf("hit order = %d,%d; want 2,1", hits[0].EntityID, hits[1].EntityID)
	}
}
