package golem

import (
	"errors"
	"math"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

func stagedNavigationRoute(t *testing.T, s *Server, id int64, corners ...surface.Corner) {
	t.Helper()
	a := s.navigation.agents[id]
	a.config.StagedPhysics = true
	view, err := s.navigation.backend.Acquire(s.runCtx, a.config.Profile)
	if err != nil {
		t.Fatal(err)
	}
	query, err := view.NewQuery(s.navigation.config.Limits)
	if err != nil {
		t.Fatal(err)
	}
	position, _, _ := s.navigation.position(id)
	projected := query.Project(s.runCtx, position, a.config.Projection, a.config.Filter)
	if projected.Location == nil {
		t.Fatal("fixture has no support")
	}
	a.view, a.query, a.location = view, query, projected.Location
	a.route, a.corners = navigationTestRoute{corners: corners}, append([]surface.Corner(nil), corners...)
	a.state.Mode, a.state.HasDestination, a.state.WorldRevision = NavigationFollowing, true, view.Revision()
	a.state.Destination, a.lastPosition, a.lastRepair = corners[len(corners)-1].Position, position, position
	a.supportEstablished = true
}

func stagedFrame(t *testing.T, s *Server, dt float64) *NavigationFrame {
	t.Helper()
	s.tick++
	f, err := s.PrepareNavigationFrame(dt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.AbortNavigationFrame(f) })
	return f
}

func TestNavigationFrameDefersPoseAndCursorAndApportionsTime(t *testing.T) {
	s, e := navigationTestServer(t, &navigationTestBackend{revision: 1})
	stagedNavigationRoute(t, s, 1,
		surface.Corner{Position: surface.Vec3{}},
		surface.Corner{Position: surface.Vec3{X: .03}},
		surface.Corner{Position: surface.Vec3{X: .06}},
		surface.Corner{Position: surface.Vec3{X: 10}})
	a := s.navigation.agents[1]
	before, support, next := e.position, a.location, a.next
	f := stagedFrame(t, s, .05)
	if e.position != before || a.location != support || a.next != next {
		t.Fatal("preparation committed a body position or proposed support/cursor")
	}
	proposals := f.Proposals()
	if len(proposals) != 1 || len(proposals[0].Segments) != 3 {
		t.Fatalf("proposals=%+v", proposals)
	}
	var duration float64
	for _, segment := range proposals[0].Segments {
		duration += segment.Delta
	}
	if math.Abs(duration-.05) > 1e-12 {
		t.Fatalf("segment durations sum to %g, want .05", duration)
	}
	proposals[0].Segments[0].Target.X = 999
	if f.Proposals()[0].Segments[0].Target.X == 999 {
		t.Fatal("caller mutated the retained proposal")
	}
	actual := surface.Vec3{X: .04}
	if err := s.CommitNavigationFrame(f, []NavigationMotionResult{{EntityID: 1, Position: actual, Path: []surface.Vec3{before, actual}}}); err != nil {
		t.Fatal(err)
	}
	if e.position != navigationFramePosition(actual) || a.next >= 3 {
		t.Fatalf("clipped motion consumed the requested path: position=%+v next=%d", e.position, a.next)
	}
	if _, err := s.PrepareNavigationFrame(.05); err == nil {
		t.Fatal("movement was admitted twice in one tick")
	}
}

func TestNavigationFrameRejectsAllResultsBeforeWriting(t *testing.T) {
	s, first := navigationTestServer(t, &navigationTestBackend{revision: 1})
	second := &navigationTestEntity{id: 2, position: surface.Vec3{Z: 2}}
	if err := s.CreateEntity(second); err != nil {
		t.Fatal(err)
	}
	cfg := s.navigation.agents[1].config
	cfg.StagedPhysics = true
	if err := s.AttachNavigation(2, cfg); err != nil {
		t.Fatal(err)
	}
	stagedNavigationRoute(t, s, 1, surface.Corner{}, surface.Corner{Position: surface.Vec3{X: 10}})
	stagedNavigationRoute(t, s, 2, surface.Corner{Position: second.position}, surface.Corner{Position: surface.Vec3{X: 10, Z: 2}})
	f := stagedFrame(t, s, .05)
	one, two := first.position, second.position
	err := s.CommitNavigationFrame(f, []NavigationMotionResult{
		{EntityID: 1, Position: surface.Vec3{X: .1}, Path: []surface.Vec3{one, {X: .1}}},
		{EntityID: 2, Position: surface.Vec3{X: .1, Z: 2}},
	})
	if !errors.Is(err, ErrNavigationFrameFault) {
		t.Fatalf("error=%v", err)
	}
	if first.position != one || second.position != two {
		t.Fatal("a partially validated frame wrote an entity")
	}
}

func TestNavigationFrameChecksWholePathAndIdleQuarantine(t *testing.T) {
	s, backend := regionalServer(t)
	a := s.navigation.agents[1]
	a.config.StagedPhysics = true
	e, _ := s.Get(1)
	entity := e.(*navigationTestEntity)
	entity.SetPosition3D(-1, 0, 0)
	backend.edit(true, false)
	f := stagedFrame(t, s, .05)
	if !f.Contains(1) || len(f.Proposals()) != 0 {
		t.Fatal("idle staged attachment was not captured independently of proposals")
	}
	if f.SafeSegment(1, surface.Vec3{X: -1}, surface.Vec3{X: 3}) {
		t.Fatal("idle physical motion crossed quarantine")
	}
	err := s.CommitNavigationFrame(f, []NavigationMotionResult{{EntityID: 1, Position: surface.Vec3{X: -1},
		Path: []surface.Vec3{{X: -1}, {X: 3}, {X: -1}}}})
	if !errors.Is(err, ErrNavigationFrameFault) || entity.position.X != -1 {
		t.Fatalf("unsafe intermediate path accepted: err=%v position=%+v", err, entity.position)
	}
}

func TestNavigationFrameReconcilesIdlePhysicalSupportLoss(t *testing.T) {
	s, entity := navigationTestServer(t, &navigationTestBackend{revision: 1})
	a := s.navigation.agents[1]
	a.config.StagedPhysics = true
	f := stagedFrame(t, s, .05)
	from := entity.position
	actual := surface.Vec3{Y: .2}
	if err := s.CommitNavigationFrame(f, []NavigationMotionResult{{EntityID: 1, Position: actual, Path: []surface.Vec3{from, actual}}}); err != nil {
		t.Fatal(err)
	}
	if a.state.Mode != NavigationUnsupported || entity.position != navigationFramePosition(actual) {
		t.Fatalf("support loss did not retain actual body: mode=%v position=%+v", a.state.Mode, entity.position)
	}
}

func TestNavigationFrameGenerationAndSafetyChangesRejectBeforeWrites(t *testing.T) {
	for _, change := range []string{"generation", "safety", "source"} {
		t.Run(change, func(t *testing.T) {
			s, backend := regionalServer(t)
			entity, _ := s.Get(1)
			e := entity.(*navigationTestEntity)
			e.SetPosition3D(-2, 0, 0)
			stagedNavigationRoute(t, s, 1, surface.Corner{Position: e.position}, surface.Corner{Position: surface.Vec3{X: -10}})
			f := stagedFrame(t, s, .05)
			from := e.position
			switch change {
			case "generation":
				_ = s.SetNavigationDestination(1, surface.Vec3{X: -20})
			case "safety":
				backend.edit(true, false)
			case "source":
				e.SetPosition3D(-3, 0, 0)
			}
			before := e.position
			err := s.CommitNavigationFrame(f, []NavigationMotionResult{{EntityID: 1, Position: surface.Vec3{X: -2.1}, Path: []surface.Vec3{from, {X: -2.1}}}})
			if !errors.Is(err, ErrNavigationFrameFault) || e.position != before {
				t.Fatalf("stale frame accepted: err=%v position=%+v", err, e.position)
			}
		})
	}
}

func TestNavigationFrameInitialProjectionDoesNotSpendTimeTwice(t *testing.T) {
	s, entity := navigationTestServer(t, &navigationTestBackend{revision: 1})
	entity.SetPosition3D(0, .02, 0)
	stagedNavigationRoute(t, s, 1, surface.Corner{}, surface.Corner{Position: surface.Vec3{X: 10}})
	s.navigation.agents[1].initialProjection = true
	f := stagedFrame(t, s, .05)
	segments := f.Proposals()[0].Segments
	if len(segments) != 2 || !segments[0].Projection || segments[0].Delta != 0 || segments[1].Delta != .05 {
		t.Fatalf("initial projection consumed simulation time: %+v", segments)
	}
	if entity.position.Y == 0 {
		t.Fatal("initial projection wrote the entity before acceptance")
	}
}

type stagedTraversalTestAction struct {
	prepared, committed, discarded int
	last                           NavigationTraversalUpdate
	onCommit                       func()
}

func (a *stagedTraversalTestAction) AdvanceNavigationTraversal(float64) (NavigationTraversalUpdate, error) {
	panic("staged traversal used the synchronous advance method")
}
func (a *stagedTraversalTestAction) CancelNavigationTraversal(string) {}
func (a *stagedTraversalTestAction) PrepareNavigationTraversal(float64) (NavigationTraversalUpdate, error) {
	a.prepared++
	return NavigationTraversalUpdate{Position: surface.Vec3{X: .1}}, nil
}
func (a *stagedTraversalTestAction) CommitNavigationTraversal(update NavigationTraversalUpdate) {
	a.committed++
	a.last = update
	if a.onCommit != nil {
		a.onCommit()
	}
}
func (a *stagedTraversalTestAction) DiscardNavigationTraversal() { a.discarded++ }

func TestNavigationFrameTraversalCommitsOrDiscardsExactlyOnce(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "abort", true: "commit"}[commit], func(t *testing.T) {
			s, entity := navigationTestServer(t, &navigationTestBackend{revision: 1})
			stagedNavigationRoute(t, s, 1, surface.Corner{}, surface.Corner{Position: surface.Vec3{X: 10}})
			action := &stagedTraversalTestAction{}
			a := s.navigation.agents[1]
			a.state.Mode = NavigationTraversing
			a.traversal = &navigationTraversal{action: action, bounded: true,
				bounds: NavigationTraversalBounds{Min: surface.Vec3{X: -1, Y: -1, Z: -1}, Max: surface.Vec3{X: 2, Y: 2, Z: 2}}}
			f := stagedFrame(t, s, .05)
			if action.prepared != 1 || a.traversal.elapsed != 0 || entity.position != (surface.Vec3{}) {
				t.Fatal("traversal preparation advanced durable motion")
			}
			if commit {
				actual := surface.Vec3{X: .04}
				if err := s.CommitNavigationFrame(f, []NavigationMotionResult{{EntityID: 1, Position: actual, Path: []surface.Vec3{{}, actual}}}); err != nil {
					t.Fatal(err)
				}
				if action.committed != 1 || action.discarded != 0 || action.last.Position != navigationFramePosition(actual) || a.traversal.elapsed != .05 {
					t.Fatalf("bad commit: action=%+v elapsed=%g", action, a.traversal.elapsed)
				}
			} else {
				if err := s.AbortNavigationFrame(f); err != nil {
					t.Fatal(err)
				}
				_ = s.AbortNavigationFrame(f)
				if action.committed != 0 || action.discarded != 1 || a.traversal.elapsed != 0 {
					t.Fatalf("bad abort: action=%+v elapsed=%g", action, a.traversal.elapsed)
				}
			}
		})
	}
}

func TestNavigationFrameConfigurationRejectsTwoMotionOwners(t *testing.T) {
	cfg := NavigationAgentConfig{StagedPhysics: true, Motor: navigationMotorFunc(func(m NavigationMotion) (surface.Vec3, error) { return m.Target, nil })}
	if err := validateNavigationAgent(&cfg); err == nil {
		t.Fatal("staged physics accepted a second synchronous motor")
	}
}

func TestNavigationFrameAbortOnRestoration(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	stagedNavigationRoute(t, s, 1, surface.Corner{}, surface.Corner{Position: surface.Vec3{X: 10}})
	f := stagedFrame(t, s, .05)
	if err := s.BeginNavigationRestore(); err != nil {
		t.Fatal(err)
	}
	if !f.closed || s.navigation.frame != nil || !errors.Is(s.ValidateNavigationFrame(f), ErrNavigationFrameFault) {
		t.Fatal("restoration kept an in-flight physical frame usable")
	}
}

var _ NavigationStagedTraversalAction = (*stagedTraversalTestAction)(nil)

func TestNavigationFrameTraversalCallbackObservesAllReconciledSupport(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	stagedNavigationRoute(t, s, 1, surface.Corner{}, surface.Corner{Position: surface.Vec3{X: 10}})
	second := &navigationTestEntity{id: 2, position: surface.Vec3{Z: 2}}
	if err := s.CreateEntity(second); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachNavigation(2, s.navigation.agents[1].config); err != nil {
		t.Fatal(err)
	}
	called := false
	action := &stagedTraversalTestAction{onCommit: func() {
		called = true
		if s.navigation.agents[2].state.Mode != NavigationUnsupported || second.position.Y != float64(float32(.2)) {
			t.Error("traversal callback observed another entity before support reconciliation")
		}
	}}
	a := s.navigation.agents[1]
	a.state.Mode = NavigationTraversing
	a.traversal = &navigationTraversal{action: action}
	f := stagedFrame(t, s, .05)
	if err := s.CommitNavigationFrame(f, []NavigationMotionResult{
		{EntityID: 1, Position: surface.Vec3{X: .1}, Path: []surface.Vec3{{}, {X: .1}}},
		{EntityID: 2, Position: surface.Vec3{Y: .2, Z: 2}, Path: []surface.Vec3{{Z: 2}, {Y: .2, Z: 2}}},
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("traversal commit was not dispatched")
	}
}

func TestNavigationFrameCapturesFrozenIdleAndRestoreBarrier(t *testing.T) {
	s, backend := regionalServer(t)
	s.navigation.agents[1].config.StagedPhysics = true
	backend.edit(true, false)
	f := stagedFrame(t, s, .05)
	if !f.Frozen(1) || !f.SafeSegment(1, surface.Vec3{}, surface.Vec3{}) || f.SafeSegment(1, surface.Vec3{}, surface.Vec3{X: .01}) {
		t.Fatal("idle quarantine did not require a stationary solver constraint")
	}
	if err := s.CommitNavigationFrame(f, []NavigationMotionResult{{EntityID: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginNavigationRestore(); err != nil {
		t.Fatal(err)
	}
	f = stagedFrame(t, s, .05)
	if !f.Frozen(1) {
		t.Fatal("restore barrier did not freeze attachment")
	}
	if err := s.CommitNavigationFrame(f, []NavigationMotionResult{{EntityID: 1}}); err != nil {
		t.Fatalf("stationary restoration frame faulted: %v", err)
	}
}
