package golem

import (
	"github.com/demiurgos-hub/golem-engine/golem/collision3d"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
)

// OverlapOfType resolves entityIDs through Server.Get and returns those that
// satisfy T, preserving input/backend order. Missing IDs and type mismatches are
// skipped. Returns nil when entityIDs is empty or no entry matches T.
// A nil Server always panics with "golem: OverlapOfType: Server must be non-nil",
// including when entityIDs is empty.
//
// Compose with overlap ID queries rather than geometry-specific generics:
//
//	mobs := golem.OverlapOfType[*Mob](s, s.OverlapCircle(x, y, r, mask))
//
// Entity IDs are unique across spaces, so it composes with Space queries too:
//
//	mobs := golem.OverlapOfType[*Mob](s, s.Space(cave).OverlapCircle(x, y, r, mask))
//
// The Server query methods below act on space 0; the Space handle offers the
// same queries for any space.
func OverlapOfType[T Entity](s *Server, entityIDs []int64) []T {
	if s == nil {
		panic("golem: OverlapOfType: Server must be non-nil")
	}
	if len(entityIDs) == 0 {
		return nil
	}
	var out []T
	for _, id := range entityIDs {
		e, ok := s.Get(id)
		if !ok {
			continue
		}
		typed, ok := e.(T)
		if !ok {
			continue
		}
		if out == nil {
			out = make([]T, 0, len(entityIDs))
		}
		out = append(out, typed)
	}
	return out
}

// OverlapBox returns the IDs of all registered entities whose collision shapes
// overlap the axis-aligned box centred at (cx, cy) with half-extents (hw, hh).
// Only entities whose layer has at least one bit in layerMask are returned.
// Returns nil if no collision backend is set or the backend does not implement
// CollisionSpatialQuery.
func (s *Server) OverlapBox(cx, cy, hw, hh float64, layerMask uint32) []int64 {
	return s.space0.overlapBox(cx, cy, hw, hh, layerMask)
}

// OverlapBox is Server.OverlapBox on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) OverlapBox(cx, cy, hw, hh float64, layerMask uint32) []int64 {
	return sp.srv.runtime(sp.id).overlapBox(cx, cy, hw, hh, layerMask)
}

// OverlapCircle returns the IDs of all registered entities whose collision
// shapes overlap the circle centred at (cx, cy) with the given radius.
// Only entities whose layer has at least one bit in layerMask are returned.
// Returns nil if no collision backend is set or the backend does not implement
// CollisionSpatialQuery.
func (s *Server) OverlapCircle(cx, cy, radius float64, layerMask uint32) []int64 {
	return s.space0.overlapCircle(cx, cy, radius, layerMask)
}

// OverlapCircle is Server.OverlapCircle on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) OverlapCircle(cx, cy, radius float64, layerMask uint32) []int64 {
	return sp.srv.runtime(sp.id).overlapCircle(cx, cy, radius, layerMask)
}

// Raycast casts a segment from (x1, y1) to (x2, y2) and returns the first
// entity hit, along with a boolean indicating whether anything was hit.
// Only entities whose layer has at least one bit in layerMask are considered.
// Returns a zero RaycastHit and false if no backend is set, the backend does
// not implement CollisionCastQuery, or no entity was hit.
func (s *Server) Raycast(x1, y1, x2, y2 float64, layerMask uint32) (CollisionRaycastHit, bool) {
	return s.space0.raycast(x1, y1, x2, y2, layerMask)
}

// Raycast is Server.Raycast on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) Raycast(x1, y1, x2, y2 float64, layerMask uint32) (CollisionRaycastHit, bool) {
	return sp.srv.runtime(sp.id).raycast(x1, y1, x2, y2, layerMask)
}

// RaycastAll casts a segment from (x1, y1) to (x2, y2) and returns all
// entities hit, sorted by Fraction (closest first).
// Returns nil if no backend is set, the backend does not implement
// CollisionCastQuery, or no entity was hit.
func (s *Server) RaycastAll(x1, y1, x2, y2 float64, layerMask uint32) []CollisionRaycastHit {
	return s.space0.raycastAll(x1, y1, x2, y2, layerMask)
}

// RaycastAll is Server.RaycastAll on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) RaycastAll(x1, y1, x2, y2 float64, layerMask uint32) []CollisionRaycastHit {
	return sp.srv.runtime(sp.id).raycastAll(x1, y1, x2, y2, layerMask)
}

// BoxCast sweeps an AABB with half-extents (hw, hh) from (ox, oy) by the
// displacement (dx, dy) and returns the first entity hit.
// Returns a zero RaycastHit and false if no backend is set, the backend does
// not implement CollisionCastQuery, or no entity was hit.
func (s *Server) BoxCast(ox, oy, hw, hh, dx, dy float64, layerMask uint32) (CollisionRaycastHit, bool) {
	return s.space0.boxCast(ox, oy, hw, hh, dx, dy, layerMask)
}

// BoxCast is Server.BoxCast on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) BoxCast(ox, oy, hw, hh, dx, dy float64, layerMask uint32) (CollisionRaycastHit, bool) {
	return sp.srv.runtime(sp.id).boxCast(ox, oy, hw, hh, dx, dy, layerMask)
}

// BoxCastAll sweeps an AABB with half-extents (hw, hh) from (ox, oy) by the
// displacement (dx, dy) and returns all entities hit, sorted by Fraction.
// Returns nil if no backend is set, the backend does not implement
// CollisionCastQuery, or no entity was hit.
func (s *Server) BoxCastAll(ox, oy, hw, hh, dx, dy float64, layerMask uint32) []CollisionRaycastHit {
	return s.space0.boxCastAll(ox, oy, hw, hh, dx, dy, layerMask)
}

// BoxCastAll is Server.BoxCastAll on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) BoxCastAll(ox, oy, hw, hh, dx, dy float64, layerMask uint32) []CollisionRaycastHit {
	return sp.srv.runtime(sp.id).boxCastAll(ox, oy, hw, hh, dx, dy, layerMask)
}

// CircleCast sweeps a circle with the given radius from (ox, oy) by the
// displacement (dx, dy) and returns the first entity hit.
// Returns a zero RaycastHit and false if no backend is set, the backend does
// not implement CollisionCastQuery, or no entity was hit.
func (s *Server) CircleCast(ox, oy, radius, dx, dy float64, layerMask uint32) (CollisionRaycastHit, bool) {
	return s.space0.circleCast(ox, oy, radius, dx, dy, layerMask)
}

// CircleCast is Server.CircleCast on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) CircleCast(ox, oy, radius, dx, dy float64, layerMask uint32) (CollisionRaycastHit, bool) {
	return sp.srv.runtime(sp.id).circleCast(ox, oy, radius, dx, dy, layerMask)
}

// CircleCastAll sweeps a circle with the given radius from (ox, oy) by the
// displacement (dx, dy) and returns all entities hit, sorted by Fraction.
// Returns nil if no backend is set, the backend does not implement
// CollisionCastQuery, or no entity was hit.
func (s *Server) CircleCastAll(ox, oy, radius, dx, dy float64, layerMask uint32) []CollisionRaycastHit {
	return s.space0.circleCastAll(ox, oy, radius, dx, dy, layerMask)
}

// CircleCastAll is Server.CircleCastAll on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) CircleCastAll(ox, oy, radius, dx, dy float64, layerMask uint32) []CollisionRaycastHit {
	return sp.srv.runtime(sp.id).circleCastAll(ox, oy, radius, dx, dy, layerMask)
}

// OverlapBox3D returns IDs whose 3D collision shapes overlap the axis-aligned
// box centered at (cx, cy, cz) with half-extents (hw, hh, hd).
// Returns nil if no 3D collision backend is set or the backend does not
// implement CollisionSpatialQuery3D.
func (s *Server) OverlapBox3D(cx, cy, cz, hw, hh, hd float64, layerMask uint32) []int64 {
	return s.space0.overlapBox3D(cx, cy, cz, hw, hh, hd, layerMask)
}

// OverlapBox3D is Server.OverlapBox3D on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) OverlapBox3D(cx, cy, cz, hw, hh, hd float64, layerMask uint32) []int64 {
	return sp.srv.runtime(sp.id).overlapBox3D(cx, cy, cz, hw, hh, hd, layerMask)
}

// OverlapSphere returns IDs whose 3D collision shapes overlap the sphere
// centered at (cx, cy, cz) with the given radius.
// Returns nil if no 3D collision backend is set or the backend does not
// implement CollisionSpatialQuery3D.
func (s *Server) OverlapSphere(cx, cy, cz, radius float64, layerMask uint32) []int64 {
	return s.space0.overlapSphere(cx, cy, cz, radius, layerMask)
}

// OverlapSphere is Server.OverlapSphere on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) OverlapSphere(cx, cy, cz, radius float64, layerMask uint32) []int64 {
	return sp.srv.runtime(sp.id).overlapSphere(cx, cy, cz, radius, layerMask)
}

// Raycast3D casts a segment from from to to and returns the first 3D hit.
func (s *Server) Raycast3D(from, to CollisionVec3, layerMask uint32) (CollisionRaycastHit3D, bool) {
	return s.space0.raycast3D(from, to, layerMask)
}

// Raycast3D is Server.Raycast3D on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) Raycast3D(from, to CollisionVec3, layerMask uint32) (CollisionRaycastHit3D, bool) {
	return sp.srv.runtime(sp.id).raycast3D(from, to, layerMask)
}

// RaycastAll3D casts a segment from from to to and returns all 3D hits sorted by Fraction.
func (s *Server) RaycastAll3D(from, to CollisionVec3, layerMask uint32) []CollisionRaycastHit3D {
	return s.space0.raycastAll3D(from, to, layerMask)
}

// RaycastAll3D is Server.RaycastAll3D on this space's collision backend: only
// entities registered in this space are considered. A space without a
// matching backend reports no hits. Call on the tick goroutine.
func (sp *Space) RaycastAll3D(from, to CollisionVec3, layerMask uint32) []CollisionRaycastHit3D {
	return sp.srv.runtime(sp.id).raycastAll3D(from, to, layerMask)
}

func (rt *spaceRuntime) overlapBox(cx, cy, hw, hh float64, layerMask uint32) []int64 {
	if rt == nil {
		return nil
	}
	if q, ok := rt.collision.(collision.SpatialQuery); ok {
		return q.OverlapBox(cx, cy, hw, hh, layerMask)
	}
	return nil
}

func (rt *spaceRuntime) overlapCircle(cx, cy, radius float64, layerMask uint32) []int64 {
	if rt == nil {
		return nil
	}
	if q, ok := rt.collision.(collision.SpatialQuery); ok {
		return q.OverlapCircle(cx, cy, radius, layerMask)
	}
	return nil
}

func (rt *spaceRuntime) raycast(x1, y1, x2, y2 float64, layerMask uint32) (CollisionRaycastHit, bool) {
	if rt == nil {
		return CollisionRaycastHit{}, false
	}
	if q, ok := rt.collision.(collision.CastQuery); ok {
		return q.Raycast(x1, y1, x2, y2, layerMask)
	}
	return CollisionRaycastHit{}, false
}

func (rt *spaceRuntime) raycastAll(x1, y1, x2, y2 float64, layerMask uint32) []CollisionRaycastHit {
	if rt == nil {
		return nil
	}
	if q, ok := rt.collision.(collision.CastQuery); ok {
		return q.RaycastAll(x1, y1, x2, y2, layerMask)
	}
	return nil
}

func (rt *spaceRuntime) boxCast(ox, oy, hw, hh, dx, dy float64, layerMask uint32) (CollisionRaycastHit, bool) {
	if rt == nil {
		return CollisionRaycastHit{}, false
	}
	if q, ok := rt.collision.(collision.CastQuery); ok {
		return q.BoxCast(ox, oy, hw, hh, dx, dy, layerMask)
	}
	return CollisionRaycastHit{}, false
}

func (rt *spaceRuntime) boxCastAll(ox, oy, hw, hh, dx, dy float64, layerMask uint32) []CollisionRaycastHit {
	if rt == nil {
		return nil
	}
	if q, ok := rt.collision.(collision.CastQuery); ok {
		return q.BoxCastAll(ox, oy, hw, hh, dx, dy, layerMask)
	}
	return nil
}

func (rt *spaceRuntime) circleCast(ox, oy, radius, dx, dy float64, layerMask uint32) (CollisionRaycastHit, bool) {
	if rt == nil {
		return CollisionRaycastHit{}, false
	}
	if q, ok := rt.collision.(collision.CastQuery); ok {
		return q.CircleCast(ox, oy, radius, dx, dy, layerMask)
	}
	return CollisionRaycastHit{}, false
}

func (rt *spaceRuntime) circleCastAll(ox, oy, radius, dx, dy float64, layerMask uint32) []CollisionRaycastHit {
	if rt == nil {
		return nil
	}
	if q, ok := rt.collision.(collision.CastQuery); ok {
		return q.CircleCastAll(ox, oy, radius, dx, dy, layerMask)
	}
	return nil
}

func (rt *spaceRuntime) overlapBox3D(cx, cy, cz, hw, hh, hd float64, layerMask uint32) []int64 {
	if rt == nil {
		return nil
	}
	if q, ok := rt.collision3D.(collision3d.SpatialQuery); ok {
		return q.OverlapBox(cx, cy, cz, hw, hh, hd, layerMask)
	}
	return nil
}

func (rt *spaceRuntime) overlapSphere(cx, cy, cz, radius float64, layerMask uint32) []int64 {
	if rt == nil {
		return nil
	}
	if q, ok := rt.collision3D.(collision3d.SpatialQuery); ok {
		return q.OverlapSphere(cx, cy, cz, radius, layerMask)
	}
	return nil
}

func (rt *spaceRuntime) raycast3D(from, to CollisionVec3, layerMask uint32) (CollisionRaycastHit3D, bool) {
	if rt == nil {
		return CollisionRaycastHit3D{}, false
	}
	if q, ok := rt.collision3D.(collision3d.CastQuery); ok {
		return q.Raycast(from, to, layerMask)
	}
	return CollisionRaycastHit3D{}, false
}

func (rt *spaceRuntime) raycastAll3D(from, to CollisionVec3, layerMask uint32) []CollisionRaycastHit3D {
	if rt == nil {
		return nil
	}
	if q, ok := rt.collision3D.(collision3d.CastQuery); ok {
		return q.RaycastAll(from, to, layerMask)
	}
	return nil
}
