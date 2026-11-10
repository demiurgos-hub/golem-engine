// Package surface defines backend-independent, Y-up navigation contracts.
// Positions use world units, with X/Z horizontal. Locations and routes are
// runtime handles: persist world identities and destinations instead.
package surface

import (
	"context"
	"math"
)

// Vec3 is a world-space position or displacement.
type Vec3 struct{ X, Y, Z float64 }

// Finite reports whether every component is finite.
func (v Vec3) Finite() bool {
	return !math.IsNaN(v.X) && !math.IsNaN(v.Y) && !math.IsNaN(v.Z) &&
		!math.IsInf(v.X, 0) && !math.IsInf(v.Y, 0) && !math.IsInf(v.Z, 0)
}

// ProfileID identifies baked clearance, height, climb, and slope settings.
type ProfileID string

// LinkID is an authored traversal identity, independent of runtime handles.
type LinkID string

// Code distinguishes navigation outcomes without exposing backend status bits.
type Code uint8

const (
	// NotReady means required navigation data has not been restored or loaded.
	NotReady Code = iota
	Complete
	Partial
	Unreachable
	NoSurface
	Ambiguous
	InvalidInput
	Unloaded
	Cancelled
	ResourceExhausted
	Stale
	Failed
)

// Status describes an operation outcome. Err carries diagnostic context.
// A partial route must never be reported as reaching the requested destination.
type Status struct {
	Code Code
	// Reason preserves a limiting cause such as exhausted output or unloaded data
	// when Code is Partial. It is otherwise normally equal to Code.
	Reason Code
	Err    error
}

// Location is an immutable, snapshot-qualified surface location owned by a
// backend. Its view must remain retained while the location is used.
type Location interface {
	Position() Vec3
	Revision() uint64
}

// TileRevision identifies a runtime dependency. Tile identities are stable
// within a world/profile; revisions change when geometry or links change.
type TileRevision struct {
	ID       string
	Revision uint64
}

// ProjectionOptions bounds both horizontal and vertical snapping. A Hint
// selects the previous floor when it remains valid; backends must not silently
// accept an out-of-bounds projection or substitute an unrelated stacked floor.
type ProjectionOptions struct {
	Horizontal, Vertical float64
	// Up and Down independently bound vertical snapping. When both are zero,
	// Vertical supplies their shared bound for compatibility with simple uses.
	Up, Down float64
	Hint     Location
}

// Filter is an immutable query policy once passed to a query. Area costs affect
// route choice and never substitute for baked agent clearance.
type Filter struct {
	IncludeFlags uint16
	ExcludeFlags uint16
	AreaCosts    map[uint8]float64
}

// Clone copies caller-owned cost data before work crosses a goroutine boundary.
func (f Filter) Clone() Filter {
	if f.AreaCosts != nil {
		costs := make(map[uint8]float64, len(f.AreaCosts))
		for area, cost := range f.AreaCosts {
			costs[area] = cost
		}
		f.AreaCosts = costs
	}
	return f
}

// Limits bounds the scratch memory and output capacity of one query.
type Limits struct {
	Nodes, PathPolygons, Corners int
}

// Link describes one directed traversal selected by a route. From and To are
// resolved surface endpoints; eligibility and action policy belong to the host.
type Link struct {
	ID       LinkID
	From, To Vec3
	Reverse  bool
}

// Corner is a steering target. Link, when non-nil, begins at this corner and
// must be completed by the host before ordinary surface movement resumes.
type Corner struct {
	Position Vec3
	Link     *Link
}

// Route is an immutable backend route. Corners and Tiles return caller-owned
// copies, including copied links. The originating view must remain retained.
type Route interface {
	Corners() []Corner
	Tiles() []TileRevision
}

// ProjectionResult contains a bounded surface projection.
type ProjectionResult struct {
	Status
	Location Location
}

// PathResult contains a complete or start-side partial route.
type PathResult struct {
	Status
	Route Route
}

// MoveResult describes a surface-constrained step and whether its target was
// reached. Position height must come from the selected surface.
type MoveResult struct {
	Status
	Location Location
	// Normal is zero when the backend cannot provide a surface normal.
	Normal Vec3
}

// HeightResult describes the height at a location's horizontal coordinates.
type HeightResult struct {
	Status
	Height float64
}

// RaycastResult describes a ray on the walkable surface, not a body collision.
type RaycastResult struct {
	Status
	Hit      bool
	Position Vec3
	Normal   Vec3
}

// Query owns mutable scratch and is neither concurrent nor reentrant. A worker
// or tick service must own it exclusively. Backends honor context cancellation
// during bounded batches of work, not just after the operation finishes.
type Query interface {
	Project(context.Context, Vec3, ProjectionOptions, Filter) ProjectionResult
	FindPath(context.Context, Location, Location, Filter) PathResult
	Move(context.Context, Location, Vec3, Filter) MoveResult
	Height(context.Context, Location) HeightResult
	Raycast(context.Context, Location, Vec3, Filter) RaycastResult
}

// CorridorQuery optionally repairs a route against actual surface movement.
// Stale means the current position is outside the reusable corridor and the host
// must request a new path. Partial preserves only the valid start-side prefix.
type CorridorQuery interface {
	RepairPath(context.Context, Route, Location, Filter) PathResult
}

// View retains an immutable navigation snapshot. NewQuery returns independent
// scratch. Release is concurrency-safe and idempotent; no query, location or
// route from the view may be used after release.
type View interface {
	Revision() uint64
	NewQuery(Limits) (Query, error)
	Release()
}

// Backend publishes immutable snapshots. Acquire and CurrentRevision are safe
// concurrently with publication. A zero revision means the profile is not ready.
// A replacement must never mutate a previously acquired view.
type Backend interface {
	Acquire(context.Context, ProfileID) (View, error)
	CurrentRevision(ProfileID) uint64
}

// PendingBackend exposes desired geometry that is not yet committed. Hosts
// conservatively pause that profile, including after failed builds, so old
// routes cannot enter geometry already changed in the world.
type PendingBackend interface {
	Pending(ProfileID) bool
}
