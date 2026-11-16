package objecttemplate

import (
	"fmt"
	"math"
	"sync/atomic"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
)

// LayerResolver maps a template layer name to the shape's collision layer bits
// and the layers it collides with. ok is false for unknown names.
type LayerResolver func(name string) (layer, mask uint32, ok bool)

// LayersResolver adapts collision.Layers to a LayerResolver.
func LayersResolver(layers *collision.Layers) LayerResolver {
	return func(name string) (uint32, uint32, bool) {
		if layers == nil {
			return 0, 0, false
		}
		return layers.Lookup(name)
	}
}

// IDAllocator yields collision IDs for placed 2D shapes. Next must return a
// strictly negative ID that is unique among live registrations on the backend;
// zero and positive IDs remain reserved for Golem entities.
type IDAllocator interface{ Next() int64 }

// AtomicAllocator is a concurrency-safe allocator returning -1, -2, ...
type AtomicAllocator struct{ next atomic.Int64 }

// Next returns the next unique negative collision ID.
func (a *AtomicAllocator) Next() int64 { return a.next.Add(-1) }

var defaultIDs = &AtomicAllocator{}

// Transform2D places a 2D template with translation, an exact quarter-turn
// rotation in degrees and positive per-axis scale. Zero scale means unit.
type Transform2D struct {
	X, Y            float64
	RotationDegrees float64
	ScaleX, ScaleY  float64
}

// Placer2D registers 2D template shapes on a collision.Backend. Backend calls
// run on the caller's tick goroutine; Placer2D adds no synchronization.
type Placer2D struct {
	Backend collision.Backend
	Catalog *Catalog
	Resolve LayerResolver
	// IDs allocates negative collision IDs; nil uses a process-wide allocator.
	IDs IDAllocator
}

// Placement2D owns the collision IDs created by one Placer2D.Place.
type Placement2D struct {
	backend  collision.Backend
	template *Template
	ids      []int64
	removed  atomic.Bool
}

// Place adds every shape of templateID at t. It validates the whole placement
// before mutating the backend.
func (pl *Placer2D) Place(templateID string, t Transform2D) (*Placement2D, error) {
	if pl == nil || pl.Backend == nil || pl.Resolve == nil || pl.Catalog == nil || pl.Catalog.Dimensions != 2 {
		return nil, invalid("Placer2D needs a backend, layer resolver and 2D catalog")
	}
	template, ok := pl.Catalog.Template(templateID)
	if !ok {
		return nil, invalid("unknown template %s", templateID)
	}
	sx, sy := t.ScaleX, t.ScaleY
	if sx == 0 && sy == 0 {
		sx, sy = 1, 1
	}
	if !finite(t.X) || !finite(t.Y) || !finite(sx) || !finite(sy) || sx <= 0 || sy <= 0 {
		return nil, invalid("2D placement transform")
	}
	turn, err := quarterTurn(t.RotationDegrees)
	if err != nil {
		return nil, err
	}
	type item struct {
		shape       collision.Shape
		layer, mask uint32
		x, y        float64
		trigger     bool
	}
	items := make([]item, 0, len(template.Shapes))
	for _, s := range template.Shapes {
		layer, mask, ok := pl.Resolve(s.Layer)
		if !ok {
			return nil, invalid("template %s shape %s: unknown layer %q", template.ID, s.ID, s.Layer)
		}
		x, y := rotateQuarter(s.OffsetX*sx, s.OffsetY*sy, turn)
		var shape collision.Shape
		switch s.Kind {
		case ShapeCircle:
			if sx != sy {
				return nil, invalid("template %s shape %s: circles need uniform scale", template.ID, s.ID)
			}
			shape = collision.Circle{R: s.Radius * sx}
		case ShapeBox:
			w, h := s.Width*sx, s.Height*sy
			if turn%2 == 1 {
				w, h = h, w
			}
			shape = collision.AABB{W: w, H: h}
		}
		items = append(items, item{shape: shape, layer: layer, mask: mask, x: t.X + x, y: t.Y + y, trigger: s.Trigger})
	}
	ids := pl.IDs
	if ids == nil {
		ids = defaultIDs
	}
	placement := &Placement2D{backend: pl.Backend, template: template, ids: make([]int64, 0, len(items))}
	for _, it := range items {
		id := ids.Next()
		if id >= 0 {
			placement.Remove()
			return nil, invalid("allocator must return negative IDs, got %d", id)
		}
		pl.Backend.Add(id, it.shape, it.layer, it.mask, it.trigger)
		placement.ids = append(placement.ids, id)
		pl.Backend.Update(id, it.x, it.y)
	}
	return placement, nil
}

// Template returns the placed template.
func (h *Placement2D) Template() *Template { return h.template }

// IDs returns a copy of the placement's collision IDs in template shape order.
func (h *Placement2D) IDs() []int64 {
	if h == nil {
		return nil
	}
	return append([]int64(nil), h.ids...)
}

// Remove unregisters every shape. It is nil-safe, idempotent and safe for
// concurrent callers: only the first call removes.
func (h *Placement2D) Remove() {
	if h == nil || !h.removed.CompareAndSwap(false, true) {
		return
	}
	for _, id := range h.ids {
		h.backend.Remove(id)
	}
}

func quarterTurn(degrees float64) (int, error) {
	if !finite(degrees) {
		return 0, invalid("rotation")
	}
	turn := math.Mod(degrees, 360)
	if turn < 0 {
		turn += 360
	}
	switch turn {
	case 0, 90, 180, 270:
		return int(turn / 90), nil
	}
	return 0, fmt.Errorf("%w: rotation must be a quarter turn (0/90/180/270), got %v", ErrInvalid, degrees)
}

// rotateQuarter turns (x, y) counter-clockwise around Z by turn quarter turns.
func rotateQuarter(x, y float64, turn int) (float64, float64) {
	switch turn {
	case 1:
		return -y, x
	case 2:
		return -x, -y
	case 3:
		return y, -x
	}
	return x, y
}
