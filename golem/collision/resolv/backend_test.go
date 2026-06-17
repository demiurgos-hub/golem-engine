package collisionresolv_test

import (
	"math"
	"testing"

	collision "golem.collision"
	collisionresolv "golem.collision/resolv"
)

func newBackend(t *testing.T) *collisionresolv.ResolvBackend {
	t.Helper()
	return collisionresolv.New()
}

func TestStep_NoOverlap(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.Circle{R: 1}, 0xFF, 0xFF, false)
	b.Add(2, collision.Circle{R: 1}, 0xFF, 0xFF, false)
	b.Update(1, 0, 0)
	b.Update(2, 10, 0) // far apart

	if got := b.Step(0); len(got) != 0 {
		t.Fatalf("expected 0 contacts, got %d", len(got))
	}
}

func TestStep_OverlapReportsContact(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.Circle{R: 1.5}, 0xFF, 0xFF, false)
	b.Add(2, collision.Circle{R: 1.5}, 0xFF, 0xFF, false)
	b.Update(1, 0, 0)
	b.Update(2, 2, 0) // overlap: combined radii 3 > distance 2

	contacts := b.Step(0)
	if len(contacts) != 1 {
		t.Fatalf("expected 1 contact, got %d", len(contacts))
	}
	c := contacts[0]
	if c.Depth <= 0 {
		t.Errorf("expected Depth > 0 for solid overlap, got %f", c.Depth)
	}
}

func TestStep_LayerMaskMismatch(t *testing.T) {
	b := newBackend(t)
	// Entity 1: layer 0x1, only collides with layer 0x2.
	// Entity 2: layer 0x4, only collides with layer 0x8.
	// Neither includes the other's layer in its mask.
	b.Add(1, collision.Circle{R: 1.5}, 0x1, 0x2, false)
	b.Add(2, collision.Circle{R: 1.5}, 0x4, 0x8, false)
	b.Update(1, 0, 0)
	b.Update(2, 1, 0) // clearly overlapping

	if got := b.Step(0); len(got) != 0 {
		t.Fatalf("expected 0 contacts due to mask mismatch, got %d", len(got))
	}
}

func TestStep_TriggerDepthZero(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.Circle{R: 1.5}, 0xFF, 0xFF, true) // trigger
	b.Add(2, collision.Circle{R: 1.5}, 0xFF, 0xFF, false)
	b.Update(1, 0, 0)
	b.Update(2, 2, 0) // overlapping

	contacts := b.Step(0)
	if len(contacts) != 1 {
		t.Fatalf("expected 1 contact for trigger overlap, got %d", len(contacts))
	}
	if contacts[0].Depth != 0 {
		t.Errorf("trigger contact must have Depth = 0, got %f", contacts[0].Depth)
	}
}

func TestStep_NormalDirection(t *testing.T) {
	b := newBackend(t)
	// Entity 1 is to the LEFT of entity 2. The normal for the contact whose A
	// is entity 1 should push A leftward (negative X). If A happens to be
	// entity 2, the normal should push it rightward (positive X).
	b.Add(1, collision.Circle{R: 1.5}, 0xFF, 0xFF, false)
	b.Add(2, collision.Circle{R: 1.5}, 0xFF, 0xFF, false)
	b.Update(1, -1, 0)
	b.Update(2, 1, 0)

	contacts := b.Step(0)
	if len(contacts) != 1 {
		t.Fatalf("expected 1 contact, got %d", len(contacts))
	}
	c := contacts[0]

	xPos := map[int64]float64{1: -1, 2: 1}
	aX, bX := xPos[c.A], xPos[c.B]

	// Normal should push A away from B along the X axis.
	if aX < bX && c.Normal.X > 0 {
		t.Errorf("entity A (x=%.0f) is left of B (x=%.0f) but Normal.X=%.2f (should be negative)", aX, bX, c.Normal.X)
	}
	if aX > bX && c.Normal.X < 0 {
		t.Errorf("entity A (x=%.0f) is right of B (x=%.0f) but Normal.X=%.2f (should be positive)", aX, bX, c.Normal.X)
	}

	// Normal must be approximately unit length.
	mag := math.Sqrt(c.Normal.X*c.Normal.X + c.Normal.Y*c.Normal.Y)
	if math.Abs(mag-1.0) > 0.01 {
		t.Errorf("normal is not unit length: magnitude = %f", mag)
	}
}

func TestSet_NoopWhenNotRegistered(t *testing.T) {
	b := newBackend(t)
	b.Add(2, collision.Circle{R: 1}, 0xFF, 0xFF, false)
	b.Update(2, 0, 0)
	// Set on an unregistered id must not panic and must not affect other entries.
	b.Set(99, collision.Circle{R: 100}, 0xFF, 0xFF, false)
	if got := b.Step(0); len(got) != 0 {
		t.Fatalf("expected 0 contacts, got %d", len(got))
	}
}

func TestSet_ChangesShape(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.Circle{R: 2}, 0xFF, 0xFF, false)
	b.Add(2, collision.Circle{R: 2}, 0xFF, 0xFF, false)
	b.Update(1, 0, 0)
	b.Update(2, 3, 0) // overlap: combined radii 4 > distance 3

	if got := b.Step(0); len(got) != 1 {
		t.Fatalf("before Set: expected 1 contact, got %d", len(got))
	}

	b.Set(1, collision.Circle{R: 0.5}, 0xFF, 0xFF, false)
	// combined radii 1 < distance 3 — no contact
	if got := b.Step(0); len(got) != 0 {
		t.Fatalf("after Set: expected 0 contacts, got %d", len(got))
	}
}

func TestSet_ChangesLayerMask(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.Circle{R: 2}, 0x1, 0x1, false)
	b.Add(2, collision.Circle{R: 2}, 0x1, 0x1, false)
	b.Update(1, 0, 0)
	b.Update(2, 1, 0) // overlapping

	if got := b.Step(0); len(got) != 1 {
		t.Fatalf("before Set: expected 1 contact, got %d", len(got))
	}

	// Change entity 1 so its mask no longer covers entity 2's layer.
	b.Set(1, collision.Circle{R: 2}, 0x1, 0x2, false)
	if got := b.Step(0); len(got) != 0 {
		t.Fatalf("after Set: expected 0 contacts (layer mismatch), got %d", len(got))
	}
}

func TestSet_ChangesTrigger(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.Circle{R: 2}, 0xFF, 0xFF, false) // solid
	b.Add(2, collision.Circle{R: 2}, 0xFF, 0xFF, false)
	b.Update(1, 0, 0)
	b.Update(2, 1, 0)

	contacts := b.Step(0)
	if len(contacts) != 1 || contacts[0].Depth == 0 {
		t.Fatalf("before Set: expected 1 solid contact, got %v", contacts)
	}

	b.Set(1, collision.Circle{R: 2}, 0xFF, 0xFF, true) // trigger
	contacts = b.Step(0)
	if len(contacts) != 1 || contacts[0].Depth != 0 {
		t.Fatalf("after Set: expected 1 trigger contact (Depth==0), got %v", contacts)
	}
}

func TestSet_PreservesPosition(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.Circle{R: 1}, 0xFF, 0xFF, false)
	b.Add(2, collision.Circle{R: 1}, 0xFF, 0xFF, false)
	b.Update(1, 5, 0)
	b.Update(2, 6, 0) // overlap: combined radii 2 > distance 1

	b.Set(1, collision.Circle{R: 1}, 0xFF, 0xFF, false) // same params, position must be kept
	contacts := b.Step(0)
	if len(contacts) != 1 {
		t.Fatalf("after Set: expected 1 contact (position preserved), got %d", len(contacts))
	}
}

func TestStep_AABBOverlap(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.AABB{W: 2, H: 2}, 0xFF, 0xFF, false)
	b.Add(2, collision.AABB{W: 2, H: 2}, 0xFF, 0xFF, false)
	b.Update(1, 0, 0)
	b.Update(2, 1, 0) // centres 1 unit apart, each extends 1 unit → overlap 1

	contacts := b.Step(0)
	if len(contacts) != 1 {
		t.Fatalf("expected 1 contact for overlapping AABBs, got %d", len(contacts))
	}
	if contacts[0].Depth <= 0 {
		t.Errorf("expected Depth > 0 for AABB overlap, got %f", contacts[0].Depth)
	}
}

func TestOverlapBox_Hit(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.AABB{W: 2, H: 2}, 0xFF, 0xFF, false)
	b.Update(1, 0, 0)

	ids := b.OverlapBox(0, 0, 2, 2, 0xFF)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("expected [1], got %v", ids)
	}
}

func TestOverlapBox_Miss(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.AABB{W: 1, H: 1}, 0xFF, 0xFF, false)
	b.Update(1, 10, 10)

	ids := b.OverlapBox(0, 0, 1, 1, 0xFF)
	if len(ids) != 0 {
		t.Fatalf("expected no hits, got %v", ids)
	}
}

func TestOverlapBox_LayerMaskFilter(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.AABB{W: 2, H: 2}, 0x1, 0xFF, false) // layer 1
	b.Add(2, collision.AABB{W: 2, H: 2}, 0x2, 0xFF, false) // layer 2
	b.Update(1, 0, 0)
	b.Update(2, 0, 0)

	// Query only layer 1; entity 2 (layer 0x2) should be excluded.
	ids := b.OverlapBox(0, 0, 2, 2, 0x1)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("expected only entity 1 (layer 0x1), got %v", ids)
	}
}

func TestOverlapCircle_Hit(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.Circle{R: 1}, 0xFF, 0xFF, false)
	b.Update(1, 0, 0)

	ids := b.OverlapCircle(0, 0, 2, 0xFF)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("expected [1], got %v", ids)
	}
}

func TestOverlapCircle_Miss(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.Circle{R: 0.5}, 0xFF, 0xFF, false)
	b.Update(1, 10, 0)

	ids := b.OverlapCircle(0, 0, 1, 0xFF)
	if len(ids) != 0 {
		t.Fatalf("expected no hits, got %v", ids)
	}
}

func TestOverlapCircle_LayerMaskFilter(t *testing.T) {
	b := newBackend(t)
	b.Add(1, collision.Circle{R: 1}, 0x1, 0xFF, false) // layer 1
	b.Add(2, collision.Circle{R: 1}, 0x2, 0xFF, false) // layer 2
	b.Update(1, 0, 0)
	b.Update(2, 0, 0)

	ids := b.OverlapCircle(0, 0, 2, 0x2)
	if len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("expected only entity 2 (layer 0x2), got %v", ids)
	}
}
