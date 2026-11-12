package box3d_test

import (
	"errors"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func TestMotionPolicyPreservesHandlesJointMassAndCheckpoint(t *testing.T) {
	w := motionWorld(t)
	h, collider := capsuleController(t, w, p.DrivenCharacter, p.Vec3{Y: 10})
	d := p.DefaultBody(p.Static)
	d.ID = "anchor"
	d.Pose.Position.Y = 12
	anchor, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	joint, err := w.CreateJoint(p.JointDesc{ID: "joint", Kind: p.Distance, A: anchor, B: h, FrameA: p.Identity(), FrameB: p.Identity(), Length: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err = w.SetVelocity(h, p.Vec3{X: 3, Y: 2}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	before, _ := w.Body(h)
	policy := p.MotionPolicy{Mode: p.ScriptedAction, Controller: p.DefaultCharacterController(p.ScriptedAction), Velocity: p.VelocityRetain}
	policy.Controller.Velocity.Z = 4
	if err = w.SetMotionPolicy(h, policy); err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	after, _ := w.Body(h)
	if after.Handle != before.Handle || after.Pose != before.Pose || after.Anchor != before.Anchor || after.LinearVelocity != before.LinearVelocity || after.Mode != p.ScriptedAction {
		t.Fatalf("in-place policy lost identity/state: %+v", after)
	}
	if _, err = w.Joint(joint); err != nil {
		t.Fatal("joint replaced", err)
	}
	checkpoint, err := w.CaptureCheckpoint("policy", "source")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, record := range checkpoint.Bodies {
		if record.Description.ID == "controller" {
			found = true
			if record.Description.Controller != policy.Controller || record.Description.LinearVelocity != before.LinearVelocity || record.ControllerFallSpeed != 2 {
				t.Fatalf("policy checkpoint lost intent: %+v", record)
			}
		}
	}
	if !found || checkpoint.Colliders[0].ID != "capsule-collider" {
		t.Fatal("stable content lost")
	}
	policy.Mode, policy.Controller, policy.Velocity = p.DrivenCharacter, p.DefaultCharacterController(p.DrivenCharacter), p.VelocityClear
	if err = w.SetMotionPolicy(h, policy); err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	after, _ = w.Body(h)
	if after.LinearVelocity != (p.Vec3{}) || after.Mass != before.Mass {
		t.Fatalf("clear/mass restoration failed: %+v", after)
	}
	if _, err = w.Joint(joint); err != nil {
		t.Fatal(err)
	}
	step(t, w, 1)
	if err = w.DestroyJoint(joint); err != nil {
		t.Fatal(err)
	}
	if err = w.DestroyCollider(collider, p.Deleted); err != nil {
		t.Fatal("collider replaced", err)
	}
	prepared, err := w.PrepareRestore(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Discard()
	restored, ok := prepared.Body("controller")
	if !ok {
		t.Fatal("restored body missing")
	}
	if restored.Mode != p.ScriptedAction || restored.LinearVelocity != before.LinearVelocity {
		t.Fatal("restored policy or velocity lost")
	}
}

func TestMotionPolicyRestoresAuthoredMassPropertiesAndQueuedForces(t *testing.T) {
	w := motionWorld(t)
	sh, err := w.CreateShape(p.ShapeDesc{ID: "mass-shape", Kind: p.Box, HalfExtents: p.Vec3{X: .5, Y: .5, Z: .5}})
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultBody(p.FreeDynamic)
	d.ID, d.Mass, d.ManualCenterOfMass, d.ManualInertia = "mass-body", 7, true, true
	d.CenterOfMass, d.InertiaTensor, d.InertiaRotation = p.Vec3{X: .1}, p.Vec3{X: 1, Y: 2, Z: 3}, p.Identity().Rotation
	d.Anchor.Position.Y = -.7
	h, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	c := p.DefaultCollider(h, sh)
	c.ID = "mass-collider"
	if _, err = w.CreateCollider(c); err != nil {
		t.Fatal(err)
	}
	linear, angular := p.Vec3{X: 2}, p.Vec3{Z: 2}
	if err = w.SetVelocity(h, linear, angular); err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	before, _ := w.Body(h)
	for _, mode := range []p.MotionMode{p.Kinematic, p.FreeDynamic} {
		if err = w.SetMotionPolicy(h, p.MotionPolicy{Mode: mode, Velocity: p.VelocityRetain}); err != nil {
			t.Fatal(err)
		}
		if err = w.Synchronize(); err != nil {
			t.Fatal(err)
		}
		state, _ := w.Body(h)
		if state.Pose != before.Pose || state.Anchor != before.Anchor || state.LinearVelocity != linear || state.AngularVelocity != angular {
			t.Fatalf("COM-relative velocity changed: %+v", state)
		}
	}
	after, _ := w.Body(h)
	if after.Mass != 7 || after.CenterOfMass != before.CenterOfMass {
		t.Fatal("authored mass/COM not restored", after)
	}
	if err = w.ApplyForce(h, p.Vec3{X: 14}, after.CenterOfMass); err != nil {
		t.Fatal(err)
	}
	if err = w.SetMotionPolicy(h, p.MotionPolicy{Mode: p.FreeDynamic, Velocity: p.VelocityClear}); err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := w.CaptureCheckpoint("mass-policy", "mass-source")
	if err != nil {
		t.Fatal(err)
	}
	if record := checkpoint.Bodies[0]; !record.Description.ManualInertia || record.Description.InertiaTensor != d.InertiaTensor || len(record.Forces) != 1 {
		t.Fatal("policy lost authored inertia/queued force", record)
	}
	step(t, w, 1)
	after, _ = w.Body(h)
	if after.LinearVelocity.X < .099 || after.LinearVelocity.X > .101 {
		t.Fatal("policy discarded/multiplied queued force", after.LinearVelocity)
	}
	if err = w.SetEnabled(h, false); err != nil {
		t.Fatal(err)
	}
	if err = w.SetMotionPolicy(h, p.MotionPolicy{Mode: p.Kinematic, Velocity: p.VelocityClear}); err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	after, _ = w.Body(h)
	if after.Enabled {
		t.Fatal("transition activated disabled body")
	}
}

func TestMotionPolicyRejectionIsAtomic(t *testing.T) {
	w := motionWorld(t)
	h, _ := capsuleController(t, w, p.Kinematic, p.Vec3{Y: 10})
	if err := w.SetVelocity(h, p.Vec3{X: 2}, p.Vec3{}); err != nil {
		t.Fatal(err)
	}
	if err := w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	before, _ := w.Body(h)
	invalid := []p.MotionPolicy{
		{Mode: p.ScriptedAction},
		{Mode: p.ExternalPose, Velocity: p.VelocityRetain},
		{Mode: p.Static, Velocity: p.VelocityRetain},
		{Mode: p.DrivenCharacter, Controller: p.DefaultCharacterController(p.Kinematic), Velocity: p.VelocityClear},
	}
	for _, policy := range invalid {
		if err := w.SetMotionPolicy(h, policy); err == nil {
			t.Fatalf("accepted invalid policy: %+v", policy)
		}
		if err := w.Synchronize(); err != nil {
			t.Fatal(err)
		}
		got, _ := w.Body(h)
		if got != before {
			t.Fatalf("rejection changed state: %+v", got)
		}
	}
	if _, err := w.Simulate(p.Frame{ID: 1, Delta: .05}); err != nil {
		t.Fatal(err)
	}
	valid := p.MotionPolicy{Mode: p.ScriptedAction, Velocity: p.VelocityClear}
	if err := w.SetMotionPolicy(h, valid); !errors.Is(err, p.ErrBusy) {
		t.Fatal("pending transition allowed", err)
	}
	if err := w.Reject(1, errors.New("fixture")); err != nil {
		t.Fatal(err)
	}
	if err := w.SetMotionPolicy(h, valid); !errors.Is(err, p.ErrFaulted) {
		t.Fatal("faulted transition allowed", err)
	}
}

func TestMotionPolicyRejectsGeometryAndRetainedSpin(t *testing.T) {
	w := motionWorld(t)
	mesh, _ := add(t, w, p.Static, p.Vec3{}, motionFloor())
	if err := w.SetMotionPolicy(mesh, p.MotionPolicy{Mode: p.FreeDynamic, Velocity: p.VelocityClear}); !errors.Is(err, p.ErrUnsupported) {
		t.Fatal(err)
	}
	if err := w.SetMotionPolicy(mesh, p.MotionPolicy{Mode: p.Kinematic, Velocity: p.VelocityClear}); err != nil {
		t.Fatal(err)
	}
	if err := w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	step(t, w, 1)
	h, _ := add(t, w, p.FreeDynamic, p.Vec3{Y: 5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: .5, Y: .5, Z: .5}})
	controller := p.MotionPolicy{Mode: p.DrivenCharacter, Controller: p.DefaultCharacterController(p.DrivenCharacter), Velocity: p.VelocityClear}
	if err := w.SetMotionPolicy(h, controller); !errors.Is(err, p.ErrUnsupported) {
		t.Fatal("box controller allowed", err)
	}
	capsule, _ := add(t, w, p.FreeDynamic, p.Vec3{Y: 10}, p.ShapeDesc{Kind: p.Capsule, Radius: .4, PointA: p.Vec3{Y: -.5}, PointB: p.Vec3{Y: .5}})
	if err := w.SetVelocity(capsule, p.Vec3{}, p.Vec3{Y: 1}); err != nil {
		t.Fatal(err)
	}
	controller.Velocity = p.VelocityRetain
	if err := w.SetMotionPolicy(capsule, controller); !errors.Is(err, p.ErrUnsupported) {
		t.Fatal("controller retained spin", err)
	}
	pose := p.Identity()
	pose.Position.Y = 10
	pose.Rotation = p.Quat{Z: .70710677, W: .70710677}
	if err := w.SetPose(capsule, pose); err != nil {
		t.Fatal(err)
	}
	controller.Velocity = p.VelocityClear
	if err := w.SetMotionPolicy(capsule, controller); !errors.Is(err, p.ErrUnsupported) {
		t.Fatal("tilted capsule allowed", err)
	}
}

func TestMotionPolicyContactContinuityAndTransactionalCleanup(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "accept", true: "reject"}[reject], func(t *testing.T) {
			w := motionWorld(t)
			_, floor := add(t, w, p.Static, p.Vec3{Y: -.5}, p.ShapeDesc{Kind: p.Box, HalfExtents: p.Vec3{X: 5, Y: .5, Z: 5}})
			h, collider := add(t, w, p.FreeDynamic, p.Vec3{Y: .5}, p.ShapeDesc{Kind: p.Sphere, Radius: .5})
			step(t, w, 1)
			if err := w.SetMotionPolicy(h, p.MotionPolicy{Mode: p.ScriptedAction, Velocity: p.VelocityClear}); err != nil {
				t.Fatal(err)
			}
			r := step(t, w, 2)
			stays := 0
			for _, e := range r.Events {
				if e.Pair.Kind == p.SolidContact {
					if e.Phase != p.Stay || e.Force != (p.Vec3{}) {
						t.Fatalf("policy fabricated lifecycle/force: %+v", e)
					}
					stays++
				}
			}
			if stays == 0 {
				t.Fatal("accepted solid pair lost")
			}
			state, _ := w.Body(h)
			away, back := state.Pose, state.Pose
			away.Position.Y += 1
			// The requested path returns, but kinematic admission correctly stops
			// short of new contact. Continuity must end at the real separation.
			r, err := w.Simulate(p.Frame{ID: 3, Delta: .05, Motions: []p.Motion{{Body: h, PoseSegments: []p.PoseMotionSegment{{From: back, Target: away, Delta: .025}, {From: away, Target: back, Delta: .025}}}}})
			if err != nil {
				t.Fatal(err)
			}
			var transitions []p.ContactPhase
			for _, e := range r.Events {
				if e.Pair.Kind == p.SolidContact && e.Phase != p.Stay {
					transitions = append(transitions, e.Phase)
				}
			}
			if len(transitions) != 1 || transitions[0] != p.Exit {
				t.Fatalf("path transitions lost: %v", transitions)
			}
			if reject {
				if err = w.Reject(3, errors.New("fixture")); err != nil {
					t.Fatal(err)
				}
				events, err := w.CloseWithEvents()
				if err != nil {
					t.Fatal(err)
				}
				if len(events) != 1 || events[0].Phase != p.Exit {
					t.Fatalf("rejected contact history leaked: %v", events)
				}
				return
			}
			if err = w.Accept(3); err != nil {
				t.Fatal(err)
			}
			freshShape, err := w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: .5})
			if err != nil {
				t.Fatal(err)
			}
			freshDesc := p.DefaultBody(p.Static)
			freshDesc.Pose.Position.Y = 3
			freshBody, err := w.CreateBody(freshDesc)
			if err != nil {
				t.Fatal(err)
			}
			if err = w.DestroyCollider(collider, p.Deleted); err != nil {
				t.Fatal(err)
			}
			fresh, err := w.CreateCollider(p.DefaultCollider(freshBody, freshShape))
			if err != nil {
				t.Fatal(err)
			}
			if fresh.Index != collider.Index || fresh.Generation == collider.Generation {
				t.Fatal("fixture did not reuse collider slot")
			}
			r = step(t, w, 4)
			for _, e := range r.Events {
				if e.Pair.A == fresh || e.Pair.B == fresh {
					t.Fatal("policy history followed reused slot", e)
				}
			}
			_ = floor
		})
	}
}
