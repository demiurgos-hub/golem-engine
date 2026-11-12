package box3d

import (
	"fmt"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

func TestMotionFallingJointedGridMakesBoundedProgress(t *testing.T) {
	config := p.DefaultConfig()
	config.Workers = 8
	w, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	const n = 100
	add := func(shape p.ShapeHandle, mode p.MotionMode, pos p.Vec3) p.BodyHandle {
		d := p.DefaultBody(mode)
		d.Pose.Position, d.Mass, d.AllowSleep, d.Continuous = pos, 20, false, true
		if mode == p.DrivenCharacter {
			d.Mass = 80
			d.Locks = p.MotionLocks{AngularX: true, AngularY: true, AngularZ: true}
		}
		body, err := w.CreateBody(d)
		if err != nil {
			t.Fatal(err)
		}
		cd := p.DefaultCollider(body, shape)
		cd.Material.Friction = .5
		if _, err := w.CreateCollider(cd); err != nil {
			t.Fatal(err)
		}
		return body
	}
	for tz := 0; tz < 2; tz++ {
		for tx := 0; tx < 5; tx++ {
			mesh := p.ShapeDesc{Kind: p.TriangleMesh, Vertices: make([]p.Vec3, (n+1)*(n+1))}
			for z := 0; z <= n; z++ {
				for x := 0; x <= n; x++ {
					mesh.Vertices[z*(n+1)+x] = p.Vec3{X: -128 + 256*(float32(tx)+float32(x)/n)/5, Z: -128 + 256*(float32(tz)+float32(z)/n)/2}
				}
			}
			for z := 0; z < n; z++ {
				for x := 0; x < n; x++ {
					a := uint32(z*(n+1) + x)
					mesh.Indices = append(mesh.Indices, a, a+n+1, a+1, a+1, a+n+1, a+n+2)
				}
			}
			ground, err := w.CreateShape(mesh)
			if err != nil {
				t.Fatal(err)
			}
			add(ground, p.Static, p.Vec3{})
		}
	}
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .4, Y: .5, Z: .4}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4990; i++ {
		add(shape, p.Static, p.Vec3{X: 128 * (.65 + .3*float32(i%71)/71), Y: .5, Z: 128 * (-.95 + 1.9*float32(i/71)/71)})
	}
	characters := make([]p.BodyHandle, n)
	for i := range characters {
		radius, height := float32(.4), float32(1.8)
		if i%2 == 1 {
			radius, height = .8, 2.4
		}
		shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Capsule, Radius: radius, PointA: p.Vec3{Y: radius}, PointB: p.Vec3{Y: height - radius}})
		if err != nil {
			t.Fatal(err)
		}
		characters[i] = add(shape, p.DrivenCharacter, p.Vec3{X: -96 + float32(i%10)*2.8, Z: float32(i/10-5) * 2.8})
	}
	bodies := make([]p.BodyHandle, n)
	for i := range bodies {
		bodies[i] = add(shape, p.FreeDynamic, p.Vec3{X: -64 + float32(i%10)*2.8, Y: 2 + float32(i%3), Z: 64 + float32(i/10)*1.1})
		if err := w.SetVelocity(bodies[i], p.Vec3{}, p.Vec3{X: .3, Y: .2, Z: .1}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i+1 < n; i += 10 {
		if _, err := w.CreateJoint(p.JointDesc{ID: p.ContentID(fmt.Sprintf("grid/%d", i)), Kind: p.Distance, A: bodies[i], B: bodies[i+1], FrameA: p.Identity(), FrameB: p.Identity(), Length: 3, Spring: true, Hertz: 2, Damping: .7}); err != nil {
			t.Fatal(err)
		}
	}
	options := b.DefaultMotionStepOptions()
	options.MaxMicrosteps = 100
	for frame := 1; frame <= 40; frame++ {
		steps := 0
		for sub := 0; sub < 4; sub++ {
			for _, handle := range characters {
				body := w.bodies[handle].id
				force := (3 - body.GetLinearVelocity().X) * 80 / .0125
				body.ApplyForceToCenter(b.Vec3{X: max(-1000, min(1000, force))}, true)
			}
			begin, end := float32(sub)*float32(.0125), float32(sub+1)*float32(.0125)
			if sub == 3 {
				end = .05
			}
			result, err := w.backend.StepMotion(end-begin, options, nil)
			if err != nil {
				t.Fatalf("frame%d sub%d work=%+v: %v", frame, sub, result, err)
			}
			steps += result.Microsteps
		}
		if steps > 30 {
			t.Logf("frame%d microsteps%d", frame, steps)
		}
	}
}
