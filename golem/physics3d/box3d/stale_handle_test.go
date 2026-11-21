package box3d

import (
	"errors"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func wantStaleHandle(t *testing.T, what string, err error, h p.Handle) {
	t.Helper()
	var stale *p.StaleHandleError
	if !errors.As(err, &stale) || stale.Handle != h || !errors.Is(err, p.ErrStale) {
		t.Fatalf("%s = %v; want *StaleHandleError for %+v", what, err, h)
	}
}

func TestStaleLookupsReportTheHandle(t *testing.T) {
	w, err := New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	if err != nil {
		t.Fatal(err)
	}
	a, err := w.CreateBody(p.DefaultBody(p.FreeDynamic))
	if err != nil {
		t.Fatal(err)
	}
	collider, err := w.CreateCollider(p.DefaultCollider(a, shape))
	if err != nil {
		t.Fatal(err)
	}
	if err = w.DestroyBody(a, p.Deleted); err != nil {
		t.Fatal(err)
	}
	_, err = w.Body(a)
	wantStaleHandle(t, "Body", err, p.Handle(a))
	wantStaleHandle(t, "ApplyImpulse", w.ApplyImpulse(a, p.Vec3{X: 1}, p.Vec3{}), p.Handle(a))
	wantStaleHandle(t, "DestroyBody", w.DestroyBody(a, p.Deleted), p.Handle(a))
	wantStaleHandle(t, "DestroyCollider", w.DestroyCollider(collider, p.Deleted), p.Handle(collider))
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	_, err = w.ClosestPoint(collider, p.Vec3{})
	wantStaleHandle(t, "ClosestPoint", err, p.Handle(collider))
	_, err = w.CreateCollider(p.DefaultCollider(a, shape))
	wantStaleHandle(t, "CreateCollider body", err, p.Handle(a))
	if err = w.DestroyShape(shape); err != nil {
		t.Fatal(err)
	}
	wantStaleHandle(t, "DestroyShape", w.DestroyShape(shape), p.Handle(shape))
	joint := p.JointHandle(collider)
	wantStaleHandle(t, "DestroyJoint", w.DestroyJoint(joint), p.Handle(joint))
	_, err = w.Joint(joint)
	wantStaleHandle(t, "Joint", err, p.Handle(joint))

	live, err := w.CreateBody(p.DefaultBody(p.Kinematic))
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	// A closed world is not a handle error: guard keeps a bare ErrStale.
	err = w.DestroyBody(live, p.Deleted)
	var stale *p.StaleHandleError
	if err != p.ErrStale || errors.As(err, &stale) {
		t.Fatalf("closed world = %v; want bare ErrStale", err)
	}
}
