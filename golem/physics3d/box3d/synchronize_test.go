package box3d

import (
	"errors"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func TestStaticSynchronizationTracksMutationsAndReusedHandles(t *testing.T) {
	w, err := New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	shape, err := w.CreateShape(p.ShapeDesc{ID: "shape", Kind: p.Sphere, Radius: .5})
	must(err)
	d := p.DefaultBody(p.Static)
	d.ID, d.Mass = "body", 13
	d.Anchor.Position.Y = 1
	h, err := w.CreateBody(d)
	must(err)
	otherDesc := p.DefaultBody(p.Static)
	otherDesc.ID, otherDesc.Pose.Position.X = "unaffected", 20
	other, err := w.CreateBody(otherDesc)
	must(err)
	cd := p.DefaultCollider(h, shape)
	cd.ID, cd.LocalPose.Position.X = "collider", 1
	c, err := w.CreateCollider(cd)
	must(err)
	must(w.Synchronize())
	initial, err := w.Body(h)
	must(err)
	unaffected, err := w.Body(other)
	must(err)
	if initial.Anchor.Position.Y != 1 || len(w.dirtyStatics) != 0 {
		t.Fatal("initial construction was not synchronized")
	}

	pose := p.Identity()
	pose.Position.X = 3
	must(w.SetOwner(h, 7))
	must(w.SetPose(h, pose))
	must(w.SetEnabled(h, false))
	// Metadata publication must not lose a queued pose when this body leaves
	// activeBodies before the direct-mutation synchronization boundary.
	must(w.SetOwner(h, 0))
	before, err := w.Body(h)
	must(err)
	if before.Pose != initial.Pose || !before.Enabled {
		t.Fatal("direct pose/activation mutation published before synchronization")
	}
	if err := w.queryReady(); !errors.Is(err, p.ErrBusy) {
		t.Fatal("dirty query boundary was lost", err)
	}
	must(w.Synchronize())
	after, err := w.Body(h)
	must(err)
	if after.Pose != pose || after.Enabled || after.Owner != 0 || after.Anchor.Position != pose.Transform(d.Anchor.Position) {
		t.Fatalf("static mutation missing from committed state: %+v", after)
	}
	unchanged, err := w.Body(other)
	must(err)
	if unchanged != unaffected {
		t.Fatal("unaffected static state changed")
	}
	must(w.SetEnabled(h, true))
	must(w.SetMotionPolicy(h, p.MotionPolicy{Mode: p.FreeDynamic, Velocity: p.VelocityClear}))
	must(w.Synchronize())
	after, err = w.Body(h)
	must(err)
	if after.Mode != p.FreeDynamic || after.Mass != 13 || after.CenterOfMass.X != 4 || !after.Enabled {
		t.Fatalf("static-to-dynamic mass/COM state lost: %+v", after)
	}
	must(w.DestroyCollider(c, p.Replaced))
	must(w.Synchronize())
	after, err = w.Body(h)
	must(err)
	if after.CenterOfMass.X != 3 {
		t.Fatalf("collider removal did not publish changed COM: %+v", after)
	}
	c, err = w.CreateCollider(cd)
	must(err)
	must(w.SetMotionPolicy(h, p.MotionPolicy{Mode: p.Static, Velocity: p.VelocityClear}))
	must(w.Synchronize())
	after, err = w.Body(h)
	must(err)
	if after.Mode != p.Static || after.Mass != 0 {
		t.Fatalf("dynamic-to-static state was omitted after leaving active set: %+v", after)
	}
	pose.Position.X = 9
	must(w.SetPose(h, pose))
	must(w.DestroyBody(h, p.Replaced))
	if _, retained := w.dirtyStatics[h]; retained {
		t.Fatal("destroyed handle remained in synchronization work")
	}
	d.ID, d.Pose.Position.X = "replacement", 30
	replacement, err := w.CreateBody(d)
	must(err)
	if replacement.Index != h.Index || replacement.Generation == h.Generation {
		t.Fatal("fixture did not reuse the body slot with a new generation")
	}
	cd.Body, cd.ID = replacement, "replacement-collider"
	_, err = w.CreateCollider(cd)
	must(err)
	must(w.Synchronize())
	if _, err := w.Body(h); !errors.Is(err, p.ErrStale) {
		t.Fatal("old body handle became live after synchronization", err)
	}
	after, err = w.Body(replacement)
	must(err)
	if after.Pose != d.Pose || !after.Enabled || after.Mode != p.Static || len(w.dirtyStatics) != 0 {
		t.Fatalf("replacement inherited dirty predecessor state: %+v", after)
	}
	_, err = w.CaptureCheckpoint("after-replacement", "source")
	must(err)
}
