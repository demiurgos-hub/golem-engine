package box3d

import p "github.com/demiurgos-hub/golem-engine/golem/physics3d"

// SetOwner binds a previously unowned body to an entity. Collider owners that
// were inherited from that body follow the binding; explicit other owners remain.
// Metadata publishes immediately with a new revision and does not dirty geometry.
func (w *World) SetOwner(h p.BodyHandle, owner int64) error {
	if err := w.guard(); err != nil {
		return err
	}
	if owner < 0 {
		return p.ErrInvalid
	}
	v, err := w.findBody(h)
	if err != nil {
		return err
	}
	previous := v.desc.Owner
	v.desc.Owner = owner
	if v.desc.Mode != p.Static || owner != 0 {
		w.activeBodies[h] = struct{}{}
	} else {
		delete(w.activeBodies, h)
	}
	for _, ch := range v.colliders {
		c := w.colliders[ch]
		if c.desc.Owner == previous {
			c.desc.Owner = owner
		}
	}
	v.committed.Owner = owner
	w.sensorReplay.ready = false
	w.mutation++
	w.revision++
	return nil
}

var _ p.OwnerBinder = (*World)(nil)
