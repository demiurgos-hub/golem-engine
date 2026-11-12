// physics-debug emits an explicit sample capture from a real Box3D world.
package main

import (
	"flag"
	"fmt"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
	"os"
)

func main() {
	out := flag.String("out", "", "new JSON capture path")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(path string) error {
	if path == "" {
		return fmt.Errorf("-out is required")
	}
	// This format/visualization sample uses the ordinary solver; it is not a
	// continuous-motion or workload qualification fixture.
	config := p.DefaultConfig()
	config.Continuous = false
	w, err := box.New(config)
	if err != nil {
		return err
	}
	defer w.Close()
	add := func(id string, mode p.MotionMode, pose p.Pose, shape p.ShapeDesc) (p.BodyHandle, error) {
		shape.ID = p.ContentID(id + "/shape")
		s, e := w.CreateShape(shape)
		if e != nil {
			return p.BodyHandle{}, e
		}
		d := p.DefaultBody(mode)
		d.ID = p.ContentID(id)
		d.Pose = pose
		body, e := w.CreateBody(d)
		if e != nil {
			return body, e
		}
		cd := p.DefaultCollider(body, s)
		cd.ID = p.ContentID(id + "/collider")
		_, e = w.CreateCollider(cd)
		return body, e
	}
	_, err = add("floor", p.Static, p.Identity(), p.ShapeDesc{Kind: p.TriangleMesh, Vertices: []p.Vec3{{X: -4, Z: -4}, {X: 4, Z: -4}, {X: -4, Z: 4}, {X: 4, Z: 4}}, Indices: []uint32{0, 2, 1, 1, 2, 3}})
	if err != nil {
		return err
	}
	pose := p.Identity()
	pose.Position = p.Vec3{Y: .505}
	body, err := add("ball", p.FreeDynamic, pose, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	if err != nil {
		return err
	}
	pose.Position = p.Vec3{X: 2, Y: .5}
	_, err = add("compound", p.Static, pose, p.ShapeDesc{Kind: p.Compound, Children: []p.ShapeChild{{ID: "box", Pose: p.Identity(), Shape: p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .3, Y: .5, Z: .4}}}, {ID: "capsule", Pose: p.Pose{Position: p.Vec3{X: 1}, Rotation: p.Quat{W: 1}}, Shape: p.ShapeDesc{Kind: p.Capsule, PointA: p.Vec3{Y: -.2}, PointB: p.Vec3{Y: .2}, Radius: .2}}}})
	if err != nil {
		return err
	}
	if err = w.SetVelocity(body, p.Vec3{X: .5}, p.Vec3{}); err != nil {
		return err
	}
	if err = w.Synchronize(); err != nil {
		return err
	}
	frame, err := w.Simulate(p.Frame{ID: 1, Delta: .05})
	if err != nil {
		return err
	}
	if err = w.Accept(1); err != nil {
		return err
	}
	q := p.Query{Kind: p.Sweep, Mode: p.All, Shape: p.ShapeDesc{Kind: p.Sphere, Radius: .2}, Pose: p.Pose{Position: p.Vec3{Y: 2}, Rotation: p.Quat{W: 1}}, Translation: p.Vec3{Y: -3}, Filter: p.QueryFilter{Mask: ^uint64(0)}, Backfaces: p.IncludeBackfaces}
	hits := make([]p.Hit, 16)
	r, err := w.Query(q, hits)
	if err != nil {
		return err
	}
	o := p.DefaultDebugOptions()
	o.Frame = &frame
	o.FrameAccepted = true
	o.Queries = []p.DebugQuery{{Label: "downward sphere sweep", Query: q, Result: r, Hits: hits[:r.Count]}}
	o.Targets = []p.DebugTarget{{Body: body, From: frame.Bodies[0].Anchor.Position, Target: p.Vec3{X: 1, Y: .5}, Label: "sample navigation target (caller annotation)"}}
	c, err := w.CaptureDebug(o)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return p.WriteDebug(f, c)
}
