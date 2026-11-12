package box3d_test

import (
	"fmt"
	"math"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	box "github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
)

func nativeWorld(t *testing.T, continuous bool) *box.World {
	t.Helper()
	c := p.DefaultConfig()
	c.Gravity, c.Continuous = p.Vec3{}, continuous
	w, err := box.New(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestNativeStepRetainsForceTorqueTimeAndPrivatePaths(t *testing.T) {
	for _, continuous := range []bool{false, true} {
		t.Run(fmt.Sprint(continuous), func(t *testing.T) {
			w := nativeWorld(t, continuous)
			body, _ := add(t, w, p.FreeDynamic, p.Vec3{Y: 10}, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
			old, _ := w.Body(body)
			if err := w.ApplyForce(body, p.Vec3{X: 10}, p.Vec3{Y: 10}); err != nil {
				t.Fatal(err)
			}
			if err := w.ApplyTorque(body, p.Vec3{Y: 1}); err != nil {
				t.Fatal(err)
			}
			linear, bounds := 0, 0
			motion := p.Motion{Body: body, Validate: func(from, to p.Vec3) bool { linear++; return true }, ValidateBounds: func(min, max p.Vec3) bool { bounds++; return true }}
			r, err := w.Simulate(p.Frame{ID: 1, Delta: .05, Motions: []p.Motion{motion}})
			if err != nil {
				t.Fatal(err)
			}
			if r.Elapsed != .05 || r.Substeps != 4 || r.Intervals != 4 || r.Microsteps != 4 || r.MotionPairTests != 0 || r.MotionPathRefinements != 0 || linear < 4 || bounds != 4 {
				t.Fatalf("native cadence/safety callbacks: result=%+v callbacks=%d/%d", r, linear, bounds)
			}
			if r.CCDCandidatePairs != 0 || r.CCDPairTests != 0 || r.CCDPredictiveContacts != 0 {
				t.Fatalf("isolated slow body reported pair work: %+v", r)
			}
			current, _ := w.Body(body)
			if current.Pose != old.Pose || current.LinearVelocity != old.LinearVelocity {
				t.Fatal("unaccepted frame published")
			}
			if len(r.Paths) != 1 || len(r.Paths[0].Poses) != 5 || len(r.Paths[0].Positions) != 5 || r.Paths[0].Fractions[0] != 0 || r.Paths[0].Fractions[4] != 1 {
				t.Fatalf("missing retained paths: %+v", r.Paths)
			}
			retained := append([]p.Pose(nil), r.Paths[0].Poses...)
			if err = w.Accept(1); err != nil {
				t.Fatal(err)
			}
			current, _ = w.Body(body)
			if math.Abs(float64(current.LinearVelocity.X-.5)) > 1e-5 || math.Abs(float64(current.AngularVelocity.Y-.5)) > 1e-5 {
				t.Fatalf("force/torque duration changed: %+v", current)
			}
			step(t, w, 2)
			for i, v := range retained {
				if r.Paths[0].Poses[i] != v {
					t.Fatal("retained sample aliases later frame")
				}
			}
		})
	}
}

func TestNativeStepCapturedSafetyFaultsBeforePublication(t *testing.T) {
	for _, continuous := range []bool{false, true} {
		for _, bounds := range []bool{false, true} {
			t.Run(fmt.Sprintf("continuous%t-bounds%t", continuous, bounds), func(t *testing.T) {
				w := nativeWorld(t, continuous)
				body, _ := add(t, w, p.FreeDynamic, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: .25})
				if err := w.SetVelocity(body, p.Vec3{X: 1}, p.Vec3{}); err != nil {
					t.Fatal(err)
				}
				old, _ := w.Body(body)
				m := p.Motion{Body: body}
				if bounds {
					m.ValidateBounds = func(min, max p.Vec3) bool { return max.X <= 0 }
				} else {
					m.Validate = func(from, to p.Vec3) bool { return to.X <= 0 }
				}
				r, err := w.Simulate(p.Frame{ID: 1, Delta: .05, Motions: []p.Motion{m}})
				if err == nil || r.Microsteps != 1 || r.Elapsed != .0125 {
					t.Fatalf("actual unsafe path was not counted and rejected: %+v error=%v", r, err)
				}
				if err := w.Accept(1); err == nil {
					t.Fatal("failed frame accepted")
				}
				state, _ := w.Body(body)
				if state.Pose != old.Pose {
					t.Fatal("failed navigation safety published body")
				}
			})
		}
	}
}

func TestNativeStepCrossingBodiesTransferMomentum(t *testing.T) {
	for _, radius := range []float32{.4, .05, .005} {
		for _, modes := range [][2]p.MotionMode{{p.FreeDynamic, p.FreeDynamic}, {p.DrivenCharacter, p.FreeDynamic}, {p.DrivenCharacter, p.DrivenCharacter}} {
			for _, bullets := range [][2]bool{{false, false}, {true, false}, {true, true}} {
				t.Run(fmt.Sprintf("radius%g-modes%v-bullets%v", radius, modes, bullets), func(t *testing.T) {
					w := nativeWorld(t, true)
					var bodies [2]p.BodyHandle
					for i := range bodies {
						d := p.DefaultBody(modes[i])
						d.Pose.Position.X, d.LinearVelocity.X = -2+float32(i)*4, 80-float32(i)*160
						d.Continuous, d.AllowSleep = bullets[i], false
						geometry := p.ShapeDesc{Kind: p.Sphere, Radius: radius}
						if modes[i] == p.DrivenCharacter {
							geometry.Kind, geometry.PointA.Y, geometry.PointB.Y = p.Capsule, -.5, .5
							d.Controller = p.DefaultCharacterController(p.DrivenCharacter)
							d.Controller.MaxDriveForce = 0
						}
						shape, err := w.CreateShape(geometry)
						if err != nil {
							t.Fatal(err)
						}
						bodies[i], err = w.CreateBody(d)
						if err != nil {
							t.Fatal(err)
						}
						if _, err = w.CreateCollider(p.DefaultCollider(bodies[i], shape)); err != nil {
							t.Fatal(err)
						}
					}
					r := step(t, w, 1)
					if r.CCDCandidatePairs == 0 || r.CCDPairTests == 0 || r.CCDPredictiveContacts == 0 {
						t.Fatalf("fast crossing omitted native CCD work: %+v", r)
					}
					a, _ := w.Body(bodies[0])
					z, _ := w.Body(bodies[1])
					force := float32(0)
					for _, event := range r.Events {
						if event.Pair.Kind == p.SolidContact {
							force += event.Force.Length()
						}
					}
					if a.Pose.Position.X >= z.Pose.Position.X || force == 0 || math.Abs(float64(a.LinearVelocity.X*a.Mass+z.LinearVelocity.X*z.Mass)) > .01 {
						t.Fatalf("moving-body transfer failed: a=%+v b=%+v force=%g", a, z, force)
					}
				})
			}
		}
	}
}

func TestNativeCCDWorkSurvivesCapturedSafetyFault(t *testing.T) {
	w := nativeWorld(t, true)
	left, _ := add(t, w, p.FreeDynamic, p.Vec3{X: -.2}, p.ShapeDesc{Kind: p.Sphere, Radius: .05})
	right, _ := add(t, w, p.FreeDynamic, p.Vec3{X: .2}, p.ShapeDesc{Kind: p.Sphere, Radius: .05})
	for body, velocity := range map[p.BodyHandle]float32{left: 80, right: -80} {
		if err := w.SetVelocity(body, p.Vec3{X: velocity}, p.Vec3{}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := w.Simulate(p.Frame{ID: 1, Delta: .05, Motions: []p.Motion{{Body: left, ValidateBounds: func(min, max p.Vec3) bool { return false }}}})
	if err == nil || r.Microsteps != 1 || r.CCDCandidatePairs == 0 || r.CCDPairTests == 0 || r.CCDPredictiveContacts == 0 {
		t.Fatalf("failed actual step omitted native work: %+v error=%v", r, err)
	}
	o := p.DefaultDebugOptions()
	o.Frame, o.IncludeUncommitted = &r, true
	capture, err := w.CaptureDebug(o)
	if err != nil {
		t.Fatal(err)
	}
	if capture.FrameAccepted || capture.Status != "faulted" || capture.CCDCandidatePairs != r.CCDCandidatePairs || capture.CCDPairTests != r.CCDPairTests || capture.CCDPredictiveContacts != r.CCDPredictiveContacts {
		t.Fatalf("diagnostic lost attempted native work: %+v", capture)
	}
}

func TestNativeCCDWorkSumsEveryObservedStep(t *testing.T) {
	run := func(substeps, frames int) [3]int {
		config := p.DefaultConfig()
		config.Gravity, config.Substeps = p.Vec3{}, substeps
		w, err := box.New(config)
		if err != nil {
			t.Fatal(err)
		}
		defer w.Close()
		for i := 0; i < 2; i++ {
			body, _ := add(t, w, p.FreeDynamic, p.Vec3{X: -2 + float32(i)*4}, p.ShapeDesc{Kind: p.Sphere, Radius: .05})
			if err := w.SetVelocity(body, p.Vec3{X: 80 - float32(i)*160}, p.Vec3{}); err != nil {
				t.Fatal(err)
			}
		}
		var totals [3]int
		for i := 1; i <= frames; i++ {
			r, err := w.Simulate(p.Frame{ID: uint64(i), Delta: .05 / float32(frames)})
			if err != nil {
				t.Fatal(err)
			}
			totals[0] += r.CCDCandidatePairs
			totals[1] += r.CCDPairTests
			totals[2] += r.CCDPredictiveContacts
			if err = w.Accept(uint64(i)); err != nil {
				t.Fatal(err)
			}
		}
		return totals
	}
	oneFrame, fourFrames := run(4, 1), run(1, 4)
	if oneFrame != fourFrames || oneFrame[0] == 0 || oneFrame[1] == 0 || oneFrame[2] == 0 {
		t.Fatalf("per-frame work lost intermediate native steps: one=%v four=%v", oneFrame, fourFrames)
	}
}
