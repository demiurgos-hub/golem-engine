package footprint

import (
	"fmt"
	"sync/atomic"

	"github.com/demiurgos-hub/golem-engine/golem/collision3d"
)

// Placer3D registers 3D footprint shapes on a collision3d.Backend.
// Backend methods are assumed to run on the game tick goroutine; this type
// does not add synchronization around Backend calls.
type Placer3D struct {
	Backend collision3d.Backend
	Resolve LayerResolver
	// IDs allocates synthetic negative collision IDs. When nil, a process-wide
	// shared AtomicAllocator is used. See IDAllocator for the reserved
	// negative namespace contract.
	IDs IDAllocator
}

// Handle3D owns the synthetic collision IDs created by Placer3D.Place.
type Handle3D struct {
	backend collision3d.Backend
	ids     []int64
	removed atomic.Bool
}

// IDs returns a copy of the synthetic collision IDs for this placement.
func (h *Handle3D) IDs() []int64 {
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
func (h *Handle3D) Remove() {
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
// fp.Dimensions must be 3. It resolves all layers and validates the transform
// before mutating the backend; on failure after any Add, already-added shapes
// are removed.
func (p *Placer3D) Place(fp *Footprint, t Transform3D) (*Handle3D, error) {
	if p == nil || p.Backend == nil {
		return nil, fmt.Errorf("footprint: 3D placer backend is required")
	}
	if p.Resolve == nil {
		return nil, fmt.Errorf("footprint: 3D placer LayerResolver is required")
	}
	if fp == nil {
		return nil, fmt.Errorf("footprint: footprint is nil")
	}
	if fp.Dimensions != 3 {
		return nil, fmt.Errorf("footprint: Placer3D requires dimensions=3, got %d", fp.Dimensions)
	}
	if err := requireFinite("translation.x", t.X); err != nil {
		return nil, fmt.Errorf("footprint: %w", err)
	}
	if err := requireFinite("translation.y", t.Y); err != nil {
		return nil, fmt.Errorf("footprint: %w", err)
	}
	if err := requireFinite("translation.z", t.Z); err != nil {
		return nil, fmt.Errorf("footprint: %w", err)
	}
	if err := validateScale(t.Scale); err != nil {
		return nil, err
	}
	turn, err := quarterTurnIndex(t.YawDegrees)
	if err != nil {
		return nil, err
	}

	alloc := p.IDs
	if alloc == nil {
		alloc = defaultIDs
	}

	type prepared struct {
		shape   collision3d.Shape
		layer   uint32
		mask    uint32
		x, y, z float64
		trig    bool
	}
	prep := make([]prepared, 0, len(fp.Shapes))
	for i, sh := range fp.Shapes {
		if err := validateShapeFor3D(fp.GUID, i, sh); err != nil {
			return nil, err
		}
		layer, mask, ok := p.Resolve(sh.Layer)
		if !ok {
			return nil, fmt.Errorf("footprint: GUID %q shape[%d]: unknown layer %q", fp.GUID, i, sh.Layer)
		}
		ox, oy, oz := transformPoint3D(sh.OffsetX, sh.OffsetY, sh.OffsetZ, t.Scale, turn)
		var shape collision3d.Shape
		switch sh.Type {
		case ShapeSphere:
			shape = collision3d.Sphere{R: sh.R * t.Scale}
		case ShapeAABB:
			w, h, d := transformAABB3D(sh.W, sh.H, sh.D, t.Scale, turn)
			shape = collision3d.AABB{W: w, H: h, D: d}
		default:
			return nil, fmt.Errorf("footprint: GUID %q shape[%d]: type %q is not valid for 3D placement", fp.GUID, i, sh.Type)
		}
		prep = append(prep, prepared{
			shape: shape,
			layer: layer,
			mask:  mask,
			x:     t.X + ox,
			y:     t.Y + oy,
			z:     t.Z + oz,
			trig:  sh.Trigger,
		})
	}

	h := &Handle3D{backend: p.Backend, ids: make([]int64, 0, len(prep))}
	for _, item := range prep {
		id := alloc.Next()
		if id >= 0 {
			h.Remove()
			return nil, fmt.Errorf("footprint: allocator must return negative IDs, got %d", id)
		}
		p.Backend.Add(id, item.shape, item.layer, item.mask, item.trig)
		h.ids = append(h.ids, id)
		p.Backend.Update(id, item.x, item.y, item.z)
	}
	return h, nil
}

func validateShapeFor3D(guid string, index int, sh Shape) error {
	prefix := fmt.Sprintf("footprint: GUID %q shape[%d]", guid, index)
	switch sh.Type {
	case ShapeAABB:
		if sh.D <= 0 {
			return fmt.Errorf("%s: 2D aabb cannot be placed with Placer3D (depth would be zero)", prefix)
		}
	case ShapeCircle:
		return fmt.Errorf("%s: circle is not valid for 3D placement", prefix)
	}
	return nil
}
