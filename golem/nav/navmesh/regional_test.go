package navmesh

import (
	"context"
	"runtime"
	"sync"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/artifact"
	"github.com/demiurgos-hub/navmesh-go/bake"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func TestRegionalSafetyConcurrentCapturePublicationAndRetirement(t *testing.T) {
	scene, cfg := adapterScene()
	w, err := dynamic.New(context.Background(), scene, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewDynamic(w)
	if err != nil {
		t.Fatal(err)
	}
	old, err := b.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Release()
	stop := make(chan struct{})
	failures := make(chan string, 1)
	var readers sync.WaitGroup
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				capture := b.Safety("human").(*regionalSafety)
				scope := capture.ForView(old)
				valid := capture.CommittedRevision() <= capture.DesiredRevision() && len(capture.scopes) <= 3
				// Release may race selection; a rejected view must reject geometry.
				if scope.CommittedRevision() == 0 {
					valid = valid && !scope.SafeSegment(surface.Vec3{}, surface.Vec3{})
				} else {
					valid = valid && scope.CommittedRevision() == capture.CommittedRevision() && scope.DesiredRevision() == capture.DesiredRevision() && scope.Token() != capture.Token()
				}
				for revision, scoped := range capture.scopes {
					valid = valid && scoped.scopeRevision == revision && scoped.CommittedRevision() == capture.CommittedRevision() && scoped.DesiredRevision() == capture.DesiredRevision()
				}
				if !valid {
					select {
					case failures <- "partially published or incoherent safety family":
					default:
					}
					return
				}
				runtime.Gosched()
			}
		}()
	}
	defer func() {
		close(stop)
		readers.Wait()
		select {
		case failure := <-failures:
			t.Error(failure)
		default:
		}
	}()
	for i := 0; i < 8; i++ {
		blocker := geom.Volume{ID: "churn", Shape: "box", Exclude: true, Disabled: i%2 != 0, Bounds: geom.Bounds{Min: geom.Vec3{X: 2, Z: -1}, Max: geom.Vec3{X: 3, Y: 2, Z: 1}}}
		if _, err := w.Apply(dynamic.Changes{Volumes: []geom.Volume{blocker}}); err != nil {
			t.Fatal(err)
		}
		candidate, err := w.Prepare(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err = w.Commit(candidate); err != nil {
			t.Fatal(err)
		}
		if i == 4 {
			old.Release()
		}
	}
}

func TestRegionalViewScopeRejectsOtherProfileAndKeepsUnloadedColumns(t *testing.T) {
	scene, cfg := adapterScene()
	human, err := dynamic.New(context.Background(), scene, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Profile.ID = "other"
	other, err := dynamic.New(context.Background(), scene, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewDynamic(human, other)
	if err != nil {
		t.Fatal(err)
	}
	old, err := b.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Release()
	otherView, err := b.Acquire(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	defer otherView.Release()
	initial := b.Safety("human").(surface.ViewRegionalSafety)
	if initial.ForView(otherView).SafeSegment(surface.Vec3{}, surface.Vec3{}) {
		t.Fatal("same backend and revision bypassed profile ownership")
	}
	q, err := old.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	upper := q.Project(context.Background(), surface.Vec3{Y: 3.1}, surface.ProjectionOptions{Horizontal: .3, Up: .3, Down: .3}, surface.Filter{})
	if upper.Code != surface.Complete {
		t.Fatal(upper.Status)
	}
	otherQuery, err := otherView.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if result := otherQuery.(surface.CorridorTransferQuery).TransferPath(context.Background(), nil, upper.Location, upper.Location.Position(), .05, surface.Filter{}); result.Code != surface.Stale {
		t.Fatal("same backend bypassed transfer profile ownership", result.Status)
	}
	tile, ok := upper.Location.(location).value.Tile()
	if !ok {
		t.Fatal("missing upper-floor tile")
	}
	key := artifact.TileKey{X: tile.Key.X, Z: tile.Key.Z}
	if _, err = human.SetLoaded([]artifact.TileKey{key}, false); err != nil {
		t.Fatal(err)
	}
	candidate, err := human.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = human.Commit(candidate); err != nil {
		t.Fatal(err)
	}
	current, err := b.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	selector := b.Safety("human").(surface.ViewRegionalSafety)
	for _, view := range []surface.View{old, current} {
		scope := selector.ForView(view)
		for _, height := range []float64{0, 3.1, 100} {
			p := surface.Vec3{Y: height}
			if scope.SafeSegment(p, p) || scope.SafeBounds(p, p) {
				t.Fatal("unloaded column escaped scoped safety on another floor")
			}
		}
	}
	old.Release()
	if b.Safety("human").(surface.ViewRegionalSafety).ForView(current).SafeSegment(surface.Vec3{}, surface.Vec3{}) {
		t.Fatal("retirement removed current-view unloaded quarantine")
	}
}

func TestRegionalAdapterSafetyRebaseAndOwnership(t *testing.T) {
	scene, cfg := adapterScene()
	for i := range scene.Assets[0].Vertices {
		scene.Assets[0].Vertices[i].X *= 8
		scene.Assets[0].Vertices[i].Z *= 2
	}
	cfg.Settings.Bounds.Min.X, cfg.Settings.Bounds.Max.X = -32, 32
	cfg.Settings.Bounds.Min.Z, cfg.Settings.Bounds.Max.Z = -8, 8
	w, err := dynamic.New(context.Background(), scene, cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewDynamic(w)
	if err != nil {
		t.Fatal(err)
	}
	old, err := b.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Release()
	q, err := old.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	project := func(x, z float64) surface.Location {
		r := q.Project(context.Background(), surface.Vec3{X: x, Y: 3.1, Z: z}, surface.ProjectionOptions{Horizontal: .3, Up: .3, Down: .3}, surface.Filter{})
		if r.Code != surface.Complete {
			t.Fatal(r.Status)
		}
		return r.Location
	}
	left, end, right := project(-24, 0), project(-20, 2), project(20, 0)
	path := q.FindPath(context.Background(), left, end, surface.Filter{})
	if path.Code != surface.Complete {
		t.Fatal(path.Status)
	}
	t.Run("CornerAllocations", func(t *testing.T) {
		corners := make([]surface.Corner, 0, len(path.Route.Corners()))
		if n := testing.AllocsPerRun(100, func() { corners = path.Route.(surface.CornerAppender).AppendCorners(corners[:0]) }); n != 0 {
			t.Fatalf("ordinary corner copying allocations %v", n)
		}
	})
	before := b.Safety("human")
	if retained, ok := before.(surface.RetainedRegionalSafety); !ok || !retained.ProtectsRetiredViews() || retained.HasQuarantine() {
		t.Fatal("known dynamic profile lacks retained-view protection")
	}
	blocker := geom.Volume{ID: "blocker", Shape: "box", Exclude: true, Bounds: geom.Bounds{Min: geom.Vec3{X: 19, Z: -1}, Max: geom.Vec3{X: 21, Y: 2, Z: 1}}}
	if _, err = w.Apply(dynamic.Changes{Volumes: []geom.Volume{blocker}}); err != nil {
		t.Fatal(err)
	}
	pending := b.Safety("human")
	if pending.Token() == before.Token() || !before.SafeLocation(right) || pending.SafeLocation(right) || !pending.SafeRoute(path.Route) || !pending.SafeLocation(left) {
		t.Fatal("regional capture lost isolation or overblocked distant route")
	}
	if pending.SafeSegment(surface.Vec3{X: -28, Y: 100}, surface.Vec3{X: 28, Y: 100}) || pending.SafeBounds(surface.Vec3{X: 19, Y: 100, Z: -1}, surface.Vec3{X: 21, Y: 101, Z: 1}) {
		t.Fatal("traversal passed through quarantine")
	}
	t.Run("SafetyAllocations", func(t *testing.T) {
		if n := testing.AllocsPerRun(100, func() {
			s := b.Safety("human")
			if !s.SafeLocation(left) || !s.SafeRoute(path.Route) {
				panic("unsafe")
			}
		}); n != 0 {
			t.Fatalf("adapter safety allocations %v", n)
		}
	})
	c, err := w.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Commit(c); err != nil {
		t.Fatal(err)
	}
	published := b.Safety("human")
	if published.CommittedRevision() != published.DesiredRevision() || !published.(surface.RetainedRegionalSafety).HasQuarantine() || published.SafeSegment(right.Position(), right.Position()) {
		t.Fatal("publication exposed geometry while an old view remains retained")
	}
	current, err := b.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Release()
	next, err := current.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	selector := published.(surface.ViewRegionalSafety)
	oldScope, currentScope := selector.ForView(old), selector.ForView(current)
	if !oldScope.(surface.RetainedRegionalSafety).ProtectsRetiredViews() || !currentScope.(surface.RetainedRegionalSafety).ProtectsRetiredViews() || oldScope.Token() == currentScope.Token() || currentScope.Token() == published.Token() {
		t.Fatal("scoped safety lacks retained protection or distinct token")
	}
	if oldScope.SafeSegment(right.Position(), right.Position()) || !currentScope.SafeSegment(right.Position(), right.Position()) || !oldScope.SafeLocation(left) || currentScope.SafeLocation(left) || currentScope.SafeRoute(path.Route) {
		t.Fatal("scope lost history isolation or accepted old handles")
	}
	secondCurrent, err := b.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	if selector.ForView(secondCurrent) != currentScope {
		t.Fatal("scope cache is per view instead of per generation")
	}
	secondCurrent.Release()
	t.Run("ScopeAllocations", func(t *testing.T) {
		if n := testing.AllocsPerRun(100, func() {
			if s := b.Safety("human").(surface.ViewRegionalSafety).ForView(current); s != currentScope || !s.SafeSegment(right.Position(), right.Position()) {
				panic("scope cache changed")
			}
		}); n != 0 {
			t.Fatalf("adapter scope reads allocate: %g", n)
		}
	})
	rebased := next.(surface.RebaseQuery).RebasePath(context.Background(), path.Route, left, left.Position(), .05, surface.Filter{})
	if rebased.Code != surface.Complete || rebased.Route == nil || rebased.Location.Revision() != current.Revision() {
		t.Fatal("rebase failed", rebased.Status)
	}
	onlySupport := next.(surface.RebaseQuery).RebasePath(context.Background(), nil, left, left.Position(), .05, surface.Filter{})
	if onlySupport.Code != surface.Complete || onlySupport.Location == nil || onlySupport.Route != nil {
		t.Fatal("support-only rebase failed", onlySupport.Status)
	}
	foreign, err := New(w.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if foreign.Safety("human").SafeLocation(rebased.Location) || foreign.Safety("human").SafeRoute(rebased.Route) {
		t.Fatal("foreign backend handles accepted")
	}
	foreignView, err := foreign.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer foreignView.Release()
	for _, invalidView := range []surface.View{nil, foreignView, secondCurrent} {
		rejected := selector.ForView(invalidView)
		if rejected.SafeLocation(left) || rejected.SafeRoute(path.Route) || rejected.SafeSegment(left.Position(), left.Position()) || rejected.SafeBounds(left.Position(), left.Position()) {
			t.Fatal("nil/foreign/released view produced usable scope")
		}
	}
	if !currentScope.SafeLocation(rebased.Location) || !currentScope.SafeRoute(rebased.Route) || oldScope.SafeLocation(rebased.Location) || oldScope.SafeRoute(rebased.Route) {
		t.Fatal("rebased handles did not respect selected generation")
	}
	unknown := b.Safety("missing")
	if unknown.CommittedRevision() != 0 || unknown.SafeLocation(left) || unknown.SafeSegment(surface.Vec3{}, surface.Vec3{}) {
		t.Fatal("unknown profile safe")
	}
	if unknown.(surface.RetainedRegionalSafety).ProtectsRetiredViews() || !unknown.(surface.RetainedRegionalSafety).HasQuarantine() {
		t.Fatal("unknown profile advertises retained-view safety")
	}
	old.Release()
	if selector.ForView(old).SafeSegment(left.Position(), left.Position()) || oldScope.SafeLocation(left) || !currentScope.SafeSegment(right.Position(), right.Position()) {
		t.Fatal("released view remained selectable or changed another immutable scope")
	}
	if b.Safety("human").Token() == published.Token() || b.Safety("human").(surface.RetainedRegionalSafety).HasQuarantine() || !b.Safety("human").SafeSegment(right.Position(), right.Position()) || !published.(surface.RetainedRegionalSafety).HasQuarantine() {
		t.Fatal("adapter did not refresh immutable safety after last retired view released")
	}
	if pending.SafeLocation(left) || pending.SafeRoute(path.Route) || b.Safety("human").SafeLocation(left) {
		t.Fatal("released handle accepted")
	}
	if r := next.(surface.RebaseQuery).RebasePath(context.Background(), path.Route, left, left.Position(), .05, surface.Filter{}); r.Code != surface.Stale {
		t.Fatal("released rebase accepted", r.Status)
	}
	if !b.Safety("human").SafeLocation(rebased.Location) || !b.Safety("human").SafeRoute(rebased.Route) {
		t.Fatal("new owned handles rejected")
	}
}

func TestCornerAppenderPreservesIndependentTraversalValues(t *testing.T) {
	scene, cfg := adapterScene()
	scene.Links = []geom.Link{{ID: "stairs", Start: geom.Vec3{X: -2, Y: .1}, End: geom.Vec3{X: -2, Y: 3.1}, Radius: .4, Area: 2, Flags: 1}}
	built, err := bake.Build(context.Background(), scene, cfg)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := navigation.NewSnapshot(built.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	v, err := b.Acquire(context.Background(), "human")
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	q, err := v.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	project := func(y float64) surface.Location {
		r := q.Project(context.Background(), surface.Vec3{X: -3, Y: y}, surface.ProjectionOptions{Horizontal: .3, Up: .3, Down: .3}, surface.Filter{})
		if r.Code != surface.Complete {
			t.Fatal(r.Status)
		}
		return r.Location
	}
	r := q.FindPath(context.Background(), project(.1), project(3.1), surface.Filter{})
	if r.Code != surface.Complete {
		t.Fatal(r.Status)
	}
	appendRoute := r.Route.(surface.CornerAppender)
	first := appendRoute.AppendCorners(nil)
	var previous *surface.Link
	for _, c := range first {
		if c.Link != nil {
			previous = c.Link
			break
		}
	}
	if previous == nil {
		t.Fatal("fixture did not traverse link")
	}
	second := appendRoute.AppendCorners(first[:0])
	var copied *surface.Link
	for _, c := range second {
		if c.Link != nil {
			copied = c.Link
			break
		}
	}
	if copied == nil || copied == previous || previous.ID != "stairs" {
		t.Fatal("append reused a previously returned link")
	}
	copied.ID = "changed"
	if previous.ID != "stairs" {
		t.Fatal("copied link mutated previous output")
	}
	for _, c := range r.Route.Corners() {
		if c.Link != nil && c.Link.ID != "stairs" {
			t.Fatal("copied link mutated backend")
		}
	}
	withPrefix := appendRoute.AppendCorners([]surface.Corner{{Position: surface.Vec3{X: 99}}})
	if withPrefix[0].Position.X != 99 || len(withPrefix) != 1+len(second) {
		t.Fatal("append overwrote caller prefix")
	}
}
