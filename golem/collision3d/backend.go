package collision3d

import "math"

type entry struct {
	id      int64
	shape   Shape
	layer   uint32
	mask    uint32
	trigger bool
	pos     Vec3
}

// SimpleBackend is a pure-Go detection-only 3D collision backend.
// It tests registered shapes pairwise, so it is intended as an MVP backend for
// modest collider counts and as a correctness target for future broadphases.
type SimpleBackend struct {
	entries map[int64]entry
}

// NewSimpleBackend creates an empty pure-Go 3D collision backend.
func NewSimpleBackend() *SimpleBackend {
	return &SimpleBackend{entries: make(map[int64]entry)}
}

// Add registers a collision shape for entityID.
func (b *SimpleBackend) Add(entityID int64, shape Shape, layer, mask uint32, trigger bool) {
	if shape == nil {
		return
	}
	e := b.entries[entityID]
	e.id = entityID
	e.shape = shape
	e.layer = layer
	e.mask = mask
	e.trigger = trigger
	b.entries[entityID] = e
}

// Remove unregisters entityID.
func (b *SimpleBackend) Remove(entityID int64) {
	delete(b.entries, entityID)
}

// Set replaces the shape, filtering, and trigger flag for a registered entity.
func (b *SimpleBackend) Set(entityID int64, shape Shape, layer, mask uint32, trigger bool) {
	e, ok := b.entries[entityID]
	if !ok || shape == nil {
		return
	}
	e.shape = shape
	e.layer = layer
	e.mask = mask
	e.trigger = trigger
	b.entries[entityID] = e
}

// Update synchronizes the entity's world-space position into the backend.
func (b *SimpleBackend) Update(entityID int64, x, y, z float64) {
	e, ok := b.entries[entityID]
	if !ok {
		return
	}
	e.pos = Vec3{X: x, Y: y, Z: z}
	b.entries[entityID] = e
}

// Step detects current overlaps and returns contacts. It does not simulate movement.
func (b *SimpleBackend) Step(_ float64) []Contact {
	entries := make([]entry, 0, len(b.entries))
	for _, e := range b.entries {
		entries = append(entries, e)
	}

	var contacts []Contact
	for i := 0; i < len(entries); i++ {
		a := entries[i]
		for j := i + 1; j < len(entries); j++ {
			c := entries[j]
			if (a.layer&c.mask) == 0 || (c.layer&a.mask) == 0 {
				continue
			}
			normal, depth, ok := overlap(a.shape, a.pos, c.shape, c.pos)
			if !ok {
				continue
			}
			if a.trigger || c.trigger {
				depth = 0
			}
			contacts = append(contacts, Contact{A: a.id, B: c.id, Normal: normal, Depth: depth})
		}
	}
	return contacts
}

// ReadBack is a no-op for SimpleBackend because it does not simulate movement.
func (b *SimpleBackend) ReadBack(_ func(entityID int64, x, y, z float64)) {}

// OverlapBox returns IDs whose shapes overlap the query AABB.
func (b *SimpleBackend) OverlapBox(cx, cy, cz, hw, hh, hd float64, layerMask uint32) []int64 {
	query := AABB{W: hw * 2, H: hh * 2, D: hd * 2}
	pos := Vec3{X: cx, Y: cy, Z: cz}
	var out []int64
	for _, e := range b.entries {
		if e.layer&layerMask == 0 {
			continue
		}
		if _, _, ok := overlap(query, pos, e.shape, e.pos); ok {
			out = append(out, e.id)
		}
	}
	return out
}

// OverlapSphere returns IDs whose shapes overlap the query sphere.
func (b *SimpleBackend) OverlapSphere(cx, cy, cz, radius float64, layerMask uint32) []int64 {
	query := Sphere{R: radius}
	pos := Vec3{X: cx, Y: cy, Z: cz}
	var out []int64
	for _, e := range b.entries {
		if e.layer&layerMask == 0 {
			continue
		}
		if _, _, ok := overlap(query, pos, e.shape, e.pos); ok {
			out = append(out, e.id)
		}
	}
	return out
}

// Raycast returns the first entity intersected by the segment from from to to.
func (b *SimpleBackend) Raycast(from, to Vec3, layerMask uint32) (RaycastHit, bool) {
	hits := b.RaycastAll(from, to, layerMask)
	if len(hits) == 0 {
		return RaycastHit{}, false
	}
	return hits[0], true
}

// RaycastAll returns all entities intersected by the segment from from to to.
func (b *SimpleBackend) RaycastAll(from, to Vec3, layerMask uint32) []RaycastHit {
	d := sub(to, from)
	var hits []RaycastHit
	for _, e := range b.entries {
		if e.layer&layerMask == 0 {
			continue
		}
		t, n, ok := raycastShape(from, to, e.shape, e.pos)
		if !ok {
			continue
		}
		hits = append(hits, RaycastHit{
			EntityID: e.id,
			Point:    add(from, mul(d, t)),
			Normal:   n,
			Fraction: t,
		})
	}
	SortHits(hits)
	return hits
}

func raycastShape(from, to Vec3, shape Shape, pos Vec3) (float64, Vec3, bool) {
	switch s := shape.(type) {
	case Sphere:
		return SegmentVsSphere(from, to, pos, s.R)
	case AABB:
		return SegmentVsAABB(from, to, pos, Vec3{X: s.W / 2, Y: s.H / 2, Z: s.D / 2})
	default:
		return 0, Vec3{}, false
	}
}

func overlap(a Shape, ap Vec3, b Shape, bp Vec3) (Vec3, float64, bool) {
	switch av := a.(type) {
	case Sphere:
		switch bv := b.(type) {
		case Sphere:
			return overlapSphereSphere(ap, av.R, bp, bv.R)
		case AABB:
			n, d, ok := overlapSphereAABB(ap, av.R, bp, halfExtents(bv))
			return n, d, ok
		}
	case AABB:
		switch bv := b.(type) {
		case Sphere:
			n, d, ok := overlapSphereAABB(bp, bv.R, ap, halfExtents(av))
			return mul(n, -1), d, ok
		case AABB:
			return overlapAABBAABB(ap, halfExtents(av), bp, halfExtents(bv))
		}
	}
	return Vec3{}, 0, false
}

func halfExtents(a AABB) Vec3 {
	return Vec3{X: a.W / 2, Y: a.H / 2, Z: a.D / 2}
}

func overlapSphereSphere(ap Vec3, ar float64, bp Vec3, br float64) (Vec3, float64, bool) {
	ab := sub(ap, bp)
	dist := length(ab)
	total := ar + br
	if dist >= total {
		return Vec3{}, 0, false
	}
	if dist <= epsilon {
		return Vec3{X: 1}, total, true
	}
	return mul(ab, 1/dist), total - dist, true
}

func overlapSphereAABB(sp Vec3, radius float64, bp, bh Vec3) (Vec3, float64, bool) {
	closest := Vec3{
		X: clamp(sp.X, bp.X-bh.X, bp.X+bh.X),
		Y: clamp(sp.Y, bp.Y-bh.Y, bp.Y+bh.Y),
		Z: clamp(sp.Z, bp.Z-bh.Z, bp.Z+bh.Z),
	}
	delta := sub(sp, closest)
	dist := length(delta)
	if dist >= radius {
		return Vec3{}, 0, false
	}
	if dist > epsilon {
		return mul(delta, 1/dist), radius - dist, true
	}

	// Sphere center is inside the box; choose the nearest face as push direction.
	dx := bh.X - math.Abs(sp.X-bp.X)
	dy := bh.Y - math.Abs(sp.Y-bp.Y)
	dz := bh.Z - math.Abs(sp.Z-bp.Z)
	switch {
	case dx <= dy && dx <= dz:
		if sp.X < bp.X {
			return Vec3{X: -1}, radius + dx, true
		}
		return Vec3{X: 1}, radius + dx, true
	case dy <= dz:
		if sp.Y < bp.Y {
			return Vec3{Y: -1}, radius + dy, true
		}
		return Vec3{Y: 1}, radius + dy, true
	default:
		if sp.Z < bp.Z {
			return Vec3{Z: -1}, radius + dz, true
		}
		return Vec3{Z: 1}, radius + dz, true
	}
}

func overlapAABBAABB(ap, ah, bp, bh Vec3) (Vec3, float64, bool) {
	dx := ap.X - bp.X
	px := ah.X + bh.X - math.Abs(dx)
	if px <= 0 {
		return Vec3{}, 0, false
	}
	dy := ap.Y - bp.Y
	py := ah.Y + bh.Y - math.Abs(dy)
	if py <= 0 {
		return Vec3{}, 0, false
	}
	dz := ap.Z - bp.Z
	pz := ah.Z + bh.Z - math.Abs(dz)
	if pz <= 0 {
		return Vec3{}, 0, false
	}
	switch {
	case px <= py && px <= pz:
		if dx < 0 {
			return Vec3{X: -1}, px, true
		}
		return Vec3{X: 1}, px, true
	case py <= pz:
		if dy < 0 {
			return Vec3{Y: -1}, py, true
		}
		return Vec3{Y: 1}, py, true
	default:
		if dz < 0 {
			return Vec3{Z: -1}, pz, true
		}
		return Vec3{Z: 1}, pz, true
	}
}
