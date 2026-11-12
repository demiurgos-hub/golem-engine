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
	if f.PhysicalFilter != nil && !p.CollisionAllowed(*f.PhysicalFilter, c.desc.Filter) {
		return h, c, false
	}
	return h, c, true
}
func (w *World) hit(id b.ShapeID) p.Hit {
	h := w.shapeOwners[id]
	c := w.colliders[h]
	return p.Hit{Collider: h, Body: c.desc.Body, Owner: c.desc.Owner, Content: c.desc.ID, Feature: p.Feature{Child: c.children[id], Valid: true}, Sensor: c.desc.Roles.Sensor, Blocking: c.desc.Roles.Solid, Revision: w.revision}
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
	r, err := w.query(q, dst)
	if r.Incomplete {
		w.stats.Overflows++
	}
	return r, err
}

// ClosestPoint returns the closest point in solid geometry or on a mesh surface,
// with exact available feature identity. Interior points remain unchanged.
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
		x.Touching = r.Distance == 0 && !r.Inside
		x.Feature.LocalID, x.Feature.Detail = r.Feature.FeatureID, p.ClosestFeatureDetail
		if r.Feature.TriangleIndex >= 0 {
			x.Feature.Triangle = uint32(r.Feature.TriangleIndex)
		}
		if !found || p.HitLess(x, best) {
			best = x
			found = true
		}
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
				r.FeatureA = p.Feature{Child: ca.children[ia], Valid: true, LocalID: v.FeatureA.FeatureID, Detail: p.PenetrationFeatureDetail}
				r.FeatureB = p.Feature{Child: cz.children[iz], Valid: true, LocalID: v.FeatureB.FeatureID, Detail: p.PenetrationFeatureDetail}
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
