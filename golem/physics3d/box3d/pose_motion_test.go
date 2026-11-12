package box3d_test

import (
	"errors"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func TestExternalPoseCheckpointKeepsReportedVelocityWithoutAutonomousDrift(t *testing.T) {
	w := motionWorld(t)
	shape, err := w.CreateShape(p.ShapeDesc{ID: "external-shape", Kind: p.Box, HalfExtents: p.Vec3{X: .2, Y: .2, Z: .2}})
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(p.ExternalPose)
	d.ID = "external-body"
	h, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	c := p.DefaultCollider(h, shape)
	c.ID = "external-collider"
	if _, err = w.CreateCollider(c); err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	target := p.Identity()
	target.Position.X = 1
	step(t, w, 1, p.Motion{Body: h, PoseSegments: []p.PoseMotionSegment{{From: p.Identity(), Target: target, Delta: .05}}})
	before, _ := w.Body(h)
	if before.LinearVelocity.X < 19.9 {
		t.Fatal("accepted trajectory velocity missing")
	}
	capture, err := w.CaptureCheckpoint("external", "source")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := w.PrepareRestore(capture)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Discard()
	fresh, _ := prepared.Body("external-body")
	if fresh.LinearVelocity != before.LinearVelocity {
		t.Fatal("reported external velocity lost in checkpoint")
	}
	if err = prepared.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = w.SetVelocity(fresh.Handle, p.Vec3{X: 1}, p.Vec3{}); !errors.Is(err, p.ErrUnsupported) {
		t.Fatal("autonomous external velocity was allowed")
	}
	step(t, w, 2)
	after, _ := w.Body(fresh.Handle)
	if after.Pose != before.Pose || after.LinearVelocity != (p.Vec3{}) {
		t.Fatalf("external pose drifted without a fresh timed proposal: before=%+v after=%+v", before, after)
	}
}

func TestPoseMotionUnsupportedControllerRotationRejectsBeforeFrame(t *testing.T) {
	w := world(t)
	h, _ := capsuleController(t, w, p.ScriptedAction, p.Vec3{Y: 10})
	state, _ := w.Body(h)
	target := state.Pose
	target.Rotation = p.Quat{Y: 1}
	segments := []p.PoseMotionSegment{{From: state.Pose, Target: target, Delta: .05}}
	if err := w.ValidatePoseMotion(h, segments); !errors.Is(err, p.ErrUnsupported) {
		t.Fatalf("unsupported rotation admitted: %v", err)
	}
	if _, err := w.Simulate(p.Frame{ID: 1, Delta: .05, Motions: []p.Motion{{Body: h, PoseSegments: segments}}}); !errors.Is(err, p.ErrUnsupported) {
		t.Fatal(err)
	}
	if w.Metrics().Fault != nil {
		t.Fatal("pre-simulation admission faulted the world")
	}
	actual, _ := w.Body(h)
	if actual != state {
		t.Fatal("rejected pose motion changed body")
	}
}

func TestPoseMotionAdmissionChecksIntermediateGeometryEnvelope(t *testing.T) {
	w := motionWorld(t)
	h, _ := add(t, w, p.ExternalPose, p.Vec3{X: 4095}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 1, Y: .2, Z: 1}})
	state, _ := w.Body(h)
	target := state.Pose
	target.Rotation = p.Quat{Y: .70710677, W: .70710677}
	if err := w.ValidatePoseMotion(h, []p.PoseMotionSegment{{From: state.Pose, Target: target, Delta: .05}}); !errors.Is(err, p.ErrInvalid) {
		t.Fatalf("intermediate rotating volume escaped envelope: %v", err)
	}
	if w.Metrics().Fault != nil {
		t.Fatal("invalid requested envelope faulted live world")
	}
}
