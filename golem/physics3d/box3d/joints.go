package box3d

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
	"math"
)

// CreateJoint creates a constraint in body-origin frames. Body geometry must be
// assembled before joints so a later centre-of-mass change cannot corrupt frames.
func (w *World) CreateJoint(d p.JointDesc) (p.JointHandle, error) {
	if err := w.guard(); err != nil {
		return p.JointHandle{}, err
	}
	a, err := w.findBody(d.A)
	if err != nil {
		return p.JointHandle{}, err
	}
	z, err := w.findBody(d.B)
	if err != nil {
		return p.JointHandle{}, err
	}
	if d.A == d.B || !d.FrameA.Valid() || !d.FrameB.Valid() || d.Kind < p.Weld || d.Kind > p.Wheel || !d.LinearVelocity.Valid() || !d.AngularVelocity.Valid() {
		return p.JointHandle{}, p.ErrInvalid
	}
	for _, x := range []float32{d.Length, d.Target, d.Lower, d.Upper, d.Hertz, d.Damping, d.MaxForce, d.MaxTorque, d.Speed, d.ConeAngle, d.LowerTwist, d.UpperTwist, d.SteeringAngle, d.SteeringHertz, d.SteeringDamping, d.MaxSteeringTorque} {
		if !p.Finite(x) {
			return p.JointHandle{}, p.ErrInvalid
		}
	}
	if d.Length < 0 || d.Hertz < 0 || d.Damping < 0 || d.MaxForce < 0 || d.MaxTorque < 0 || d.Limit && d.Lower > d.Upper || d.Kind == p.Hinge && d.Limit && (d.Lower < -.99*math.Pi || d.Upper > .99*math.Pi) || d.ConeLimit && (d.ConeAngle < 0 || d.ConeAngle > math.Pi) || d.TwistLimit && (d.LowerTwist > d.UpperTwist || d.LowerTwist < -.99*math.Pi || d.UpperTwist > .99*math.Pi) {
		return p.JointHandle{}, p.ErrInvalid
	}
	base := b.DefaultWeldJointDef().Base
	base.BodyIdA = a.id
	base.BodyIdB = z.id
	base.LocalFrameA = bt(d.FrameA)
	base.LocalFrameB = bt(d.FrameB)
	base.CollideConnected = d.CollideConnected
	if d.Kind == p.Spherical && !d.TargetRotation.Valid() {
		return p.JointHandle{}, p.ErrInvalid
	}
	identity, err := w.claimHandle()
	if err != nil {
		return p.JointHandle{}, err
	}
	var id b.JointID
	switch d.Kind {
	case p.Weld:
		x := b.DefaultWeldJointDef()
		x.Base = base
		x.LinearHertz = d.Hertz
		x.AngularHertz = d.Hertz
		x.LinearDampingRatio = d.Damping
		x.AngularDampingRatio = d.Damping
		id = w.backend.CreateWeldJoint(&x)
	case p.Distance:
		x := b.DefaultDistanceJointDef()
		x.Base = base
		x.Length = d.Length
		x.EnableSpring = d.Spring
		x.Hertz = d.Hertz
		x.DampingRatio = d.Damping
		x.EnableLimit = d.Limit
		x.MinLength = d.Lower
		x.MaxLength = d.Upper
		x.EnableMotor = d.Drive
		x.MaxMotorForce = d.MaxForce
		x.MotorSpeed = d.Speed
		id = w.backend.CreateDistanceJoint(&x)
	case p.Hinge:
		x := b.DefaultRevoluteJointDef()
		x.Base = base
		x.TargetAngle = d.Target
		x.EnableSpring = d.Spring
		x.Hertz = d.Hertz
		x.DampingRatio = d.Damping
		x.EnableLimit = d.Limit
		x.LowerAngle = d.Lower
		x.UpperAngle = d.Upper
		x.EnableMotor = d.Drive
		x.MaxMotorTorque = d.MaxTorque
		x.MotorSpeed = d.Speed
		id = w.backend.CreateRevoluteJoint(&x)
	case p.Slider:
		x := b.DefaultPrismaticJointDef()
		x.Base = base
		x.TargetTranslation = d.Target
		x.EnableSpring = d.Spring
		x.Hertz = d.Hertz
		x.DampingRatio = d.Damping
		x.EnableLimit = d.Limit
		x.LowerTranslation = d.Lower
		x.UpperTranslation = d.Upper
		x.EnableMotor = d.Drive
		x.MaxMotorForce = d.MaxForce
		x.MotorSpeed = d.Speed
		id = w.backend.CreatePrismaticJoint(&x)
	case p.Spherical:
		x := b.DefaultSphericalJointDef()
		x.Base = base
		x.EnableSpring = d.Spring
		x.Hertz = d.Hertz
		x.DampingRatio = d.Damping
		x.TargetRotation = bq(d.TargetRotation)
		x.EnableConeLimit = d.ConeLimit
		x.ConeAngle = d.ConeAngle
		x.EnableTwistLimit = d.TwistLimit
		x.LowerTwistAngle = d.LowerTwist
		x.UpperTwistAngle = d.UpperTwist
		x.EnableMotor = d.Drive
		x.MaxMotorTorque = d.MaxTorque
		x.MotorVelocity = bv(d.AngularVelocity)
		id = w.backend.CreateSphericalJoint(&x)
	case p.Motor:
		x := b.DefaultMotorJointDef()
		x.Base = base
		x.LinearVelocity = bv(d.LinearVelocity)
		x.AngularVelocity = bv(d.AngularVelocity)
		x.MaxVelocityForce = d.MaxForce
		x.MaxVelocityTorque = d.MaxTorque
		x.LinearHertz = d.Hertz
		x.AngularHertz = d.Hertz
		x.LinearDampingRatio = d.Damping
		x.AngularDampingRatio = d.Damping
		x.MaxSpringForce = d.MaxForce
		x.MaxSpringTorque = d.MaxTorque
		id = w.backend.CreateMotorJoint(&x)
	case p.Upright:
		x := b.DefaultParallelJointDef()
		x.Base = base
		x.Hertz = d.Hertz
		x.DampingRatio = d.Damping
		x.MaxTorque = d.MaxTorque
		id = w.backend.CreateParallelJoint(&x)
	case p.Wheel:
		x := b.DefaultWheelJointDef()
		x.Base = base
		x.EnableSuspensionSpring = d.Spring
		x.SuspensionHertz = d.Hertz
		x.SuspensionDampingRatio = d.Damping
		x.EnableSuspensionLimit = d.Limit
		x.LowerSuspensionLimit = d.Lower
		x.UpperSuspensionLimit = d.Upper
		x.EnableSpinMotor = d.Drive
		x.MaxSpinTorque = d.MaxTorque
		x.SpinSpeed = d.Speed
		x.EnableSteering = d.Steering
		x.TargetSteeringAngle = d.SteeringAngle
		x.SteeringHertz = d.SteeringHertz
		x.SteeringDampingRatio = d.SteeringDamping
		x.MaxSteeringTorque = d.MaxSteeringTorque
		id = w.backend.CreateWheelJoint(&x)
	}
	h := p.JointHandle(identity)
	w.joints[h] = &joint{id: id, desc: d}
	w.changed()
	return h, nil
}

// DestroyJoint removes a live constraint and wakes its attached bodies.
func (w *World) DestroyJoint(h p.JointHandle) error {
	if err := w.guard(); err != nil {
		return err
	}
	v, ok := w.joints[h]
	if !ok {
		return p.ErrStale
	}
	v.id.Destroy(true)
	delete(w.joints, h)
	w.releaseHandle(p.Handle(h))
	w.changed()
	return nil
}

// Joint returns current constraint force and torque in world space.
func (w *World) Joint(h p.JointHandle) (p.JointState, error) {
	v, ok := w.joints[h]
	if !ok {
		return p.JointState{}, p.ErrStale
	}
	return p.JointState{Handle: h, ID: v.desc.ID, Force: pv(v.id.GetConstraintForce()), Torque: pv(v.id.GetConstraintTorque())}, nil
}
