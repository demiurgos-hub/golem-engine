package box3d

import (
	"fmt"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

// SetMotionPolicy changes body authority in place after validating the complete
// candidate. Body, collider and joint handles survive; no simulation time passes.
func (w *World) SetMotionPolicy(h p.BodyHandle, policy p.MotionPolicy) error {
	if err := w.guard(); err != nil {
		return err
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	v, err := w.findBody(h)
	if err != nil {
		return err
	}
	state := w.bodyState(h, v)
	d := v.desc
	d.Mode, d.Controller, d.Pose = policy.Mode, policy.Controller, state.Pose
	d.LinearVelocity, d.AngularVelocity = state.LinearVelocity, state.AngularVelocity
	if policy.Velocity == p.VelocityClear {
		d.LinearVelocity, d.AngularVelocity = p.Vec3{}, p.Vec3{}
	}
	if err := d.Validate(); err != nil {
		return err
	}
	if d.Mode == p.Static && (d.LinearVelocity != (p.Vec3{}) || d.AngularVelocity != (p.Vec3{})) ||
		d.Mode == p.DrivenCharacter && d.AngularVelocity != (p.Vec3{}) {
		return fmt.Errorf("%w: target policy cannot retain these velocities", p.ErrUnsupported)
	}
	for _, handle := range v.colliders {
		c := w.colliders[handle]
		if containsMesh(w.shapes[c.desc.Shape].desc) && d.Mode != p.Static &&
			(!w.config.Continuous || d.Mode == p.FreeDynamic || d.Mode == p.DrivenCharacter) {
			return fmt.Errorf("%w: moving mesh policy requires continuous kinematic motion", p.ErrUnsupported)
		}
	}
	if d.Controller.Enabled {
		if _, err := w.controllerGeometryFor(h, state.Pose, d.Controller); err != nil {
			return err
		}
	}
	contacts, err := w.policyContactCandidates(h)
	if err != nil {
		return err
	}

	typ := b.KinematicBody
	if d.Mode == p.Static {
		typ = b.StaticBody
	}
	if d.Mode == p.FreeDynamic || d.Mode == p.DrivenCharacter {
		typ = b.DynamicBody
	}
	v.id.SetType(typ)
	v.desc = d
	locks := b.MotionLocks(d.Locks)
	if d.Mode == p.DrivenCharacter {
		locks.AngularX, locks.AngularY, locks.AngularZ = true, true, true
	}
	v.id.SetMotionLocks(locks)
	v.id.EnableContactRecycling(d.Mode != p.DrivenCharacter)
	w.adjustMass(v)
	// Mass/COM and lock changes can alter velocity; apply the explicit disposition
	// last so retention means the same world COM velocity, not the same origin speed.
	v.id.SetLinearVelocity(bv(d.LinearVelocity))
	v.id.SetAngularVelocity(bv(d.AngularVelocity))
	v.frozen = false
	if d.Mode != p.Static || d.Owner != 0 {
		w.activeBodies[h] = struct{}{}
	} else {
		delete(w.activeBodies, h)
	}
	v.support = p.CharacterSupport{}
	v.fallSpeed = 0
	if d.Controller.Enabled && policy.Velocity == p.VelocityRetain {
		v.fallSpeed = d.LinearVelocity.Y
	}
	if len(contacts) > 0 && w.policyContacts == nil {
		w.policyContacts = map[p.ContactPair]struct{}{}
	}
	for _, pair := range contacts {
		w.policyContacts[pair] = struct{}{}
	}
	w.changed()
	return nil
}

var _ p.MotionPolicyWorld = (*World)(nil)
