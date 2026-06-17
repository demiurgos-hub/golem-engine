package nav

// Point is a world-space coordinate on a nav path.
type Point struct{ X, Y float64 }

// Backend is the interface implemented by nav backends.
// Build one from your map data at startup and pass it to Server.SetNavBackend.
type Backend interface {
	// FindPath returns world-space waypoints from (x0,y0) to (x1,y1),
	// including both endpoints. Returns nil, false if no path exists or the
	// start/goal coordinates are out of bounds or impassable.
	FindPath(x0, y0, x1, y1 float64) ([]Point, bool)
}

// DynamicBackend is an optional extension of Backend for backends that support
// runtime walkability updates (e.g. doors, destructible walls). Check for it
// with a type assertion or use Server.SetNavWalkable, which does so internally.
type DynamicBackend interface {
	Backend
	// SetWalkable marks the cell containing world-space coordinate (x, y) as
	// passable (true) or impassable (false).
	SetWalkable(x, y float64, walkable bool)
}
