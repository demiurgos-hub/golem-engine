package box3d_test

import (
	"errors"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

func contactTransactionWorld(t *testing.T) (*box.World, p.BodyHandle) {
	t.Helper()
	w := motionWorld(t)
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: 1})
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := w.CreateBody(p.DefaultBody(p.Static))
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultCollider(anchor, shape)
	d.Roles = p.Roles{Sensor: true, Query: true}
	if _, err = w.CreateCollider(d); err != nil {
		t.Fatal(err)
	}
	body, _ := add(t, w, p.FreeDynamic, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .25})
	return w, body
}

func TestRejectedContactDoesNotPublishShutdownExit(t *testing.T) {
	w, _ := contactTransactionWorld(t)
	r, err := w.Simulate(p.Frame{ID: 1, Delta: .05})
	if err != nil || len(r.Events) == 0 {
		t.Fatal(r, err)
	}
	if err = w.Reject(1, errors.New("quarantined")); err != nil {
		t.Fatal(err)
	}
	events, err := w.CloseWithEvents()
	if err != nil || len(events) != 0 {
		t.Fatalf("private contact escaped rejection: %v %v", events, err)
	}
}

func TestRejectedSeparationPreservesAcceptedShutdownPair(t *testing.T) {
	w, body := contactTransactionWorld(t)
	r := step(t, w, 1)
	pair := r.Events[0].Pair
	if err := w.SetVelocity(body, p.Vec3{X: 100}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	r, err := w.Simulate(p.Frame{ID: 2, Delta: .05})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range r.Events {
		found = found || event.Phase == p.Exit
	}
	if !found {
		t.Fatal("fixture failed to separate")
	}
	if err = w.Reject(2, errors.New("quarantined")); err != nil {
		t.Fatal(err)
	}
	events, err := w.CloseWithEvents()
	if err != nil || len(events) != 1 || events[0].Pair != pair || events[0].Phase != p.Exit || events[0].Reason != p.Shutdown || events[0].Frame != 1 {
		t.Fatalf("accepted contact history was replaced: %v %v", events, err)
	}
	events, err = w.CloseWithEvents()
	if err != nil || len(events) != 0 {
		t.Fatal("shutdown repeated", events, err)
	}
}

func TestPendingFrameShutdownUsesOnlyAcceptedPairs(t *testing.T) {
	w, _ := contactTransactionWorld(t)
	if _, err := w.Simulate(p.Frame{ID: 1, Delta: .05}); err != nil {
		t.Fatal(err)
	}
	events, err := w.CloseWithEvents()
	if err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
}
