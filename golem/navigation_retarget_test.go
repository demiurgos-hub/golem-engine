package golem

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

func retargetTestPath(from, to surface.Vec3) surface.PathResult {
	return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{corners: []surface.Corner{{Position: from}, {Position: to}}}}
}

func retargetTestRepair(_ context.Context, route surface.Route, location surface.Location) surface.PathResult {
	corners := route.Corners()
	return retargetTestPath(location.Position(), corners[len(corners)-1].Position)
}

func awaitRetargetCompletion(t *testing.T, s *Server) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.navigation.prepare()
		if !s.navigation.agents[1].requesting {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("replacement did not complete")
}

func TestNavigationRetargetContinuesAndRepairsFromCurrentBody(t *testing.T) {
	for _, staged := range []bool{false, true} {
		name := "synchronous"
		if staged {
			name = "staged"
		}
		t.Run(name, func(t *testing.T) {
			gate, started := make(chan struct{}), make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(gate) })
			b := &navigationTestBackend{revision: 1, repair: retargetTestRepair}
			b.path = func(ctx context.Context, from, to surface.Vec3) surface.PathResult {
				if to.X == 20 {
					close(started)
					select {
					case <-gate:
					case <-ctx.Done():
					}
				}
				return retargetTestPath(from, to)
			}
			s, e := navigationTestServer(t, b)
			s.navigation.config.PathDeadline = time.Second
			if err := s.SetNavigationDestination(1, surface.Vec3{X: 10}); err != nil {
				t.Fatal(err)
			}
			awaitNavigation(t, s, func() bool { return s.navigation.agents[1].state.Mode == NavigationFollowing })
			a := s.navigation.agents[1]
			a.config.StagedPhysics = staged
			a.config.Acceleration = 4
			oldView, speed := a.view.(*navigationTestView), a.speed
			if err := s.RetargetNavigationDestination(1, surface.Vec3{X: 20}); err != nil {
				t.Fatal(err)
			}
			generation := a.state.Generation
			tick := func() {
				if !staged {
					navigationTestTick(s)
					return
				}
				s.tick++
				s.navigation.prepare()
				f, err := s.PrepareNavigationFrame(.05)
				if err != nil {
					t.Fatal(err)
				}
				proposals := f.Proposals()
				if len(proposals) != 1 || len(proposals[0].Segments) == 0 {
					t.Fatalf("retarget stopped staged motion: %+v", proposals)
				}
				path := []surface.Vec3{e.position}
				for _, segment := range proposals[0].Segments {
					path = append(path, segment.Target)
				}
				if err := s.CommitNavigationFrame(f, []NavigationMotionResult{{EntityID: 1, Position: path[len(path)-1], Path: path}}); err != nil {
					t.Fatal(err)
				}
			}
			tick()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("replacement did not start")
			}
			for i := 0; i < 5; i++ {
				before := e.position
				if err := s.RetargetNavigationDestination(1, surface.Vec3{X: 20}); err != nil {
					t.Fatal(err)
				}
				tick()
				state, _ := s.NavigationAgentState(1)
				if e.position.X <= before.X || a.speed != speed || state.Generation != generation || !state.RetargetPending || state.Mode != NavigationFollowing || oldView.released.Load() {
					t.Fatalf("retarget interrupted retained motion: before=%+v after=%+v speed=%g state=%+v", before, e.position, a.speed, state)
				}
			}
			before := e.position
			release.Do(func() { close(gate) })
			awaitRetargetCompletion(t, s)
			state, _ := s.NavigationAgentState(1)
			if state.RetargetPending || state.Mode != NavigationFollowing || !oldView.released.Load() || e.position != before || a.corners[0].Position != before || a.speed != speed {
				t.Fatalf("replacement did not atomically repair from current body: position=%+v corners=%+v speed=%g state=%+v", e.position, a.corners, a.speed, state)
			}
			tick()
			if e.position.X <= before.X {
				t.Fatal("replacement pulled body backwards or paused")
			}
		})
	}
}

func TestNavigationRetargetCoalescesCompletedResultWithoutFalseArrival(t *testing.T) {
	b := &navigationTestBackend{revision: 1, repair: retargetTestRepair}
	s, _ := navigationTestServer(t, b)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 10})
	awaitNavigation(t, s, func() bool { return s.navigation.agents[1].state.Mode == NavigationFollowing })
	_ = s.RetargetNavigationDestination(1, surface.Vec3{X: 20})
	navigationTestTick(s)
	deadline := time.Now().Add(time.Second)
	for s.NavigationStats().Paths.Completions == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.NavigationStats().Paths.Completions != 1 {
		t.Fatal("expected completed pending replacement")
	}
	_ = s.RetargetNavigationDestination(1, surface.Vec3{X: 30})
	s.navigation.prepare()
	a := s.navigation.agents[1]
	if a.corners[len(a.corners)-1].Position.X != 20 || !a.retargeting || a.state.Destination.X != 30 || !a.needsPath || a.state.Mode != NavigationFollowing {
		t.Fatal("interim route lost the latest pending goal")
	}
	navigationTestTick(s)
	awaitRetargetCompletion(t, s)
	if a.corners[len(a.corners)-1].Position.X != 30 || a.retargeting {
		t.Fatal("latest replacement was not admitted")
	}
}

func TestNavigationRetargetEvolvingTwentyHzGoalsDoNotStarve(t *testing.T) {
	type request struct {
		target  surface.Vec3
		release chan struct{}
	}
	requests := make(chan request, 4)
	b := &navigationTestBackend{revision: 1, repair: retargetTestRepair}
	s, e := navigationTestServer(t, b)
	s.navigation.config.PathDeadline = time.Second
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 100})
	awaitNavigation(t, s, func() bool { return s.navigation.agents[1].state.Mode == NavigationFollowing })
	b.mu.Lock()
	b.path = func(ctx context.Context, from, to surface.Vec3) surface.PathResult {
		r := request{target: to, release: make(chan struct{})}
		select {
		case requests <- r:
		case <-ctx.Done():
			return surface.PathResult{}
		}
		select {
		case <-r.release:
		case <-ctx.Done():
			return surface.PathResult{}
		}
		return retargetTestPath(from, to)
	}
	b.mu.Unlock()
	var admissions []NavigationPathAdmissionMetrics
	s.navigation.config.ObservePathAdmission = func(m NavigationPathAdmissionMetrics) { admissions = append(admissions, m) }
	var active *request
	var latest surface.Vec3
	for tick := 0; tick < 24; tick++ {
		latest = surface.Vec3{X: 20, Z: 10 + float64(tick)/10}
		before := e.position
		if err := s.RetargetNavigationDestination(1, latest); err != nil {
			t.Fatal(err)
		}
		navigationTestTick(s) // Each simulated tick advances 50 ms of actual motion.
		time.Sleep(time.Millisecond)
		if active == nil {
			select {
			case r := <-requests:
				active = &r
			default:
			}
		}
		// Deliberately take four simulation ticks per path: cancelling work on
		// each new 20 Hz goal would prevent every replacement from completing.
		if tick%4 == 3 && active != nil {
			close(active.release)
			active = nil
			time.Sleep(time.Millisecond)
		}
		state, _ := s.NavigationAgentState(1)
		stats := s.NavigationStats().Paths
		if navDistance(before, e.position) < .09 || state.Destination != latest || state.Mode != NavigationFollowing || stats.Running > 1 || stats.Queued > 1 {
			t.Fatalf("evolving goals stopped motion or expanded work: before=%+v after=%+v state=%+v stats=%+v", before, e.position, state, stats)
		}
	}
	if len(admissions) < 3 || e.position.Z < .5 {
		t.Fatalf("changing goals starved turning: admissions=%d position=%+v", len(admissions), e.position)
	}
	if active != nil {
		close(active.release)
		active = nil
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case r := <-requests:
			close(r.release)
		default:
		}
		navigationTestTick(s)
		state, _ := s.NavigationAgentState(1)
		if !state.RetargetPending && state.Mode == NavigationFollowing {
			if admissions[len(admissions)-1].Destination != latest {
				t.Fatal("final admission reported an interim goal")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("latest goal did not admit after evolving input stopped")
}

func TestNavigationRetargetCoalescesBeforeFirstAdmission(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	b := &navigationTestBackend{revision: 1, repair: retargetTestRepair}
	b.path = func(ctx context.Context, from, to surface.Vec3) surface.PathResult {
		if to.X == 10 {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return retargetTestPath(from, to)
	}
	s, e := navigationTestServer(t, b)
	e.position.Y = .2 // First admission still needs the ordinary bounded projection.
	_ = s.RetargetNavigationDestination(1, surface.Vec3{X: 10})
	navigationTestTick(s)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first path did not start")
	}
	for i := 0; i < 8; i++ {
		_ = s.RetargetNavigationDestination(1, surface.Vec3{X: 20 + float64(i)})
		navigationTestTick(s)
	}
	if s.NavigationStats().Paths.Accepted != 1 {
		t.Fatal("changing initial goals restarted the in-flight search")
	}
	once.Do(func() { close(release) })
	awaitNavigation(t, s, func() bool {
		state, _ := s.NavigationAgentState(1)
		return state.Mode == NavigationFollowing && !state.RetargetPending && state.Destination.X == 27
	})
	if e.position.Y != 0 || e.position.X <= 0 || s.navigation.agents[1].corners[len(s.navigation.agents[1].corners)-1].Position.X != 27 {
		t.Fatalf("initial coalescing lost projection or latest route: %+v", e.position)
	}
}

func TestNavigationRetargetFailureStopsAndReleasesBothViews(t *testing.T) {
	for _, failure := range []string{"unreachable", "repair", "support", "partial"} {
		t.Run(failure, func(t *testing.T) {
			b := &navigationTestBackend{revision: 1, repair: retargetTestRepair}
			s, _ := navigationTestServer(t, b)
			_ = s.SetNavigationDestination(1, surface.Vec3{X: 10})
			awaitNavigation(t, s, func() bool { return s.navigation.agents[1].state.Mode == NavigationFollowing })
			b.mu.Lock()
			switch failure {
			case "unreachable":
				b.path = func(context.Context, surface.Vec3, surface.Vec3) surface.PathResult {
					return surface.PathResult{Status: surface.Status{Code: surface.Unreachable}}
				}
			case "repair":
				b.repair = func(context.Context, surface.Route, surface.Location) surface.PathResult {
					return surface.PathResult{Status: surface.Status{Code: surface.Failed, Err: errors.New("repair failed")}}
				}
			case "support":
				b.projectOffset.Y = .1
			case "partial":
				b.repair = func(ctx context.Context, route surface.Route, location surface.Location) surface.PathResult {
					r := retargetTestRepair(ctx, route, location)
					r.Code = surface.Partial
					return r
				}
			}
			b.mu.Unlock()
			_ = s.RetargetNavigationDestination(1, surface.Vec3{X: 20})
			navigationTestTick(s)
			awaitRetargetCompletion(t, s)
			a := s.navigation.agents[1]
			if a.retargeting || a.view != nil || a.state.Velocity != (surface.Vec3{}) || b.releases.Load() != 2 || a.state.Mode == NavigationFollowing {
				t.Fatalf("failed replacement retained movement or leases: %+v releases=%d", a.state, b.releases.Load())
			}
			if failure == "partial" && len(a.state.PartialPath) == 0 {
				t.Fatal("missing rejected partial diagnostic")
			}
		})
	}
}

func TestNavigationRetargetRetriesStaleRepairWithoutStopping(t *testing.T) {
	b := &navigationTestBackend{revision: 1, repair: retargetTestRepair}
	s, e := navigationTestServer(t, b)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 10})
	awaitNavigation(t, s, func() bool { return s.navigation.agents[1].state.Mode == NavigationFollowing })
	a := s.navigation.agents[1]
	oldView, speed := a.view, a.speed
	b.mu.Lock()
	b.repair = func(context.Context, surface.Route, surface.Location) surface.PathResult {
		return surface.PathResult{Status: surface.Status{Code: surface.Stale}}
	}
	b.mu.Unlock()
	_ = s.RetargetNavigationDestination(1, surface.Vec3{X: 20})
	navigationTestTick(s)
	awaitRetargetCompletion(t, s)
	if a.view != oldView || !a.retargeting || !a.needsPath || a.state.Mode != NavigationFollowing || a.speed != speed || a.state.Status.Code != surface.Complete {
		t.Fatalf("stale repair lost retained route: %+v", a.state)
	}
	b.mu.Lock()
	b.repair = retargetTestRepair
	b.mu.Unlock()
	before := e.position
	navigationTestTick(s)
	if e.position.X <= before.X {
		t.Fatal("retry stopped retained movement")
	}
	awaitRetargetCompletion(t, s)
	if a.retargeting || a.corners[len(a.corners)-1].Position.X != 20 {
		t.Fatal("fresh-origin retry failed")
	}
}
