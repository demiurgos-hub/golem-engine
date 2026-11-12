package box3d

import (
	"fmt"
	"sort"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

// ContactInterval is one connected contact interval, with exact boundary leaves.
type ContactInterval struct{ Enter, Exit MotionHit }

// ColliderMotionIntervals returns the complete union of convex-leaf motion
// intervals in stable chronological order. Rotational boundaries are enclosed
// within the distance tolerance; unresolved/budget-limited work fails explicitly.
// Output truncation never means clearance.
func (w *World) ColliderMotionIntervals(a, z ColliderMotion, tolerance float32, dst []ContactInterval) (p.QueryResult, error) {
	if err := w.queryReady(); err != nil {
		return p.QueryResult{Incomplete: true}, err
	}
	ca, aok := w.colliders[a.Collider]
	cz, zok := w.colliders[z.Collider]
	if !aok || !zok {
		return p.QueryResult{Incomplete: true}, p.ErrStale
	}
	if !a.Start.Valid() || !a.End.Valid() || !z.Start.Valid() || !z.End.Valid() || !p.Finite(tolerance) || tolerance <= 0 {
		return p.QueryResult{Incomplete: true}, p.ErrInvalid
	}
	pose := func(v p.Pose) b.WorldTransform { return b.WorldTransform{P: bp(v.Position), Q: bq(v.Rotation)} }
	var values []ContactInterval
	buffer := make([]b.MotionInterval, 16)
	for _, ia := range ca.shapes {
		for _, iz := range cz.shapes {
			r, err := ia.MotionIntervals(iz, pose(a.Start), pose(a.End), pose(z.Start), pose(z.End), tolerance, true, buffer)
			if err == nil && r.Incomplete {
				buffer = make([]b.MotionInterval, r.Total)
				r, err = ia.MotionIntervals(iz, pose(a.Start), pose(a.End), pose(z.Start), pose(z.End), tolerance, true, buffer)
			}
			if err != nil {
				return p.QueryResult{Incomplete: true}, fmt.Errorf("physics3d: sensor intervals: %w", err)
			}
			if r.Incomplete {
				return p.QueryResult{Incomplete: true}, b.ErrMotionIntervalLimit
			}
			convert := func(h b.MotionCastResult) MotionHit {
				fa, fb := p.Feature{Child: ca.children[ia], Valid: true}, p.Feature{Child: cz.children[iz], Valid: true}
				if h.FeatureA.TriangleIndex >= 0 {
					fa.Triangle = uint32(h.FeatureA.TriangleIndex)
				}
				if h.FeatureB.TriangleIndex >= 0 {
					fb.Triangle = uint32(h.FeatureB.TriangleIndex)
				}
				return MotionHit{A: a.Collider, B: z.Collider, Hit: h.Hit, Initial: h.Initial, Touching: h.Touching, Penetrating: h.Penetrating, Fraction: h.Fraction, Separation: h.Separation, Depth: h.Depth, PointA: pp(h.PointA), PointB: pp(h.PointB), Normal: pv(h.Normal), FeatureA: fa, FeatureB: fb, Revision: w.revision}
			}
			for _, interval := range buffer[:r.Count] {
				if len(values) == 16384 {
					return p.QueryResult{Incomplete: true}, b.ErrMotionIntervalLimit
				}
				values = append(values, ContactInterval{convert(interval.Enter), convert(interval.Exit)})
			}
		}
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].Enter.Fraction < values[j].Enter.Fraction })
	merged := values[:0]
	for _, v := range values {
		if len(merged) == 0 || v.Enter.Fraction > merged[len(merged)-1].Exit.Fraction {
			merged = append(merged, v)
		} else if v.Exit.Fraction > merged[len(merged)-1].Exit.Fraction {
			merged[len(merged)-1].Exit = v.Exit
		}
	}
	r := p.QueryResult{Count: copy(dst, merged), Total: len(merged), Revision: w.revision}
	r.Incomplete = r.Count < r.Total
	return r, nil
}
