package physics3d

// PolicyVelocity explicitly selects what happens to the body's current COM
// linear and angular velocity. The zero value is invalid.
type PolicyVelocity uint8

const (
	VelocityRetain PolicyVelocity = iota + 1
	VelocityClear
)

// MotionPolicy replaces motion authority and the entire optional controller
// configuration together. Controller.Velocity is new intent, independent of
// Velocity's treatment of existing physical momentum. Geometry, pose, authored
// mass properties, forces, activation, ownership and handles remain unchanged.
type MotionPolicy struct {
	Mode       MotionMode
	Controller CharacterControllerConfig
	Velocity   PolicyVelocity
}

// Validate checks the policy independently of existing geometry and momentum.
func (p MotionPolicy) Validate() error {
	if p.Mode > ExternalPose || p.Velocity != VelocityRetain && p.Velocity != VelocityClear {
		return ErrInvalid
	}
	return p.Controller.Validate(p.Mode)
}

// MotionPolicyWorld optionally changes a live body's policy atomically at a
// mutation boundary. Rejection leaves all state intact. Implementations reject
// incompatible geometry or retained velocities rather than silently clearing
// them. Synchronize publishes success without advancing time. Existing accepted
// contacts retain their identity while the actual geometry continues touching.
type MotionPolicyWorld interface {
	SetMotionPolicy(BodyHandle, MotionPolicy) error
}
