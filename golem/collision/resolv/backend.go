package collisionresolv

import (
	"math"

	"golem.collision"

	"github.com/solarlune/resolv"
)

// entry holds a registered entity's resolv shape and collision filtering data.
type entry struct {
	id        int64
	shape     resolv.IShape
	origShape collision.Shape
	layer     uint32
	mask      uint32
	trigger   bool
}

// ResolvBackend implements collision.Backend using solarlune/resolv for
// detection-only collision (no physics simulation).
//
// Shapes are kept in a plain map and tested pairwise each Step; no resolv.Space
// is used, which avoids the fixed-bounds requirement of resolv.NewSpace and
// suits game servers with unbounded or dynamically-sized worlds.
type ResolvBackend struct {
	entries map[int64]entry
}

// New creates a ResolvBackend ready for use.
func New() *ResolvBackend {
	return &ResolvBackend{entries: make(map[int64]entry)}
}

// Add registers a collision shape for the given entity.
// Unrecognised shape types are silently ignored.
func (b *ResolvBackend) Add(entityID int64, shape collision.Shape, layer, mask uint32, trigger bool) {
	var rs resolv.IShape
	switch s := shape.(type) {
	case collision.Circle:
		rs = resolv.NewCircle(0, 0, s.R)
	case collision.AABB:
		hw, hh := s.W/2, s.H/2
		// Points are clockwise offsets from the polygon's centre position.
		rs = resolv.NewConvexPolygon(0, 0, []float64{
			-hw, -hh,
			hw, -hh,
			hw, hh,
			-hw, hh,
		})
	default:
		return
	}
	b.entries[entityID] = entry{id: entityID, shape: rs, origShape: shape, layer: layer, mask: mask, trigger: trigger}
}

// Remove unregisters the collision shape for the given entity.
func (b *ResolvBackend) Remove(entityID int64) {
	delete(b.entries, entityID)
}

// Set replaces the shape, layer, mask, and trigger flag for a registered
// entity. The entity's current position is preserved. No-op if entityID is
// not registered.
func (b *ResolvBackend) Set(entityID int64, shape collision.Shape, layer, mask uint32, trigger bool) {
	old, ok := b.entries[entityID]
	if !ok {
		return
	}
	pos := old.shape.Position()
	b.Add(entityID, shape, layer, mask, trigger)
	b.entries[entityID].shape.SetPositionVec(pos)
}

// Update synchronises the entity's world-space position into its resolv shape.
func (b *ResolvBackend) Update(entityID int64, x, y float64) {
	e, ok := b.entries[entityID]
	if !ok {
		return
	}
	e.shape.SetPosition(x, y)
}

// Step tests all pairs of registered shapes for intersection.
// A pair is tested only when both entities include each other's layer in their
// collision mask. dt is accepted for interface compatibility but ignored (resolv
// does not simulate movement).
//
// Trigger semantics: resolv has no native trigger concept. When either entity in
// a pair is a trigger, the contact is emitted with Depth = 0 (overlap detected,
// no push-out required). Solid pairs use the MTV magnitude as Depth.
func (b *ResolvBackend) Step(_ float64) []collision.Contact {
	// Snapshot to a slice for indexed O(n²) iteration without revisiting pairs.
	entries := make([]entry, 0, len(b.entries))
	for _, e := range b.entries {
		entries = append(entries, e)
	}

	var contacts []collision.Contact
	for i := 0; i < len(entries); i++ {
		a := entries[i]
		for j := i + 1; j < len(entries); j++ {
			bEnt := entries[j]
			// Both parties must include the other's layer in their mask.
			if (a.layer&bEnt.mask) == 0 || (bEnt.layer&a.mask) == 0 {
				continue
			}
			is := a.shape.Intersection(bEnt.shape)
			if is.IsEmpty() {
				continue
			}
			depth := math.Sqrt(is.MTV.X*is.MTV.X + is.MTV.Y*is.MTV.Y)
			if a.trigger || bEnt.trigger {
				// Trigger overlap: signal detection without push-out.
				// Normal carries the approach direction when the MTV is non-zero.
				var normal collision.Vec2
				if depth > 0 {
					normal = collision.Vec2{X: is.MTV.X / depth, Y: is.MTV.Y / depth}
				}
				contacts = append(contacts, collision.Contact{
					A: a.id, B: bEnt.id, Normal: normal, Depth: 0,
				})
				continue
			}
			if depth == 0 {
				continue
			}
			contacts = append(contacts, collision.Contact{
				A:      a.id,
				B:      bEnt.id,
				Normal: collision.Vec2{X: is.MTV.X / depth, Y: is.MTV.Y / depth},
				Depth:  depth,
			})
		}
	}
	return contacts
}

// ReadBack is a no-op for resolv: it does not simulate movement so entity
// positions are never modified by the backend.
func (b *ResolvBackend) ReadBack(_ func(entityID int64, x, y float64)) {}

// OverlapBox returns the IDs of all registered entities whose shapes overlap
// the axis-aligned box centred at (cx, cy) with half-extents (hw, hh).
// Only entities whose layer has at least one bit in layerMask are returned.
func (b *ResolvBackend) OverlapBox(cx, cy, hw, hh float64, layerMask uint32) []int64 {
	var out []int64
	for _, e := range b.entries {
		if e.layer&layerMask == 0 {
			continue
		}
		ex, ey := e.shape.Position().X, e.shape.Position().Y
		if overlapBoxShape(cx, cy, hw, hh, ex, ey, e.origShape) {
			out = append(out, e.id)
		}
	}
	return out
}

// OverlapCircle returns the IDs of all registered entities whose shapes overlap
// the circle centred at (cx, cy) with the given radius.
// Only entities whose layer has at least one bit in layerMask are returned.
func (b *ResolvBackend) OverlapCircle(cx, cy, radius float64, layerMask uint32) []int64 {
	var out []int64
	for _, e := range b.entries {
		if e.layer&layerMask == 0 {
			continue
		}
		ex, ey := e.shape.Position().X, e.shape.Position().Y
		if overlapCircleShape(cx, cy, radius, ex, ey, e.origShape) {
			out = append(out, e.id)
		}
	}
	return out
}

// overlapBoxShape reports whether the AABB centred at (qx, qy) with half-extents
// (hw, hh) overlaps the entity shape centred at (ex, ey).
func overlapBoxShape(qx, qy, hw, hh, ex, ey float64, s collision.Shape) bool {
	switch shape := s.(type) {
	case collision.AABB:
		ehw, ehh := shape.W/2, shape.H/2
		return math.Abs(qx-ex) < hw+ehw && math.Abs(qy-ey) < hh+ehh
	case collision.Circle:
		// Closest point on the query AABB to the circle centre.
		cx := math.Max(qx-hw, math.Min(ex, qx+hw))
		cy := math.Max(qy-hh, math.Min(ey, qy+hh))
		dx, dy := ex-cx, ey-cy
		return dx*dx+dy*dy < shape.R*shape.R
	}
	return false
}

// overlapCircleShape reports whether the circle centred at (qx, qy) with the
// given radius overlaps the entity shape centred at (ex, ey).
func overlapCircleShape(qx, qy, r, ex, ey float64, s collision.Shape) bool {
	switch shape := s.(type) {
	case collision.Circle:
		dx, dy := qx-ex, qy-ey
		total := r + shape.R
		return dx*dx+dy*dy < total*total
	case collision.AABB:
		// Closest point on the entity AABB to the query circle centre.
		cx := math.Max(ex-shape.W/2, math.Min(qx, ex+shape.W/2))
		cy := math.Max(ey-shape.H/2, math.Min(qy, ey+shape.H/2))
		dx, dy := qx-cx, qy-cy
		return dx*dx+dy*dy < r*r
	}
	return false
}

// Raycast returns the first registered entity hit by the segment (x1,y1)→(x2,y2).
// Only entities whose layer has at least one bit in layerMask are considered.
func (b *ResolvBackend) Raycast(x1, y1, x2, y2 float64, layerMask uint32) (collision.RaycastHit, bool) {
	hits := b.castSegment(x1, y1, x2, y2, 0, 0, layerMask, false)
	if len(hits) == 0 {
		return collision.RaycastHit{}, false
	}
	return hits[0], true
}

// RaycastAll returns all registered entities hit by the segment (x1,y1)→(x2,y2),
// sorted by Fraction (closest first).
func (b *ResolvBackend) RaycastAll(x1, y1, x2, y2 float64, layerMask uint32) []collision.RaycastHit {
	return b.castSegment(x1, y1, x2, y2, 0, 0, layerMask, true)
}

// BoxCast sweeps an AABB with half-extents (hw, hh) from (ox, oy) by (dx, dy)
// and returns the first entity hit.
func (b *ResolvBackend) BoxCast(ox, oy, hw, hh, dx, dy float64, layerMask uint32) (collision.RaycastHit, bool) {
	hits := b.castSegment(ox, oy, ox+dx, oy+dy, hw, hh, layerMask, false)
	if len(hits) == 0 {
		return collision.RaycastHit{}, false
	}
	return hits[0], true
}

// BoxCastAll returns all entities hit by sweeping an AABB with half-extents
// (hw, hh) from (ox, oy) by (dx, dy), sorted by Fraction.
func (b *ResolvBackend) BoxCastAll(ox, oy, hw, hh, dx, dy float64, layerMask uint32) []collision.RaycastHit {
	return b.castSegment(ox, oy, ox+dx, oy+dy, hw, hh, layerMask, true)
}

// CircleCast sweeps a circle with the given radius from (ox, oy) by (dx, dy)
// and returns the first entity hit.
func (b *ResolvBackend) CircleCast(ox, oy, radius, dx, dy float64, layerMask uint32) (collision.RaycastHit, bool) {
	hits := b.castSegment(ox, oy, ox+dx, oy+dy, -radius, 0, layerMask, false)
	if len(hits) == 0 {
		return collision.RaycastHit{}, false
	}
	return hits[0], true
}

// CircleCastAll returns all entities hit by sweeping a circle with the given
// radius from (ox, oy) by (dx, dy), sorted by Fraction.
func (b *ResolvBackend) CircleCastAll(ox, oy, radius, dx, dy float64, layerMask uint32) []collision.RaycastHit {
	return b.castSegment(ox, oy, ox+dx, oy+dy, -radius, 0, layerMask, true)
}

// castSegment is the shared cast implementation for the resolv backend.
// qhw/qhh encode the query shape using the same convention as the cp backend:
//
//	qhw == 0, qhh == 0  → ray
//	qhw > 0, qhh > 0    → box sweep (AABB half-extents)
//	qhw < 0, qhh == 0   → circle sweep (radius = -qhw)
func (b *ResolvBackend) castSegment(px, py, qx, qy, qhw, qhh float64, layerMask uint32, collectAll bool) []collision.RaycastHit {
	dx, dy := qx-px, qy-py
	if dx*dx+dy*dy == 0 {
		return nil
	}

	var hits []collision.RaycastHit

	for _, e := range b.entries {
		if e.layer&layerMask == 0 {
			continue
		}
		ex, ey := e.shape.Position().X, e.shape.Position().Y

		var t float64
		var n collision.Vec2
		var ok bool

		switch {
		case qhw == 0 && qhh == 0:
			t, n, ok = castRayVsShape(px, py, qx, qy, ex, ey, e.origShape)
		case qhw > 0 && qhh > 0:
			t, n, ok = castBoxVsShape(px, py, qx, qy, qhw, qhh, ex, ey, e.origShape)
		default:
			t, n, ok = castCircleVsShape(px, py, qx, qy, -qhw, ex, ey, e.origShape)
		}

		if !ok {
			continue
		}
		hits = append(hits, collision.RaycastHit{
			EntityID: e.id,
			Point:    collision.Vec2{X: px + t*dx, Y: py + t*dy},
			Normal:   n,
			Fraction: t,
		})
		if !collectAll {
			break
		}
	}

	if len(hits) == 0 {
		return nil
	}
	collision.SortHits(hits)
	if !collectAll {
		return hits[:1]
	}
	return hits
}

func castRayVsShape(px, py, qx, qy, ex, ey float64, s collision.Shape) (float64, collision.Vec2, bool) {
	switch shape := s.(type) {
	case collision.Circle:
		return collision.SegmentVsCircle(px, py, qx, qy, ex, ey, shape.R)
	case collision.AABB:
		return collision.SegmentVsAABB(px, py, qx, qy, ex, ey, shape.W/2, shape.H/2)
	}
	return 0, collision.Vec2{}, false
}

func castBoxVsShape(px, py, qx, qy, qhw, qhh, ex, ey float64, s collision.Shape) (float64, collision.Vec2, bool) {
	switch shape := s.(type) {
	case collision.AABB:
		return collision.SegmentVsAABB(px, py, qx, qy, ex, ey, shape.W/2+qhw, shape.H/2+qhh)
	case collision.Circle:
		return collision.SegmentVsRoundedRect(px, py, qx, qy, ex, ey, qhw, qhh, shape.R)
	}
	return 0, collision.Vec2{}, false
}

func castCircleVsShape(px, py, qx, qy, r, ex, ey float64, s collision.Shape) (float64, collision.Vec2, bool) {
	switch shape := s.(type) {
	case collision.Circle:
		return collision.SegmentVsCircle(px, py, qx, qy, ex, ey, r+shape.R)
	case collision.AABB:
		return collision.SegmentVsRoundedRect(px, py, qx, qy, ex, ey, shape.W/2, shape.H/2, r)
	}
	return 0, collision.Vec2{}, false
}
