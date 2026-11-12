package box3d

import (
	"fmt"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

// A context belongs to one tick/query worker, including every callback. Its
// bounded hit heap retains only caller capacity while counting all matches.
type queryScratch struct {
	world           *World
	query           p.Query
	proxy           b.ShapeProxy
	points          [b.MaxShapeCastPoints]b.Vec3
	initial         map[b.ShapeID]bool
	hits            []p.Hit
	nearest         p.Hit
	length          float32
	total, capacity int
	found, blocked  bool
	err             error
}

func (w *World) queryScratch(index int) *queryScratch {
	for len(w.queryContexts) <= index {
		w.queryContexts = append(w.queryContexts, &queryScratch{initial: make(map[b.ShapeID]bool)})
	}
	return w.queryContexts[index]
}

func (s *queryScratch) prepare(w *World, q p.Query, capacity int) error {
	s.world, s.query, s.capacity = w, q, capacity
	s.length = q.Translation.Length()
	s.found, s.blocked, s.err, s.total = false, false, nil, 0
	s.nearest = p.Hit{}
	clear(s.initial)
	s.hits = s.hits[:0]
	s.proxy = b.ShapeProxy{Points: s.points[:0]}
	point := func(v p.Vec3) { s.proxy.Points = append(s.proxy.Points, bv(q.Pose.Rotation.Rotate(v))) }
	if q.Kind == p.Ray {
		point(p.Vec3{})
	} else {
		switch q.Shape.Kind {
		case p.Sphere:
			point(p.Vec3{})
			s.proxy.Radius = q.Shape.Radius
		case p.Capsule:
			point(q.Shape.PointA)
			point(q.Shape.PointB)
			s.proxy.Radius = q.Shape.Radius
		case p.Box:
			for _, x := range []float32{-1, 1} {
				for _, y := range []float32{-1, 1} {
					for _, z := range []float32{-1, 1} {
						point(p.Vec3{X: x * q.Shape.HalfExtents.X, Y: y * q.Shape.HalfExtents.Y, Z: z * q.Shape.HalfExtents.Z})
					}
				}
			}
		case p.Convex:
			if len(q.Shape.Vertices) > b.MaxShapeCastPoints {
				return p.ErrUnsupported
			}
			for _, v := range q.Shape.Vertices {
				point(v)
			}
		default:
			return p.ErrUnsupported
		}
	}
	s.proxy.Count = int32(len(s.proxy.Points))
	return nil
}

func (s *queryScratch) release() {
	// Release caller-owned inputs and obsolete content strings between queries.
	s.world, s.query, s.nearest, s.err = nil, p.Query{}, p.Hit{}, nil
	clear(s.hits)
	s.hits = s.hits[:0]
}

func (s *queryScratch) add(hit p.Hit) {
	s.total++
	s.blocked = s.blocked || hit.Blocking
	if s.query.Mode != p.All {
		if !s.found || p.HitLess(hit, s.nearest) {
			s.nearest = hit
		}
	} else if len(s.hits) < s.capacity {
		s.hits = append(s.hits, hit)
		for i := len(s.hits) - 1; i > 0; {
			parent := (i - 1) / 2
			if !p.HitLess(s.hits[parent], s.hits[i]) {
				break
			}
			s.hits[parent], s.hits[i] = s.hits[i], s.hits[parent]
			i = parent
		}
	} else if len(s.hits) > 0 && p.HitLess(hit, s.hits[0]) {
		s.hits[0] = hit
		for i := 0; ; {
			left := 2*i + 1
			if left >= len(s.hits) {
				break
			}
			child := left
			if right := left + 1; right < len(s.hits) && p.HitLess(s.hits[left], s.hits[right]) {
				child = right
			}
			if !p.HitLess(s.hits[i], s.hits[child]) {
				break
			}
			s.hits[i], s.hits[child] = s.hits[child], s.hits[i]
			i = child
		}
	}
	s.found = true
}

func queryInitial(id b.ShapeID, context any) bool {
	s := context.(*queryScratch)
	w, q := s.world, s.query
	if _, _, ok := w.eligible(id, q.Filter); !ok {
		return true
	}
	h := w.hit(id)
	h.Initial = true
	if q.Kind == p.Ray {
		r, ok := id.ClosestPointFeature(bp(q.Pose.Position), 0)
		if !ok || r.Distance > 0 {
			return true
		}
		h.Point, h.Normal, h.Penetrating, h.Touching = pp(r.Point), pv(r.Normal), r.Inside, !r.Inside
		h.Feature.LocalID, h.Feature.Detail = r.Feature.FeatureID, p.ClosestFeatureDetail
	} else {
		best, found, valid := id.ProxyPenetrationDeepest(bp(q.Pose.Position), &s.proxy, q.Backfaces == p.IncludeBackfaces)
		if !valid {
			s.err = fmt.Errorf("%w: initial penetration", p.ErrUnsupported)
			return false
		}
		if !found {
			return true
		}
		h.Point, h.Normal, h.Depth = pp(best.Point), pv(best.Normal), best.Depth
		h.Feature.LocalID, h.Feature.Detail = best.Feature.FeatureID, p.PenetrationFeatureDetail
		h.Penetrating, h.Touching = best.Depth > 0, best.Depth == 0
		if best.Feature.TriangleIndex >= 0 {
			h.Feature.Triangle = uint32(best.Feature.TriangleIndex)
		}
	}
	s.initial[id] = true
	if q.Initial == p.IgnoreInitial && q.Kind != p.Overlap && s.length != 0 {
		return true
	}
	if q.Initial == p.BlockInitial {
		h.Blocking = true
	}
	s.add(h)
	return q.Mode != p.Any
}

func queryCast(id b.ShapeID, point b.Pos, normal b.Vec3, fraction float32, _ uint64, triangle, child int, context any) float32 {
	s := context.(*queryScratch)
	w, q := s.world, s.query
	if _, _, ok := w.eligible(id, q.Filter); !ok || s.initial[id] {
		return 1
	}
	h := w.hit(id)
	h.Point, h.Normal, h.SurfaceNormal = pp(point), pv(normal), pv(normal)
	h.HasSurfaceNormal = q.Kind == p.Ray
	h.Fraction, h.Distance, h.Touching = fraction, fraction*s.length, true
	if triangle >= 0 {
		h.Feature.Triangle = uint32(triangle)
	}
	if child >= 0 {
		h.Feature.Child = uint32(child)
	}
	s.add(h)
	if q.Mode == p.Any {
		return 0
	}
	if q.Mode == p.Nearest {
		return fraction
	}
	return 1
}

func (w *World) query(q p.Query, dst []p.Hit) (p.QueryResult, error) {
	return w.queryWithScratch(q, dst, w.queryScratch(0))
}

func (w *World) queryWithScratch(q p.Query, dst []p.Hit, s *queryScratch) (p.QueryResult, error) {
	defer s.release()
	if err := s.prepare(w, q, len(dst)); err != nil {
		return p.QueryResult{}, err
	}
	filter := b.DefaultQueryFilter()
	filter.CategoryBits, filter.MaskBits, filter.IgnoreShapeMask, filter.BackFaces = ^uint64(0), q.Filter.Mask, true, q.Backfaces == p.IncludeBackfaces
	bounds := b.OffsetAABB(b.MakeAABB(s.proxy.Points, s.proxy.Radius), bp(q.Pose.Position))
	w.backend.OverlapAABB(bounds, filter, queryInitial, s)
	if s.err != nil {
		return p.QueryResult{}, s.err
	}
	if q.Kind != p.Overlap && s.length > 0 && !(s.found && q.Mode != p.All) {
		if q.Kind == p.Ray {
			w.backend.CastRay(bp(q.Pose.Position), bv(q.Translation), filter, queryCast, s)
		} else {
			if _, valid := w.backend.PreciseCastShape(bp(q.Pose.Position), &s.proxy, bv(q.Translation), filter, .000001, queryCast, s); !valid {
				return p.QueryResult{}, fmt.Errorf("physics3d: geometric sweep did not converge")
			}
		}
	}
	r := p.QueryResult{Revision: w.revision}
	if q.Mode != p.All {
		if s.found {
			r.Total, r.Blocked = 1, s.nearest.Blocking
			if len(dst) > 0 {
				dst[0], r.Count = s.nearest, 1
			} else {
				r.Incomplete = true
			}
		}
		return r, nil
	}
	p.SortHits(s.hits)
	r.Count, r.Total, r.Blocked = copy(dst, s.hits), s.total, s.blocked
	r.Incomplete = r.Count < r.Total
	return r, nil
}
