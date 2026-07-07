package golem

import "github.com/demiurgos-hub/golem-engine/golem/nav"

// SetNavBackend registers a nav backend for use by Server.FindPath and
// Server.SetNavWalkable. Call this at startup after building your grid from
// map data. Passing nil disables the nav backend.
func (s *Server) SetNavBackend(b nav.Backend) {
	s.navBackend = b
}

// FindPath returns world-space waypoints from (x0,y0) to (x1,y1).
// The first element is the start position; subsequent elements are the
// world-space centres of each grid cell along the route, ending at the goal.
// Returns nil, false when no nav backend is set, no path exists, or either
// coordinate is outside the grid.
func (s *Server) FindPath(x0, y0, x1, y1 float64) ([]NavPoint, bool) {
	if s.navBackend == nil {
		return nil, false
	}
	return s.navBackend.FindPath(x0, y0, x1, y1)
}

// SetNavWalkable marks the cell at world-space (x, y) as passable (true) or
// impassable (false). No-op if no nav backend is set or the backend does not
// implement NavDynamicBackend.
func (s *Server) SetNavWalkable(x, y float64, walkable bool) {
	if d, ok := s.navBackend.(nav.DynamicBackend); ok {
		d.SetWalkable(x, y, walkable)
	}
}
