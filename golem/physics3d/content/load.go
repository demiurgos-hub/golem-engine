package content

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// OwnerResolver resolves a stable source owner to an optional application entity.
// It runs before any world mutation; an empty source owner maps to zero.
type OwnerResolver func(sourceID string) (int64, error)

// Instance owns exactly the handles created by one successful source load.
// It is tick-owned, like World; source lookup maps are never exposed mutably.
type Instance struct {
	world      p.World
	prepared   *Prepared
	bodies     map[string]p.BodyHandle
	colliders  map[string]p.ColliderHandle
	joints     map[string]p.JointHandle
	bodyOrder  []p.BodyHandle
	shapeOrder []p.ShapeHandle
	jointOrder []p.JointHandle
	removed    bool
	removing   bool
}

// Load prepares and transactionally constructs one source using DefaultSettings.
// Use Prepare(...).Load when solver-policy settings or diagnostics are needed.
func Load(world p.World, source *Source, resolve OwnerResolver) (*Instance, error) {
	prepared, err := Prepare(source, DefaultSettings())
	if err != nil {
		return nil, err
	}
	return prepared.Load(world, resolve)
}

// Load resolves owners and rejects unsupported semantics before mutation. Failed
// construction removes newly created joints, bodies and shapes in reverse order;
// rollback errors are joined with the original error. Existing objects are untouched.
func (prepared *Prepared) Load(world p.World, resolve OwnerResolver) (*Instance, error) {
	if prepared == nil || world == nil {
		return nil, p.ErrInvalid
	}
	if len(prepared.diagnostics) != 0 {
		return nil, fmt.Errorf("%w: %+v", p.ErrUnsupported, prepared.diagnostics)
	}
	source := &prepared.source
	owners, err := resolveOwners(source, resolve)
	if err != nil {
		return nil, err
	}
	assets := make(map[string]Asset, len(source.Assets))
	for _, asset := range source.Assets {
		assets[asset.ID] = asset
	}
	materials := make(map[string]p.Material, len(source.Materials))
	for _, material := range source.Materials {
		desc, err := materialDescription(source.WorldID, material)
		if err != nil {
			return nil, err
		}
		materials[material.ID] = desc
	}
	instance := &Instance{world: world, prepared: prepared, bodies: map[string]p.BodyHandle{}, colliders: map[string]p.ColliderHandle{}, joints: map[string]p.JointHandle{}}
	fail := func(cause error) (*Instance, error) {
		if rollback := instance.Remove(); rollback != nil {
			return instance, errors.Join(cause, fmt.Errorf("incomplete rollback; retry Instance.Remove: %w", rollback))
		}
		return nil, cause
	}
	bodies := append([]Body(nil), source.Bodies...)
	sort.Slice(bodies, func(i, j int) bool { return bodies[i].ID < bodies[j].ID })
	for _, body := range bodies {
		desc := bodyDescription(body)
		desc.Owner = owners[body.OwnerID]
		handle, err := world.CreateBody(desc)
		if err != nil {
			return fail(fmt.Errorf("body %s: %w", body.ID, err))
		}
		instance.bodies[body.ID] = handle
		instance.bodyOrder = append(instance.bodyOrder, handle)
	}
	colliders := append([]Collider(nil), source.Colliders...)
	sort.Slice(colliders, func(i, j int) bool { return colliders[i].ID < colliders[j].ID })
	for _, collider := range colliders {
		geometry, err := collider.Geometry(assets)
		if err != nil {
			return fail(err)
		}
		shape, err := world.CreateShape(geometry)
		if err != nil {
			return fail(fmt.Errorf("geometry %s: %w", collider.ID, err))
		}
		instance.shapeOrder = append(instance.shapeOrder, shape)
		desc := p.DefaultCollider(instance.bodies[collider.BodyID], shape)
		desc.ID, desc.Owner = p.ContentID(collider.ID), owners[collider.OwnerID]
		desc.LocalPose = p.Pose{Position: collider.Center, Rotation: collider.Rotation}
		desc.Enabled = collider.Enabled && collider.Active
		desc.Roles = p.Roles{Solid: collider.Solid, Sensor: collider.Sensor, Query: collider.Query}
		desc.Filter = p.CollisionFilter{Category: uint64(1) << collider.Layer, Mask: uint64(source.LayerCollisionMasks[collider.Layer])}
		desc.Material = materials[collider.MaterialID]
		handle, err := world.CreateCollider(desc)
		if err != nil {
			return fail(fmt.Errorf("collider %s: %w", collider.ID, err))
		}
		instance.colliders[collider.ID] = handle
	}
	joints := append([]Joint(nil), source.Joints...)
	sort.Slice(joints, func(i, j int) bool { return joints[i].ID < joints[j].ID })
	var fixedWorld p.BodyHandle
	for _, joint := range joints {
		if !joint.Active {
			continue
		}
		desc, err := jointDescription(joint)
		if err != nil {
			return fail(err)
		}
		desc.A = instance.bodies[joint.BodyID]
		if joint.ConnectedBodyID == "" {
			if fixedWorld == (p.BodyHandle{}) {
				body := p.DefaultBody(p.Static)
				body.ID = prepared.worldAnchorID()
				fixedWorld, err = world.CreateBody(body)
				if err != nil {
					return fail(err)
				}
				instance.bodyOrder = append(instance.bodyOrder, fixedWorld)
			}
			desc.B = fixedWorld
		} else {
			desc.B = instance.bodies[joint.ConnectedBodyID]
		}
		handle, err := world.CreateJoint(desc)
		if err != nil {
			return fail(fmt.Errorf("joint %s: %w", joint.ID, err))
		}
		instance.joints[joint.ID] = handle
		instance.jointOrder = append(instance.jointOrder, handle)
	}
	if err := world.Synchronize(); err != nil {
		return fail(err)
	}
	return instance, nil
}

func resolveOwners(source *Source, resolve OwnerResolver) (map[string]int64, error) {
	owners := map[string]int64{"": 0}
	for _, body := range source.Bodies {
		owners[body.OwnerID] = 0
	}
	for _, collider := range source.Colliders {
		owners[collider.OwnerID] = 0
	}
	ids := make([]string, 0, len(owners))
	for id := range owners {
		if id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		if resolve == nil {
			return nil, invalid("owner resolver required for %s", id)
		}
		owner, err := resolve(id)
		if err != nil {
			return nil, fmt.Errorf("owner %s: %w", id, err)
		}
		owners[id] = owner
	}
	return owners, nil
}

func bodyDescription(body Body) p.BodyDesc {
	mode := p.Static
	if body.Kind == "dynamic" {
		mode = p.FreeDynamic
	} else if body.Kind == "kinematic" {
		mode = p.Kinematic
	}
	d := p.DefaultBody(mode)
	d.ID, d.Pose = p.ContentID(body.ID), p.Pose{Position: body.Position, Rotation: body.Rotation}
	d.Mass, d.LinearDamping, d.AngularDamping = body.Mass, body.LinearDamping, body.AngularDamping
	d.Enabled = body.Enabled && body.Active
	if !body.UseGravity {
		d.GravityScale = 0
	}
	d.Continuous = body.CollisionDetection == "continuous" || body.CollisionDetection == "continuousDynamic"
	d.Locks = p.MotionLocks{LinearX: body.Constraints&2 != 0, LinearY: body.Constraints&4 != 0, LinearZ: body.Constraints&8 != 0,
		AngularX: body.Constraints&16 != 0, AngularY: body.Constraints&32 != 0, AngularZ: body.Constraints&64 != 0}
	d.ManualCenterOfMass, d.ManualInertia = !body.AutomaticCenterOfMass, !body.AutomaticInertiaTensor
	d.CenterOfMass, d.InertiaTensor, d.InertiaRotation = body.CenterOfMass, body.InertiaTensor, body.InertiaRotation
	return d
}

func materialDescription(world string, material Material) (p.Material, error) {
	if material.StaticFriction != material.DynamicFriction {
		return p.Material{}, fmt.Errorf("%w: material %s combination/static friction", p.ErrUnsupported, material.ID)
	}
	combine := map[string]p.CombineMode{"average": p.Average, "minimum": p.Minimum, "multiply": p.Multiply, "maximum": p.Maximum}
	hash := sha256.Sum256([]byte(world + "\x00" + material.ID))
	return p.Material{ID: binary.LittleEndian.Uint64(hash[:8]), Friction: material.DynamicFriction, Restitution: material.Restitution,
		FrictionCombine: combine[material.FrictionCombine], RestitutionCombine: combine[material.BounceCombine]}, nil
}

// Body returns the live source-identity mapping, false after removal.
func (i *Instance) Body(id string) (p.BodyHandle, bool) {
	h, ok := i.bodies[id]
	return h, ok && !i.removed && !i.removing
}

// Collider returns the live source-identity mapping, false after removal.
func (i *Instance) Collider(id string) (p.ColliderHandle, bool) {
	h, ok := i.colliders[id]
	return h, ok && !i.removed && !i.removing
}

// Joint returns an active joint's source-identity mapping, false after removal.
func (i *Instance) Joint(id string) (p.JointHandle, bool) {
	h, ok := i.joints[id]
	return h, ok && !i.removed && !i.removing
}

// Prepared returns the immutable capture that owns all authored metadata.
func (i *Instance) Prepared() *Prepared { return i.prepared }

// Remove destroys owned handles in reverse construction order. It is idempotent
// after success. Failed cleanup retains failed handles so a later call can retry.
func (i *Instance) Remove() error {
	if i == nil || i.removed {
		return nil
	}
	i.removing = true
	var errs []error
	for n := len(i.jointOrder) - 1; n >= 0; n-- {
		if i.jointOrder[n] == (p.JointHandle{}) {
			continue
		}
		if err := i.world.DestroyJoint(i.jointOrder[n]); err != nil && !errors.Is(err, p.ErrStale) {
			errs = append(errs, err)
		} else {
			i.jointOrder[n] = p.JointHandle{}
		}
	}
	for n := len(i.bodyOrder) - 1; n >= 0; n-- {
		if i.bodyOrder[n] == (p.BodyHandle{}) {
			continue
		}
		if err := i.world.DestroyBody(i.bodyOrder[n], p.Unloaded); err != nil && !errors.Is(err, p.ErrStale) {
			errs = append(errs, err)
		} else {
			i.bodyOrder[n] = p.BodyHandle{}
		}
	}
	for n := len(i.shapeOrder) - 1; n >= 0; n-- {
		if i.shapeOrder[n] == (p.ShapeHandle{}) {
			continue
		}
		if err := i.world.DestroyShape(i.shapeOrder[n]); err != nil && !errors.Is(err, p.ErrStale) {
			errs = append(errs, err)
		} else {
			i.shapeOrder[n] = p.ShapeHandle{}
		}
	}
	if err := i.world.Synchronize(); err != nil {
		errs = append(errs, err)
	}
	i.removed = len(errs) == 0
	return errors.Join(errs...)
}
