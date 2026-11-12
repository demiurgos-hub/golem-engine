package content

import (
	"fmt"
	"math"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func jointDescription(j Joint) (p.JointDesc, error) {
	unsupported := func(field string) (p.JointDesc, error) {
		return p.JointDesc{}, fmt.Errorf("%w: joint %s %s", p.ErrUnsupported, j.ID, field)
	}
	if j.MassScale != 1 || j.ConnectedMassScale != 1 {
		return unsupported("per-joint mass scaling")
	}
	if j.BreakForce != nil || j.BreakTorque != nil {
		return unsupported("break thresholds")
	}
	if j.EnableProjection || !j.EnablePreprocessing {
		return unsupported("projection/preprocessing override")
	}
	if j.UseSpring || j.Kind == "spring" {
		return unsupported("physical stiffness/damping awaiting effective-mass spring support")
	}
	if j.UseMotor && j.Motor.FreeSpin {
		return unsupported("free-spin motor")
	}
	if j.UseLimits || j.Kind == "character" {
		for _, limit := range []JointLimit{j.LowTwist, j.HighTwist, j.Swing1, j.Swing2} {
			if limit.Bounciness != 0 || limit.BounceMinVelocity != 0 || limit.ContactDistance != 0 || limit.Spring != 0 || limit.Damper != 0 {
				return unsupported("limit bounce, margin or spring")
			}
		}
	}
	rotation := axisRotation(j.Axis)
	d := p.JointDesc{ID: p.ContentID(j.ID), FrameA: p.Pose{Position: j.Anchor, Rotation: rotation},
		FrameB: p.Pose{Position: j.ConnectedAnchor, Rotation: j.ReferenceRotation.Mul(rotation)}, CollideConnected: j.EnableCollision,
		TargetRotation: p.Quat{W: 1}, Limit: j.UseLimits, Drive: j.UseMotor}
	const degreesToRadians float32 = math.Pi / 180
	switch j.Kind {
	case "fixed":
		d.Kind = p.Weld
	case "hinge":
		d.Kind = p.Hinge
		d.Lower, d.Upper = j.LowTwist.Limit*degreesToRadians, j.HighTwist.Limit*degreesToRadians
		d.MaxTorque, d.Speed = j.Motor.Force, j.Motor.TargetVelocity*degreesToRadians
	case "character":
		if j.Swing1.Limit != j.Swing2.Limit {
			return unsupported("asymmetric swing cone")
		}
		d.Kind = p.Spherical
		d.ConeLimit, d.TwistLimit = true, true
		d.ConeAngle = j.Swing1.Limit * degreesToRadians
		d.LowerTwist, d.UpperTwist = j.LowTwist.Limit*degreesToRadians, j.HighTwist.Limit*degreesToRadians
	default:
		return unsupported("kind")
	}
	return d, nil
}

func axisRotation(axis p.Vec3) p.Quat {
	axis = axis.Unit()
	if axis.Z < -0.999999 {
		return p.Quat{X: 1}
	}
	cross := (p.Vec3{Z: 1}).Cross(axis)
	w := 1 + axis.Z
	length := float32(math.Sqrt(float64(cross.Dot(cross) + w*w)))
	return p.Quat{X: cross.X / length, Y: cross.Y / length, Z: cross.Z / length, W: w / length}
}
