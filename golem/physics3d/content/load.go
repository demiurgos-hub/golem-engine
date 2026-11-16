package content

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"

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
// Authored characters require an explicit CharacterPolicy through Prepare.
func Load(world p.World, source *Source, resolve OwnerResolver) (*Instance, error) {
	prepared, err := Prepare(source, DefaultSettings())
	if err != nil {
		return nil, err
	}
	return prepared.Load(world, resolve)
}

// shapePlan is one collider's shape construction. Shared plans after the first
// of their asset reuse that shape's backend geometry with their own Scale.
type shapePlan struct {
	collider Collider
	geometry p.ShapeDesc
	pose     p.Pose
	share    int
}

func (prepared *Prepared) shapePlans() ([]shapePlan, error) {
	source := &prepared.source
	assets := make(map[string]Asset, len(source.Assets))
	for _, asset := range source.Assets {
		assets[asset.ID] = asset
	}
	bodies := make(map[string]Body, len(source.Bodies))
	for _, body := range source.Bodies {
		bodies[body.ID] = body
	}
	colliders := append([]Collider(nil), source.Colliders...)
	sort.Slice(colliders, func(i, j int) bool { return colliders[i].ID < colliders[j].ID })
	plans := make([]shapePlan, len(colliders))
	first := map[[2]string]int{}
	for i, collider := range colliders {
		geometry, pose, shared, err := collider.Placement(assets, bodies[collider.BodyID])
		if err != nil {
			return nil, err
		}
		plans[i] = shapePlan{collider: collider, geometry: geometry, pose: pose, share: -1}
		if shared {
			key := [2]string{collider.AssetID, collider.Shape + "/" + strconv.Itoa(collider.HullVertexLimit)}
			if index, ok := first[key]; ok {
				plans[i].share = index
			} else {
				first[key] = i
			}
		}
	}
	return plans, nil
}

// CookedShapes holds one source's collider geometry cooked outside a world by a
// backend ShapePreparer. Shapes that share an asset are cooked once.
type CookedShapes struct {
	prepared *Prepared
	plans    []shapePlan
	shapes   []p.PreparedShape
}

// CookShapes cooks every shape Load would create with up to workers concurrent
// calls to preparer. It touches no world, so independent sources may be cooked
// concurrently; LoadCooked then registers the results without recooking.
func (prepared *Prepared) CookShapes(ctx context.Context, preparer p.ShapePreparer, workers int) (*CookedShapes, error) {
	if prepared == nil || preparer == nil {
		return nil, p.ErrInvalid
	}
	plans, err := prepared.shapePlans()
	if err != nil {
		return nil, err
	}
	cooked := &CookedShapes{prepared: prepared, plans: plans, shapes: make([]p.PreparedShape, len(plans))}
	next := make(chan int)
	errs := make(chan error, max(1, workers))
	var wait sync.WaitGroup
	for range max(1, workers) {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for i := range next {
				shape, err := preparer.PrepareShape(plans[i].geometry)
				if err != nil {
					select {
					case errs <- fmt.Errorf("geometry %s: %w", plans[i].collider.ID, err):
					default:
					}
					continue
				}
				cooked.shapes[i] = shape
			}
		}()
	}
	for i := range plans {
		if plans[i].share >= 0 {
			continue
		}
		if len(errs) > 0 || ctx.Err() != nil {
			break
		}
		next <- i
	}
	close(next)
	wait.Wait()
	select {
	case err = <-errs:
		return nil, err
	default:
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return cooked, nil
}

// Load resolves owners and rejects unsupported semantics before mutation. Failed
// construction removes newly created joints, bodies and shapes in reverse order;
// rollback errors are joined with the original error. Existing objects are untouched.
func (prepared *Prepared) Load(world p.World, resolve OwnerResolver) (*Instance, error) {
	if prepared == nil || world == nil {
		return nil, p.ErrInvalid
	}
	plans, err := prepared.shapePlans()
	if err != nil {
		return nil, err
	}
	return prepared.load(world, resolve, plans, nil)
}

// LoadCooked is Load using shapes from CookShapes on this Prepared. The world
// must implement PreparedShapeWorld for the backend that cooked them.
func (prepared *Prepared) LoadCooked(world p.World, resolve OwnerResolver, cooked *CookedShapes) (*Instance, error) {
	if prepared == nil || world == nil || cooked == nil || cooked.prepared != prepared {
		return nil, p.ErrInvalid
	}
	if _, ok := world.(p.PreparedShapeWorld); !ok {
		return nil, fmt.Errorf("%w: world cannot register prepared shapes", p.ErrUnsupported)
	}
	return prepared.load(world, resolve, cooked.plans, cooked.shapes)
}

func (prepared *Prepared) load(world p.World, resolve OwnerResolver, plans []shapePlan, cooked []p.PreparedShape) (*Instance, error) {
	if len(prepared.diagnostics) != 0 {
		return nil, fmt.Errorf("%w: %+v", p.ErrUnsupported, prepared.diagnostics)
	}
	source := &prepared.source
	owners, err := resolveOwners(source, resolve)
	if err != nil {
		return nil, err
	}
	scaled, _ := world.(p.ScaledShapeWorld)
	registered, _ := world.(p.PreparedShapeWorld)
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
		if character, ok := prepared.characters[body.ID]; ok {
			desc.Controller = character.config
			desc.Enabled = desc.Enabled && character.active
		}
		desc.Owner = owners[body.OwnerID]
		handle, err := world.CreateBody(desc)
		if err != nil {
			return fail(fmt.Errorf("body %s: %w", body.ID, err))
		}
		instance.bodies[body.ID] = handle
		instance.bodyOrder = append(instance.bodyOrder, handle)
	}
	handles := make([]p.ShapeHandle, len(plans))
	for i, plan := range plans {
		collider := plan.collider
		var shape p.ShapeHandle
		switch {
		case cooked != nil && cooked[i] != nil:
			shape, err = registered.CreatePreparedShape(cooked[i])
		case plan.share >= 0 && scaled != nil:
			shape, err = scaled.CreateScaledShape(handles[plan.share], plan.geometry.ID, plan.geometry.Scale)
		default:
			shape, err = world.CreateShape(plan.geometry)
		}
		if err != nil {
			return fail(fmt.Errorf("geometry %s: %w", collider.ID, err))
		}
		handles[i] = shape
		instance.shapeOrder = append(instance.shapeOrder, shape)
		desc := p.DefaultCollider(instance.bodies[collider.BodyID], shape)
		desc.ID, desc.Owner = p.ContentID(collider.ID), owners[collider.OwnerID]
		desc.LocalPose = plan.pose
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
