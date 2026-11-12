package golem

import (
	"fmt"
	"math"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// SetPhysicsMotionPolicy atomically changes an attachment's body mode and
// controller policy without recreating it. Velocity retention/clearing is
// explicit. Success invalidates prior trajectory tokens without rewinding poses
// or removing transferred momentum. Call on the tick owner outside frames and
// contact callbacks; QueuePhysicsMotionPolicy defers callback requests.
func (s *Server) SetPhysicsMotionPolicy(id int64, policy p.MotionPolicy) error {
	ps := s.physics
	if err := ps.mutable(); err != nil {
		return err
	}
	if s.navigation != nil && s.navigation.frame != nil {
		return p.ErrBusy
	}
	a := ps.agents[id]
	if a == nil {
		return p.ErrStale
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	capability, ok := ps.world.(p.MotionPolicyWorld)
	if !ok {
		return p.ErrUnsupported
	}
	if ps.trajectoryGeneration == math.MaxUint64 || a.policyGeneration == math.MaxUint64 {
		return p.ErrInvalid
	}
	if n := s.navigation; n != nil {
		if agent := n.agents[id]; agent != nil && (!physicsNavigationMode(policy.Mode) || agent.state.HasDestination && policy.Mode == p.FreeDynamic) {
			return fmt.Errorf("%w: motion policy conflicts with navigation attachment or intent", p.ErrUnsupported)
		}
	}
	if err := capability.SetMotionPolicy(a.body, policy); err != nil {
		return err
	}
	ps.trajectoryGeneration++
	a.policyGeneration++
	delete(ps.trajectories, id)
	if err := ps.world.Synchronize(); err != nil {
		ps.fault = err
		return err
	}
	state, err := ps.world.Body(a.body)
	if err != nil || !validPhysicsState(state) || state.Mode != policy.Mode {
		ps.fault = fmt.Errorf("%w: invalid motion-policy readback: %v", p.ErrFaulted, err)
		return ps.fault
	}
	ps.write(a, state, true)
	return nil
}

// QueuePhysicsMotionPolicy schedules a copied policy for the next tick. A
// removed/replaced attachment or intervening policy change makes it a no-op;
// invalid or unsupported policies otherwise follow queued-command error handling.
// This method is tick-owned and is safe inside accepted contact callbacks.
func (s *Server) QueuePhysicsMotionPolicy(id int64, policy p.MotionPolicy) error {
	ps := s.physics
	if ps == nil || ps.agents[id] == nil {
		return p.ErrStale
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	a := ps.agents[id]
	generation := a.policyGeneration
	return s.QueuePhysicsCommand(func(p.World) error {
		if ps.agents[id] != a || a.policyGeneration != generation {
			return nil
		}
		return s.SetPhysicsMotionPolicy(id, policy)
	})
}
