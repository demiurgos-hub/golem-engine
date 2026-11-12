package physics3d

// CharacterControllerConfig opts a body's sole solid upright capsule into
// explicit physical movement policy. Dimensions come from collider geometry.
// Kinematic/scripted modes can step using retained swept phases; driven bodies
// keep finite mass, gravity and contact impulses and require StepHeight zero.
// Controller rotation is pose-owned: nonzero angular velocities and tilted
// capsule geometry are unsupported. Following support transports position; it
// does not rotate the upright character with its platform.
type CharacterControllerConfig struct {
	Velocity                                                            Vec3 // retained physical intent when a frame provides no Motion for this body
	MaxDriveForce                                                       float32
	Enabled, FollowSupport, Gravity                                     bool // Gravity applies to kinematic/scripted falling; driven bodies use BodyDesc.GravityScale
	StepHeight, GroundDistance, MaxSlopeDegrees, Skin, MaxDepenetration float32
	MaxSupportRotation                                                  float32
	SupportPenetration                                                  float32 // physical support tolerance; never changes query geometry
	Iterations, MaxHits                                                 int
}

// DefaultCharacterController returns explicit capsule movement policy for mode.
func DefaultCharacterController(mode MotionMode) CharacterControllerConfig {
	c := CharacterControllerConfig{Enabled: true, FollowSupport: true, Gravity: true, GroundDistance: .05, MaxSlopeDegrees: 45, Skin: .001, MaxDepenetration: .5, MaxSupportRotation: .005, SupportPenetration: .01, Iterations: 8, MaxHits: 4096, MaxDriveForce: 1000}
	if mode == Kinematic || mode == ScriptedAction {
		c.StepHeight = .3
	}
	return c
}

// Validate checks controller work limits and physical authority compatibility.
func (c CharacterControllerConfig) Validate(mode MotionMode) error {
	if !c.Enabled {
		return nil
	}
	if !c.Velocity.Valid() || !Finite(c.MaxDriveForce) || c.MaxDriveForce < 0 {
		return ErrInvalid
	}
	if mode != Kinematic && mode != ScriptedAction && mode != DrivenCharacter {
		return ErrUnsupported
	}
	for _, v := range []float32{c.StepHeight, c.GroundDistance, c.MaxSlopeDegrees, c.Skin, c.MaxDepenetration, c.MaxSupportRotation, c.SupportPenetration} {
		if !Finite(v) || v < 0 {
			return ErrInvalid
		}
	}
	if c.MaxSlopeDegrees >= 90 || c.StepHeight > 2 || c.GroundDistance > 1 || c.MaxDepenetration > 1 || c.MaxSupportRotation <= 0 || c.MaxSupportRotation > .05 || c.SupportPenetration > .05 || c.Iterations < 1 || c.Iterations > 64 || c.MaxHits < 1 || c.MaxHits > 65536 || mode == DrivenCharacter && c.StepHeight != 0 {
		return ErrInvalid
	}
	return nil
}

// CharacterVelocityWriter optionally changes retained controller intent at a
// tick boundary. It never advances time or changes accepted body/entity poses.
type CharacterVelocityWriter interface{ SetCharacterVelocity(BodyHandle, Vec3) error }

// CharacterSupport identifies current nearby physical support. LocalPoint belongs
// to the support body origin, never its centre of mass. Handles are ephemeral and
// must be re-probed after checkpoint restore, removal or generation replacement.
type CharacterSupport struct {
	Grounded                  bool
	Collider                  ColliderHandle
	Body                      BodyHandle
	Feature                   Feature
	Point, LocalPoint, Normal Vec3
}
