package golem

import (
	"github.com/demiurgos-hub/golem-engine/golem/collision3d"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
)

// OverlapBox returns the IDs of all registered entities whose collision shapes
// overlap the axis-aligned box centred at (cx, cy) with half-extents (hw, hh).
// Only entities whose layer has at least one bit in layerMask are returned.
// Returns nil if no collision backend is set or the backend does not implement
// CollisionSpatialQuery.
func (s *Server) OverlapBox(cx, cy, hw, hh float64, layerMask uint32) []int64 {
	if q, ok := s.collision.(collision.SpatialQuery); ok {
		return q.OverlapBox(cx, cy, hw, hh, layerMask)
	}
	return nil
}

// OverlapCircle returns the IDs of all registered entities whose collision
// shapes overlap the circle centred at (cx, cy) with the given radius.
// Only entities whose layer has at least one bit in layerMask are returned.
// Returns nil if no collision backend is set or the backend does not implement
// CollisionSpatialQuery.
func (s *Server) OverlapCircle(cx, cy, radius float64, layerMask uint32) []int64 {
	if q, ok := s.collision.(collision.SpatialQuery); ok {
		return q.OverlapCircle(cx, cy, radius, layerMask)
	}
	return nil
}

// Raycast casts a segment from (x1, y1) to (x2, y2) and returns the first
// entity hit, along with a boolean indicating whether anything was hit.
// Only entities whose layer has at least one bit in layerMask are considered.
// Returns a zero RaycastHit and false if no backend is set, the backend does
// not implement CollisionCastQuery, or no entity was hit.
func (s *Server) Raycast(x1, y1, x2, y2 float64, layerMask uint32) (CollisionRaycastHit, bool) {
	if q, ok := s.collision.(collision.CastQuery); ok {
		return q.Raycast(x1, y1, x2, y2, layerMask)
	}
	return CollisionRaycastHit{}, false
}

// RaycastAll casts a segment from (x1, y1) to (x2, y2) and returns all
// entities hit, sorted by Fraction (closest first).
// Returns nil if no backend is set, the backend does not implement
// CollisionCastQuery, or no entity was hit.
func (s *Server) RaycastAll(x1, y1, x2, y2 float64, layerMask uint32) []CollisionRaycastHit {
	if q, ok := s.collision.(collision.CastQuery); ok {
		return q.RaycastAll(x1, y1, x2, y2, layerMask)
	}
	return nil
}

// BoxCast sweeps an AABB with half-extents (hw, hh) from (ox, oy) by the
// displacement (dx, dy) and returns the first entity hit.
// Returns a zero RaycastHit and false if no backend is set, the backend does
// not implement CollisionCastQuery, or no entity was hit.
func (s *Server) BoxCast(ox, oy, hw, hh, dx, dy float64, layerMask uint32) (CollisionRaycastHit, bool) {
	if q, ok := s.collision.(collision.CastQuery); ok {
		return q.BoxCast(ox, oy, hw, hh, dx, dy, layerMask)
	}
	return CollisionRaycastHit{}, false
}

// BoxCastAll sweeps an AABB with half-extents (hw, hh) from (ox, oy) by the
// displacement (dx, dy) and returns all entities hit, sorted by Fraction.
// Returns nil if no backend is set, the backend does not implement
// CollisionCastQuery, or no entity was hit.
func (s *Server) BoxCastAll(ox, oy, hw, hh, dx, dy float64, layerMask uint32) []CollisionRaycastHit {
	if q, ok := s.collision.(collision.CastQuery); ok {
		return q.BoxCastAll(ox, oy, hw, hh, dx, dy, layerMask)
	}
	return nil
}

// CircleCast sweeps a circle with the given radius from (ox, oy) by the
// displacement (dx, dy) and returns the first entity hit.
// Returns a zero RaycastHit and false if no backend is set, the backend does
// not implement CollisionCastQuery, or no entity was hit.
func (s *Server) CircleCast(ox, oy, radius, dx, dy float64, layerMask uint32) (CollisionRaycastHit, bool) {
	if q, ok := s.collision.(collision.CastQuery); ok {
		return q.CircleCast(ox, oy, radius, dx, dy, layerMask)
	}
	return CollisionRaycastHit{}, false
}

// CircleCastAll sweeps a circle with the given radius from (ox, oy) by the
// displacement (dx, dy) and returns all entities hit, sorted by Fraction.
// Returns nil if no backend is set, the backend does not implement
// CollisionCastQuery, or no entity was hit.
func (s *Server) CircleCastAll(ox, oy, radius, dx, dy float64, layerMask uint32) []CollisionRaycastHit {
	if q, ok := s.collision.(collision.CastQuery); ok {
		return q.CircleCastAll(ox, oy, radius, dx, dy, layerMask)
	}
	return nil
}

// OverlapBox3D returns IDs whose 3D collision shapes overlap the axis-aligned
// box centered at (cx, cy, cz) with half-extents (hw, hh, hd).
// Returns nil if no 3D collision backend is set or the backend does not
// implement CollisionSpatialQuery3D.
func (s *Server) OverlapBox3D(cx, cy, cz, hw, hh, hd float64, layerMask uint32) []int64 {
	if q, ok := s.collision3D.(collision3d.SpatialQuery); ok {
		return q.OverlapBox(cx, cy, cz, hw, hh, hd, layerMask)
	}
	return nil
}

// OverlapSphere returns IDs whose 3D collision shapes overlap the sphere
// centered at (cx, cy, cz) with the given radius.
// Returns nil if no 3D collision backend is set or the backend does not
// implement CollisionSpatialQuery3D.
func (s *Server) OverlapSphere(cx, cy, cz, radius float64, layerMask uint32) []int64 {
	if q, ok := s.collision3D.(collision3d.SpatialQuery); ok {
		return q.OverlapSphere(cx, cy, cz, radius, layerMask)
	}
	return nil
}

// Raycast3D casts a segment from from to to and returns the first 3D hit.
func (s *Server) Raycast3D(from, to CollisionVec3, layerMask uint32) (CollisionRaycastHit3D, bool) {
	if q, ok := s.collision3D.(collision3d.CastQuery); ok {
		return q.Raycast(from, to, layerMask)
	}
	return CollisionRaycastHit3D{}, false
}

// RaycastAll3D casts a segment from from to to and returns all 3D hits sorted by Fraction.
func (s *Server) RaycastAll3D(from, to CollisionVec3, layerMask uint32) []CollisionRaycastHit3D {
	if q, ok := s.collision3D.(collision3d.CastQuery); ok {
		return q.RaycastAll(from, to, layerMask)
	}
	return nil
}
