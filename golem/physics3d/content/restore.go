package content

import (
	"bytes"
	"fmt"
	"sort"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// InstanceState preserves source ownership without persisting runtime handles.
// Physical pose/velocity state belongs to the paired world checkpoint.
type InstanceState struct {
	Fingerprint string
	Source      []byte
	Settings    Settings
}

// ContentIDs contains the stable identities owned by one complete source lease.
// Identity namespaces are independent; an ID may occur in different slices.
type ContentIDs struct {
	Bodies, Shapes, Colliders, Joints []p.ContentID
}

// ContentIDs returns copied ownership identities without copying source geometry.
func (prepared *Prepared) ContentIDs() ContentIDs {
	var ids ContentIDs
	if prepared == nil {
		return ids
	}
	for _, body := range prepared.source.Bodies {
		ids.Bodies = append(ids.Bodies, p.ContentID(body.ID))
	}
	for _, collider := range prepared.source.Colliders {
		ids.Colliders = append(ids.Colliders, p.ContentID(collider.ID))
		ids.Shapes = append(ids.Shapes, p.ContentID(collider.ID+"/geometry"))
	}
	anchor := false
	for _, joint := range prepared.source.Joints {
		if joint.Active {
			ids.Joints = append(ids.Joints, p.ContentID(joint.ID))
			anchor = anchor || joint.ConnectedBodyID == ""
		}
	}
	if anchor {
		ids.Bodies = append(ids.Bodies, prepared.worldAnchorID())
	}
	return ids
}

// BodyIDs returns copied stable body identities without copying source geometry.
func (prepared *Prepared) BodyIDs() []string {
	if prepared == nil {
		return nil
	}
	ids := make([]string, 0, len(prepared.source.Bodies))
	for _, body := range prepared.source.Bodies {
		ids = append(ids, body.ID)
	}
	return ids
}

// CaptureState captures one complete live source lease for checkpoint recovery.
func (i *Instance) CaptureState() (InstanceState, error) {
	if i == nil || i.removed || i.removing {
		return InstanceState{}, p.ErrStale
	}
	for _, h := range i.bodyOrder {
		if _, err := i.world.Body(h); err != nil {
			return InstanceState{}, err
		}
	}
	return InstanceState{Fingerprint: i.prepared.fingerprint, Source: i.prepared.SourceBytes(), Settings: i.prepared.settings}, nil
}

// RestoreInstance rebinds an exact source lease to privately restored identities.
// It creates or deletes nothing. Use the returned lease only after the paired
// world replacement commits; discard it if that replacement is discarded.
func RestoreInstance(world p.World, state InstanceState, lookup p.ContentLookup) (*Instance, error) {
	if world == nil || lookup == nil {
		return nil, p.ErrInvalid
	}
	source, _, err := Decode(bytes.NewReader(state.Source), state.Settings.Limits)
	if err != nil {
		return nil, err
	}
	prepared, err := Prepare(source, state.Settings)
	if err != nil {
		return nil, err
	}
	if prepared.Fingerprint() != state.Fingerprint || len(prepared.Diagnostics()) != 0 {
		return nil, fmt.Errorf("%w: instance fingerprint or preparation mismatch", p.ErrInvalid)
	}
	i := &Instance{world: world, prepared: prepared, bodies: map[string]p.BodyHandle{}, colliders: map[string]p.ColliderHandle{}, joints: map[string]p.JointHandle{}}
	bodies := append([]Body(nil), source.Bodies...)
	sort.Slice(bodies, func(i, j int) bool { return bodies[i].ID < bodies[j].ID })
	for _, body := range bodies {
		state, ok := lookup.FindBody(p.ContentID(body.ID))
		if !ok {
			return nil, fmt.Errorf("%w: restored source body %s", p.ErrStale, body.ID)
		}
		i.bodies[body.ID] = state.Handle
		i.bodyOrder = append(i.bodyOrder, state.Handle)
	}
	colliders := append([]Collider(nil), source.Colliders...)
	sort.Slice(colliders, func(i, j int) bool { return colliders[i].ID < colliders[j].ID })
	for _, collider := range colliders {
		h, ok := lookup.FindCollider(p.ContentID(collider.ID))
		if !ok {
			return nil, p.ErrStale
		}
		shape, ok := lookup.FindShape(p.ContentID(collider.ID + "/geometry"))
		if !ok {
			return nil, p.ErrStale
		}
		i.colliders[collider.ID] = h
		i.shapeOrder = append(i.shapeOrder, shape)
	}
	joints := append([]Joint(nil), source.Joints...)
	sort.Slice(joints, func(i, j int) bool { return joints[i].ID < joints[j].ID })
	anchorAdded := false
	for _, joint := range joints {
		if !joint.Active {
			continue
		}
		if joint.ConnectedBodyID == "" && !anchorAdded {
			state, ok := lookup.FindBody(prepared.worldAnchorID())
			if !ok {
				return nil, p.ErrStale
			}
			i.bodyOrder = append(i.bodyOrder, state.Handle)
			anchorAdded = true
		}
		h, ok := lookup.FindJoint(p.ContentID(joint.ID))
		if !ok {
			return nil, p.ErrStale
		}
		i.joints[joint.ID] = h
		i.jointOrder = append(i.jointOrder, h)
	}
	return i, nil
}

func (prepared *Prepared) worldAnchorID() p.ContentID {
	return p.ContentID(prepared.source.WorldID + "/" + prepared.fingerprint + "/world-joint-anchor")
}
