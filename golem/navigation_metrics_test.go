package golem

import (
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

func TestNavigationPathAdmissionObservesAcceptedRouteBeforeCallerSampling(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1})
	var observed []NavigationPathAdmissionMetrics
	s.navigation.config.ObservePathAdmission = func(m NavigationPathAdmissionMetrics) {
		state, _ := s.NavigationAgentState(m.EntityID)
		if state.Mode != NavigationFollowing || state.Generation != m.Generation || state.WorldRevision != m.WorldRevision {
			t.Fatalf("observer ran before admission: %+v %+v", m, state)
		}
		observed = append(observed, m)
	}
	issued := NavigationNow()
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 10}); err != nil {
		t.Fatal(err)
	}
	awaitNavigation(t, s, func() bool { return len(observed) > 0 })
	if len(observed) != 1 || observed[0].AdmittedAt.Before(issued) || observed[0].AdmittedAt.After(NavigationNow()) || observed[0].Tick == 0 {
		t.Fatalf("bad admission observation: %+v", observed)
	}
	for i := 0; i < 3; i++ {
		navigationTestTick(s)
	}
	if len(observed) != 1 {
		t.Fatal("unchanged following route emitted duplicate admission")
	}
}

func TestNavigationPathAdmissionDoesNotCountFailedProjection(t *testing.T) {
	s, _ := navigationTestServer(t, &navigationTestBackend{revision: 1, project: func(surface.Vec3) bool { return false }})
	admissions := 0
	s.navigation.config.ObservePathAdmission = func(NavigationPathAdmissionMetrics) { admissions++ }
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 10}); err != nil {
		t.Fatal(err)
	}
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationUnsupported })
	if admissions != 0 {
		t.Fatal("failed projection reported successful admission")
	}
}

func TestQualificationSupportToleranceDoesNotSnapRemovedBridge(t *testing.T) {
	backend := &navigationTestBackend{revision: 1, height: 7}
	s, entity := navigationTestServer(t, backend)
	s.navigation.agents[1].config.SupportTolerance = .2
	if err := s.SetNavigationDestination(1, surface.Vec3{X: 10, Y: 7}); err != nil {
		t.Fatal(err)
	}
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationFollowing })
	before := entity.position
	backend.change(0)
	awaitNavigation(t, s, func() bool { state, _ := s.NavigationAgentState(1); return state.Mode == NavigationUnsupported })
	if navDistance(before, entity.position) > .001 {
		t.Fatalf("removed bridge snapped body despite explicit tolerance: %+v -> %+v", before, entity.position)
	}
}
