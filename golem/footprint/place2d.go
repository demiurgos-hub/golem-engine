package footprint

import (
	"fmt"
	"sync/atomic"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
)

// Placer2D registers 2D footprint shapes on a collision.Backend.
// Backend methods are assumed to run on the game tick goroutine; this type
// does not add synchronization around Backend calls.
type Placer2D struct {
	Backend collision.Backend
	Resolve LayerResolver
	// IDs allocates synthetic negative collision IDs. When nil, a process-wide
	// shared AtomicAllocator is used. See IDAllocator for the reserved
	// negative namespace contract.
	IDs IDAllocator
}

// Handle2D owns the synthetic collision IDs created by Placer2D.Place.
type Handle2D struct {
	backend collision.Backend
	ids     []int64
	removed atomic.Bool
}

// IDs returns a copy of the synthetic collision IDs for this placement.
func (h *Handle2D) IDs() []int64 {
	if h == nil || len(h.ids) == 0 {
		return nil
	}
	out := make([]int64, len(h.ids))
	copy(out, h.ids)
	return out
}

// Remove unregisters every shape ID from the backend.
// It is nil-safe, idempotent, and safe for concurrent callers: only the first
// successful call performs backend removals.
func (h *Handle2D) Remove() {
	if h == nil || !h.removed.CompareAndSwap(false, true) {
		return
	}
	if h.backend == nil {
		return
	}
	for _, id := range h.ids {
		h.backend.Remove(id)
	}
}

// Place adds every shape from fp at the given transform.
// fp.Dimensions must be 2. It resolves all layers and validates the transform
// before mutating the backend; on failure after any Add, already-added shapes
// are removed.
func (p *Placer2D) Place(fp *Footprint, t Transform2D) (*Handle2D, error) {
	if p == nil || p.Backend == nil {
		return nil, fmt.Errorf("footprint: 2D placer backend is required")
	}
	if p.Resolve == nil {
		return nil, fmt.Errorf("footprint: 2D placer LayerResolver is required")
	}
	if fp == nil {
		return nil, fmt.Errorf("footprint: footprint is nil")
	}
	if fp.Dimensions != 2 {
		return nil, fmt.Errorf("footprint: Placer2D requires dimensions=2, got %d", fp.Dimensions)
	}
	if err := requireFinite("translation.x", t.X); err != nil {
		return nil, fmt.Errorf("footprint: %w", err)
	}
	if err := requireFinite("translation.y", t.Y); err != nil {
		return nil, fmt.Errorf("footprint: %w", err)
	}
	if err := validateScale(t.Scale); err != nil {
		return nil, err
	}
	turn, err := quarterTurnIndex(t.RotationDegrees)
	if err != nil {
		return nil, err
	}

	alloc := p.IDs
	if alloc == nil {
		alloc = defaultIDs
	}

	type prepared struct {
		shape collision.Shape
		layer uint32
		mask  uint32
		x, y  float64
		trig  bool
	}
	prep := make([]prepared, 0, len(fp.Shapes))
	for i, sh := range fp.Shapes {
		if err := validateShapeFor2D(fp.GUID, i, sh); err != nil {
			return nil, err
		}
		layer, mask, ok := p.Resolve(sh.Layer)
		if !ok {
			return nil, fmt.Errorf("footprint: GUID %q shape[%d]: unknown layer %q", fp.GUID, i, sh.Layer)
		}
		ox, oy := transformPoint2D(sh.OffsetX, sh.OffsetY, t.Scale, turn)
		var shape collision.Shape
		switch sh.Type {
		case ShapeCircle:
			shape = collision.Circle{R: sh.R * t.Scale}
		case ShapeAABB:
			w, h := transformAABB2D(sh.W, sh.H, t.Scale, turn)
			shape = collision.AABB{W: w, H: h}
		default:
			return nil, fmt.Errorf("footprint: GUID %q shape[%d]: type %q is not valid for 2D placement", fp.GUID, i, sh.Type)
		}
		prep = append(prep, prepared{
			shape: shape,
			layer: layer,
			mask:  mask,
			x:     t.X + ox,
			y:     t.Y + oy,
			trig:  sh.Trigger,
		})
	}

	h := &Handle2D{backend: p.Backend, ids: make([]int64, 0, len(prep))}
	for _, item := range prep {
		id := alloc.Next()
		if id >= 0 {
			h.Remove()
			return nil, fmt.Errorf("footprint: allocator must return negative IDs, got %d", id)
		}
		p.Backend.Add(id, item.shape, item.layer, item.mask, item.trig)
		h.ids = append(h.ids, id)
		p.Backend.Update(id, item.x, item.y)
	}
	return h, nil
}

func validateShapeFor2D(guid string, index int, sh Shape) error {
	prefix := fmt.Sprintf("footprint: GUID %q shape[%d]", guid, index)
	switch sh.Type {
	case ShapeCircle:
		if sh.D != 0 {
			return fmt.Errorf("%s: 2D circle must not have depth", prefix)
		}
	case ShapeAABB:
		if sh.D != 0 {
			return fmt.Errorf("%s: 3D aabb cannot be placed with Placer2D (depth would be lost)", prefix)
		}
	case ShapeSphere:
		return fmt.Errorf("%s: sphere is not valid for 2D placement", prefix)
	}
	return nil
}
