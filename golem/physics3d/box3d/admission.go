package box3d

import (
	"fmt"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func (w *World) admissionHits(q p.Query) ([]p.Hit, error) {
	hits := make([]p.Hit, 16)
	for {
		r, e := w.Query(q, hits)
		if e != nil {
			return nil, e
		}
		if !r.Incomplete {
			return hits[:r.Count], nil
		}
		if r.Total > 16384 {
			return nil, fmt.Errorf("physics3d: placement query overflow")
		}
		hits = make([]p.Hit, max(r.Total, len(hits)*2))
	}
}
func (w *World) projectionClear(handle p.BodyHandle, delta p.Vec3) (bool, error) {
	v := w.bodies[handle]
	pose := w.bodyState(handle, v).Pose
	target := pose
	target.Position = target.Position.Add(delta)
	if !w.bodyInBounds(v, target) {
		return false, nil
	}
	for _, h := range v.colliders {
		c := w.colliders[h]
		if !c.desc.Enabled || !c.desc.Roles.Solid {
			continue
		}
		filter := p.QueryFilter{Mask: ^uint64(0), PhysicalFilter: &c.desc.Filter, RequireSolid: true, IncludeNonQuery: true, Sensors: p.IncludeSensors, ExcludeBodies: []p.BodyHandle{handle}}
		ok, e := w.shapeProjectionClear(w.shapes[c.desc.Shape].desc, pose.Compose(c.desc.LocalPose), delta, filter)
		if e != nil || !ok {
			return ok, e
		}
	}
	return true, nil
}
func (w *World) shapeProjectionClear(shape p.ShapeDesc, pose p.Pose, delta p.Vec3, filter p.QueryFilter) (bool, error) {
	if shape.Kind == p.Compound {
		for _, child := range shape.Children {
			ok, e := w.shapeProjectionClear(child.Shape, pose.Compose(child.Pose), delta, filter)
			if e != nil || !ok {
				return ok, e
			}
		}
		return true, nil
	}
	q := p.Query{Kind: p.Sweep, Mode: p.All, Shape: shape, Pose: pose, Translation: delta, Filter: filter, Initial: p.ReportInitial}
	hits, e := w.admissionHits(q)
	if e != nil {
		return false, e
	}
	initial := map[p.ColliderHandle]float32{}
	const epsilon float32 = 0.00001
	for _, hit := range hits {
		if !hit.Blocking {
			continue
		}
		if hit.Initial {
			initial[hit.Collider] = max(initial[hit.Collider], hit.Depth)
			if hit.Penetrating && hit.Normal.Dot(delta) < -epsilon {
				return false, nil
			}
			continue
		}
		if hit.Distance+epsilon < delta.Length() {
			return false, nil
		}
	}
	q.Pose.Position = q.Pose.Position.Add(delta)
	q.Translation = p.Vec3{}
	q.Kind = p.Overlap
	hits, e = w.admissionHits(q)
	if e != nil {
		return false, e
	}
	for _, hit := range hits {
		if !hit.Blocking || hit.Depth <= epsilon {
			continue
		}
		depth, hadInitial := initial[hit.Collider]
		if !hadInitial || hit.Depth >= depth-epsilon {
			return false, nil
		}
	}
	return true, nil
}

// ValidatePlacement checks a body's complete solid geometry at a candidate pose.
// It does not move the body or apply forces. maxPenetration is a query policy,
// independent of solver contact slop; overflow or unsupported geometry is an error.
func (w *World) ValidatePlacement(handle p.BodyHandle, pose p.Pose, maxPenetration float32) (bool, error) {
	if err := w.queryReady(); err != nil {
		return false, err
	}
	if !pose.Valid() || !p.Finite(maxPenetration) || maxPenetration < 0 {
		return false, p.ErrInvalid
	}
	v, e := w.findBody(handle)
	if e != nil {
		return false, e
	}
	if !w.bodyInBounds(v, pose) {
		return false, nil
	}
	if v.desc.Controller.Enabled {
		if _, err := w.controllerGeometryAt(handle, pose); err != nil {
			return false, err
		}
	}
	for _, h := range v.colliders {
		c := w.colliders[h]
		if !c.desc.Enabled || !c.desc.Roles.Solid {
			continue
		}
		filter := p.QueryFilter{Mask: ^uint64(0), PhysicalFilter: &c.desc.Filter, RequireSolid: true, IncludeNonQuery: true, Sensors: p.IncludeSensors, ExcludeBodies: []p.BodyHandle{handle}}
		ok, e := w.shapePlacementClear(w.shapes[c.desc.Shape].desc, pose.Compose(c.desc.LocalPose), filter, maxPenetration)
		if e != nil || !ok {
			return ok, e
		}
	}
	return true, nil
}
func (w *World) shapePlacementClear(shape p.ShapeDesc, pose p.Pose, filter p.QueryFilter, tolerance float32) (bool, error) {
	if shape.Kind == p.Compound {
		for _, child := range shape.Children {
			ok, e := w.shapePlacementClear(child.Shape, pose.Compose(child.Pose), filter, tolerance)
			if e != nil || !ok {
				return ok, e
			}
		}
		return true, nil
	}
	hits, e := w.admissionHits(p.Query{Kind: p.Overlap, Mode: p.All, Shape: shape, Pose: pose, Filter: filter, Initial: p.ReportInitial})
	if e != nil {
		return false, e
	}
	for _, hit := range hits {
		if hit.Blocking && hit.Depth > tolerance {
			return false, nil
		}
	}
	return true, nil
}
