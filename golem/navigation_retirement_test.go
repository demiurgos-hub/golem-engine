package golem

import (
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

type retainedTestBackend struct {
	*regionalTestBackend
	retiring bool
}

func (b *retainedTestBackend) Safety(p surface.ProfileID) surface.RegionalSafety {
	s := b.regionalTestBackend.Safety(p).(regionalTestSafety)
	if b.retiring {
		s.desired = s.revision
	}
	return retainedTestSafety{s}
}
func (b *retainedTestBackend) Pending(p surface.ProfileID) bool {
	return !b.retiring && b.regionalTestBackend.Pending(p)
}

type retainedTestSafety struct{ regionalTestSafety }

func (s retainedTestSafety) ProtectsRetiredViews() bool { return true }
func (s retainedTestSafety) HasQuarantine() bool        { return s.dirty }

type scopedRetainedTestBackend struct{ *retainedTestBackend }

func (b *scopedRetainedTestBackend) Safety(p surface.ProfileID) surface.RegionalSafety {
	return scopedRetainedTestSafety{b.retainedTestBackend.Safety(p).(retainedTestSafety)}
}

type scopedRetainedTestSafety struct{ retainedTestSafety }

func (s scopedRetainedTestSafety) ForView(view surface.View) surface.RegionalSafety {
	if s.desired == s.revision && view.Revision() == s.revision {
		s.dirty = false
	}
	return s.retainedTestSafety
}

func TestNavigationScopedRetirementProtectsOnlyOldViewCrossings(t *testing.T) {
	s, base := regionalServer(t)
	b := &scopedRetainedTestBackend{&retainedTestBackend{regionalTestBackend: base}}
	s.navigation.backend = b
	s.navigation.config.RebaseAgentsPerTick = 1
	s.navigation.config.RebaseBudget = time.Second
	current := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5}, surface.Vec3{X: 5})
	old := seedRegionalAgent(t, s, 2, surface.Vec3{X: -5}, surface.Vec3{X: -10})
	old.config.Motor = regionalMotor(func(NavigationMotion) (surface.Vec3, error) { return surface.Vec3{X: 5}, nil })
	b.edit(true, false)
	s.navigation.advanceAgent(current, .05)
	if !current.quarantined {
		t.Fatal("fixture must start with a quarantined corridor")
	}
	b.edit(true, true)
	b.retiring = true
	navigationTestTick(s)
	latestPosition, _, _ := s.navigation.position(1)
	oldPosition, _, _ := s.navigation.position(2)
	if current.view.Revision() != 2 || current.quarantined || latestPosition.X <= -5 {
		t.Fatalf("latest view remained blocked by another lease: %v %+v", latestPosition, current.state)
	}
	if old.view.Revision() != 1 || !old.quarantined || oldPosition.X != -5 {
		t.Fatalf("old view crossed retired quarantine: %v %+v", oldPosition, old.state)
	}
}

func TestNavigationUnchangedPartialRouteRetiresWithoutLosingPartialStatus(t *testing.T) {
	for _, quarantined := range []bool{false, true} {
		name := "moving"
		if quarantined {
			name = "quarantined"
		}
		t.Run(name, func(t *testing.T) {
			s, b := regionalServer(t)
			partial := surface.Status{Code: surface.Partial}
			b.rebaseStatus = &partial
			a := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5}, surface.Vec3{X: 1})
			a.config.AllowPartial = true
			a.state.Destination = surface.Vec3{X: 10}
			a.state.Status = partial
			if quarantined {
				b.edit(true, false)
				navigationTestTick(s)
				if !a.quarantined {
					t.Fatal("fixture corridor did not enter quarantine")
				}
			}
			b.edit(false, true)
			navigationTestTick(s)
			if a.view == nil || a.view.Revision() != 2 || a.state.Status.Code != surface.Partial || a.state.Mode != NavigationFollowing || a.quarantined || s.NavigationStats().Paths.Accepted != 0 {
				t.Fatalf("partial corridor unnecessarily searched or lost status: %+v", a.state)
			}
			for i := 0; i < 70; i++ {
				navigationTestTick(s)
			}
			if a.state.Mode != NavigationBlocked || a.state.Status.Code != surface.Partial || a.state.Destination.X != 10 {
				t.Fatalf("partial prefix falsely reported destination reached: %+v", a.state)
			}
		})
	}
}

func TestNavigationRetirementBudgetIsFairWithoutPausingDistantMovement(t *testing.T) {
	s, base := regionalServer(t)
	b := &retainedTestBackend{regionalTestBackend: base}
	s.navigation.backend = b
	s.navigation.config.RebaseAgentsPerTick = 2
	s.navigation.config.RebaseBudget = time.Second
	for id := int64(1); id <= 5; id++ {
		seedRegionalAgent(t, s, id, surface.Vec3{X: -5, Z: 20 + float64(id)}, surface.Vec3{X: 20, Z: 20 + float64(id)})
	}
	for iteration := 0; iteration < 10; iteration++ {
		b.edit(false, true)
		prior := make(map[int64]float64)
		for id := range s.navigation.agents {
			p, _, _ := s.navigation.position(id)
			prior[id] = p.X
		}
		navigationTestTick(s)
		if s.navigation.tickMetrics.RebaseAttempts > 2 || s.navigation.tickMetrics.AgentsScheduled != 5 {
			t.Fatalf("budget/cadence not preserved: %+v", s.navigation.tickMetrics)
		}
		for id, a := range s.navigation.agents {
			p, _, _ := s.navigation.position(id)
			if p.X <= prior[id] || a.quarantined || a.state.Mode != NavigationFollowing || a.state.Velocity.X <= 0 {
				t.Fatalf("agent %d stopped during bounded retirement: position=%v state=%+v", id, p, a.state)
			}
			if iteration >= 2 && a.view.Revision()+2 < b.CurrentRevision("human") {
				t.Fatalf("agent %d starved: revision=%d current=%d", id, a.view.Revision(), b.CurrentRevision("human"))
			}
		}
	}
	// A quiet drain retires the remaining views within one full fair sweep.
	for i := 0; i < 3; i++ {
		navigationTestTick(s)
	}
	for id, a := range s.navigation.agents {
		if a.view.Revision() != b.CurrentRevision("human") {
			t.Fatalf("agent %d retained an old generation", id)
		}
	}
	if base.releases.Load() < 20 {
		t.Fatal("successful transfers retained their old leases")
	}
}

func TestNavigationRetiredViewStillChecksMotorSegments(t *testing.T) {
	s, base := regionalServer(t)
	b := &retainedTestBackend{regionalTestBackend: base, retiring: true}
	s.navigation.backend = b
	s.navigation.config.RebaseAgentsPerTick = 1
	s.navigation.config.RebaseBudget = time.Second
	seedRegionalAgent(t, s, 1, surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 20, Z: 20})
	a := seedRegionalAgent(t, s, 2, surface.Vec3{X: -5}, surface.Vec3{X: -10})
	a.config.Motor = regionalMotor(func(NavigationMotion) (surface.Vec3, error) { return surface.Vec3{X: 5}, nil })
	b.edit(true, true)
	navigationTestTick(s)
	p, _, _ := s.navigation.position(2)
	if p.X != -5 || !a.quarantined || a.view.Revision() != 1 {
		t.Fatalf("retired motor bypassed changed columns: %v %+v", p, a.state)
	}
	ready := s.NavigationReady()
	if !ready.Ready || !ready.Degraded || len(ready.PendingProfiles) != 0 {
		t.Fatalf("retiring publication readiness=%+v", ready)
	}
}

func TestNavigationQuarantinedChangedSupportStillRetires(t *testing.T) {
	s, base := regionalServer(t)
	b := &retainedTestBackend{regionalTestBackend: base, retiring: true}
	s.navigation.backend = b
	s.navigation.config.RebaseAgentsPerTick = 1
	s.navigation.config.RebaseBudget = time.Second
	seedRegionalAgent(t, s, 1, surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 20, Z: 20})
	a := seedRegionalAgent(t, s, 2, surface.Vec3{X: -5}, surface.Vec3{X: 5})
	old := a.view.(*regionalTestView)
	base.changedSupport = true
	b.edit(true, true)
	navigationTestTick(s)
	if !a.quarantined || old.released.Load() {
		t.Fatal("fixture did not retain a quarantined old route")
	}
	navigationTestTick(s)
	if !old.released.Load() {
		t.Fatal("quarantine prevented scheduled retirement of changed support")
	}
}

func TestNavigationCallbackRestoreStopsRemainingMovement(t *testing.T) {
	s, _ := regionalServer(t)
	a := seedRegionalAgent(t, s, 1, surface.Vec3{X: -5, Z: 20}, surface.Vec3{X: 20, Z: 20})
	seedRegionalAgent(t, s, 2, surface.Vec3{X: -5, Z: 30}, surface.Vec3{X: 20, Z: 30})
	a.config.Motor = regionalMotor(func(m NavigationMotion) (surface.Vec3, error) {
		if err := s.BeginNavigationRestore(); err != nil {
			t.Fatal(err)
		}
		return m.Target, nil
	})
	navigationTestTick(s)
	for _, id := range []int64{1, 2} {
		p, _, _ := s.navigation.position(id)
		if p.X != -5 {
			t.Fatalf("agent %d moved through callback-induced restore: %v", id, p)
		}
	}
}
