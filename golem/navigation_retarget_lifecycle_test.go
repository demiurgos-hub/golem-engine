package golem

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

func retargetLifecyclePath(from, to surface.Vec3) surface.PathResult {
	return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: from}, {Position: to}}}}
}

func retargetLifecycleServer(t *testing.T, firstGoal float64, configure ...func(*navigationTestBackend)) (*Server, *navigationTestEntity, func()) {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	var blocked atomic.Bool
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	b := &navigationTestBackend{revision: 1}
	b.path = func(_ context.Context, from, to surface.Vec3) surface.PathResult {
		if to.X == 10 && blocked.CompareAndSwap(false, true) {
			close(started)
			<-release // Exercise completion fencing even when a backend finishes late.
		}
		return retargetLifecyclePath(from, to)
	}
	b.repair = func(_ context.Context, route surface.Route, at surface.Location) surface.PathResult {
		corners := route.Corners()
		return retargetLifecyclePath(at.Position(), corners[len(corners)-1].Position)
	}
	for _, apply := range configure {
		apply(b)
	}
	s, e := navigationTestServer(t, b)
	t.Cleanup(unblock)
	if err := s.SetNavigationDestination(1, surface.Vec3{X: firstGoal}); err != nil {
		t.Fatal(err)
	}
	awaitNavigation(t, s, func() bool {
		state, _ := s.NavigationAgentState(1)
		return state.Mode == NavigationFollowing && e.position.X > 0
	})
	if err := s.RetargetNavigationDestination(1, surface.Vec3{X: 10}); err != nil {
		t.Fatal(err)
	}
	awaitNavigation(t, s, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	})
	return s, e, unblock
}

func TestNavigationRetargetDoesNotBeginOldDirectedLink(t *testing.T) {
	s, e, release := retargetLifecycleServer(t, 5, func(b *navigationTestBackend) {
		originalPath := b.path
		b.path = func(ctx context.Context, from, to surface.Vec3) surface.PathResult {
			if to.X != 5 {
				return originalPath(ctx, from, to)
			}
			start, end := surface.Vec3{X: 1}, surface.Vec3{X: 2}
			return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{
				{Position: from}, {Position: start, Link: &surface.Link{ID: "old-link", From: start, To: end}}, {Position: end}, {Position: to},
			}}}
		}
		originalRepair := b.repair
		b.repair = func(ctx context.Context, route surface.Route, at surface.Location) surface.PathResult {
			corners := route.Corners()
			if corners[len(corners)-1].Position.X != 5 {
				return originalRepair(ctx, route, at)
			}
			remaining := []surface.Corner{{Position: at.Position()}}
			for _, corner := range corners {
				if corner.Link != nil || corner.Position.X > at.Position().X {
					remaining = append(remaining, corner)
				}
			}
			return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{remaining}}
		}
	})
	begun := 0
	s.navigation.agents[1].config.Traversal = &navigationTestTraversal{begin: func() { begun++ }}
	awaitNavigation(t, s, func() bool {
		state, _ := s.NavigationAgentState(1)
		return state.Mode == NavigationPending && e.position.X >= .99
	})
	state, _ := s.NavigationAgentState(1)
	if begun != 0 || !state.RetargetPending || e.position.X > 1.01 {
		t.Fatalf("obsolete directed link began while retargeting: begins=%d position=%+v state=%+v", begun, e.position, state)
	}
	release()
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationArrived })
	if begun != 0 || navDistance(e.position, surface.Vec3{X: 10}) > .01 {
		t.Fatalf("replacement traversed an obsolete link: begins=%d position=%+v", begun, e.position)
	}
}

func TestNavigationRetargetPhysicalRejectionFencesReplacement(t *testing.T) {
	for _, supported := range []bool{true, false} {
		name := "supported"
		if !supported {
			name = "unsupported"
		}
		t.Run(name, func(t *testing.T) {
			s, e, release := retargetLifecycleServer(t, 5)
			a := s.navigation.agents[1]
			a.config.StagedPhysics = true
			generation := a.state.Generation
			before := e.position
			actual := surface.Vec3{X: before.X + .02, Y: before.Y, Z: before.Z + .02}
			wantMode := NavigationBlocked
			if !supported {
				actual.Y = .2
				wantMode = NavigationUnsupported
			}
			frame := stagedFrame(t, s, .05)
			if err := s.CommitNavigationFrame(frame, []NavigationMotionResult{{EntityID: 1, Position: actual, Path: []surface.Vec3{before, actual}, RejectionReason: "blocked physical motion"}}); err != nil {
				t.Fatal(err)
			}
			state, _ := s.NavigationAgentState(1)
			if state.RetargetPending || state.Generation <= generation || state.Mode != wantMode || e.position != navigationFramePosition(actual) {
				t.Fatalf("physical rejection lost the actual pose or replacement fence: position=%+v state=%+v", e.position, state)
			}
			if supported && (a.location == nil || navDistance(a.location.Position(), actual) > .001) {
				t.Fatalf("physical rejection did not reconcile actual support: %+v", a.location)
			}
			generation = state.Generation
			release()
			deadline := time.Now().Add(time.Second)
			drained := false
			for time.Now().Before(deadline) {
				s.navigation.prepare()
				stats := s.NavigationStats().Paths
				if stats.Running == 0 && stats.Queued == 0 && stats.Completions == 0 {
					drained = true
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !drained {
				t.Fatal("cancelled physical replacement did not finish")
			}
			state, _ = s.NavigationAgentState(1)
			if state.Generation != generation || state.Mode != wantMode || state.RetargetPending || e.position != navigationFramePosition(actual) {
				t.Fatalf("late replacement revived rejected physical motion: position=%+v state=%+v", e.position, state)
			}
		})
	}
}

func TestNavigationRetargetLifecycleFencesLateReplacement(t *testing.T) {
	for _, operation := range []string{"stop", "set", "reproject", "detach_reattach"} {
		t.Run(operation, func(t *testing.T) {
			s, e, release := retargetLifecycleServer(t, 5)
			before, _ := s.NavigationAgentState(1)
			if !before.RetargetPending {
				t.Fatal("replacement was not pending")
			}
			var err error
			switch operation {
			case "stop":
				err = s.StopNavigation(1)
			case "set":
				err = s.SetNavigationDestination(1, surface.Vec3{X: -4})
			case "reproject":
				e.position = surface.Vec3{X: -3}
				err = s.ReprojectNavigation(1)
			case "detach_reattach":
				s.DetachNavigation(1)
				err = s.AttachNavigation(1, NavigationAgentConfig{Profile: "human", Speed: 2, Projection: surface.ProjectionOptions{Horizontal: .5, Up: .5, Down: .5}})
				if err == nil {
					err = s.SetNavigationDestination(1, surface.Vec3{X: -4})
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			state, _ := s.NavigationAgentState(1)
			if state.RetargetPending || operation != "detach_reattach" && state.Generation <= before.Generation {
				t.Fatalf("%s did not invalidate replacement: %+v", operation, state)
			}
			generation := state.Generation
			position := e.position
			release()
			awaitNavigation(t, s, func() bool {
				stats := s.NavigationStats().Paths
				return stats.Running == 0 && stats.Queued == 0 && stats.Completions == 0
			})
			state, _ = s.NavigationAgentState(1)
			if state.Generation != generation || state.RetargetPending {
				t.Fatalf("late result replaced %s intent: %+v", operation, state)
			}
			if operation == "stop" {
				if state.Mode != NavigationIdle || state.HasDestination || e.position != position {
					t.Fatalf("late replacement resumed a stopped entity: %+v at %+v", state, e.position)
				}
				return
			}
			want := surface.Vec3{X: -4}
			if operation == "reproject" {
				want.X = 10
			}
			awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationArrived })
			if navDistance(e.position, want) > .01 {
				t.Fatalf("%s ended at obsolete replacement: %+v, want %+v", operation, e.position, want)
			}
		})
	}
}

func TestNavigationRetargetOldEndpointWaitsForReplacement(t *testing.T) {
	s, e, release := retargetLifecycleServer(t, 1)
	awaitNavigation(t, s, func() bool {
		state, _ := s.NavigationAgentState(1)
		return e.position.X >= .99 && state.Mode == NavigationPending
	})
	state, _ := s.NavigationAgentState(1)
	if state.Mode != NavigationPending || !state.RetargetPending || !state.HasDestination || state.Destination.X != 10 || state.Velocity != (surface.Vec3{}) {
		t.Fatalf("old endpoint was reported as final arrival: %+v", state)
	}
	position := e.position
	for i := 0; i < 5; i++ {
		navigationTestTick(s)
	}
	if e.position != position {
		t.Fatalf("old endpoint wait drifted: %+v -> %+v", position, e.position)
	}
	release()
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationArrived })
	if navDistance(e.position, surface.Vec3{X: 10}) > .01 {
		t.Fatalf("replacement did not resume from old endpoint: %+v", e.position)
	}
}
