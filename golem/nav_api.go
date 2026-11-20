package golem

import "github.com/demiurgos-hub/golem-engine/golem/nav"

// SetNavBackend registers the space-0 nav backend used by Server.FindPath,
// Server.SetNavWalkable and NavAgent for entities in space 0. Call this at
// startup after building your grid from map data. Passing nil disables the
// nav backend. Use Space(id).SetNavBackend for other spaces.
func (s *Server) SetNavBackend(b nav.Backend) {
	s.space0.navBackend = b
}

// FindPath returns world-space waypoints from (x0,y0) to (x1,y1) in space 0.
// The first element is the start position; subsequent elements are the
// world-space centres of each grid cell along the route, ending at the goal.
// Returns nil, false when no nav backend is set, no path exists, or either
// coordinate is outside the grid.
func (s *Server) FindPath(x0, y0, x1, y1 float64) ([]NavPoint, bool) {
	return s.space0.findPath(x0, y0, x1, y1)
}

// SetNavWalkable marks the space-0 cell at world-space (x, y) as passable
// (true) or impassable (false). No-op if no nav backend is set or the backend
// does not implement NavDynamicBackend.
func (s *Server) SetNavWalkable(x, y float64, walkable bool) {
	s.space0.setNavWalkable(x, y, walkable)
}

// SetNavBackend registers this space's nav backend, used by FindPath,
// SetNavWalkable and NavAgent for entities in this space. Passing nil
// disables it. Call before Run or on the tick goroutine.
func (sp *Space) SetNavBackend(b nav.Backend) {
	if b == nil && sp.srv.runtime(sp.id) == nil {
		return
	}
	sp.srv.ensureRuntime(sp.id).navBackend = b
}

// FindPath returns world-space waypoints from (x0,y0) to (x1,y1) on this
// space's nav backend; see Server.FindPath. Returns nil, false when the space
// has no nav backend.
func (sp *Space) FindPath(x0, y0, x1, y1 float64) ([]NavPoint, bool) {
	return sp.srv.runtime(sp.id).findPath(x0, y0, x1, y1)
}

// SetNavWalkable marks the cell at world-space (x, y) of this space's nav
// backend as passable or impassable; see Server.SetNavWalkable.
func (sp *Space) SetNavWalkable(x, y float64, walkable bool) {
	sp.srv.runtime(sp.id).setNavWalkable(x, y, walkable)
}

func (rt *spaceRuntime) findPath(x0, y0, x1, y1 float64) ([]NavPoint, bool) {
	if rt == nil || rt.navBackend == nil {
		return nil, false
	}
	return rt.navBackend.FindPath(x0, y0, x1, y1)
}

func (rt *spaceRuntime) setNavWalkable(x, y float64, walkable bool) {
	if rt == nil {
		return
	}
	if d, ok := rt.navBackend.(nav.DynamicBackend); ok {
		d.SetWalkable(x, y, walkable)
	}
}
