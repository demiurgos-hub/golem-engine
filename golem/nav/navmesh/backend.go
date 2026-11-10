// Package navmesh adapts navmesh-go's pure-Go snapshots to Golem's independent
// surface contracts. It owns no entities, server ticks, workers, or game policy.
package navmesh

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
)

type source struct {
	snapshot *navigation.Snapshot
	world    *dynamic.World
}

// Backend serves independently baked profiles. Its profile registry is fixed
// at construction; each dynamic world publishes its own immutable generations.
type Backend struct{ sources map[surface.ProfileID]source }

// New constructs an adapter over static snapshots with unique profile IDs.
func New(snapshots ...*navigation.Snapshot) (*Backend, error) {
	b := &Backend{sources: make(map[surface.ProfileID]source)}
	for _, snapshot := range snapshots {
		if snapshot == nil {
			return nil, errors.New("nav/navmesh: nil snapshot")
		}
		id := surface.ProfileID(snapshot.Profile().ID)
		if err := b.add(id, source{snapshot: snapshot}); err != nil {
			return nil, err
		}
	}
	if len(b.sources) == 0 {
		return nil, errors.New("nav/navmesh: no profiles")
	}
	return b, nil
}

// NewDynamic constructs an adapter that acquires managed generation leases.
func NewDynamic(worlds ...*dynamic.World) (*Backend, error) {
	b := &Backend{sources: make(map[surface.ProfileID]source)}
	for _, world := range worlds {
		if world == nil || world.Snapshot() == nil {
			return nil, errors.New("nav/navmesh: nil world")
		}
		id := surface.ProfileID(world.Snapshot().Profile().ID)
		if err := b.add(id, source{world: world}); err != nil {
			return nil, err
		}
	}
	if len(b.sources) == 0 {
		return nil, errors.New("nav/navmesh: no profiles")
	}
	return b, nil
}

func (b *Backend) add(id surface.ProfileID, s source) error {
	if id == "" {
		return errors.New("nav/navmesh: profile ID is empty")
	}
	if _, exists := b.sources[id]; exists {
		return fmt.Errorf("nav/navmesh: duplicate profile %q", id)
	}
	b.sources[id] = s
	return nil
}

// CurrentRevision returns zero for an unknown profile.
func (b *Backend) CurrentRevision(profile surface.ProfileID) uint64 {
	if b == nil {
		return 0
	}
	s, ok := b.sources[profile]
	if !ok {
		return 0
	}
	if s.world != nil {
		return s.world.Snapshot().Revision()
	}
	return s.snapshot.Revision()
}

// Pending reports desired geometry that has not been committed. Hosts pause
// this profile until its atomic replacement is available.
func (b *Backend) Pending(profile surface.ProfileID) bool {
	if b == nil {
		return false
	}
	s, ok := b.sources[profile]
	return ok && s.world != nil && s.world.Pending()
}

// Acquire retains the current immutable profile snapshot until View.Release.
func (b *Backend) Acquire(ctx context.Context, profile surface.ProfileID) (surface.View, error) {
	if ctx == nil {
		return nil, errors.New("nav/navmesh: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, errors.New("nav/navmesh: nil backend")
	}
	s, ok := b.sources[profile]
	if !ok {
		return nil, fmt.Errorf("nav/navmesh: unknown profile %q", profile)
	}
	v := &view{snapshot: s.snapshot}
	if s.world != nil {
		v.lease = s.world.Acquire()
		v.snapshot = v.lease.Snapshot
	}
	return v, nil
}

type view struct {
	snapshot *navigation.Snapshot
	lease    *dynamic.Lease
	released atomic.Bool
}

func (v *view) Revision() uint64 { return v.snapshot.Revision() }
func (v *view) Release() {
	if v.released.CompareAndSwap(false, true) && v.lease != nil {
		v.lease.Release()
	}
}
func (v *view) NewQuery(l surface.Limits) (surface.Query, error) {
	if v.released.Load() {
		return nil, errors.New("nav/navmesh: released view")
	}
	q, err := v.snapshot.NewQuery(navigation.Limits{Nodes: l.Nodes, PathPolygons: l.PathPolygons, Corners: l.Corners})
	if err != nil {
		return nil, err
	}
	return &query{view: v, query: q}, nil
}

type location struct {
	value navigation.Surface
	view  *view
}

func (l location) Position() surface.Vec3 { return vector(l.value.Position()) }
func (l location) Revision() uint64       { return l.value.Revision() }

type route struct {
	value *navigation.Path
	view  *view
}

func (r *route) Corners() []surface.Corner {
	if r == nil || r.view.released.Load() {
		return nil
	}
	raw := r.value.Corners()
	out := make([]surface.Corner, len(raw))
	for i, c := range raw {
		out[i].Position = vector(c.Position)
		if c.Link != nil {
			out[i].Link = &surface.Link{ID: surface.LinkID(c.Link.ID), From: vector(c.Link.From), To: vector(c.Link.To), Reverse: c.Link.Reverse}
		}
	}
	return out
}
func (r *route) Tiles() []surface.TileRevision {
	if r == nil || r.view.released.Load() {
		return nil
	}
	raw := r.value.Tiles()
	out := make([]surface.TileRevision, len(raw))
	for i, t := range raw {
		out[i] = surface.TileRevision{ID: fmt.Sprintf("%d/%d/%d", t.Key.X, t.Key.Z, t.Key.Layer), Revision: t.Revision}
	}
	return out
}

type query struct {
	view  *view
	query *navigation.Query
}

func (q *query) surface(l surface.Location) (navigation.Surface, bool) {
	v, ok := l.(location)
	return v.value, ok && v.view == q.view && !q.view.released.Load()
}
func stale() surface.Status {
	return surface.Status{Code: surface.Stale, Reason: surface.Stale, Err: errors.New("nav/navmesh: stale or foreign location/view")}
}
func (q *query) Project(ctx context.Context, p surface.Vec3, o surface.ProjectionOptions, f surface.Filter) surface.ProjectionResult {
	if q.view.released.Load() {
		return surface.ProjectionResult{Status: stale()}
	}
	filter, err := convertFilter(f)
	if err != nil {
		return surface.ProjectionResult{Status: invalid(err)}
	}
	options := navigation.ProjectionOptions{Horizontal: o.Horizontal, Up: o.Up, Down: o.Down}
	if o.Up == 0 && o.Down == 0 {
		options.Up, options.Down = o.Vertical, o.Vertical
	}
	if o.Hint != nil {
		var ok bool
		options.Hint, ok = q.surface(o.Hint)
		if !ok {
			return surface.ProjectionResult{Status: stale()}
		}
	}
	r := q.query.Project(ctx, point(p), options, filter)
	out := surface.ProjectionResult{Status: status(r.Status)}
	if r.Surface.Valid() {
		out.Location = location{r.Surface, q.view}
	}
	return out
}
func (q *query) FindPath(ctx context.Context, a, b surface.Location, f surface.Filter) surface.PathResult {
	start, ok := q.surface(a)
	if !ok {
		return surface.PathResult{Status: stale()}
	}
	end, ok := q.surface(b)
	if !ok {
		return surface.PathResult{Status: stale()}
	}
	filter, err := convertFilter(f)
	if err != nil {
		return surface.PathResult{Status: invalid(err)}
	}
	r := q.query.FindPath(ctx, start, end, filter)
	out := surface.PathResult{Status: status(r.Status)}
	if r.Path != nil {
		out.Route = &route{r.Path, q.view}
	}
	return out
}
func (q *query) Move(ctx context.Context, a surface.Location, target surface.Vec3, f surface.Filter) surface.MoveResult {
	start, ok := q.surface(a)
	if !ok {
		return surface.MoveResult{Status: stale()}
	}
	filter, err := convertFilter(f)
	if err != nil {
		return surface.MoveResult{Status: invalid(err)}
	}
	r := q.query.Move(ctx, start, point(target), filter)
	out := surface.MoveResult{Status: status(r.Status)}
	out.Normal = vector(r.Normal)
	if r.Surface.Valid() {
		out.Location = location{r.Surface, q.view}
	}
	return out
}
func (q *query) Height(ctx context.Context, a surface.Location) surface.HeightResult {
	start, ok := q.surface(a)
	if !ok {
		return surface.HeightResult{Status: stale()}
	}
	r := q.query.Height(ctx, start)
	return surface.HeightResult{Status: status(r.Status), Height: r.Height}
}
func (q *query) Raycast(ctx context.Context, a surface.Location, target surface.Vec3, f surface.Filter) surface.RaycastResult {
	start, ok := q.surface(a)
	if !ok {
		return surface.RaycastResult{Status: stale()}
	}
	filter, err := convertFilter(f)
	if err != nil {
		return surface.RaycastResult{Status: invalid(err)}
	}
	r := q.query.Raycast(ctx, start, point(target), filter)
	return surface.RaycastResult{Status: status(r.Status), Hit: r.Hit, Position: vector(r.Position), Normal: vector(r.Normal)}
}
func (q *query) RepairPath(ctx context.Context, previous surface.Route, current surface.Location, f surface.Filter) surface.PathResult {
	path, ok := previous.(*route)
	if !ok || path == nil || path.view.released.Load() {
		return surface.PathResult{Status: stale()}
	}
	position, ok := q.surface(current)
	if !ok {
		return surface.PathResult{Status: stale()}
	}
	filter, err := convertFilter(f)
	if err != nil {
		return surface.PathResult{Status: invalid(err)}
	}
	r := q.query.RepairPath(ctx, path.value, position, filter)
	out := surface.PathResult{Status: status(r.Status)}
	if r.Path != nil {
		out.Route = &route{r.Path, q.view}
	}
	return out
}

func point(v surface.Vec3) navigation.Vec3  { return navigation.Vec3{X: v.X, Y: v.Y, Z: v.Z} }
func vector(v navigation.Vec3) surface.Vec3 { return surface.Vec3{X: v.X, Y: v.Y, Z: v.Z} }
func convertFilter(f surface.Filter) (navigation.Filter, error) {
	out := navigation.Filter{IncludeFlags: f.IncludeFlags, ExcludeFlags: f.ExcludeFlags}
	for area, cost := range f.AreaCosts {
		if area >= 64 || math.IsNaN(cost) || math.IsInf(cost, 0) || cost < 1 || cost > math.MaxFloat32 {
			return out, errors.New("nav/navmesh: area costs require IDs below 64 and finite costs >= 1")
		}
		out.AreaCosts[area] = float32(cost)
	}
	return out, nil
}
func invalid(err error) surface.Status {
	return surface.Status{Code: surface.InvalidInput, Reason: surface.InvalidInput, Err: err}
}
func status(s navigation.Status) surface.Status {
	return surface.Status{Code: code(s.Code), Reason: code(s.Reason), Err: s.Err}
}
func code(c navigation.Code) surface.Code {
	switch c {
	case navigation.Complete:
		return surface.Complete
	case navigation.Partial:
		return surface.Partial
	case navigation.Unreachable:
		return surface.Unreachable
	case navigation.NoSurface:
		return surface.NoSurface
	case navigation.InvalidInput:
		return surface.InvalidInput
	case navigation.Unloaded:
		return surface.Unloaded
	case navigation.Cancelled:
		return surface.Cancelled
	case navigation.ResourceExhausted:
		return surface.ResourceExhausted
	case navigation.Stale:
		return surface.Stale
	case navigation.Ambiguous:
		return surface.Ambiguous
	case navigation.NotReady:
		return surface.NotReady
	default:
		return surface.Failed
	}
}

// PreparedBuild adapts a privately prepared dynamic candidate to a host's
// commit/discard boundary without importing the root Golem runtime.
type PreparedBuild struct {
	World     *dynamic.World
	Candidate *dynamic.Candidate
}

// PrepareBuild captures the current desired world and marks capacity pressure
// as explicitly retryable for hosts with bounded retry scheduling.
func PrepareBuild(ctx context.Context, world *dynamic.World) (*PreparedBuild, error) {
	if world == nil {
		return nil, errors.New("nav/navmesh: nil dynamic world")
	}
	candidate, err := world.Prepare(ctx)
	if err != nil {
		return nil, buildError(err)
	}
	return &PreparedBuild{World: world, Candidate: candidate}, nil
}

type transientBuildError struct{ cause error }

func (e transientBuildError) Error() string              { return e.cause.Error() }
func (e transientBuildError) Unwrap() error              { return e.cause }
func (e transientBuildError) RetryNavigationBuild() bool { return true }
func buildError(err error) error {
	if errors.Is(err, dynamic.ErrBusy) {
		return transientBuildError{cause: err}
	}
	return err
}

// Commit publishes the complete replacement atomically if still current.
func (b *PreparedBuild) Commit() error {
	if b == nil || b.World == nil || b.Candidate == nil {
		return errors.New("nav/navmesh: invalid prepared build")
	}
	return buildError(b.World.Commit(b.Candidate))
}

// Discard releases pending preparation without losing desired source state.
func (b *PreparedBuild) Discard() {
	if b != nil && b.World != nil && b.Candidate != nil {
		b.World.Discard(b.Candidate)
	}
}

var _ surface.Backend = (*Backend)(nil)
