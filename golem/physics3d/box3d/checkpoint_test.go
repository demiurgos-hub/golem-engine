package box3d_test

import (
	"errors"
	"os"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
)

func checkpointFixture(t *testing.T, sensor bool) (*box.World, p.BodyHandle, p.Checkpoint) {
	t.Helper()
	config := p.DefaultConfig()
	config.Gravity = p.Vec3{}
	w, err := box.New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	shape, err := w.CreateShape(p.ShapeDesc{ID: "sphere", Kind: p.Sphere, Radius: 1})
	if err != nil {
		t.Fatal(err)
	}
	var handles []p.BodyHandle
	for i, id := range []p.ContentID{"ground", "mover"} {
		d := p.DefaultBody(p.Static)
		d.ID = id
		if i == 1 {
			d.Mode = p.FreeDynamic
			d.Pose.Position.Y = 2
			d.Anchor.Position.Y = -.25
			if sensor {
				d.Pose.Position.Y = .5
			}
		}
		h, err := w.CreateBody(d)
		if err != nil {
			t.Fatal(err)
		}
		handles = append(handles, h)
		c := p.DefaultCollider(h, shape)
		c.ID = p.ContentID(string(id) + "-collider")
		if i == 1 && sensor {
			c.Roles = p.Roles{Sensor: true, Query: true}
		}
		if _, err = w.CreateCollider(c); err != nil {
			t.Fatal(err)
		}
	}
	if !sensor {
		_, err = w.CreateJoint(p.JointDesc{ID: "tether", Kind: p.Distance, A: handles[0], B: handles[1], FrameA: p.Identity(), FrameB: p.Identity(), Length: 2})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	if err = w.ApplyForce(handles[1], p.Vec3{X: 2}, p.Vec3{Y: 2}); err != nil {
		t.Fatal(err)
	}
	capture, err := w.CaptureCheckpoint("save-1", "physics-source")
	if err != nil {
		t.Fatal(err)
	}
	return w, handles[1], capture
}

func TestCheckpointRestoreRecreatesStableBodiesJointsAndForces(t *testing.T) {
	w, old, capture := checkpointFixture(t, false)
	prepared, err := w.PrepareRestore(capture)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Discard()
	fresh, ok := prepared.Body("mover")
	if !ok || fresh.Handle == old || fresh.Anchor.Position.Y != 1.75 {
		t.Fatalf("fresh state=%+v", fresh)
	}
	if _, err = w.Body(old); err != nil {
		t.Fatal("preparation changed the live body")
	}
	if err = prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Body(old); !errors.Is(err, p.ErrStale) {
		t.Fatal("old generational handle survived restore")
	}
	if _, err = w.Body(fresh.Handle); err != nil {
		t.Fatal(err)
	}
	again, err := w.CaptureCheckpoint("save-2", "physics-source")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Joints) != 1 || again.Joints[0].A != "ground" || again.Joints[0].B != "mover" || len(again.Bodies[1].Forces) != 1 {
		t.Fatalf("state not preserved: %+v", again)
	}
	if _, err = w.Simulate(p.Frame{ID: 1, Delta: .05}); err != nil {
		t.Fatal(err)
	}
	if err = w.Accept(1); err != nil {
		t.Fatal(err)
	}
	again, err = w.CaptureCheckpoint("save-3", "physics-source")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Bodies[1].Forces) != 0 {
		t.Fatal("restored force was not consumed exactly once")
	}
}

func TestCheckpointPrepareFailureAndInterveningForceAreAtomic(t *testing.T) {
	w, body, capture := checkpointFixture(t, false)
	bad := capture
	bad.Joints = append([]p.CheckpointJoint(nil), capture.Joints...)
	bad.Joints[0].Parameters = []byte(`{"Kind":99,"FrameA":{"Rotation":{"W":1}},"FrameB":{"Rotation":{"W":1}}}`)
	before := w.Metrics()
	if candidate, err := w.PrepareRestore(bad); err == nil {
		candidate.Discard()
		t.Fatal("unsupported joint restore succeeded")
	}
	if state, err := w.Body(body); err != nil || state.Pose.Position.Y != 2 || w.Metrics().Bodies != before.Bodies {
		t.Fatal("failed private restoration changed live state")
	}
	prepared, err := w.PrepareRestore(capture)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Discard()
	if err = w.ApplyForce(body, p.Vec3{Z: 3}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	if err = prepared.Commit(); !errors.Is(err, p.ErrStale) {
		t.Fatalf("queued force failed to invalidate prepared replacement: %v", err)
	}
	if _, err = w.Body(body); err != nil {
		t.Fatal("stale replacement destroyed live body")
	}
}

func TestCheckpointRecoversFaultAndClassifiesRestoredContacts(t *testing.T) {
	w, _, capture := checkpointFixture(t, true)
	if _, err := w.Simulate(p.Frame{ID: 1, Delta: .05}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.PrepareRestore(capture); !errors.Is(err, p.ErrBusy) {
		t.Fatal("restore admitted while simulation pending")
	}
	if err := w.Reject(1, errors.New("unsafe frame")); err != nil {
		t.Fatal(err)
	}
	prepared, err := w.PrepareRestore(capture)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Discard()
	if err = prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	r, err := w.Simulate(p.Frame{ID: 1, Delta: .05})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Events) == 0 {
		t.Fatal("restored overlap produced no contact")
	}
	for _, event := range r.Events {
		if event.Reason != p.Restored || event.Phase != p.RestoredContact {
			t.Fatalf("ordinary contact enter escaped restoration: %+v", event)
		}
	}
	if err = w.Accept(1); err != nil {
		t.Fatal(err)
	}
	r, err = w.Simulate(p.Frame{ID: 2, Delta: .05})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range r.Events {
		if event.Reason == p.Restored {
			t.Fatal("restore marker persisted past acceptance")
		}
	}
}

func TestCheckpointRebindsContentLeaseToFreshHandles(t *testing.T) {
	w := world(t)
	file, err := os.Open("../../../golem-unity/Tests/Fixtures/physics-source.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	source, _, err := content.Decode(file, content.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	preparedSource, err := content.Prepare(source, content.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	instance, err := preparedSource.Load(w, func(string) (int64, error) { return 42, nil })
	if err != nil {
		t.Fatal(err)
	}
	body, _ := instance.Body("actor")
	pose := p.Identity()
	pose.Position = p.Vec3{X: 3, Y: 2}
	if err = w.SetPose(body, pose); err != nil {
		t.Fatal(err)
	}
	other := p.DefaultBody(p.Static)
	other.ID = "unrelated"
	if _, err = w.CreateBody(other); err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	min, max, ok, err := instance.Bounds()
	if err != nil || !ok || min.X != 2.5 || max.X != 3.5 {
		t.Fatalf("live bounds ignored dynamic pose: %v %v %v", min, max, err)
	}
	lease, err := instance.CaptureState()
	if err != nil {
		t.Fatal(err)
	}
	capture, err := w.CaptureCheckpoint("source-save", "source")
	if err != nil {
		t.Fatal(err)
	}
	restore, err := w.PrepareRestore(capture)
	if err != nil {
		t.Fatal(err)
	}
	defer restore.Discard()
	restoredLease, err := content.RestoreInstance(w, lease, restore)
	if err != nil {
		t.Fatal(err)
	}
	unrelated, ok := restore.FindBody("unrelated")
	if !ok {
		t.Fatal("unrelated body absent")
	}
	if err = restore.Commit(); err != nil {
		t.Fatal(err)
	}
	fresh, ok := restoredLease.Body("actor")
	if !ok || fresh == body {
		t.Fatal("source retained stale handles")
	}
	if err = restoredLease.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err = w.Body(fresh); !errors.Is(err, p.ErrStale) {
		t.Fatal("restored source did not unload")
	}
	if _, err = w.Body(unrelated.Handle); err != nil || w.Metrics().Bodies != 1 {
		t.Fatal("source unload destroyed unrelated restored body")
	}
}
