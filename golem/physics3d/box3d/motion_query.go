package box3d

import (
	"fmt"
	"sort"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

// ColliderMotion supplies BODY poses for one collider's linear-origin,
// shortest-arc rotational trajectory. Collider/compound local poses stay baked.
type ColliderMotion struct {
	Collider   p.ColliderHandle
	Start, End p.Pose
}

// MotionHit is the earliest leaf contact in a collider pair. Normal points A to B.
// A positive Separation is a tolerance-band hit, not exact touching. Depth is a
// leaf constraint, never the minimum translation of an entire concave object.
type MotionHit struct {
	A, B                                p.ColliderHandle
	Hit, Initial, Touching, Penetrating bool
	Fraction, Separation, Depth         float32
	PointA, PointB, Normal              p.Vec3
	FeatureA, FeatureB                  p.Feature
	Revision                            uint64
}

// MotionQuery sweeps an existing collider against the world. Targets override
// the body trajectory of specified colliders; omitted targets stay at their
// current pose. Candidates are conservatively tested from full trajectories,
// not solely the current broadphase tree. The query's own body is excluded.
type MotionQuery struct {
	Motion    ColliderMotion
	Targets   []ColliderMotion
	Filter    p.QueryFilter
	Mode      p.QueryMode
	Initial   p.InitialOverlapPolicy
	Backfaces p.BackfacePolicy
	Tolerance float32
}

// CastColliderMotion reads exact persistent geometry without changing poses or
// applying role/layer filters. Synchronize is required after world mutations.
// Errors never mean a clear path. Rotating triangle pairs require two-sided
// queries; triangle/triangle casts remain explicitly unsupported.
func (w *World) CastColliderMotion(a, z ColliderMotion, tolerance float32, backfaces p.BackfacePolicy) (MotionHit, error) {
	if err := w.queryReady(); err != nil {
		return MotionHit{}, err
	}
	return w.castColliderMotion(a, z, tolerance, backfaces)
}

func (w *World) castColliderMotion(a, z ColliderMotion, tolerance float32, backfaces p.BackfacePolicy) (MotionHit, error) {
	if !a.Start.Valid() || !a.End.Valid() || !z.Start.Valid() || !z.End.Valid() || !p.Finite(tolerance) || tolerance <= 0 || backfaces > p.IncludeBackfaces {
		return MotionHit{}, p.ErrInvalid
	}
	ca, ok := w.colliders[a.Collider]
	if !ok {
		return MotionHit{}, p.ErrStale
	}
	cz, ok := w.colliders[z.Collider]
	if !ok {
		return MotionHit{}, p.ErrStale
	}
	result := MotionHit{A: a.Collider, B: z.Collider, Revision: w.revision}
	worldPose := func(v p.Pose) b.WorldTransform { return b.WorldTransform{P: bp(v.Position), Q: bq(v.Rotation)} }
	for _, ia := range ca.shapes {
		for _, iz := range cz.shapes {
			r, err := ia.CastMotion(iz, worldPose(a.Start), worldPose(a.End), worldPose(z.Start), worldPose(z.End), tolerance, backfaces == p.IncludeBackfaces)
			if err != nil {
				return MotionHit{}, fmt.Errorf("physics3d: collider motion: %w", err)
			}
			if !r.Hit {
				continue
			}
			fa, fb := p.Feature{Child: ca.children[ia], Valid: true}, p.Feature{Child: cz.children[iz], Valid: true}
			if r.FeatureA.TriangleIndex >= 0 {
				fa.Triangle = uint32(r.FeatureA.TriangleIndex)
			}
			if r.FeatureB.TriangleIndex >= 0 {
				fb.Triangle = uint32(r.FeatureB.TriangleIndex)
			}
			less := fa.Child < result.FeatureA.Child || fa.Child == result.FeatureA.Child && (fa.Triangle < result.FeatureA.Triangle || fa.Triangle == result.FeatureA.Triangle && (fb.Child < result.FeatureB.Child || fb.Child == result.FeatureB.Child && fb.Triangle < result.FeatureB.Triangle))
			if !result.Hit || r.Fraction < result.Fraction || r.Fraction == result.Fraction && (r.Penetrating && !result.Penetrating || r.Penetrating == result.Penetrating && less) {
				result = MotionHit{A: a.Collider, B: z.Collider, Hit: true, Initial: r.Initial, Touching: r.Touching, Penetrating: r.Penetrating, Fraction: r.Fraction, Separation: r.Separation, Depth: r.Depth, PointA: pp(r.PointA), PointB: pp(r.PointB), Normal: pv(r.Normal), FeatureA: fa, FeatureB: fb, Revision: w.revision}
			}
		}
	}
	return result, nil
}

// QueryColliderMotion writes stable fraction-ordered target hits. Normal points
// target-to-query; Distance is query-origin translation distance (zero for a
// pure rotation). A full caller buffer reports Incomplete and the complete Total.
// IgnoreInitial omits pairs already geometrically touching/penetrating at t=0;
// this does not infer later exits/re-entry intervals for concave geometry.
func (w *World) QueryColliderMotion(q MotionQuery, dst []p.Hit) (p.QueryResult, error) {
	if err := w.queryReady(); err != nil {
		return p.QueryResult{}, err
	}
	query, ok := w.colliders[q.Motion.Collider]
	if !ok {
		return p.QueryResult{}, p.ErrStale
	}
	if q.Mode > p.All || q.Initial > p.BlockInitial || q.Backfaces > p.IncludeBackfaces || q.Filter.Sensors > p.OnlySensors || !q.Motion.Start.Valid() || !q.Motion.End.Valid() || !p.Finite(q.Tolerance) || q.Tolerance <= 0 {
		return p.QueryResult{}, p.ErrInvalid
	}
	targets := make(map[p.ColliderHandle]ColliderMotion, len(q.Targets))
	bodyMotions := make(map[p.BodyHandle]ColliderMotion, len(q.Targets))
	for _, m := range q.Targets {
		c, ok := w.colliders[m.Collider]
		if !ok {
			return p.QueryResult{}, p.ErrStale
		}
		if _, duplicate := targets[m.Collider]; duplicate || !m.Start.Valid() || !m.End.Valid() {
			return p.QueryResult{}, p.ErrInvalid
		}
		if previous, ok := bodyMotions[c.desc.Body]; ok && (previous.Start != m.Start || previous.End != m.End) {
			return p.QueryResult{}, fmt.Errorf("%w: inconsistent body trajectories", p.ErrInvalid)
		}
		targets[m.Collider] = m
		bodyMotions[c.desc.Body] = m
	}
	var hits []p.Hit
	distance := q.Motion.End.Position.Sub(q.Motion.Start.Position).Length()
	candidates := map[p.ColliderHandle]bool{}
	worldPose := func(v p.Pose) b.WorldTransform { return b.WorldTransform{P: bp(v.Position), Q: bq(v.Rotation)} }
	var bounds b.AABB
	for i, id := range query.shapes {
		a, err := id.MotionBounds(worldPose(q.Motion.Start), worldPose(q.Motion.End))
		if err != nil {
			return p.QueryResult{}, err
		}
		if i == 0 {
			bounds = a
		} else {
			bounds = bounds.Union(a)
		}
	}
	if len(query.shapes) == 0 {
		return p.QueryResult{Revision: w.revision}, nil
	}
	pad := b.Vec3{X: q.Tolerance, Y: q.Tolerance, Z: q.Tolerance}
	bounds.LowerBound = b.Sub(bounds.LowerBound, pad)
	bounds.UpperBound = b.Add(bounds.UpperBound, pad)
	filter := b.DefaultQueryFilter()
	filter.MaskBits = q.Filter.Mask
	filter.IgnoreShapeMask = true
	w.backend.OverlapAABB(bounds, filter, func(id b.ShapeID, _ any) bool {
		if handle, _, ok := w.eligible(id, q.Filter); ok {
			candidates[handle] = true
		}
		return true
	}, nil)
	// Current trees cannot find a target whose trajectory crosses the query but
	// starts/ends elsewhere. Test supplied moving body bounds independently.
	for body, m := range bodyMotions {
		for _, handle := range w.bodies[body].colliders {
			c := w.colliders[handle]
			for _, id := range c.shapes {
				if _, _, ok := w.eligible(id, q.Filter); !ok {
					continue
				}
				targetBounds, err := id.MotionBounds(worldPose(m.Start), worldPose(m.End))
				if err != nil {
					return p.QueryResult{}, err
				}
				if bounds.Overlaps(targetBounds) {
					candidates[handle] = true
					break
				}
			}
		}
	}
	for handle := range candidates {
		c := w.colliders[handle]
		if c.desc.Body == query.desc.Body || len(c.shapes) == 0 {
			continue
		}
		if _, _, ok := w.eligible(c.shapes[0], q.Filter); !ok {
			continue
		}
		m, exists := targets[handle]
		if !exists {
			if sibling, ok := bodyMotions[c.desc.Body]; ok {
				m = sibling
				m.Collider = handle
			} else {
				state := w.bodyState(c.desc.Body, w.bodies[c.desc.Body])
				m = ColliderMotion{handle, state.Pose, state.Pose}
			}
		}
		r, err := w.castColliderMotion(q.Motion, m, q.Tolerance, q.Backfaces)
		if err != nil {
			return p.QueryResult{}, err
		}
		if !r.Hit || r.Initial && q.Initial == p.IgnoreInitial {
			continue
		}
		h := w.hit(c.shapes[0])
		h.Feature = r.FeatureB
		h.Point = r.PointB
		h.Normal = r.Normal.Mul(-1)
		h.Fraction = r.Fraction
		h.Distance = r.Fraction * distance
		h.Initial = r.Initial
		h.Touching = r.Touching
		h.Penetrating = r.Penetrating
		h.Depth = r.Depth
		if r.Initial && q.Initial == p.BlockInitial {
			h.Blocking = true
		}
		hits = append(hits, h)
	}
	p.SortHits(hits)
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Fraction < hits[j].Fraction })
	if q.Mode != p.All && len(hits) > 1 {
		hits = hits[:1]
	}
	result := p.QueryResult{Count: copy(dst, hits), Total: len(hits), Revision: w.revision}
	result.Incomplete = result.Count < result.Total
	for _, h := range hits {
		result.Blocked = result.Blocked || h.Blocking
	}
	if result.Incomplete {
		w.stats.Overflows++
	}
	return result, nil
}
