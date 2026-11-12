package golem

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

type regionalTestBackend struct {
	*navigationTestBackend
	mu                 sync.Mutex
	token              uint64
	dirty              bool
	changedSupport     bool
	rebaseStatus       *surface.Status
	transferEnabled    bool
	transfers, repairs int
}

func (b *regionalTestBackend) Safety(surface.ProfileID) surface.RegionalSafety {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.CurrentRevision("human")
	desired := r
	if b.dirty {
		desired++
	}
	return regionalTestSafety{revision: r, desired: desired, token: b.token, dirty: b.dirty}
}
func (b *regionalTestBackend) edit(dirty bool, publish bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.token++
	b.dirty = dirty
	if publish {
		b.navigationTestBackend.change(0)
	}
}
func (b *regionalTestBackend) Pending(surface.ProfileID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dirty
}
func (b *regionalTestBackend) Acquire(ctx context.Context, p surface.ProfileID) (surface.View, error) {
	v, err := b.navigationTestBackend.Acquire(ctx, p)
	if err != nil {
		return nil, err
	}
	return &regionalTestView{navigationTestView: v.(*navigationTestView), owner: b}, nil
}

type regionalTestView struct {
	*navigationTestView
	owner *regionalTestBackend
}

func (v *regionalTestView) NewQuery(surface.Limits) (surface.Query, error) {
	query := &regionalTestQuery{navigationTestQuery: navigationTestQuery{view: v.navigationTestView}, owner: v.owner}
	if v.owner.transferEnabled {
		return &regionalTransferQuery{query}, nil
	}
	return query, nil
}

type regionalTestQuery struct {
	navigationTestQuery
	owner *regionalTestBackend
}

func (q *regionalTestQuery) RebasePath(_ context.Context, r surface.Route, l surface.Location, p surface.Vec3, tolerance float64, _ surface.Filter) surface.RebaseResult {
	q.owner.mu.Lock()
	q.owner.repairs++
	changed := q.owner.changedSupport
	status := surface.Status{Code: surface.Complete}
	if q.owner.rebaseStatus != nil {
		status = *q.owner.rebaseStatus
	}
	q.owner.mu.Unlock()
	if changed || navDistance(l.Position(), p) > tolerance {
		return surface.RebaseResult{Status: surface.Status{Code: surface.Stale}}
	}
	var route surface.Route
	if r != nil {
		corners := r.Corners()
		corners[0].Position = p
		route = navigationTestRoute{corners}
	}
	return surface.RebaseResult{Status: status, Location: navigationTestLocation{p, q.view.revision}, Route: route}
}

type regionalTestSafety struct {
	revision, desired, token uint64
	dirty                    bool
}

func (s regionalTestSafety) CommittedRevision() uint64 { return s.revision }
func (s regionalTestSafety) DesiredRevision() uint64   { return s.desired }
func (s regionalTestSafety) Token() uint64             { return s.token }
func (s regionalTestSafety) SafeLocation(l surface.Location) bool {
	return l != nil && s.SafeSegment(l.Position(), l.Position())
}
func (s regionalTestSafety) SafeRoute(r surface.Route) bool {
	if r == nil {
		return false
	}
	c := r.Corners()
	for i := range c {
		if !s.SafeSegment(c[i].Position, c[i].Position) {
			return false
		}
		if i > 0 && !s.SafeSegment(c[i-1].Position, c[i].Position) {
			return false
		}
	}
	return true
}
func (s regionalTestSafety) SafeSegment(a, b surface.Vec3) bool {
	if !a.Finite() || !b.Finite() {
		return false
	}
	if !s.dirty {
		return true
	}
	// The fixture quarantine is the full-height column x=[0,2], z=[-2,2].
	lo, hi := 0.0, 1.0
	for _, v := range [][4]float64{{a.X, b.X, 0, 2}, {a.Z, b.Z, -2, 2}} {
		d := v[1] - v[0]
		if d == 0 {
			if v[0] < v[2] || v[0] > v[3] {
				return true
			}
			continue
		}
		x, y := (v[2]-v[0])/d, (v[3]-v[0])/d
		if x > y {
			x, y = y, x
		}
		if x > lo {
			lo = x
		}
		if y < hi {
			hi = y
		}
		if lo > hi {
			return true
		}
	}
	return false
}
func (s regionalTestSafety) SafeBounds(a, b surface.Vec3) bool {
	return !s.dirty || b.X < 0 || a.X > 2 || b.Z < -2 || a.Z > 2
}

func regionalServer(t *testing.T) (*Server, *regionalTestBackend) {
	t.Helper()
	base := &navigationTestBackend{revision: 1}
	s, _ := navigationTestServer(t, base)
	b := &regionalTestBackend{navigationTestBackend: base, token: 1}
	s.navigation.backend = b
	s.navigation.config.RequiredProfiles = []surface.ProfileID{"human"}
	return s, b
}

func TestNavigationUnsafeCompletedPathWaitsWithoutRepeatedSearch(t *testing.T) {
	s, b := regionalServer(t)
	_, writer, _ := s.navigation.position(1)
	writer.SetPosition3D(-5, 0, 0)
	admissions := 0
	s.navigation.config.ObservePathAdmission = func(NavigationPathAdmissionMetrics) { admissions++ }
	b.edit(true, false)
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 5}); err != nil {
		t.Fatal(err)
	}
	a := s.navigation.agents[1]
	awaitNavigation(t, s, func() bool { return a.quarantined && a.admissionPending })
	if a.route == nil || a.view == nil || a.needsPath || admissions != 0 {
		t.Fatalf("unsafe result did not retain its evidence: %+v", a.state)
	}
	for i := 0; i < 20; i++ {
		// Overlapping edits change the token but do not make the corridor safe.
		b.edit(true, false)
		navigationTestTick(s)
	}
	if got := s.NavigationStats().Paths.Accepted; got != 1 {
		t.Fatalf("unchanged unsafe intent repeated %d searches", got)
	}
	if admissions != 0 || a.state.Mode != NavigationPending {
		t.Fatal("quarantined result was prematurely admitted")
	}
	b.edit(false, true)
	navigationTestTick(s)
	if admissions != 1 || a.admissionPending || a.quarantined || a.state.Mode != NavigationFollowing || a.view.Revision() != 2 {
		t.Fatalf("safe rebased result was not admitted exactly once: admissions=%d state=%+v", admissions, a.state)
	}
	if got := s.NavigationStats().Paths.Accepted; got != 1 {
		t.Fatalf("publication unnecessarily searched again: %d", got)
	}
}

func TestNavigationSupersededQuarantinedResultNeverAdmits(t *testing.T) {
	s, b := regionalServer(t)
	_, writer, _ := s.navigation.position(1)
	writer.SetPosition3D(-5, 0, 0)
	var admissions []NavigationPathAdmissionMetrics
	s.navigation.config.ObservePathAdmission = func(m NavigationPathAdmissionMetrics) { admissions = append(admissions, m) }
	b.edit(true, false)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 5})
	a := s.navigation.agents[1]
	awaitNavigation(t, s, func() bool { return a.quarantined && a.admissionPending })
	old := a.view.(*regionalTestView)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: -10})
	generation := a.state.Generation
	awaitNavigation(t, s, func() bool { return len(admissions) > 0 })
	if !old.released.Load() || len(admissions) != 1 || admissions[0].Generation != generation || a.state.Destination.X != -10 {
		t.Fatalf("old intent escaped cancellation: %+v %+v", admissions, a.state)
	}
}

func TestNavigationQuarantinedResultRechecksExternalDisplacement(t *testing.T) {
	s, b := regionalServer(t)
	_, writer, _ := s.navigation.position(1)
	writer.SetPosition3D(-5, 0, 0)
	admissions := 0
	s.navigation.config.ObservePathAdmission = func(NavigationPathAdmissionMetrics) { admissions++ }
	b.edit(true, false)
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 5})
	a := s.navigation.agents[1]
	awaitNavigation(t, s, func() bool { return a.quarantined && a.admissionPending })
	old := a.view.(*regionalTestView)
	writer.SetPosition3D(-10, 0, 0)
	// Clearing quarantine without publication must still validate the body's
	// current search origin before an admission callback can observe Following.
	b.edit(false, false)
	navigationTestTick(s)
	if admissions != 0 || !old.released.Load() {
		t.Fatal("displaced result escaped origin validation")
	}
	awaitNavigation(t, s, func() bool { return admissions == 1 })
	if s.NavigationStats().Paths.Accepted != 2 || a.lastPosition.X >= -9 {
		t.Fatalf("replacement path did not start at the actual body: %+v", a.state)
	}
}

func TestNavigationPartialAsyncResultRebasesWithoutReportingComplete(t *testing.T) {
	s, b := regionalServer(t)
	partial := surface.Status{Code: surface.Partial, Reason: surface.Unreachable}
	b.rebaseStatus = &partial
	a := s.navigation.agents[1]
	a.config.AllowPartial = true
	started, release := make(chan struct{}), make(chan struct{})
	b.path = func(ctx context.Context, from, to surface.Vec3) surface.PathResult {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		to.X /= 2
		return surface.PathResult{Status: partial, Route: navigationTestRoute{[]surface.Corner{{Position: from}, {Position: to}}}}
	}
	var admitted NavigationPathAdmissionMetrics
	s.navigation.config.ObservePathAdmission = func(m NavigationPathAdmissionMetrics) { admitted = m }
	_ = s.SetNavigationDestination(1, surface.Vec3{X: 10})
	navigationTestTick(s)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("path did not start")
	}
	b.edit(false, true)
	close(release)
	awaitNavigation(t, s, func() bool { return a.state.Mode == NavigationFollowing })
	if a.view.Revision() != 2 || a.state.Status != partial || admitted.Status != partial || s.NavigationStats().Paths.Accepted != 1 {
		t.Fatalf("partial async route did not transfer intact: %+v admission=%+v", a.state, admitted)
	}
}
func seedRegionalAgent(t *testing.T, s *Server, id int64, from, to surface.Vec3) *navigationAgent {
	t.Helper()
	if id != 1 {
		if err := s.CreateEntity(&navigationTestEntity{id: id, position: from}); err != nil {
			t.Fatal(err)
		}
		if err := s.AttachNavigation(id, NavigationAgentConfig{Profile: "human", Speed: 2, Projection: surface.ProjectionOptions{Horizontal: 1, Up: 1, Down: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	_, writer, _ := s.navigation.position(id)
	writer.SetPosition3D(float32(from.X), float32(from.Y), float32(from.Z))
	a := s.navigation.agents[id]
	v, err := s.navigation.backend.Acquire(s.runCtx, "human")
	if err != nil {
		t.Fatal(err)
	}
	q, err := v.NewQuery(surface.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	a.view, a.query = v, q
	a.location = navigationTestLocation{from, v.Revision()}
	a.route = navigationTestRoute{[]surface.Corner{{Position: from}, {Position: to}}}
	a.corners, a.next = a.route.Corners(), 1
	a.lastPosition, a.lastRepair = from, from
	a.supportEstablished = true
	a.state = NavigationState{Mode: NavigationFollowing, Destination: to, HasDestination: true, WorldRevision: v.Revision()}
	return a
}

func TestNavigationRegionalIsolationAndReadiness(t *testing.T) {
	s, b := regionalServer(t)
	a := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5}, surface.Vec3{X: 5})
	seedRegionalAgent(t, s, 2, surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 5, Z: 20})
	b.edit(true, false)
	for i := 0; i < 5; i++ {
		navigationTestTick(s)
	}
	p, _, _ := s.navigation.position(1)
	far, _, _ := s.navigation.position(2)
	if p.X != -5 || far.X <= -5 {
		t.Fatalf("local=%v distant=%v", p, far)
	}
	if !a.quarantined || a.view == nil || !a.state.HasDestination {
		t.Fatal("quarantine lost support or intent")
	}
	r := s.NavigationReady()
	if !r.Ready || !r.Degraded || len(r.PendingProfiles) != 1 {
		t.Fatalf("readiness=%+v", r)
	}
	if s.NavigationStats().Paths.Accepted != 0 {
		t.Fatal("quarantined path was busy-resubmitted")
	}
	b.edit(false, true)
	navigationTestTick(s)
	p, _, _ = s.navigation.position(1)
	if p.X <= -5 || a.quarantined || a.view.Revision() != 2 {
		t.Fatalf("did not resume on publication: %+v", a.state)
	}
}

func TestNavigationRegionalQuarantineResumesAccelerationFromRest(t *testing.T) {
	s, b := regionalServer(t)
	a := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5}, surface.Vec3{X: 5})
	a.config.Acceleration, a.speed = 1, 2
	a.state.Velocity = surface.Vec3{X: 2}
	b.edit(true, false)
	s.navigation.advanceAgent(a, .05)
	if !a.quarantined || a.speed != 0 || a.state.Velocity != (surface.Vec3{}) {
		t.Fatalf("quarantine retained hidden motion: speed=%v state=%+v", a.speed, a.state)
	}
	b.edit(false, true)
	s.navigation.advanceAgent(a, .05)
	p, _, _ := s.navigation.position(1)
	if a.quarantined || a.speed <= 0 || a.speed > .051 || p.X <= -5 || p.X > -4.997 {
		t.Fatalf("resume bypassed acceleration from rest: position=%v speed=%v state=%+v", p, a.speed, a.state)
	}
}

type regionalMotor func(NavigationMotion) (surface.Vec3, error)

type countingRegionalSafety struct {
	surface.RegionalSafety
	checks *int
}

type countingSegmentSafety struct {
	surface.RegionalSafety
	checks *int
}

func (s countingSegmentSafety) SafeSegment(from, to surface.Vec3) bool {
	*s.checks++
	return s.RegionalSafety.SafeSegment(from, to)
}

func TestNavigationSegmentCacheRequiresSameCaptureAndEndpoints(t *testing.T) {
	s, b := regionalServer(t)
	a := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 20, Z: 20})
	from, to := surface.Vec3{X: -5}, surface.Vec3{X: 5}
	checks := 0
	safety := countingSegmentSafety{b.Safety("human"), &checks}
	for i := 0; i < 10; i++ {
		if !s.navigation.segmentSafe(a, safety, from, to) {
			t.Fatal("clear segment rejected")
		}
	}
	if checks != 1 {
		t.Fatalf("identical immutable segment checked %d times", checks)
	}
	b.edit(true, false)
	safety = countingSegmentSafety{b.Safety("human"), &checks}
	if s.navigation.segmentSafe(a, safety, from, to) || checks != 2 {
		t.Fatal("changed capture reused a previously clear crossing")
	}
	from.Z, to.Z = 20, 20
	if !s.navigation.segmentSafe(a, safety, from, to) {
		t.Fatal("distant segment rejected")
	}
	to.Z = -20
	if s.navigation.segmentSafe(a, safety, from, to) {
		t.Fatal("changed endpoint reused a safe segment")
	}
	position := surface.Vec3{X: -5}
	if !s.navigation.pointSafe(a, safety, position) || !s.navigation.pointSafe(a, safety, position) {
		t.Fatal("distant point rejected")
	}
	if s.navigation.pointSafe(a, safety, surface.Vec3{X: 1}) {
		t.Fatal("changed position reused a safe point")
	}
}

type countingSupportSafety struct {
	surface.RegionalSafety
	checks *int
}

func (s countingSupportSafety) SafeLocation(l surface.Location) bool {
	*s.checks++
	return s.RegionalSafety.SafeLocation(l)
}

func TestNavigationRegionalSupportSafetyCachesOnlyImmutableLocation(t *testing.T) {
	s, b := regionalServer(t)
	a := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 20, Z: 20})
	checks := 0
	safety := countingSupportSafety{b.Safety("human"), &checks}
	for i := 0; i < 20; i++ {
		if !s.navigation.regionSafe(a, safety, a.location.Position()) {
			t.Fatal("safe location rejected")
		}
	}
	if checks != 1 {
		t.Fatalf("unchanged immutable location checked %d times", checks)
	}
	// Moving assigns a distinct immutable location and must clear cached safety.
	s.navigation.advanceAgent(a, .05)
	if a.locationSafetyChecked {
		t.Fatal("movement retained previous location's safety")
	}
	if !s.navigation.regionSafe(a, safety, a.location.Position()) || checks != 2 {
		t.Fatalf("replacement location not checked: %d", checks)
	}
	b.edit(true, false)
	safety = countingSupportSafety{b.Safety("human"), &checks}
	if !s.navigation.regionSafe(a, safety, a.location.Position()) || checks != 3 {
		t.Fatalf("new safety capture not checked: %d", checks)
	}
	// Position checks remain fresh even while the retained support is cached.
	if s.navigation.regionSafe(a, safety, surface.Vec3{X: 1}) {
		t.Fatal("cached support allowed body inside quarantine")
	}
}

func (s countingRegionalSafety) SafeRoute(r surface.Route) bool {
	*s.checks++
	return s.RegionalSafety.SafeRoute(r)
}

func TestNavigationRegionalRouteSafetyCachedUntilCaptureOrRouteChanges(t *testing.T) {
	s, b := regionalServer(t)
	a := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 20, Z: 20})
	checks := 0
	safety := countingRegionalSafety{b.Safety("human"), &checks}
	for i := 0; i < 20; i++ {
		if !s.navigation.regionSafe(a, safety, a.location.Position()) {
			t.Fatal("unchanged route rejected")
		}
	}
	if checks != 1 {
		t.Fatalf("walked unchanged corridor %d times", checks)
	}
	b.edit(true, false)
	safety = countingRegionalSafety{b.Safety("human"), &checks}
	if !s.navigation.regionSafe(a, safety, a.location.Position()) || checks != 2 {
		t.Fatalf("new capture not checked: %d", checks)
	}
	// Publication uses the production transfer path to replace all handles.
	b.edit(false, true)
	if !s.navigation.rebaseAgent(a, a.location.Position()) {
		t.Fatal("rebase failed")
	}
	safety = countingRegionalSafety{b.Safety("human"), &checks}
	if !s.navigation.regionSafe(a, safety, a.location.Position()) || checks != 3 {
		t.Fatalf("new route not checked: %d", checks)
	}
	// A new unsafe capture must bypass the previously cached successful result.
	a.route = navigationTestRoute{[]surface.Corner{{Position: surface.Vec3{X: -5}}, {Position: surface.Vec3{X: 5}}}}
	a.routeSafetyChecked = false
	b.edit(true, false)
	if s.navigation.regionSafe(a, countingRegionalSafety{b.Safety("human"), &checks}, a.location.Position()) {
		t.Fatal("unsafe replacement inherited cached safety")
	}
}

func (f regionalMotor) MoveNavigation(m NavigationMotion) (surface.Vec3, error) { return f(m) }

func TestNavigationRegionalMotorCrossingAndCallbackEdit(t *testing.T) {
	for _, editInCallback := range []bool{false, true} {
		t.Run(map[bool]string{false: "crossing", true: "callback"}[editInCallback], func(t *testing.T) {
			s, b := regionalServer(t)
			a := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5}, surface.Vec3{X: -10})
			if !editInCallback {
				b.edit(true, false)
			}
			a.config.Motor = regionalMotor(func(m NavigationMotion) (surface.Vec3, error) {
				if editInCallback {
					b.edit(true, false)
				}
				return surface.Vec3{X: 5}, nil
			})
			navigationTestTick(s)
			p, _, _ := s.navigation.position(1)
			if p.X != -5 || !a.quarantined {
				t.Fatalf("motor bypassed quarantine: %v %+v", p, a.state)
			}
		})
	}
}

func TestNavigationRegionalCollisionCrossingRestoresAcceptedPosition(t *testing.T) {
	s, b := regionalServer(t)
	a := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5}, surface.Vec3{X: -10})
	b.edit(true, false)
	s.tick++
	s.navigation.advance(.05)
	accepted := a.lastPosition
	_, writer, _ := s.navigation.position(1)
	writer.SetPosition3D(5, 0, 0)
	s.navigation.reconcile()
	p, _, _ := s.navigation.position(1)
	if navDistance(p, accepted) > 1e-6 || !a.quarantined {
		t.Fatalf("collision crossing retained: %v expected %v", p, accepted)
	}
}

func TestNavigationRegionalTraversalCollisionCannotBypassSafety(t *testing.T) {
	for _, quarantine := range []bool{false, true} {
		t.Run(map[bool]string{false: "bounds", true: "quarantine"}[quarantine], func(t *testing.T) {
			s, b := regionalServer(t)
			from, to := surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 5, Z: 20}
			a := seedRegionalAgent(t, s, 1, from, to)
			link := surface.Link{ID: "jump", From: from, To: to}
			a.route = navigationTestRoute{[]surface.Corner{{Position: from, Link: &link}, {Position: to}}}
			a.corners, a.next = a.route.Corners(), 0
			action := &boundedRegionalTraversal{navigationTestTraversal: navigationTestTraversal{end: surface.Vec3{X: -4, Y: 1, Z: 20}}, bounds: NavigationTraversalBounds{Min: from, Max: surface.Vec3{X: 5, Y: 2, Z: 20}}}
			a.config.Traversal = action
			s.navigation.beginTraversal(a, link)
			s.tick++
			s.navigation.advance(.05)
			accepted := a.lastPosition
			_, writer, _ := s.navigation.position(1)
			if quarantine {
				b.edit(true, false)
				writer.SetPosition3D(5, 1, -20)
			} else {
				writer.SetPosition3D(50, 1, 20)
			}
			s.navigation.reconcile()
			p, _, _ := s.navigation.position(1)
			if navDistance(p, accepted) > 1e-6 || action.cancelled != 1 {
				t.Fatalf("collision bypassed traversal safety: position=%v accepted=%v cancelled=%d", p, accepted, action.cancelled)
			}
			if quarantine && !a.quarantined || !quarantine && a.state.Mode != NavigationFailed {
				t.Fatalf("wrong interruption: %+v", a.state)
			}
		})
	}
}

func TestNavigationRegionalRebasePreservesIntentVelocityAndReleases(t *testing.T) {
	s, b := regionalServer(t)
	a := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 20, Z: 20})
	a.state.Velocity = surface.Vec3{X: 2}
	a.speed = 2
	for i := 0; i < 20; i++ {
		b.edit(false, true)
		s.navigation.advanceAgent(a, 0)
		if a.view == nil || a.view.Revision() != uint64(i+2) || a.state.Velocity.X != 2 || a.state.Destination.X != 20 {
			t.Fatalf("transfer changed intent/motion: %+v", a.state)
		}
	}
	if b.releases.Load() != 20 {
		t.Fatalf("retained old views: releases=%d", b.releases.Load())
	}
}

func TestNavigationRegionalTickMetrics(t *testing.T) {
	s, _ := regionalServer(t)
	a := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 20, Z: 20})
	a.config.Motor = regionalMotor(func(m NavigationMotion) (surface.Vec3, error) {
		time.Sleep(time.Millisecond)
		return m.Target, nil
	})
	var sample NavigationTickMetrics
	s.navigation.config.ObserveTick = func(m NavigationTickMetrics) { sample = m }
	navigationTestTick(s)
	if sample.Tick != s.tick || sample.AgentsScheduled != 1 || sample.Prepare+sample.Movement+sample.Reconcile <= time.Duration(0) {
		t.Fatalf("metrics=%+v", sample)
	}
}

type boundedRegionalTraversal struct {
	navigationTestTraversal
	bounds NavigationTraversalBounds
}

func (a *boundedRegionalTraversal) BeginNavigationTraversal(r NavigationTraversalRequest) (NavigationTraversalAction, error) {
	a.request = r
	return a, nil
}
func (a *boundedRegionalTraversal) NavigationTraversalBounds() NavigationTraversalBounds {
	return a.bounds
}

func TestNavigationRegionalTraversalBoundsAndPublication(t *testing.T) {
	for _, bounded := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown-bounds", true: "bounded"}[bounded], func(t *testing.T) {
			s, b := regionalServer(t)
			from, to := surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 5, Z: 20}
			a := seedRegionalAgent(t, s, 1, from, to)
			link := surface.Link{ID: "jump", From: from, To: to}
			a.route = navigationTestRoute{[]surface.Corner{{Position: from, Link: &link}, {Position: to}}}
			a.corners, a.next = a.route.Corners(), 0
			plain := &navigationTestTraversal{end: surface.Vec3{X: -4, Y: 1, Z: 20}}
			bound := &boundedRegionalTraversal{navigationTestTraversal: *plain, bounds: NavigationTraversalBounds{Min: surface.Vec3{X: -5, Z: 20}, Max: surface.Vec3{X: 5, Y: 2, Z: 20}}}
			if bounded {
				a.config.Traversal = bound
			} else {
				a.config.Traversal = plain
			}
			s.navigation.beginTraversal(a, link)
			if a.state.Mode != NavigationTraversing {
				t.Fatalf("begin=%+v", a.state)
			}
			b.edit(true, false)
			navigationTestTick(s)
			if !bounded {
				if plain.cancelled != 1 || a.state.Mode != NavigationPending {
					t.Fatalf("unbounded action survived edit: %+v", a.state)
				}
				return
			}
			if bound.cancelled != 0 || a.state.Mode != NavigationTraversing {
				t.Fatalf("distant bounded action stopped: %+v", a.state)
			}
			b.edit(false, true)
			navigationTestTick(s)
			if bound.cancelled != 0 || a.state.Mode != NavigationTraversing || a.view.Revision() != 2 {
				t.Fatalf("bounded action failed transfer: %+v", a.state)
			}
			bound.end, bound.complete = to, true
			navigationTestTick(s)
			if bound.cancelled != 0 || a.traversal != nil {
				t.Fatal("landing failed after transfer")
			}
		})
	}
}

func TestNavigationRegionalTraversalCannotLeaveDeclaredBounds(t *testing.T) {
	s, _ := regionalServer(t)
	from, to := surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 5, Z: 20}
	a := seedRegionalAgent(t, s, 1, from, to)
	link := surface.Link{ID: "jump", From: from, To: to}
	a.route = navigationTestRoute{[]surface.Corner{{Position: from, Link: &link}, {Position: to}}}
	a.corners, a.next = a.route.Corners(), 0
	action := &boundedRegionalTraversal{navigationTestTraversal: navigationTestTraversal{end: surface.Vec3{X: 100, Z: 20}}, bounds: NavigationTraversalBounds{Min: from, Max: surface.Vec3{X: 5, Y: 2, Z: 20}}}
	a.config.Traversal = action
	s.navigation.beginTraversal(a, link)
	navigationTestTick(s)
	p, _, _ := s.navigation.position(1)
	if p != from || action.cancelled != 1 || a.state.Mode != NavigationFailed {
		t.Fatalf("escaped bounds: %v %+v", p, a.state)
	}
}

func TestNavigationRegionalAdmitsUnchangedAsyncResultAfterPublication(t *testing.T) {
	s, b := regionalServer(t)
	started, release := make(chan struct{}), make(chan struct{})
	b.path = func(ctx context.Context, from, to surface.Vec3) surface.PathResult {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: from}, {Position: to}}}}
	}
	_, writer, _ := s.navigation.position(1)
	writer.SetPosition3D(-5, 0, 20)
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 5, Z: 20}); err != nil {
		t.Fatal(err)
	}
	navigationTestTick(s)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("path did not start")
	}
	b.edit(false, true)
	close(release)
	awaitNavigation(t, s, func() bool { return s.navigation.agents[1].state.Mode == NavigationFollowing })
	a := s.navigation.agents[1]
	if a.view.Revision() != 2 || s.NavigationStats().Paths.Accepted != 1 {
		t.Fatalf("unchanged async result was replanned: %+v", a.state)
	}
}

func TestNavigationRegionalQuarantinesAsyncResultAfterEdit(t *testing.T) {
	s, b := regionalServer(t)
	started, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	b.path = func(ctx context.Context, from, to surface.Vec3) surface.PathResult {
		first.Do(func() {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
		return surface.PathResult{Status: surface.Status{Code: surface.Complete}, Route: navigationTestRoute{[]surface.Corner{{Position: from}, {Position: to}}}}
	}
	_, writer, _ := s.navigation.position(1)
	writer.SetPosition3D(-5, 0, 0)
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 5}); err != nil {
		t.Fatal(err)
	}
	navigationTestTick(s)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("path did not start")
	}
	b.edit(true, false)
	close(release)
	awaitNavigation(t, s, func() bool { return s.navigation.agents[1].quarantined })
	a := s.navigation.agents[1]
	for i := 0; i < 5; i++ {
		navigationTestTick(s)
	}
	p, _, _ := s.navigation.position(1)
	if p.X != -5 || a.view == nil || a.route == nil || !a.admissionPending || a.state.Mode != NavigationPending || !a.state.HasDestination || s.NavigationStats().Paths.Accepted != 1 {
		t.Fatalf("unsafe result admitted or resubmitted: position=%v state=%+v", p, a.state)
	}
	if b.releases.Load() != 0 {
		t.Fatalf("quarantined owned result lost its view: releases=%d", b.releases.Load())
	}
	b.edit(false, true)
	awaitNavigation(t, s, func() bool { return a.state.Mode == NavigationFollowing })
	if a.view.Revision() != 2 || a.quarantined || a.admissionPending || s.NavigationStats().Paths.Accepted != 1 || b.releases.Load() != 1 {
		t.Fatalf("intent did not resume after publication: %+v", a.state)
	}
}
