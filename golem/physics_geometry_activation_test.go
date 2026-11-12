package golem

import (
	"context"
	"errors"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"testing"
)

func TestPhysicsGeometryActivatesBeforeNavigationAndRetriesWithoutRecreatingBodies(t *testing.T) {
	s, w, backend := physicsGeometryFixture(t)
	source := physicsGeometrySource(t, 1)
	r := geometryRequest(source, 1, backend, nil)
	failure := errors.New("navigation build failed")
	change := PhysicsGeometryActivation{Identity: r.Identity, Profiles: r.Profiles, Bounds: r.Bounds, Source: source, ResolveOwner: func(string) (int64, error) { return 0, nil }, Quarantine: r.Quarantine,
		PrepareNavigation: func(context.Context) (NavigationBuildResult, error) { return nil, failure }}
	if _, err := s.ActivatePhysicsGeometry(change); err != nil {
		t.Fatal(err)
	}
	lease, ok := s.PhysicsGeometry("cell")
	if !ok || len(w.bodies) != 1 || !backend.dirty || backend.CurrentRevision("human") != 1 {
		t.Fatal("physical activation did not precede navigation publication")
	}
	body, _ := lease.Body("actor")
	if pending := s.PendingPhysicsGeometry(); len(pending) != 1 || !pending[0].Activated {
		t.Fatal("activated pending change missing")
	}
	status := awaitPhysicsGeometry(t, s, "cell")
	if !errors.Is(status.Err, failure) || status.Committed {
		t.Fatalf("failed navigation=%+v", status)
	}
	if _, err := w.Body(body); err != nil || !backend.dirty {
		t.Fatal("navigation failure lost defined new physical state or quarantine")
	}
	w.creating = func() { t.Error("same identity retry recreated physical body") }
	change.PrepareNavigation = func(context.Context) (NavigationBuildResult, error) {
		return &physicsCheckpointNavigation{commit: func() error { backend.edit(false, true); return nil }}, nil
	}
	if _, err := s.ActivatePhysicsGeometry(change); err != nil {
		t.Fatal(err)
	}
	if status = awaitPhysicsGeometry(t, s, "cell"); !status.Committed {
		t.Fatal(status.Err)
	}
	if _, err := w.Body(body); err != nil || len(s.PendingPhysicsGeometry()) != 0 || backend.dirty {
		t.Fatal("successful retry changed bodies or retained pending quarantine")
	}
}

func TestPhysicsActivatedNavigationCannotPublishSupersededDesiredIdentity(t *testing.T) {
	s, _, backend := physicsGeometryFixture(t)
	source := physicsGeometrySource(t, 1)
	r := geometryRequest(source, 1, backend, nil)
	backend.edit(true, false)
	s.physics.geometryIdentities = map[string]PhysicsGeometryIdentity{"cell": r.Identity}
	newer := r
	newer.Identity.Revision++
	s.rememberPhysicsGeometry(newer, false)
	private := &physicsCheckpointNavigation{commit: func() error { t.Error("superseded navigation published"); return nil }}
	b := &physicsActivatedNavigation{server: s, request: r, navigation: private}
	if err := b.Commit(); !errors.Is(err, p.ErrStale) {
		t.Fatalf("old desired identity remained admissible: %v", err)
	}
	b.Discard()
	if !private.discarded || !backend.dirty || s.PendingPhysicsGeometry()[0].Identity != newer.Identity {
		t.Fatal("discard disturbed newer desired change or leaked candidate")
	}
}
