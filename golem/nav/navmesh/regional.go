package navmesh

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
)

type regionalSafetyCache struct {
	mu    sync.Mutex
	value atomic.Pointer[regionalSafety]
}

type regionalSafety struct {
	owner         *Backend
	profile       surface.ProfileID
	state         *dynamic.Safety
	topology      navigation.Topology
	revision      uint64
	scopeRevision uint64
	scopes        map[uint64]*regionalSafety
}

var rejectedRegionalSafety = &regionalSafety{}

// Safety returns a cached immutable capture of this profile's quarantine and
// published dependencies. Unknown profiles return a capture that rejects use.
func (b *Backend) Safety(profile surface.ProfileID) surface.RegionalSafety {
	if b == nil {
		return rejectedRegionalSafety
	}
	s, ok := b.sources[profile]
	if !ok {
		return rejectedRegionalSafety
	}
	var state *dynamic.Safety
	if s.world != nil {
		state = s.world.Safety()
	}
	if cached := s.safety.value.Load(); cached != nil && cached.state == state {
		return cached
	}
	s.safety.mu.Lock()
	defer s.safety.mu.Unlock()
	// Recapture after waiting so an older caller cannot overwrite a newer
	// wrapper family. World capture never calls back into this adapter lock.
	if s.world != nil {
		state = s.world.Safety()
	}
	if cached := s.safety.value.Load(); cached != nil && cached.state == state {
		return cached
	}
	v := &regionalSafety{owner: b, profile: profile, state: state, scopes: make(map[uint64]*regionalSafety, 3)}
	if s.snapshot != nil {
		v.topology, v.revision = s.snapshot.Topology(), s.snapshot.Revision()
		v.scopes[v.revision] = &regionalSafety{owner: b, profile: profile, topology: v.topology, revision: v.revision, scopeRevision: v.revision}
	} else {
		state.ForEachScope(func(revision uint64, scope *dynamic.Safety) {
			v.scopes[revision] = &regionalSafety{owner: b, profile: profile, state: scope, scopeRevision: revision}
		})
	}
	s.safety.value.Store(v)
	return v
}

// ForView selects cached safety for a live view of this backend and profile.
// Current views avoid other generations' retirement quarantine; old views keep
// every published change since their generation. No view or snapshot is kept
// by the cache, and handles from other generations cannot use the result.
func (s *regionalSafety) ForView(value surface.View) surface.RegionalSafety {
	v, ok := value.(*view)
	if !ok || v == nil || s == nil || s.owner == nil || v.owner != s.owner || v.released.Load() || surface.ProfileID(v.snapshot.Profile().ID) != s.profile {
		return rejectedRegionalSafety
	}
	revision := v.Revision()
	if s.scopeRevision != 0 {
		if s.scopeRevision == revision {
			return s
		}
		return rejectedRegionalSafety
	}
	if cached := s.scopes[revision]; cached != nil {
		return cached
	}
	return rejectedRegionalSafety
}

// CommittedRevision reports the published revision captured by this safety state.
func (s *regionalSafety) CommittedRevision() uint64 {
	if s.state != nil {
		return s.state.CommittedRevision()
	}
	return s.revision
}

// DesiredRevision reports the accepted geometry revision in this capture.
func (s *regionalSafety) DesiredRevision() uint64 {
	if s.state != nil {
		return s.state.DesiredRevision()
	}
	return s.revision
}

// Token identifies this coherent safety capture within its backend and profile.
func (s *regionalSafety) Token() uint64 {
	if s.state != nil {
		return s.state.Token()
	}
	return s.revision
}

// ProtectsRetiredViews permits safe old-view movement outside the union of all
// changes published since every still-retained generation. Static views never
// change; unknown profiles do not advertise this capability.
func (s *regionalSafety) ProtectsRetiredViews() bool {
	return s != nil && s.owner != nil && (s.state != nil || s.revision != 0)
}

// HasQuarantine includes pending edits, unloaded columns and changed regions
// still protected by retired views, even when committed and desired agree.
func (s *regionalSafety) HasQuarantine() bool {
	if s == nil || s.owner == nil {
		return true
	}
	if s.state != nil {
		return s.state.HasQuarantine()
	}
	return s.revision == 0
}

// SafeLocation checks live ownership, unchanged support and regional quarantine.
func (s *regionalSafety) SafeLocation(value surface.Location) bool {
	l, ok := value.(location)
	if !ok || l.view == nil || l.view.owner != s.owner || l.view.released.Load() || s.scopeRevision != 0 && l.view.Revision() != s.scopeRevision {
		return false
	}
	if s.state != nil {
		return s.state.SafeLocation(l.value)
	}
	return s.topology.SurfaceUnchanged(l.value)
}

// SafeRoute checks live ownership and every retained corridor dependency.
func (s *regionalSafety) SafeRoute(value surface.Route) bool {
	r, ok := value.(*route)
	if !ok || r == nil || r.view == nil || r.view.owner != s.owner || r.view.released.Load() || s.scopeRevision != 0 && r.view.Revision() != s.scopeRevision {
		return false
	}
	if s.state != nil {
		return s.state.SafePath(r.value)
	}
	return s.topology.PathUnchanged(r.value)
}

// SafeSegment rejects any crossing of the captured quarantine columns.
func (s *regionalSafety) SafeSegment(from, to surface.Vec3) bool {
	if s.state != nil {
		return s.state.SafeSegment(point(from), point(to))
	}
	return s.revision != 0 && from.Finite() && to.Finite()
}

// SafeBounds rejects invalid bounds or overlap with captured quarantine columns.
func (s *regionalSafety) SafeBounds(min, max surface.Vec3) bool {
	if s.state != nil {
		return s.state.SafeBounds(point(min), point(max))
	}
	return s.revision != 0 && min.Finite() && max.Finite() && min.X <= max.X && min.Y <= max.Y && min.Z <= max.Z
}

// RebasePath transfers unchanged support to this view and repairs an optional
// corridor. Repair can change corners or trim a suffix and return Partial.
func (q *query) RebasePath(ctx context.Context, previous surface.Route, support surface.Location, actual surface.Vec3, tolerance float64, f surface.Filter) surface.RebaseResult {
	return q.transferPath(ctx, previous, support, actual, tolerance, f, false)
}

// TransferPath transfers a wholly unchanged corridor without repairing or
// trimming it. The caller retains its existing corner cursor and partial status.
func (q *query) TransferPath(ctx context.Context, previous surface.Route, support surface.Location, actual surface.Vec3, tolerance float64, f surface.Filter) surface.RebaseResult {
	return q.transferPath(ctx, previous, support, actual, tolerance, f, true)
}

func (q *query) transferPath(ctx context.Context, previous surface.Route, support surface.Location, actual surface.Vec3, tolerance float64, f surface.Filter, unchanged bool) surface.RebaseResult {
	old, ok := support.(location)
	if !ok || old.view == nil || old.view.owner != q.view.owner || old.view.released.Load() || q.view.released.Load() || old.view.snapshot.Profile() != q.view.snapshot.Profile() {
		return surface.RebaseResult{Status: stale()}
	}
	var path *navigation.Path
	if previous != nil {
		r, ok := previous.(*route)
		if !ok || r == nil || r.view == nil || r.view.owner != q.view.owner || r.view.released.Load() || r.view.snapshot.Profile() != q.view.snapshot.Profile() || unchanged && r.view != old.view {
			return surface.RebaseResult{Status: stale()}
		}
		path = r.value
	}
	filter, err := convertFilter(f)
	if err != nil {
		return surface.RebaseResult{Status: invalid(err)}
	}
	var r navigation.RebaseResult
	if unchanged {
		r = q.query.RebaseUnchangedPath(ctx, path, old.value, point(actual), tolerance, filter)
	} else {
		r = q.query.RebasePath(ctx, path, old.value, point(actual), tolerance, filter)
	}
	out := surface.RebaseResult{Status: status(r.Status)}
	if r.Surface.Valid() {
		out.Location = location{r.Surface, q.view}
	}
	if r.Path != nil {
		out.Route = &route{r.Path, q.view}
	}
	return out
}

var _ surface.RegionalBackend = (*Backend)(nil)
var _ surface.RetainedRegionalSafety = (*regionalSafety)(nil)
var _ surface.ViewRegionalSafety = (*regionalSafety)(nil)
var _ surface.RebaseQuery = (*query)(nil)
var _ surface.CorridorTransferQuery = (*query)(nil)
