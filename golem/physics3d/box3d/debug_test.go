package box3d

import (
	"bytes"
	"errors"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"testing"
)

func TestDebugExactGeometryBoundsAndFaultPoses(t *testing.T) {
	w, err := New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	s, err := w.CreateShape(p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 1, Y: 2, Z: 3}})
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(p.FreeDynamic)
	d.Pose.Position.Y = 10
	body, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	cd := p.DefaultCollider(body, s)
	cd.LocalPose.Position.X = 2
	_, err = w.CreateCollider(cd)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	o := p.DefaultDebugOptions()
	c, err := w.CaptureDebug(o)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Geometry) != 1 || len(c.Geometry[0].Vertices) != 8 || len(c.Geometry[0].Indices) != 36 {
		t.Fatalf("missing exact native hull: %+v", c)
	}
	for _, v := range c.Geometry[0].Vertices {
		if v.X != 1 && v.X != 3 {
			t.Fatal("collider transform lost", v)
		}
	}
	c.Geometry[0].Vertices[0].X = 100
	again, _ := w.CaptureDebug(o)
	if again.Geometry[0].Vertices[0].X == 100 {
		t.Fatal("capture aliases geometry")
	}
	o.MaxVertices = 7
	c, err = w.CaptureDebug(o)
	if err != nil || !c.Incomplete || len(c.Geometry) != 0 || c.Omitted.Vertices != 8 || !c.Colliders[0].GeometryOmitted {
		t.Fatal("geometry cap substituted/truncated geometry", c, err)
	}
	o = p.DefaultDebugOptions()
	o.IncludeUncommitted = true
	r, err := w.Simulate(p.Frame{ID: 1, Delta: .05})
	if err != nil {
		t.Fatal(err)
	}
	o.Frame = &r
	c, err = w.CaptureDebug(o)
	if err != nil || c.Status != "pending" || c.Bodies[0].Uncommitted == nil || c.Bodies[0].State.Pose.Position.Y != 10 || c.Bodies[0].Uncommitted.Pose.Position.Y >= 10 {
		t.Fatal("candidate mislabeled", c, err)
	}
	if err = w.Reject(1, errors.New("debug fixture rejection")); err != nil {
		t.Fatal(err)
	}
	c, err = w.CaptureDebug(o)
	if err != nil || c.Status != "faulted" || c.FrameAccepted {
		t.Fatal(c, err)
	}
	var buf bytes.Buffer
	if err = p.WriteDebug(&buf, c); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ReadDebug(&buf); err != nil {
		t.Fatal(err)
	}
}
