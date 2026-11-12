package physics3d

import (
	"bytes"
	"reflect"
	"testing"
)

func TestCheckpointJointRoundTripContainsNoHandles(t *testing.T) {
	d := JointDesc{ID: "hinge", A: BodyHandle{World: 7, Index: 9, Generation: 3}, B: BodyHandle{World: 7, Index: 10, Generation: 3}, Kind: Hinge,
		FrameA: Identity(), FrameB: Identity(), Drive: true, Speed: 2, MaxTorque: 3, TargetRotation: Quat{W: 1}, Steering: true, SteeringAngle: .2}
	j, err := CaptureJoint(d, "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(j.Parameters, []byte("World")) || bytes.Contains(j.Parameters, []byte("Generation")) {
		t.Fatal("runtime handles were persisted")
	}
	a, b := BodyHandle{World: 100, Index: 1, Generation: 1}, BodyHandle{World: 100, Index: 2, Generation: 1}
	got, err := j.DecodeJoint(a, b)
	if err != nil {
		t.Fatal(err)
	}
	d.A, d.B = a, b
	if !reflect.DeepEqual(got, d) {
		t.Fatalf("joint settings lost: got=%+v want=%+v", got, d)
	}
	j.Parameters = []byte(`{"Kind":3,"a":{"World":999}}`)
	if _, err := j.DecodeJoint(a, b); err == nil {
		t.Fatal("persisted runtime handle accepted")
	}
}

func TestCheckpointRejectsForeignStableReferencesAndUnknownFields(t *testing.T) {
	d := DefaultBody(FreeDynamic)
	d.ID = "body"
	c := Checkpoint{Version: 1, ID: "checkpoint", SourceFingerprint: "source", Revision: 1, Config: DefaultConfig(),
		Shapes: []ShapeDesc{{ID: "shape", Kind: Sphere, Radius: 1}}, Bodies: []CheckpointBody{{Description: d}},
		Colliders: []CheckpointCollider{{ID: "collider", Body: "body", Shape: "shape", LocalPose: Identity(), Roles: Roles{Solid: true}}}}
	data, err := EncodeCheckpoint(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCheckpoint(data); err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(`{"unknown":1,`), data[1:]...)
	if _, err := DecodeCheckpoint(bad); err == nil {
		t.Fatal("unknown field accepted")
	}
	c.Colliders[0].Body = "other-world-body"
	if err := c.Validate(); err == nil {
		t.Fatal("dangling stable reference accepted")
	}
}
