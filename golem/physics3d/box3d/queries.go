package box3d

import (
	"fmt"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
	"slices"
)

func (w *World) queryReady() error {
	if err := w.guard(); err != nil {
		return err
	}
	if w.dirty {
		return fmt.Errorf("%w: Synchronize is required before querying pose changes", p.ErrBusy)
	}
	return nil
}
func (w *World) eligible(id b.ShapeID, f p.QueryFilter) (p.ColliderHandle, *collider, bool) {
	h, ok := w.shapeOwners[id]
	if !ok {
		return h, nil, false
	}
	c := w.colliders[h]
	v := w.bodies[c.desc.Body]
	if !c.desc.Enabled || !v.id.IsEnabled() || !c.desc.Roles.Query && !f.IncludeNonQuery || f.RequireSolid && !c.desc.Roles.Solid || c.desc.Filter.Category&f.Mask == 0 || slices.Contains(f.ExcludeColliders, h) || slices.Contains(f.ExcludeBodies, c.desc.Body) || c.desc.Owner != 0 && slices.Contains(f.ExcludeOwners, c.desc.Owner) {
		return h, c, false
	}
	if f.Sensors == p.ExcludeSensors && c.desc.Roles.Sensor || f.Sensors == p.OnlySensors && !c.desc.Roles.Sensor {
		return h, c, false
	}
	return h, c, true
}
func (w *World) hit(id b.ShapeID) p.Hit {
	h := w.shapeOwners[id]
	c := w.colliders[h]
	return p.Hit{Collider: h, Body: c.desc.Body, Owner: c.desc.Owner, Content: c.desc.ID, Feature: p.Feature{Child: c.children[id], Valid: true}, Sensor: c.desc.Roles.Sensor, Blocking: c.desc.Roles.Solid, Revision: w.revision}
}
func proxy(s p.ShapeDesc, q p.Quat) (b.ShapeProxy, error) {
	var pts []p.Vec3
	radius := s.Radius
	switch s.Kind {
	case p.Sphere:
		pts = []p.Vec3{{}}
	case p.Capsule:
		pts = []p.Vec3{s.PointA, s.PointB}
	case p.Box:
		radius = 0
		for _, x := range []float32{-1, 1} {
			for _, y := range []float32{-1, 1} {
				for _, z := range []float32{-1, 1} {
					pts = append(pts, p.Vec3{X: s.HalfExtents.X * x, Y: s.HalfExtents.Y * y, Z: s.HalfExtents.Z * z})
				}
			}
		}
	case p.Convex:
		pts = s.Vertices
		radius = 0
	default:
		return b.ShapeProxy{}, p.ErrUnsupported
	}
	if len(pts) > b.MaxShapeCastPoints {
		return b.ShapeProxy{}, p.ErrUnsupported
	}
	out := b.ShapeProxy{Points: make([]b.Vec3, len(pts)), Count: int32(len(pts)), Radius: radius}
	for i, v := range pts {
		out.Points[i] = bv(q.Rotate(v))
	}
	return out, nil
}

// Query executes exact spatial traversal and writes a stable, bounded result list.
func (w *World) Query(q p.Query, dst []p.Hit) (p.QueryResult, error) {
	if err := w.queryReady(); err != nil {
		return p.QueryResult{}, err
	}
	if err := q.Validate(); err != nil {
		return p.QueryResult{}, err
	}
	w.stats.Queries++
	filter := b.DefaultQueryFilter()
	filter.CategoryBits = ^uint64(0)
	filter.MaskBits = q.Filter.Mask
	filter.IgnoreShapeMask = true
	filter.BackFaces = q.Backfaces == p.IncludeBackfaces
	var hits []p.Hit
	var pr b.ShapeProxy
	var err error
	if q.Kind == p.Ray {
		pr = b.ShapeProxy{Points: []b.Vec3{{}}, Count: 1}
	} else {
		pr, err = proxy(q.Shape, q.Pose.Rotation)
		if err != nil {
			return p.QueryResult{}, err
		}
	}
	initial := map[b.ShapeID]bool{}
	w.backend.OverlapShape(bp(q.Pose.Position), &pr, filter, func(id b.ShapeID, _ any) bool {
		_, _, ok := w.eligible(id, q.Filter)
		if !ok {
			return true
		}
		initial[id] = true
		if q.Initial == p.IgnoreInitial && q.Kind != p.Overlap && q.Translation.Length() != 0 {
			return true
		}
		h := w.hit(id)
		h.Initial = true
		h.Fraction = 0
		h.Distance = 0
		if q.Kind == p.Ray {
			r, ok := id.ClosestPointFeature(bp(q.Pose.Position), 4*p.CoordinateLimit)
			if !ok {
				return true
			}
			h.Point = pp(r.Point)
			h.Normal = pv(r.Normal)
			h.Penetrating = r.Inside
			h.Touching = !r.Inside
			h.Depth = r.Distance
		} else {
			scratch := make([]b.CapsulePenetration, 8)
			n, total, valid := id.ProxyPenetrations(bp(q.Pose.Position), &pr, q.Backfaces == p.IncludeBackfaces, scratch)
			if valid && total > len(scratch) {
				scratch = make([]b.CapsulePenetration, total)
				n, _, valid = id.ProxyPenetrations(bp(q.Pose.Position), &pr, q.Backfaces == p.IncludeBackfaces, scratch)
			}
			if !valid {
				err = fmt.Errorf("%w: initial penetration", p.ErrUnsupported)
				return false
			}
			if valid && n > 0 {
				best := scratch[0]
				for _, x := range scratch[1:n] {
					if x.Depth > best.Depth {
						best = x
					}
				}
				h.Point = pp(best.Point)
				h.Normal = pv(best.Normal)
				h.Depth = best.Depth
				h.Penetrating = best.Depth > 0
				h.Touching = !h.Penetrating
				if best.Feature.TriangleIndex >= 0 {
					h.Feature.Triangle = uint32(best.Feature.TriangleIndex)
				}
			} else {
				return true
			}
		}
		if q.Initial == p.BlockInitial {
			h.Blocking = true
		}
		hits = append(hits, h)
		return true
	}, nil)
	if err != nil {
		return p.QueryResult{}, err
	}
	if q.Kind != p.Overlap && q.Translation.Length() > 0 {
		callback := func(id b.ShapeID, point b.Pos, normal b.Vec3, fraction float32, _ uint64, triangle, child int, _ any) float32 {
			_, _, ok := w.eligible(id, q.Filter)
			if !ok || initial[id] && q.Initial != p.IgnoreInitial {
				return 1
			}
			h := w.hit(id)
			h.Point = pp(point)
			h.Normal = pv(normal)
			h.SurfaceNormal = pv(normal)
			h.HasSurfaceNormal = q.Kind == p.Ray
			h.Fraction = fraction
			h.Distance = fraction * q.Translation.Length()
			h.Touching = true
			if triangle >= 0 {
				h.Feature.Triangle = uint32(triangle)
			}
			if child >= 0 {
				h.Feature.Child = uint32(child)
			}
			hits = append(hits, h)
			return 1
		}
		if q.Kind == p.Ray {
			w.backend.CastRay(bp(q.Pose.Position), bv(q.Translation), filter, callback, nil)
		} else {
			if _, valid := w.backend.PreciseCastShape(bp(q.Pose.Position), &pr, bv(q.Translation), filter, 0.000001, callback, nil); !valid {
				return p.QueryResult{}, fmt.Errorf("physics3d: geometric sweep did not converge")
			}
		}
	}
	p.SortHits(hits)
	total := len(hits)
	if q.Mode != p.All && total > 1 {
		hits = hits[:1]
		total = 1
	}
	count := copy(dst, hits)
	r := p.QueryResult{Count: count, Total: total, Incomplete: count < total, Revision: w.revision}
	for _, h := range hits {
		r.Blocked = r.Blocked || h.Blocking
	}
	if r.Incomplete {
		w.stats.Overflows++
	}
	return r, nil
}

// ClosestPoint returns the nearest surface point and exact available feature.
func (w *World) ClosestPoint(h p.ColliderHandle, point p.Vec3) (p.Hit, error) {
	if err := w.queryReady(); err != nil {
		return p.Hit{}, err
	}
	if !point.InBounds() {
		return p.Hit{}, p.ErrInvalid
	}
	c, ok := w.colliders[h]
	if !ok {
		return p.Hit{}, p.ErrStale
	}
	best := p.Hit{Distance: 4 * p.CoordinateLimit}
	found := false
	for _, id := range c.shapes {
		r, ok := id.ClosestPointFeature(bp(point), best.Distance)
		if !ok {
			continue
		}
		x := w.hit(id)
		x.Point = pp(r.Point)
		x.Normal = pv(r.Normal)
		x.Distance = r.Distance
		x.Penetrating = r.Inside
		x.Touching = r.Distance == 0
		if r.Feature.TriangleIndex >= 0 {
			x.Feature.Triangle = uint32(r.Feature.TriangleIndex)
		}
		best = x
		found = true
	}
	if !found {
		return p.Hit{}, p.ErrInvalid
	}
	return best, nil
}

// Penetration computes the deepest available geometric child-pair constraint.
// A compound may require several corrections; this is not a union-wide MTD.
func (w *World) Penetration(a, z p.ColliderHandle) (p.PenetrationResult, error) {
	if err := w.queryReady(); err != nil {
		return p.PenetrationResult{}, err
	}
	ca, ok := w.colliders[a]
	if !ok {
		return p.PenetrationResult{}, p.ErrStale
	}
	cz, ok := w.colliders[z]
	if !ok {
		return p.PenetrationResult{}, p.ErrStale
	}
	r := p.PenetrationResult{A: a, B: z, Revision: w.revision}
	for _, ia := range ca.shapes {
		for _, iz := range cz.shapes {
			v, valid := ia.PairPenetration(iz)
			if !valid {
				return r, p.ErrUnsupported
			}
			if v.Touching {
				r.Touching = true
			}
			if v.Depth > r.Depth || v.Touching && !r.FeatureA.Valid {
				r.Depth = v.Depth
				r.Normal = pv(v.Normal)
				r.PointA = pp(v.PointA)
				r.PointB = pp(v.PointB)
				r.FeatureA = p.Feature{Child: ca.children[ia], Valid: true}
				r.FeatureB = p.Feature{Child: cz.children[iz], Valid: true}
				if v.FeatureA.TriangleIndex >= 0 {
					r.FeatureA.Triangle = uint32(v.FeatureA.TriangleIndex)
				}
				if v.FeatureB.TriangleIndex >= 0 {
					r.FeatureB.Triangle = uint32(v.FeatureB.TriangleIndex)
				}
			}
		}
	}
	r.Penetrating = r.Depth > 0
	r.Touching = r.Touching && !r.Penetrating
	return r, nil
}

var _ p.World = (*World)(nil)
