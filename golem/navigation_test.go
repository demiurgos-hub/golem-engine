package golem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

type navigationTestEntity struct {
	id       int64
	position surface.Vec3
	removed  func()
}

func (e *navigationTestEntity) EntityID() int64  { return e.id }
func (e *navigationTestEntity) TypeName() string { return "navigation-test" }
func (e *navigationTestEntity) Position() (float32, float32) {
	return float32(e.position.X), float32(e.position.Z)
}
func (e *navigationTestEntity) Position3D() (float32, float32, float32) {
	return float32(e.position.X), float32(e.position.Y), float32(e.position.Z)
}
func (e *navigationTestEntity) SetPosition3D(x, y, z float32) {
	e.position = surface.Vec3{X: float64(x), Y: float64(y), Z: float64(z)}
}
func (e *navigationTestEntity) IsGlobal() bool               { return false }
func (e *navigationTestEntity) FlushUpdate() ([]byte, error) { return nil, nil }
func (e *navigationTestEntity) FullUpdate() ([]byte, error)  { return nil, nil }
func (e *navigationTestEntity) OnRemove() {
	if e.removed != nil {
		e.removed()
	}
}

type navigationTestBackend struct {
	mu            sync.Mutex
	revision      uint64
	height        float64
	pending       bool
	acquireErr    error
	project       func(surface.Vec3) bool
	projectOffset surface.Vec3
	path          func(context.Context, surface.Vec3, surface.Vec3) surface.PathResult
	repair        func(context.Context, surface.Route, surface.Location) surface.PathResult
	releases      atomic.Int64
}

func (b *navigationTestBackend) CurrentRevision(surface.ProfileID) uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.revision
}
func (b *navigationTestBackend) Acquire(ctx context.Context, _ surface.ProfileID) (surface.View, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.acquireErr != nil {
		return nil, b.acquireErr
	}
	return &navigationTestView{backend: b, revision: b.revision, height: b.height, path: b.path, project: b.project, projectOffset: b.projectOffset, repair: b.repair}, nil
}
func (b *navigationTestBackend) Pending(surface.ProfileID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.pending
}
func (b *navigationTestBackend) change(height float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.revision++
	b.height = height
}

type navigationTestView struct {
	backend       *navigationTestBackend
	revision      uint64
	height        float64
	project       func(surface.Vec3) bool
	projectOffset surface.Vec3
	path          func(context.Context, surface.Vec3, surface.Vec3) surface.PathResult
	repair        func(context.Context, surface.Route, surface.Location) surface.PathResult
	released      atomic.Bool
}

func (v *navigationTestView) Revision() uint64 { return v.revision }
func (v *navigationTestView) Release() {
	if v.released.CompareAndSwap(false, true) {
		v.backend.releases.Add(1)
	}
}
func (v *navigationTestView) NewQuery(surface.Limits) (surface.Query, error) {
	if v.repair != nil {
		return &navigationRepairQuery{navigationTestQuery: navigationTestQuery{view: v}}, nil
	}
	return &navigationTestQuery{view: v}, nil
}

type navigationRepairQuery struct{ navigationTestQuery }

func (q *navigationRepairQuery) RepairPath(ctx context.Context, r surface.Route, l surface.Location, _ surface.Filter) surface.PathResult {
	return q.view.repair(ctx, r, l)
}

type navigationTestLocation struct {
	p        surface.Vec3
	revision uint64
}

func (l navigationTestLocation) Position() surface.Vec3 { return l.p }
func (l navigationTestLocation) Revision() uint64       { return l.revision }

type navigationTestRoute struct{ corners []surface.Corner }

func (r navigationTestRoute) Corners() []surface.Corner {
	return append([]surface.Corner(nil), r.corners...)
}
func (r navigationTestRoute) Tiles() []surface.TileRevision {
	return []surface.TileRevision{{ID: "0/0/0", Revision: 1}}
}

type navigationTestQuery struct{ view *navigationTestView }

func (q *navigationTestQuery) Project(ctx context.Context, p surface.Vec3, o surface.ProjectionOptions, _ surface.Filter) surface.ProjectionResult {
	if q.view.project != nil && !q.view.project(p) {
		return surface.ProjectionResult{Status: surface.Status{Code: surface.NoSurface}}
	}
	if err := ctx.Err(); err != nil {
		return surface.ProjectionResult{Status: surface.Status{Code: surface.Cancelled, Err: err}}
	}
	up, down := o.Up, o.Down
	if up == 0 && down == 0 {
		up, down = o.Vertical, o.Vertical
	}
	if q.view.height > p.Y+up+1e-6 || q.view.height < p.Y-down-1e-6 {
		return surface.ProjectionResult{Status: surface.Status{Code: surface.NoSurface}}
	}
	p.Y = q.view.height
	p.X += q.view.projectOffset.X
	p.Y += q.view.projectOffset.Y
	p.Z += q.view.projectOffset.Z
	return surface.ProjectionResult{Status: surface.Status{Code: surface.Complete}, Location: navigationTestLocation{p, q.view.revision}}
}
func (q *navigationTestQuery) FindPath(ctx context.Context, a, b surface.Location, _ surface.Filter) surface.PathResult {
	if q.view.path != nil {
		return q.view.path(ctx, a.Position(), b.Position())
	}
	return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: a.Position()}, {Position: b.Position()}}}}
}
func (q *navigationTestQuery) Move(_ context.Context, _ surface.Location, p surface.Vec3, _ surface.Filter) surface.MoveResult {
	p.Y = q.view.height
	return surface.MoveResult{Status: surface.Status{Code: surface.Complete}, Location: navigationTestLocation{p, q.view.revision}}
}
func (q *navigationTestQuery) Height(context.Context, surface.Location) surface.HeightResult {
	return surface.HeightResult{Status: surface.Status{Code: surface.Complete}, Height: q.view.height}
}
func (q *navigationTestQuery) Raycast(_ context.Context, _ surface.Location, p surface.Vec3, _ surface.Filter) surface.RaycastResult {
	return surface.RaycastResult{Status: surface.Status{Code: surface.Complete}, Position: p}
}

func navigationTestServer(t *testing.T, b *navigationTestBackend) (*Server, *navigationTestEntity) {
	t.Helper()
	s := NewServer(ServerConfig{})
	if err := s.SetSurfaceNavigation(b, NavigationConfig{MovementBudget: time.Second}); err != nil {
		t.Fatal(err)
	}
	e := &navigationTestEntity{id: 1, position: surface.Vec3{Y: b.height}}
	if err := s.CreateEntity(e); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachNavigation(1, NavigationAgentConfig{Profile: "human", Speed: 2, Projection: surface.ProjectionOptions{Horizontal: .5, Up: .5, Down: .5}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.runCtx = ctx
	s.life = lifecycleRunning
	s.navigation.start(ctx)
	t.Cleanup(func() { cancel(); s.navigation.stop() })
	return s, e
}
func navigationTestTick(s *Server) {
	s.tick++
	s.navigation.prepare()
	s.navigation.advance(.05)
	s.navigation.reconcile()
}
func awaitNavigation(t *testing.T, s *Server, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		navigationTestTick(s)
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	state, _ := s.NavigationAgentState(1)
	t.Fatalf("navigation did not reach expected state: %+v", state)
}

func TestSurfaceNavigationMovesAndReleasesOnArrival(t *testing.T) {
	b := &navigationTestBackend{revision: 1, height: 3}
	s, e := navigationTestServer(t, b)
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 1, Y: 3}); err != nil {
		t.Fatal(err)
	}
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationArrived })
	if navDistance(e.position, surface.Vec3{X: 1, Y: 3}) > .01 {
		t.Fatalf("position=%+v", e.position)
	}
	if b.releases.Load() != 1 {
		t.Fatalf("released views=%d", b.releases.Load())
	}
}

func TestSurfaceNavigationRejectsCancelledDestinationResult(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	b := &navigationTestBackend{revision: 1}
	b.path = func(ctx context.Context, a, b surface.Vec3) surface.PathResult {
		if b.X == 10 {
			close(started)
			<-release
		}
		return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: a}, {Position: b}}}}
	}
	s, e := navigationTestServer(t, b)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 10})
	navigationTestTick(s)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first query did not start")
	}
	_ = s.SetNavigationDestination(1, surface.Vec3{X: -1})
	close(release)
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationArrived })
	if e.position.X >= 0 {
		t.Fatalf("followed old destination: %+v", e.position)
	}
	awaitNavigation(t, s, func() bool { return b.releases.Load() == 2 })
}

func TestSurfaceNavigationRemovedFloorDoesNotSnapToGround(t *testing.T) {
	b := &navigationTestBackend{revision: 1, height: .2}
	s, e := navigationTestServer(t, b)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 10, Y: .2})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationFollowing })
	before := e.position
	b.change(0)
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationUnsupported })
	if navDistance(before, e.position) > .001 {
		t.Fatalf("removed support teleported agent: %+v -> %+v", before, e.position)
	}
}

func TestSurfaceNavigationReadinessAndDetachLifecycle(t *testing.T) {
	b := &navigationTestBackend{revision: 1}
	s, e := navigationTestServer(t, b)
	_ = s.BeginNavigationRestore()
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 1})
	for i := 0; i < 4; i++ {
		navigationTestTick(s)
	}
	if e.position.X != 0 || s.NavigationReady().Ready {
		t.Fatal("restoration barrier did not block movement")
	}
	_ = s.CompleteNavigationRestore(errors.New("required object unavailable"))
	for i := 0; i < 4; i++ {
		navigationTestTick(s)
	}
	if e.position.X != 0 || s.NavigationReady().Err == nil {
		t.Fatal("failed restore was hidden")
	}
	_ = s.BeginNavigationRestore()
	_ = s.CompleteNavigationRestore(nil)
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationFollowing })
	e.removed = func() {
		if _, ok := s.NavigationAgentState(1); ok {
			t.Error("navigation still attached during OnRemove")
		}
	}
	s.DeleteEntity(1)
	if b.releases.Load() != 1 {
		t.Fatalf("detachment leaked view: %d", b.releases.Load())
	}
}

type navigationBlockingMotor struct{}

func (navigationBlockingMotor) MoveNavigation(m NavigationMotion) (surface.Vec3, error) {
	return m.From, nil
}
func TestSurfaceNavigationBodyStallQueuesRepair(t *testing.T) {
	b := &navigationTestBackend{revision: 1}
	s, e := navigationTestServer(t, b)
	s.navigation.agents[1].config.Motor = navigationBlockingMotor{}
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 1})
	awaitNavigation(t, s, func() bool { return s.NavigationStats().Paths.Accepted >= 2 })
	if e.position.X != 0 {
		t.Fatal("movement ignored body motor")
	}
}

type navigationTestTraversal struct {
	cancelled int
	request   NavigationTraversalRequest
	end       surface.Vec3
	begin     func()
	onCancel  func()
	beginErr  error
	complete  bool
}

func (a *navigationTestTraversal) BeginNavigationTraversal(r NavigationTraversalRequest) (NavigationTraversalAction, error) {
	a.request = r
	if a.begin != nil {
		a.begin()
	}
	return a, a.beginErr
}
func (a *navigationTestTraversal) AdvanceNavigationTraversal(float64) (NavigationTraversalUpdate, error) {
	return NavigationTraversalUpdate{Position: a.end, Complete: a.complete}, nil
}
func (a *navigationTestTraversal) CancelNavigationTraversal(string) {
	a.cancelled++
	if a.onCancel != nil {
		a.onCancel()
	}
}
func TestSurfaceNavigationInterruptedTraversalCancelsOnce(t *testing.T) {
	b := &navigationTestBackend{revision: 1}
	b.path = func(_ context.Context, a, b surface.Vec3) surface.PathResult {
		return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: a, Link: &surface.Link{ID: "bridge", From: a, To: b}}, {Position: b}}}}
	}
	s, _ := navigationTestServer(t, b)
	action := &navigationTestTraversal{}
	s.navigation.agents[1].config.Traversal = action
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 2})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationTraversing })
	if action.request.Link.ID != "bridge" {
		t.Fatal("link identity lost")
	}
	_ = s.StopNavigation(1)
	_ = s.StopNavigation(1)
	s.DeleteEntity(1)
	if action.cancelled != 1 {
		t.Fatalf("cancel calls=%d", action.cancelled)
	}
}

func TestNavigationCheckpointValidatesBeforeChangingIntent(t *testing.T) {
	b := &navigationTestBackend{revision: 1}
	s, _ := navigationTestServer(t, b)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 4})
	checkpoint, err := s.CaptureNavigationCheckpoint("checkpoint-7", "sources", 7, []byte(`{"revision":7}`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeNavigationCheckpoint(data)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.BeginNavigationRestore()
	restored.Agents = append(restored.Agents, NavigationIntent{EntityID: 99, Profile: "human", Destination: surface.Vec3{X: 1}})
	if err := s.RestoreNavigationIntents(restored, "sources"); err == nil {
		t.Fatal("missing required identity accepted")
	}
	state, _ := s.NavigationAgentState(1)
	if state.Destination.X != 4 {
		t.Fatal("failed restore partially changed intent")
	}
	if err := s.RestoreNavigationIntents(checkpoint, "wrong"); err == nil {
		t.Fatal("fingerprint mismatch accepted")
	}
	if _, err := DecodeNavigationCheckpoint(append(data, []byte(` {}`)...)); err == nil {
		t.Fatal("trailing data accepted")
	}
	if err := s.RestoreNavigationIntents(checkpoint, "sources"); err != nil {
		t.Fatal(err)
	}
}

type navigationTestBuild struct {
	committed, discarded *atomic.Int64
	err                  error
}

func (b navigationTestBuild) Commit() error {
	if b.err == nil {
		b.committed.Add(1)
	}
	return b.err
}
func (b navigationTestBuild) Discard() { b.discarded.Add(1) }
func TestNavigationBuildRejectsSupersededAndFailedResults(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	var committed, discarded atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	_, err := s.QueueNavigationBuild("region", 0, func(context.Context) (NavigationBuildResult, error) {
		close(started)
		<-release
		return navigationTestBuild{committed: &committed, discarded: &discarded}, nil
	})
	if err != nil {
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
	_, err = s.QueueNavigationBuild("region", 0, func(context.Context) (NavigationBuildResult, error) {
		return navigationTestBuild{committed: &committed, discarded: &discarded, err: errors.New("invalid replacement")}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationBuildStatus("region"); return !state.Pending })
	if committed.Load() != 0 || discarded.Load() != 2 {
		t.Fatalf("committed=%d discarded=%d", committed.Load(), discarded.Load())
	}
}

func TestNavigationQueueCoalescesAndAgesPriorities(t *testing.T) {
	p := newNavigationPool(1, 2, 1)
	p.ctx = context.Background()
	work := func(context.Context) (any, error) { return nil, nil }
	complete := func(any, error) {}
	if err := p.submit("old", -8, work, complete, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.submit("new", 8, work, complete, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.submit("new", 8, work, complete, nil); err != nil {
		t.Fatal("coalescing consumed capacity", err)
	}
	if !errors.Is(p.submit("extra", 0, work, complete, nil), ErrNavigationQueueFull) {
		t.Fatal("queue limit ignored")
	}
	for i := 0; i < 140; i++ {
		job := p.take()
		if job == nil {
			t.Fatal("missing job")
		}
		delete(p.running, job.id)
		job.cancel()
		if job.key == "old" {
			return
		}
		if err := p.submit("new", 8, work, complete, nil); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("low priority work starved")
}

func TestSurfaceNavigationAlignsInitialVoxelHeight(t *testing.T) {
	b := &navigationTestBackend{revision: 1, height: .2}
	s, e := navigationTestServer(t, b)
	e.position.Y = 0
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 1, Y: 0})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationArrived })
	if math.Abs(e.position.Y-.2) > .001 {
		t.Fatalf("initial surface alignment failed: %+v", e.position)
	}
}

func TestSurfaceNavigationUnavailableTargetRetriesOnNewRevision(t *testing.T) {
	b := &navigationTestBackend{revision: 1, project: func(p surface.Vec3) bool { return p.X < 1 }}
	s, _ := navigationTestServer(t, b)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 2})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationBlocked })
	state, _ := s.NavigationAgentState(1)
	if state.WorldRevision != 1 {
		t.Fatal("failed target did not retain observed revision")
	}
	b.mu.Lock()
	b.project = nil
	b.revision++
	b.mu.Unlock()
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationArrived })
}

func TestSurfaceNavigationPendingGeometryImmediatelyPausesProfile(t *testing.T) {
	b := &navigationTestBackend{revision: 1}
	s, e := navigationTestServer(t, b)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 10})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationFollowing })
	b.mu.Lock()
	b.pending = true
	b.mu.Unlock()
	before := e.position
	for i := 0; i < 5; i++ {
		navigationTestTick(s)
	}
	state, _ := s.NavigationAgentState(1)
	if state.Mode != NavigationPending || e.position != before || s.NavigationReady().Ready {
		t.Fatalf("pending geometry did not quarantine old route: %+v", state)
	}
	b.mu.Lock()
	b.pending = false
	b.revision++
	b.mu.Unlock()
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationFollowing })
}

type navigationProfileBackend struct{ *navigationTestBackend }

func (b navigationProfileBackend) CurrentRevision(id surface.ProfileID) uint64 {
	if id != "human" {
		return 0
	}
	return b.navigationTestBackend.CurrentRevision(id)
}
func TestSurfaceNavigationRequiredProfileBlocksMovement(t *testing.T) {
	b := &navigationTestBackend{revision: 1}
	s, e := navigationTestServer(t, b)
	s.navigation.backend = navigationProfileBackend{b}
	s.navigation.config.RequiredProfiles = []surface.ProfileID{"missing"}
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 1})
	for i := 0; i < 5; i++ {
		navigationTestTick(s)
	}
	if s.NavigationReady().Ready || s.NavigationStats().Paths.Accepted != 0 || e.position.X != 0 {
		t.Fatal("missing required profile did not enforce readiness")
	}
}

type navigationMotorFunc func(NavigationMotion) (surface.Vec3, error)

func (f navigationMotorFunc) MoveNavigation(m NavigationMotion) (surface.Vec3, error) { return f(m) }
func TestSurfaceNavigationMotorReentrancyAndNumericBounds(t *testing.T) {
	for _, mode := range []string{"stop", "overflow"} {
		t.Run(mode, func(t *testing.T) {
			s, e := navigationTestServer(t, &navigationTestBackend{revision: 1})
			s.navigation.agents[1].config.Motor = navigationMotorFunc(func(m NavigationMotion) (surface.Vec3, error) {
				if mode == "stop" {
					_ = s.StopNavigation(1)
					return surface.Vec3{X: 100}, nil
				}
				return surface.Vec3{X: math.MaxFloat64}, nil
			})
			_ = s.SetNavigationDestination(1, surface.Vec3{X: 1})
			awaitNavigation(t, s, func() bool {
				state, _ := s.NavigationAgentState(1)
				return state.Mode == NavigationIdle || state.Mode == NavigationFailed
			})
			if e.position.X != 0 || !e.position.Finite() {
				t.Fatalf("stale/overflow motor result wrote position: %+v", e.position)
			}
		})
	}
}

func TestSurfaceNavigationTraversalBeginReentrancy(t *testing.T) {
	b := &navigationTestBackend{revision: 1, path: func(_ context.Context, a, b surface.Vec3) surface.PathResult {
		return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: a, Link: &surface.Link{ID: "jump", From: a, To: b}}, {Position: b}}}}
	}}
	s, _ := navigationTestServer(t, b)
	action := &navigationTestTraversal{begin: func() { _ = s.StopNavigation(1) }}
	s.navigation.agents[1].config.Traversal = action
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 2})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationIdle })
	if action.cancelled != 1 {
		t.Fatalf("stale returned action cancellation=%d", action.cancelled)
	}
}

func FuzzNavigationCheckpoint(f *testing.F) {
	seed, _ := json.Marshal(NavigationCheckpoint{Version: 1, CheckpointID: "fixture", SourceFingerprint: "source", DesiredRevision: 1, DesiredWorld: []byte("opaque-world")})
	f.Add(seed)
	f.Add([]byte(`{"version":999}`))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		checkpoint, err := DecodeNavigationCheckpoint(data)
		if err != nil {
			return
		}
		encoded, err := json.Marshal(checkpoint)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeNavigationCheckpoint(encoded); err != nil {
			t.Fatalf("accepted checkpoint cannot roundtrip: %v", err)
		}
	})
}

func TestSurfaceNavigationDetachPreservesReentrantReplacement(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	old := s.navigation.agents[1]
	action := &navigationTestTraversal{onCancel: func() {
		s.DetachNavigation(1)
		if err := s.AttachNavigation(1, old.config); err != nil {
			t.Fatal(err)
		}
	}}
	old.traversal = &navigationTraversal{action: action}
	s.DetachNavigation(1)
	if current := s.navigation.agents[1]; current == nil || current == old || len(s.navigation.order) != 1 {
		t.Fatal("detach removed replacement attachment")
	}
	if action.cancelled != 1 {
		t.Fatal("old action was not cancelled exactly once")
	}
}

func TestSurfaceNavigationRequiredBarrierCleanupIsBudgeted(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	s.navigation.backend = navigationProfileBackend{s.navigation.backend.(*navigationTestBackend)}
	s.navigation.config.RequiredProfiles = []surface.ProfileID{"missing"}
	s.navigation.config.MovementAgentsPerTick = 1
	var cancelled int
	for id := int64(2); id <= 8; id++ {
		if err := s.CreateEntity(&navigationTestEntity{id: id}); err != nil {
			t.Fatal(err)
		}
		if err := s.AttachNavigation(id, s.navigation.agents[1].config); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range s.navigation.agents {
		a.traversal = &navigationTraversal{action: &navigationTestTraversal{onCancel: func() { cancelled++ }}}
	}
	navigationTestTick(s)
	if cancelled != 1 {
		t.Fatalf("barrier cleanup exceeded one-agent budget: %d", cancelled)
	}
	for i := 1; i < 8; i++ {
		navigationTestTick(s)
	}
	if cancelled != 8 {
		t.Fatalf("barrier cleanup starved agents: %d", cancelled)
	}
}

func TestSurfaceNavigationAcquireFailureRetriesAfterRevisionChange(t *testing.T) {
	b := &navigationTestBackend{revision: 1, acquireErr: errors.New("temporary snapshot failure")}
	s, _ := navigationTestServer(t, b)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 1})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationFailed })
	b.mu.Lock()
	b.acquireErr = nil
	b.revision++
	b.mu.Unlock()
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationArrived })
}

func TestSurfaceNavigationBeginErrorCancelsReturnedAction(t *testing.T) {
	b := &navigationTestBackend{revision: 1, path: func(_ context.Context, a, b surface.Vec3) surface.PathResult {
		return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: a, Link: &surface.Link{ID: "jump", From: a, To: b}}, {Position: b}}}}
	}}
	s, _ := navigationTestServer(t, b)
	action := &navigationTestTraversal{beginErr: errors.New("eligibility changed")}
	s.navigation.agents[1].config.Traversal = action
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 2})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationFailed })
	if action.cancelled != 1 {
		t.Fatalf("returned rejected action cancellation=%d", action.cancelled)
	}
}

func TestNavigationCheckpointEncodedEnvelopeLimit(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	if _, err := s.CaptureNavigationCheckpoint("checkpoint", "source", 1, make([]byte, 48<<20)); err == nil {
		t.Fatal("accepted payload whose base64 plus envelope exceeds decoder limit")
	}
	c, err := s.CaptureNavigationCheckpoint("checkpoint", "source", 1, []byte("world"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := EncodeNavigationCheckpoint(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeNavigationCheckpoint(data); err != nil {
		t.Fatal(err)
	}
}

func TestNavigationBuildHistoryIsBoundedAndGenerationsSurviveForget(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	s.navigation.config.BuildHistoryCapacity = 1
	var committed, discarded atomic.Int64
	work := func(context.Context) (NavigationBuildResult, error) {
		return navigationTestBuild{committed: &committed, discarded: &discarded}, nil
	}
	first, err := s.QueueNavigationBuild("first", 0, work)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ForgetNavigationBuild("first"); err == nil {
		t.Fatal("forgot pending build")
	}
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationBuildStatus("first"); return state.Committed })
	if _, err := s.QueueNavigationBuild("second", 0, work); !errors.Is(err, ErrNavigationQueueFull) {
		t.Fatal("unbounded history", err)
	}
	if err := s.ForgetNavigationBuild("first"); err != nil {
		t.Fatal(err)
	}
	second, err := s.QueueNavigationBuild("first", 0, work)
	if err != nil || second <= first {
		t.Fatal("reused stale generation", first, second, err)
	}
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationBuildStatus("first"); return state.Committed })
}

func TestSurfaceNavigationPartialCorridorFollowsPrefix(t *testing.T) {
	b := &navigationTestBackend{revision: 1, path: func(_ context.Context, a, b surface.Vec3) surface.PathResult {
		return surface.PathResult{Status: surface.Status{Code: surface.Partial, Reason: surface.Unreachable}, Route: navigationTestRoute{[]surface.Corner{{Position: a}, {Position: b}}}}
	}, repair: func(_ context.Context, r surface.Route, l surface.Location) surface.PathResult {
		corners := r.Corners()
		return surface.PathResult{Status: surface.Status{Code: surface.Partial, Reason: surface.Unreachable}, Route: navigationTestRoute{[]surface.Corner{{Position: l.Position()}, corners[len(corners)-1]}}}
	}}
	s, e := navigationTestServer(t, b)
	s.navigation.agents[1].config.AllowPartial = true
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 2})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationBlocked })
	state, _ := s.NavigationAgentState(1)
	if e.position.X < 1.99 || state.Status.Code != surface.Partial || s.NavigationStats().Paths.Accepted != 1 {
		t.Fatalf("partial corridor was not followed: position=%+v state=%+v paths=%d", e.position, state, s.NavigationStats().Paths.Accepted)
	}
}

func TestSurfaceNavigationMotorOffsetFollowsProjectedCorridor(t *testing.T) {
	for _, code := range []surface.Code{surface.Complete, surface.Partial} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			b := &navigationTestBackend{revision: 1, path: func(_ context.Context, a, b surface.Vec3) surface.PathResult {
				return surface.PathResult{Status: surface.Status{Code: code}, Route: navigationTestRoute{[]surface.Corner{{Position: a}, {Position: b}}}}
			}, repair: func(_ context.Context, r surface.Route, l surface.Location) surface.PathResult {
				corners := r.Corners()
				return surface.PathResult{Status: surface.Status{Code: code}, Route: navigationTestRoute{[]surface.Corner{{Position: l.Position()}, corners[len(corners)-1]}}}
			}}
			s, e := navigationTestServer(t, b)
			a := s.navigation.agents[1]
			a.config.AllowPartial = true
			a.config.Motor = navigationMotorFunc(func(m NavigationMotion) (surface.Vec3, error) {
				if navDistance(m.From, e.position) > 1e-6 {
					t.Fatalf("motor received surface instead of body: from=%+v body=%+v", m.From, e.position)
				}
				m.Target.Y -= .02
				return m.Target, nil
			})
			_ = s.SetNavigationDestination(1, surface.Vec3{X: 1})
			awaitNavigation(t, s, func() bool {
				state, _ := s.NavigationAgentState(1)
				return state.Mode == NavigationArrived || state.Mode == NavigationBlocked
			})
			state, _ := s.NavigationAgentState(1)
			if e.position.X < .99 || math.Abs(e.position.Y+.02) > 1e-6 || state.Status.Code != code || s.NavigationStats().Paths.Accepted != 1 {
				t.Fatalf("body offset blocked surface movement: body=%+v state=%+v paths=%d", e.position, state, s.NavigationStats().Paths.Accepted)
			}
		})
	}
}

func TestSurfaceNavigationOffsetMotorStillDetectsPhysicalStall(t *testing.T) {
	s, e := navigationTestServer(t, &navigationTestBackend{revision: 1})
	a := s.navigation.agents[1]
	a.config.Motor = navigationMotorFunc(func(m NavigationMotion) (surface.Vec3, error) {
		m.From.Y = m.Target.Y - .02
		return m.From, nil
	})
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 1})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationBlocked })
	state, _ := s.NavigationAgentState(1)
	if e.position.X != 0 || state.Status.Code != surface.Unreachable || s.NavigationStats().Paths.Accepted != 4 {
		t.Fatalf("projected target hid physical stall: body=%+v state=%+v paths=%d", e.position, state, s.NavigationStats().Paths.Accepted)
	}
}

func TestSurfaceNavigationSupportToleranceAndNoLateralSnap(t *testing.T) {
	t.Run("configured height error", func(t *testing.T) {
		s, e := navigationTestServer(t, &navigationTestBackend{revision: 1})
		a := s.navigation.agents[1]
		a.config.SupportTolerance = .15
		a.config.Motor = navigationMotorFunc(func(m NavigationMotion) (surface.Vec3, error) { m.Target.Y -= .12; return m.Target, nil })
		_ = s.SetNavigationDestination(1, surface.Vec3{X: 1})
		awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationArrived })
		if math.Abs(e.position.Y+.12) > 1e-6 {
			t.Fatal("navigation replaced physical height", e.position)
		}
	})
	t.Run("retained support is a distance bound", func(t *testing.T) {
		b := &navigationTestBackend{revision: 1}
		s, e := navigationTestServer(t, b)
		_ = s.SetNavigationDestination(1, surface.Vec3{X: 1})
		awaitNavigation(t, s, func() bool { return e.position.X > .1 })
		before := e.position
		b.mu.Lock()
		b.projectOffset = surface.Vec3{X: .04, Y: .04}
		b.revision++
		b.mu.Unlock()
		awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationUnsupported })
		if navDistance(before, e.position) != 0 {
			t.Fatal("unsupported body snapped to adjacent geometry", before, e.position)
		}
	})
	t.Run("invalid tolerance", func(t *testing.T) {
		for _, tolerance := range []float64{-1, math.NaN(), math.Inf(1), .6} {
			cfg := NavigationAgentConfig{Profile: "human", Speed: 1, Projection: surface.ProjectionOptions{Horizontal: .5, Vertical: .5}, SupportTolerance: tolerance}
			if validateNavigationAgent(&cfg) == nil {
				t.Fatal("invalid support tolerance accepted", tolerance)
			}
		}
	})
}

func TestSurfaceNavigationRepairedCornerStillBrakesForTurn(t *testing.T) {
	b := &navigationTestBackend{revision: 1, repair: func(_ context.Context, r surface.Route, l surface.Location) surface.PathResult {
		return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: l.Position()}, {Position: surface.Vec3{X: .1}}, {Position: surface.Vec3{X: .1, Z: 10}}}}}
	}}
	s, _ := navigationTestServer(t, b)
	a := s.navigation.agents[1]
	a.view, _ = b.Acquire(s.runCtx, "human")
	a.query, _ = a.view.NewQuery(surface.Limits{})
	a.location = navigationTestLocation{revision: 1}
	a.route = navigationTestRoute{[]surface.Corner{{Position: surface.Vec3{X: 10}}}}
	a.lastRepair = surface.Vec3{X: -1}
	a.state = NavigationState{Mode: NavigationFollowing, HasDestination: true, WorldRevision: 1}
	a.config.Speed, a.config.Acceleration, a.speed = 6, 10, 5
	s.navigation.advanceAgent(a, .1)
	if a.speed > 4.01 {
		t.Fatalf("current-position corner masked turn braking: speed=%v", a.speed)
	}
}

func TestSurfaceNavigationTraversalLandingRequiresSupportedBody(t *testing.T) {
	b := &navigationTestBackend{revision: 1, path: func(_ context.Context, a, b surface.Vec3) surface.PathResult {
		return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: a, Link: &surface.Link{ID: "jump", From: a, To: b}}, {Position: b}}}}
	}}
	s, _ := navigationTestServer(t, b)
	action := &navigationTestTraversal{end: surface.Vec3{X: 2, Y: .08}, complete: true}
	s.navigation.agents[1].config.Traversal = action
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 2})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationUnsupported })
	if action.cancelled != 1 {
		t.Fatal("unsupported landing completed without cancelling action")
	}
}

func TestSurfaceNavigationTraversalLandingUsesConfiguredSupportTolerance(t *testing.T) {
	b := &navigationTestBackend{revision: 1, path: func(_ context.Context, a, b surface.Vec3) surface.PathResult {
		return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: a, Link: &surface.Link{ID: "jump", From: a, To: b}}, {Position: b}}}}
	}}
	s, e := navigationTestServer(t, b)
	action := &navigationTestTraversal{end: surface.Vec3{X: 2, Y: .15}, complete: true}
	s.navigation.agents[1].config.Traversal = action
	s.navigation.agents[1].config.SupportTolerance = .2
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 2})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationArrived })
	if action.cancelled != 0 || math.Abs(e.position.Y-.15) > 1e-6 {
		t.Fatalf("supported landing failed or replaced physical height: cancels=%d body=%+v", action.cancelled, e.position)
	}
}

func TestSurfaceNavigationMotorGeometryChangeRejectsPosition(t *testing.T) {
	b := &navigationTestBackend{revision: 1}
	s, e := navigationTestServer(t, b)
	s.navigation.agents[1].config.Motor = navigationMotorFunc(func(m NavigationMotion) (surface.Vec3, error) {
		b.mu.Lock()
		b.pending = true
		b.mu.Unlock()
		return surface.Vec3{X: 100}, nil
	})
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 2})
	awaitNavigation(t, s, func() bool { b.mu.Lock(); defer b.mu.Unlock(); return b.pending })
	if e.position.X != 0 {
		t.Fatal("stale geometry callback moved entity")
	}
}

func TestSurfaceNavigationDeleteRejectsReentrantAttachment(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	a := s.navigation.agents[1]
	a.traversal = &navigationTraversal{action: &navigationTestTraversal{onCancel: func() {
		s.DeleteEntity(1)
		if err := s.AttachNavigation(1, a.config); err == nil {
			t.Fatal("attached entity being deleted")
		}
	}}}
	s.DeleteEntity(1)
	if _, ok := s.Get(1); ok {
		t.Fatal("entity was not deleted")
	}
	if _, ok := s.NavigationAgentState(1); ok {
		t.Fatal("deleted entity retained navigation")
	}
	if len(s.navigation.deleting) != 0 {
		t.Fatal("deletion guard leaked")
	}
}

func TestSurfaceNavigationRejectsPartialUnlessOptedIn(t *testing.T) {
	b := &navigationTestBackend{revision: 1, path: func(_ context.Context, a, b surface.Vec3) surface.PathResult {
		return surface.PathResult{Status: surface.Status{Code: surface.Partial, Reason: surface.Unreachable}, Route: navigationTestRoute{[]surface.Corner{{Position: a}, {Position: b}}}}
	}}
	s, e := navigationTestServer(t, b)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 2})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationBlocked })
	state, _ := s.NavigationAgentState(1)
	if state.Status.Code != surface.Partial || e.position.X != 0 || len(state.PartialPath) != 2 {
		t.Fatalf("partial path policy: %+v", state)
	}
	state.PartialPath[0].Position.X = 99
	stored, _ := s.NavigationAgentState(1)
	if stored.PartialPath[0].Position.X == 99 {
		t.Fatal("diagnostic prefix was not copied")
	}
}

func TestSurfaceNavigationPathDeadlineRejectsLateSuccess(t *testing.T) {
	b := &navigationTestBackend{revision: 1, path: func(ctx context.Context, a, b surface.Vec3) surface.PathResult {
		<-ctx.Done()
		return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: a}, {Position: b}}}}
	}}
	s, e := navigationTestServer(t, b)
	s.navigation.config.PathDeadline = 5 * time.Millisecond
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 2})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Status.Code == surface.Cancelled })
	state, _ := s.NavigationAgentState(1)
	if !errors.Is(state.Status.Err, context.DeadlineExceeded) || e.position.X != 0 {
		t.Fatalf("late path success escaped deadline: %+v", state)
	}
}

func TestSurfaceNavigationTraversalTimeoutCancelsOnce(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	a := s.navigation.agents[1]
	a.config.TraversalTimeout = time.Second
	action := &navigationTestTraversal{end: surface.Vec3{X: 99}}
	a.traversal = &navigationTraversal{action: action}
	a.state.Mode = NavigationTraversing
	_, writer, _ := s.navigation.position(1)
	s.navigation.advanceTraversal(a, 1, writer)
	state, _ := s.NavigationAgentState(1)
	if action.cancelled != 1 || state.Status.Code != surface.Cancelled || !errors.Is(state.Status.Err, context.DeadlineExceeded) {
		t.Fatalf("traversal timeout: %+v cancellations=%d", state, action.cancelled)
	}
}

func TestSurfaceNavigationStallRetriesAreBounded(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	a := s.navigation.agents[1]
	a.config.Motor = navigationMotorFunc(func(m NavigationMotion) (surface.Vec3, error) { return m.From, nil })
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 2})
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationBlocked })
	state, _ := s.NavigationAgentState(1)
	if state.Status.Code != surface.Unreachable || s.NavigationStats().Paths.Accepted != 4 {
		t.Fatalf("stall retries were not bounded: %+v count=%d", state, s.NavigationStats().Paths.Accepted)
	}
}

type navigationRetryError struct {
	cause    error
	classify func()
}

func (e navigationRetryError) Error() string { return e.cause.Error() }
func (e navigationRetryError) Unwrap() error { return e.cause }
func (e navigationRetryError) RetryNavigationBuild() bool {
	if e.classify != nil {
		e.classify()
	}
	return true
}

func navigationBuildClock(s *Server) *time.Time {
	now := time.Unix(100, 0)
	s.navigation.buildClock = func() time.Time { return now }
	s.navigation.config.BuildDrainBudget = time.Second
	return &now
}

func TestNavigationBuildDebounceCapsContinuousCoalescingAndCapacity(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	now := navigationBuildClock(s)
	start := *now
	s.navigation.config.BuildQueueCapacity = 1
	var version, committed, discarded atomic.Int64
	queue := func(v int64) {
		t.Helper()
		_, err := s.QueueNavigationBuild("region", 0, func(context.Context) (NavigationBuildResult, error) {
			version.Store(v)
			return navigationTestBuild{committed: &committed, discarded: &discarded}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	queue(0)
	if _, err := s.QueueNavigationBuild("other", 0, func(context.Context) (NavigationBuildResult, error) { return nil, nil }); !errors.Is(err, ErrNavigationQueueFull) {
		t.Fatal("delayed requests bypassed queue capacity", err)
	}
	for i := int64(1); i <= 9; i++ {
		*now = start.Add(time.Duration(i) * 25 * time.Millisecond)
		queue(i)
		s.navigation.prepare()
		if s.NavigationStats().Builds.Accepted != 0 {
			t.Fatal("debounce dispatched early")
		}
	}
	if s.NavigationStats().Builds.Delayed != 1 {
		t.Fatal("coalescing leaked delayed capacity")
	}
	*now = start.Add(250 * time.Millisecond)
	queue(10)
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationBuildStatus("region"); return state.Committed })
	state, _ := s.NavigationBuildStatus("region")
	if version.Load() != 10 || committed.Load() != 1 || discarded.Load() != 0 || state.Attempts != 1 || !state.QueuedAt.Equal(start) || !state.CompletedAt.Equal(*now) || s.NavigationStats().Builds.Delayed != 0 {
		t.Fatalf("coalesced result/state: %+v version=%d", state, version.Load())
	}
}

func TestNavigationBuildRetryBackoffAndExhaustion(t *testing.T) {
	for _, commitFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(commitFailure), func(t *testing.T) {
			s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
			now := navigationBuildClock(s)
			var calls, committed, discarded atomic.Int64
			failure := navigationRetryError{cause: errors.New("temporary capacity")}
			_, err := s.QueueNavigationBuild("region", 0, func(context.Context) (NavigationBuildResult, error) {
				calls.Add(1)
				if commitFailure {
					return navigationTestBuild{committed: &committed, discarded: &discarded, err: failure}, nil
				}
				return nil, failure
			})
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 1; attempt <= 4; attempt++ {
				state, _ := s.NavigationBuildStatus("region")
				*now = state.NextAttempt
				awaitNavigation(t, s, func() bool {
					state, _ := s.NavigationBuildStatus("region")
					return state.Attempts == attempt && state.Err != nil
				})
				state, _ = s.NavigationBuildStatus("region")
				if attempt <= 3 {
					want := time.Duration(1<<(attempt-1)) * 50 * time.Millisecond
					if !state.Pending || state.NextAttempt.Sub(*now) != want {
						t.Fatalf("retry %d state=%+v", attempt, state)
					}
				} else if state.Pending || state.Committed || state.CompletedAt.IsZero() {
					t.Fatalf("retry exhaustion %+v", state)
				}
			}
			if calls.Load() != 4 || committed.Load() != 0 || len(s.navigation.buildRequests) != 0 || s.NavigationStats().Builds.Delayed != 0 {
				t.Fatal("retry resources/counts incorrect")
			}
			if commitFailure && discarded.Load() != 4 {
				t.Fatal("failed candidates not discarded")
			}
		})
	}
}

func TestNavigationBuildCancelledRetryNeverRunsObsoleteIntent(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	now := navigationBuildClock(s)
	var calls, committed, discarded atomic.Int64
	_, _ = s.QueueNavigationBuild("region", 0, func(context.Context) (NavigationBuildResult, error) {
		calls.Add(1)
		return nil, navigationRetryError{cause: errors.New("busy")}
	})
	state, _ := s.NavigationBuildStatus("region")
	*now = state.NextAttempt
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationBuildStatus("region"); return state.Pending && state.Err != nil })
	s.CancelNavigationBuild("region")
	*now = now.Add(time.Hour)
	s.navigation.prepare()
	if calls.Load() != 1 || s.NavigationStats().Builds.Delayed != 0 {
		t.Fatal("cancelled retry executed")
	}
	_, err := s.QueueNavigationBuild("region", 0, func(context.Context) (NavigationBuildResult, error) {
		return navigationTestBuild{committed: &committed, discarded: &discarded}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	state, _ = s.NavigationBuildStatus("region")
	*now = state.NextAttempt
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationBuildStatus("region"); return state.Committed })
	if calls.Load() != 1 || committed.Load() != 1 {
		t.Fatal("replacement did not supersede cancelled retry")
	}
}

func TestNavigationBuildTerminalErrorsAndClassifierSupersession(t *testing.T) {
	for _, cause := range []error{errors.New("deterministic"), context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
			now := navigationBuildClock(s)
			err := cause
			if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
				err = navigationRetryError{cause: cause}
			}
			_, _ = s.QueueNavigationBuild("region", 0, func(context.Context) (NavigationBuildResult, error) { return nil, err })
			state, _ := s.NavigationBuildStatus("region")
			*now = state.NextAttempt
			awaitNavigation(t, s, func() bool { state, _ := s.NavigationBuildStatus("region"); return !state.Pending })
			state, _ = s.NavigationBuildStatus("region")
			if state.Attempts != 1 || !errors.Is(state.Err, cause) {
				t.Fatalf("terminal error retried %+v", state)
			}
		})
	}
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	now := navigationBuildClock(s)
	var committed, discarded atomic.Int64
	var replacement uint64
	_, _ = s.QueueNavigationBuild("region", 0, func(context.Context) (NavigationBuildResult, error) {
		return nil, navigationRetryError{cause: errors.New("busy"), classify: func() {
			replacement, _ = s.QueueNavigationBuild("region", 0, func(context.Context) (NavigationBuildResult, error) {
				return navigationTestBuild{committed: &committed, discarded: &discarded}, nil
			})
		}}
	})
	state, _ := s.NavigationBuildStatus("region")
	*now = state.NextAttempt
	awaitNavigation(t, s, func() bool { return replacement != 0 })
	state, _ = s.NavigationBuildStatus("region")
	if state.Generation != replacement || !state.Pending || state.Attempts != 0 {
		t.Fatalf("old classifier overwrote replacement %+v", state)
	}
	*now = state.NextAttempt
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationBuildStatus("region"); return state.Committed })
}

func TestNavigationBuildDispatchBudgetAndShutdownDiscardDelayed(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	now := navigationBuildClock(s)
	s.navigation.config.BuildDispatchesPerTick = 1
	var committed, discarded atomic.Int64
	for _, key := range []string{"first", "second", "third"} {
		_, err := s.QueueNavigationBuild(key, 0, func(context.Context) (NavigationBuildResult, error) {
			return navigationTestBuild{committed: &committed, discarded: &discarded}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	*now = now.Add(50 * time.Millisecond)
	s.navigation.prepare()
	first, _ := s.NavigationBuildStatus("first")
	second, _ := s.NavigationBuildStatus("second")
	if first.Attempts != 1 || second.Attempts != 0 || s.NavigationStats().Builds.Delayed != 2 {
		t.Fatal("dispatch budget or admission order violated")
	}
	s.navigation.stopBuildRequests()
	if len(s.navigation.buildRequests) != 0 || s.NavigationStats().Builds.Delayed != 0 {
		t.Fatal("shutdown retained delayed closures")
	}
	for _, key := range []string{"first", "second", "third"} {
		state, _ := s.NavigationBuildStatus(key)
		if state.Pending || !errors.Is(state.Err, context.Canceled) {
			t.Fatalf("shutdown state %+v", state)
		}
	}
}

type navigationCallbackBuild struct {
	commit  func() error
	discard func()
}

func (b navigationCallbackBuild) Commit() error { return b.commit() }
func (b navigationCallbackBuild) Discard() {
	if b.discard != nil {
		b.discard()
	}
}

func TestNavigationBuildResultCallbacksPreserveReplacement(t *testing.T) {
	for _, duringDiscard := range []bool{false, true} {
		t.Run(fmt.Sprint(duringDiscard), func(t *testing.T) {
			s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
			now := navigationBuildClock(s)
			var committed, discarded atomic.Int64
			var replacement uint64
			replace := func() {
				replacement, _ = s.QueueNavigationBuild("region", 0, func(context.Context) (NavigationBuildResult, error) {
					return navigationTestBuild{committed: &committed, discarded: &discarded}, nil
				})
			}
			_, _ = s.QueueNavigationBuild("region", 0, func(context.Context) (NavigationBuildResult, error) {
				return navigationCallbackBuild{commit: func() error {
					if duringDiscard {
						return errors.New("deterministic")
					}
					replace()
					return nil
				}, discard: func() { replace() }}, nil
			})
			state, _ := s.NavigationBuildStatus("region")
			*now = state.NextAttempt
			awaitNavigation(t, s, func() bool { return replacement != 0 })
			state, _ = s.NavigationBuildStatus("region")
			if state.Generation != replacement || !state.Pending || s.NavigationStats().Builds.Delayed != 1 {
				t.Fatalf("result callback lost replacement %+v", state)
			}
			*now = state.NextAttempt
			awaitNavigation(t, s, func() bool { state, _ := s.NavigationBuildStatus("region"); return state.Committed })
			if committed.Load() != 1 || len(s.navigation.buildRequests) != 0 {
				t.Fatal("replacement did not publish")
			}
		})
	}
}
