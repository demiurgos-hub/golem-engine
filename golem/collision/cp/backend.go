package collisioncp

import (
	"math"

	"github.com/demiurgos-hub/golem-engine/golem/collision"

	cp "github.com/jakecoffman/cp/v2"
)

// bodyEntry holds a registered entity's cp body, shape, trigger flag, and
// whether the body is dynamic (physics-simulated) or kinematic (game-driven).
type bodyEntry struct {
	body    *cp.Body
	shape   *cp.Shape
	trigger bool
	dynamic bool
}

// CpBackend implements collision.Backend using jakecoffman/cp (Chipmunk2D port).
//
// Entities registered with Add are kinematic: game logic remains authoritative
// over their positions (set via Update each tick). Entities registered with
// AddDynamic are dynamic: cp integrates their velocity, applies collision
// impulses, and writes corrected positions back via ReadBack. Both body types
// can coexist in the same space.
//
// SetVelocity and Space are additional methods available on the concrete
// *CpBackend type only; they are not part of collision.Backend.
type CpBackend struct {
	space   *cp.Space
	entries map[int64]bodyEntry
}

// New creates a CpBackend with a zero-gravity space.
func New() *CpBackend {
	space := cp.NewSpace()
	return &CpBackend{
		space:   space,
		entries: make(map[int64]bodyEntry),
	}
}

// Space returns the underlying cp.Space for callers that need direct access
// (e.g. to set gravity, damping, or add static terrain shapes).
func (b *CpBackend) Space() *cp.Space {
	return b.space
}

// Add registers a collision shape for the given entity as a kinematic body.
// Game logic drives the entity position each tick via Update; cp reports
// contacts but does not autonomously move kinematic bodies.
// Unrecognised shape types are silently ignored.
func (b *CpBackend) Add(entityID int64, shape collision.Shape, layer, mask uint32, trigger bool) {
	body := cp.NewKinematicBody()
	body.UserData = entityID

	var cpShape *cp.Shape
	switch s := shape.(type) {
	case collision.Circle:
		cpShape = cp.NewCircle(body, s.R, cp.Vector{})
	case collision.AABB:
		cpShape = cp.NewBox(body, s.W, s.H, 0)
	default:
		return
	}

	// cp uses uint for layer/mask categories; convert from uint32.
	cpShape.SetFilter(cp.NewShapeFilter(cp.NO_GROUP, uint(layer), uint(mask)))
	cpShape.SetSensor(trigger)

	b.space.AddBody(body)
	b.space.AddShape(cpShape)

	b.entries[entityID] = bodyEntry{body: body, shape: cpShape, trigger: trigger}
}

// AddDynamic registers a collision shape for the given entity as a dynamic
// body. cp integrates the body's velocity, applies collision-response
// impulses when it contacts other solid shapes, and reports the corrected
// position via ReadBack each tick. Update is a no-op for dynamic bodies—cp
// owns their position after registration.
//
// x, y set the initial world-space position. Use the entity's current
// Position() at spawn time.
//
// mass and moment control the body's inertia. Use Chipmunk's helpers for
// sensible values (e.g. cp.MomentForCircle, cp.MomentForBox).
//
// When trigger is true the shape is a sensor: overlaps are detected and
// reported, but cp does not generate collision-response impulses for it.
//
// Unrecognised shape types are silently ignored.
func (b *CpBackend) AddDynamic(entityID int64, shape collision.Shape, layer, mask uint32, trigger bool, mass, moment, x, y float64) {
	body := cp.NewBody(mass, moment)
	body.SetPosition(cp.Vector{X: x, Y: y})
	body.UserData = entityID

	var cpShape *cp.Shape
	switch s := shape.(type) {
	case collision.Circle:
		cpShape = cp.NewCircle(body, s.R, cp.Vector{})
	case collision.AABB:
		cpShape = cp.NewBox(body, s.W, s.H, 0)
	default:
		return
	}

	cpShape.SetFilter(cp.NewShapeFilter(cp.NO_GROUP, uint(layer), uint(mask)))
	cpShape.SetSensor(trigger)

	b.space.AddBody(body)
	b.space.AddShape(cpShape)

	b.entries[entityID] = bodyEntry{body: body, shape: cpShape, trigger: trigger, dynamic: true}
}

// Remove unregisters the entity's body and shape from the space.
func (b *CpBackend) Remove(entityID int64) {
	e, ok := b.entries[entityID]
	if !ok {
		return
	}
	b.space.RemoveShape(e.shape)
	b.space.RemoveBody(e.body)
	delete(b.entries, entityID)
}

// Set replaces the shape, layer, mask, and trigger flag for a registered
// entity. The entity's current position is preserved. No-op if entityID is
// not registered.
//
// If the entity was registered as dynamic (via AddDynamic), the new body is
// also dynamic with the same mass, moment, and velocity. If kinematic, the
// new body is kinematic.
//
// Chipmunk shapes cannot be resized in place, so Set removes the existing
// body and shape from the space and creates new ones.
func (b *CpBackend) Set(entityID int64, shape collision.Shape, layer, mask uint32, trigger bool) {
	e, ok := b.entries[entityID]
	if !ok {
		return
	}
	pos := e.body.Position()
	isDynamic := e.dynamic
	if isDynamic {
		mass := e.body.Mass()
		moment := e.body.Moment()
		vel := e.body.Velocity()
		b.Remove(entityID)
		b.AddDynamic(entityID, shape, layer, mask, trigger, mass, moment, pos.X, pos.Y)
		b.SetVelocity(entityID, vel.X, vel.Y)
	} else {
		b.Remove(entityID)
		b.Add(entityID, shape, layer, mask, trigger)
		b.Update(entityID, pos.X, pos.Y)
	}
}

// Update synchronises the entity's world-space position into its cp body.
// For dynamic bodies this is a no-op: cp owns their position after
// registration and overriding it each tick would defeat the physics solver.
func (b *CpBackend) Update(entityID int64, x, y float64) {
	e, ok := b.entries[entityID]
	if !ok || e.dynamic {
		return
	}
	e.body.SetPosition(cp.Vector{X: x, Y: y})
}

// SetVelocity sets the velocity of a registered entity's cp body. This is the
// primary way to drive dynamic bodies from game code—call it in OnTick before
// the collision step runs. No-op if the entity is not registered.
//
// This method exists on *CpBackend only; it is not part of collision.Backend.
// Keep a concrete *CpBackend reference (the same one passed to
// Server.SetCollisionBackend) to call it.
func (b *CpBackend) SetVelocity(entityID int64, vx, vy float64) {
	e, ok := b.entries[entityID]
	if !ok {
		return
	}
	e.body.SetVelocityVector(cp.Vector{X: vx, Y: vy})
}

// Step advances the cp simulation by dt and collects all contacts for this
// frame. Each colliding pair is reported once with the normal pointing in the
// push-out direction for entity A.
//
// Contact detection uses SpaceShapeQuery so it works for all body type
// combinations (including kinematic-kinematic). space.Step is still called
// so that dynamic bodies receive physics integration and collision response.
//
// Trigger semantics: if either entity in a pair is a trigger (sensor shape),
// the contact is emitted with Depth = 0. For solid pairs, contacts with
// non-positive depth are skipped (shapes merely touching, not penetrating).
func (b *CpBackend) Step(dt float64) []collision.Contact {
	// Advance physics; integrates dynamic bodies and resolves collision
	// response. No-op for kinematic bodies.
	b.space.Step(dt)

	// Seen set prevents reporting the same pair twice (ShapeQuery visits
	// each pair from both sides).
	seen := make(map[[2]int64]struct{})
	var contacts []collision.Contact

	for aID, aEntry := range b.entries {
		// ShapeQuery finds all shapes in the space overlapping aEntry.shape.
		// It applies ShapeFilter, so layer/mask rules are respected.
		// It does NOT exclude kinematic-kinematic pairs, unlike EachArbiter.
		b.space.ShapeQuery(aEntry.shape, func(found *cp.Shape, points *cp.ContactPointSet) {
			bID, ok := found.Body().UserData.(int64)
			if !ok {
				return
			}

			pair := [2]int64{aID, bID}
			pairRev := [2]int64{bID, aID}
			if _, exists := seen[pair]; exists {
				return
			}
			if _, exists := seen[pairRev]; exists {
				return
			}
			seen[pair] = struct{}{}

			if points.Count == 0 {
				return
			}

			// ShapesCollide(a=aEntry, b=found) sets Normal pointing from
			// aEntry toward found (A → B). Negate to get the push-out
			// direction for A (away from B).
			n := collision.Vec2{X: -points.Normal.X, Y: -points.Normal.Y}

			bEntry := b.entries[bID]
			if aEntry.trigger || bEntry.trigger {
				// Trigger overlap: signal detection without push-out.
				contacts = append(contacts, collision.Contact{
					A: aID, B: bID, Normal: n, Depth: 0,
				})
				return
			}

			// Distance < 0 when shapes are penetrating; depth > 0 = overlap.
			depth := -points.Points[0].Distance
			if depth <= 0 {
				return
			}
			contacts = append(contacts, collision.Contact{
				A: aID, B: bID, Normal: n, Depth: depth,
			})
		})
	}

	return contacts
}

// ReadBack calls fn for each registered entity with its current body position.
// For kinematic bodies this mirrors what was last set by Update. For dynamic
// bodies the position reflects cp's post-step integration and collision
// resolution, which is how physics-corrected positions are written back to
// entities via PositionWriter each tick.
//
// fn is only called for bodies whose UserData is an int64 (i.e., bodies
// managed by this backend). Bodies added directly via Space() with other
// UserData types are silently skipped.
//
// Note: ReadBack only has an effect on entities whose type implements
// PositionWriter (SetPosition(x, y float32)). All generated Synced* types
// implement it; custom entity types must add SetPosition to receive
// physics-corrected positions.
func (b *CpBackend) ReadBack(fn func(entityID int64, x, y float64)) {
	b.space.EachBody(func(body *cp.Body) {
		id, ok := body.UserData.(int64)
		if !ok {
			return
		}
		p := body.Position()
		fn(id, p.X, p.Y)
	})
}

// OverlapBox returns the IDs of all registered entities whose shapes overlap
// the axis-aligned box centred at (cx, cy) with half-extents (hw, hh).
// Only entities whose layer has at least one bit in layerMask are returned.
// Uses a bounding-box broadphase via space.BBQuery followed by exact
// narrowphase per entity; no persistent shape is registered.
func (b *CpBackend) OverlapBox(cx, cy, hw, hh float64, layerMask uint32) []int64 {
	bb := cp.NewBBForExtents(cp.Vector{X: cx, Y: cy}, hw, hh)
	filter := cp.NewShapeFilter(cp.NO_GROUP, cp.ALL_CATEGORIES, uint(layerMask))

	var out []int64
	b.space.BBQuery(bb, filter, func(found *cp.Shape, _ any) {
		id, ok := found.Body().UserData.(int64)
		if !ok {
			return
		}
		e, ok := b.entries[id]
		if !ok {
			return
		}
		pos := e.body.Position()
		if overlapBoxCpShape(cx, cy, hw, hh, pos.X, pos.Y, found) {
			out = append(out, id)
		}
	}, nil)
	return out
}

// OverlapCircle returns the IDs of all registered entities whose shapes overlap
// the circle centred at (cx, cy) with the given radius.
// Only entities whose layer has at least one bit in layerMask are returned.
func (b *CpBackend) OverlapCircle(cx, cy, radius float64, layerMask uint32) []int64 {
	bb := cp.NewBBForCircle(cp.Vector{X: cx, Y: cy}, radius)
	filter := cp.NewShapeFilter(cp.NO_GROUP, cp.ALL_CATEGORIES, uint(layerMask))

	var out []int64
	b.space.BBQuery(bb, filter, func(found *cp.Shape, _ any) {
		id, ok := found.Body().UserData.(int64)
		if !ok {
			return
		}
		e, ok := b.entries[id]
		if !ok {
			return
		}
		pos := e.body.Position()
		if overlapCircleCpShape(cx, cy, radius, pos.X, pos.Y, found) {
			out = append(out, id)
		}
	}, nil)
	return out
}

// Raycast returns the first registered entity hit by the segment (x1,y1)→(x2,y2).
// Only entities whose layer has at least one bit in layerMask are considered.
func (b *CpBackend) Raycast(x1, y1, x2, y2 float64, layerMask uint32) (collision.RaycastHit, bool) {
	hits := b.castSegment(x1, y1, x2, y2, 0, 0, layerMask, false)
	if len(hits) == 0 {
		return collision.RaycastHit{}, false
	}
	return hits[0], true
}

// RaycastAll returns all registered entities hit by the segment (x1,y1)→(x2,y2),
// sorted by Fraction (closest first).
func (b *CpBackend) RaycastAll(x1, y1, x2, y2 float64, layerMask uint32) []collision.RaycastHit {
	return b.castSegment(x1, y1, x2, y2, 0, 0, layerMask, true)
}

// BoxCast sweeps an AABB with half-extents (hw, hh) from (ox, oy) by (dx, dy)
// and returns the first entity hit.
func (b *CpBackend) BoxCast(ox, oy, hw, hh, dx, dy float64, layerMask uint32) (collision.RaycastHit, bool) {
	hits := b.castSegment(ox, oy, ox+dx, oy+dy, hw, hh, layerMask, false)
	if len(hits) == 0 {
		return collision.RaycastHit{}, false
	}
	return hits[0], true
}

// BoxCastAll returns all entities hit by sweeping an AABB with half-extents
// (hw, hh) from (ox, oy) by (dx, dy), sorted by Fraction.
func (b *CpBackend) BoxCastAll(ox, oy, hw, hh, dx, dy float64, layerMask uint32) []collision.RaycastHit {
	return b.castSegment(ox, oy, ox+dx, oy+dy, hw, hh, layerMask, true)
}

// CircleCast sweeps a circle with the given radius from (ox, oy) by (dx, dy)
// and returns the first entity hit.
func (b *CpBackend) CircleCast(ox, oy, radius, dx, dy float64, layerMask uint32) (collision.RaycastHit, bool) {
	hits := b.castSegment(ox, oy, ox+dx, oy+dy, -radius, 0, layerMask, false)
	if len(hits) == 0 {
		return collision.RaycastHit{}, false
	}
	return hits[0], true
}

// CircleCastAll returns all entities hit by sweeping a circle with the given
// radius from (ox, oy) by (dx, dy), sorted by Fraction.
func (b *CpBackend) CircleCastAll(ox, oy, radius, dx, dy float64, layerMask uint32) []collision.RaycastHit {
	return b.castSegment(ox, oy, ox+dx, oy+dy, -radius, 0, layerMask, true)
}

// castSegment is the shared implementation for all cast methods.
// qhw/qhh encode the query shape:
//   - qhw == 0, qhh == 0  → ray/line segment
//   - qhw > 0, qhh > 0    → box sweep (AABB half-extents)
//   - qhw < 0, qhh == 0   → circle sweep (radius = -qhw)
//
// When collectAll is false the function returns after finding the first hit.
func (b *CpBackend) castSegment(px, py, qx, qy, qhw, qhh float64, layerMask uint32, collectAll bool) []collision.RaycastHit {
	dx, dy := qx-px, qy-py
	segLen := math.Sqrt(dx*dx + dy*dy)
	if segLen == 0 {
		return nil
	}

	// Broadphase: query the bounding box of the entire sweep.
	minX := math.Min(px, qx)
	maxX := math.Max(px, qx)
	minY := math.Min(py, qy)
	maxY := math.Max(py, qy)
	pad := math.Abs(qhw)
	if qhh > 0 {
		pad = math.Max(pad, qhh)
	}
	bb := cp.NewBBForExtents(
		cp.Vector{X: (minX + maxX) / 2, Y: (minY + maxY) / 2},
		(maxX-minX)/2+pad,
		(maxY-minY)/2+pad,
	)
	filter := cp.NewShapeFilter(cp.NO_GROUP, cp.ALL_CATEGORIES, uint(layerMask))

	var hits []collision.RaycastHit

	b.space.BBQuery(bb, filter, func(found *cp.Shape, _ any) {
		id, ok := found.Body().UserData.(int64)
		if !ok {
			return
		}
		e, ok := b.entries[id]
		if !ok {
			return
		}
		pos := e.body.Position()
		ex, ey := pos.X, pos.Y

		var t float64
		var n collision.Vec2
		var hit bool

		switch {
		case qhw == 0 && qhh == 0:
			// Raycast
			hit, t, n = castRayVsCpShape(px, py, qx, qy, ex, ey, found)
		case qhw > 0 && qhh > 0:
			// BoxCast: Minkowski-expand the entity shape by (qhw,qhh)
			hit, t, n = castBoxVsCpShape(px, py, qx, qy, qhw, qhh, ex, ey, found)
		default:
			// CircleCast: radius = -qhw
			r := -qhw
			hit, t, n = castCircleVsCpShape(px, py, qx, qy, r, ex, ey, found)
		}

		if !hit {
			return
		}
		hx, hy := px+t*dx, py+t*dy
		hits = append(hits, collision.RaycastHit{
			EntityID: id,
			Point:    collision.Vec2{X: hx, Y: hy},
			Normal:   n,
			Fraction: t,
		})
	}, nil)

	if len(hits) == 0 {
		return nil
	}
	collision.SortHits(hits)
	if !collectAll && len(hits) > 0 {
		return hits[:1]
	}
	return hits
}

// castRayVsCpShape dispatches a ray test to the correct shape type.
func castRayVsCpShape(px, py, qx, qy, ex, ey float64, s *cp.Shape) (bool, float64, collision.Vec2) {
	switch s.Class.(type) {
	case *cp.Circle:
		r := s.Class.(*cp.Circle).Radius()
		t, n, ok := collision.SegmentVsCircle(px, py, qx, qy, ex, ey, r)
		return ok, t, n
	default:
		hw := (s.BB().R - s.BB().L) / 2
		hh := (s.BB().T - s.BB().B) / 2
		t, n, ok := collision.SegmentVsAABB(px, py, qx, qy, ex, ey, hw, hh)
		return ok, t, n
	}
}

// castBoxVsCpShape dispatches a box-sweep test (Minkowski-expanded entity shape).
func castBoxVsCpShape(px, py, qx, qy, qhw, qhh, ex, ey float64, s *cp.Shape) (bool, float64, collision.Vec2) {
	switch s.Class.(type) {
	case *cp.Circle:
		r := s.Class.(*cp.Circle).Radius()
		// Swept box vs static circle = segment vs rounded rect.
		t, n, ok := collision.SegmentVsRoundedRect(px, py, qx, qy, ex, ey, qhw, qhh, r)
		return ok, t, n
	default:
		hw := (s.BB().R - s.BB().L) / 2
		hh := (s.BB().T - s.BB().B) / 2
		// Swept box vs static AABB = segment vs expanded AABB (Minkowski sum).
		t, n, ok := collision.SegmentVsAABB(px, py, qx, qy, ex, ey, hw+qhw, hh+qhh)
		return ok, t, n
	}
}

// castCircleVsCpShape dispatches a circle-sweep test.
func castCircleVsCpShape(px, py, qx, qy, r, ex, ey float64, s *cp.Shape) (bool, float64, collision.Vec2) {
	switch s.Class.(type) {
	case *cp.Circle:
		er := s.Class.(*cp.Circle).Radius()
		// Swept circle vs static circle = ray vs circle with summed radii.
		t, n, ok := collision.SegmentVsCircle(px, py, qx, qy, ex, ey, r+er)
		return ok, t, n
	default:
		hw := (s.BB().R - s.BB().L) / 2
		hh := (s.BB().T - s.BB().B) / 2
		// Swept circle vs static AABB = segment vs rounded rect.
		t, n, ok := collision.SegmentVsRoundedRect(px, py, qx, qy, ex, ey, hw, hh, r)
		return ok, t, n
	}
}

// overlapBoxCpShape tests whether the AABB at (qx,qy) with half-extents (hw,hh)
// overlaps the cp shape whose body centre is at (ex,ey).
func overlapBoxCpShape(qx, qy, hw, hh, ex, ey float64, s *cp.Shape) bool {
	switch s.Class.(type) {
	case *cp.Circle:
		r := s.Class.(*cp.Circle).Radius()
		// Closest point on the AABB to the circle centre.
		clampX := math.Max(qx-hw, math.Min(ex, qx+hw))
		clampY := math.Max(qy-hh, math.Min(ey, qy+hh))
		dx, dy := ex-clampX, ey-clampY
		return dx*dx+dy*dy < r*r
	default:
		// For box or polygon shapes use the bounding-box test; the BBQuery
		// broadphase already filtered by BB, so this is correct for AABB shapes.
		shapeHW := (s.BB().R - s.BB().L) / 2
		shapeHH := (s.BB().T - s.BB().B) / 2
		return math.Abs(qx-ex) < hw+shapeHW && math.Abs(qy-ey) < hh+shapeHH
	}
}

// overlapCircleCpShape tests whether the circle at (qx,qy) with radius r
// overlaps the cp shape whose body centre is at (ex,ey).
func overlapCircleCpShape(qx, qy, r, ex, ey float64, s *cp.Shape) bool {
	switch s.Class.(type) {
	case *cp.Circle:
		er := s.Class.(*cp.Circle).Radius()
		dx, dy := qx-ex, qy-ey
		total := r + er
		return dx*dx+dy*dy < total*total
	default:
		// For box / polygon shapes use closest-point-on-AABB test.
		shapeHW := (s.BB().R - s.BB().L) / 2
		shapeHH := (s.BB().T - s.BB().B) / 2
		clampX := math.Max(ex-shapeHW, math.Min(qx, ex+shapeHW))
		clampY := math.Max(ey-shapeHH, math.Min(qy, ey+shapeHH))
		dx, dy := qx-clampX, qy-clampY
		return dx*dx+dy*dy < r*r
	}
}
