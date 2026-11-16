package objecttemplate

import (
	"errors"
	"fmt"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
)

// Placer3D places templates of one 3D catalog into one physics world. Like the
// world it is tick-owned and not safe for concurrent use. Placement does not
// synchronize the world; call World.Synchronize before querying new geometry.
type Placer3D struct {
	world      p.World
	catalog    *Catalog
	cooked     *Cooked
	shapes     map[string]*sharedShape
	placements map[string]*Placement3D
}

type sharedShape struct {
	handle p.ShapeHandle
	refs   int
	parent string
}

// Placement3D owns the bodies and colliders of one placed template.
type Placement3D struct {
	placer    *Placer3D
	id        string
	template  *Template
	transform Transform
	bodies    []p.BodyHandle
	colliders []PlacedCollider
	shapes    []string
	removed   bool
}

// PlacedCollider maps a live collider to its template source, including the
// authored layer and metadata hosts use for gameplay classification.
type PlacedCollider struct {
	Handle p.ColliderHandle
	Body   p.BodyHandle
	Source *content.Collider
}

// NewPlacer3D binds a 3D catalog to a world. cooked may be nil, in which case
// shapes are cooked by the world when first placed; otherwise it must come
// from catalog.Cook with the world's own backend preparer.
func NewPlacer3D(world p.World, catalog *Catalog, cooked *Cooked) (*Placer3D, error) {
	if world == nil || catalog == nil || catalog.Dimensions != 3 {
		return nil, invalid("Placer3D needs a world and a 3D catalog")
	}
	if cooked != nil {
		if cooked.catalog != catalog {
			return nil, invalid("cooked shapes belong to another catalog")
		}
		if _, ok := world.(p.PreparedShapeWorld); !ok {
			return nil, fmt.Errorf("%w: world cannot register prepared shapes", p.ErrUnsupported)
		}
	}
	return &Placer3D{world: world, catalog: catalog, cooked: cooked, shapes: map[string]*sharedShape{}, placements: map[string]*Placement3D{}}, nil
}

// Placement returns a live placement by ID.
func (pl *Placer3D) Placement(id string) (*Placement3D, bool) {
	v, ok := pl.placements[id]
	return v, ok
}

// Place creates one static or kinematic copy of templateID. Body and collider
// IDs are prefixed with id, which must be unique among live placements. Every
// body and collider gets owner. A failed placement leaves the world unchanged.
func (pl *Placer3D) Place(id, templateID string, at Transform, owner int64) (*Placement3D, error) {
	if id == "" {
		return nil, invalid("placement ID required")
	}
	if _, exists := pl.placements[id]; exists {
		return nil, invalid("duplicate placement %s", id)
	}
	t, ok := pl.catalog.Template(templateID)
	if !ok {
		return nil, invalid("unknown template %s", templateID)
	}
	if err := at.validate(); err != nil {
		return nil, err
	}
	scale := at.scale()
	unit := unitScale(scale)
	placement := &Placement3D{placer: pl, id: id, template: t, transform: at}
	fail := func(cause error) (*Placement3D, error) {
		if rollback := placement.destroy(); rollback != nil {
			return nil, errors.Join(cause, fmt.Errorf("incomplete rollback: %w", rollback))
		}
		return nil, cause
	}
	bodies := make([]p.BodyHandle, len(t.bodies))
	poses := make([]p.Pose, len(t.bodies))
	for i, plan := range t.bodies {
		desc := plan.desc
		local := desc.Pose.Position
		if !unit {
			local = scaleVec(local, scale)
		}
		desc.Pose = p.Pose{Position: at.Position.Add(at.Rotation.Rotate(local)), Rotation: normalized(at.Rotation.Mul(desc.Pose.Rotation))}
		desc.ID = p.ContentID(id + "/" + plan.source.ID)
		desc.Owner = owner
		handle, err := pl.world.CreateBody(desc)
		if err != nil {
			return fail(fmt.Errorf("body %s: %w", plan.source.ID, err))
		}
		bodies[i], poses[i] = handle, desc.Pose
		placement.bodies = append(placement.bodies, handle)
	}
	for i := range t.colliders {
		plan := &t.colliders[i]
		local := plan.local
		m := identity3()
		if !unit {
			local.Position = conjugate(t.bodies[plan.body].rotation, scale).apply(local.Position)
			m = conjugate(plan.rotation, scale)
		}
		key, shape, err := pl.shapeFor(plan, m, scale, unit)
		if err != nil {
			return fail(fmt.Errorf("collider %s: %w", plan.source.ID, err))
		}
		placement.shapes = append(placement.shapes, key)
		body := bodies[plan.body]
		if plan.ownBody {
			desc := t.bodies[plan.body].desc
			desc.Pose = poses[plan.body].Compose(local)
			desc.Pose.Rotation = normalized(desc.Pose.Rotation)
			desc.ID = p.ContentID(id + "/" + plan.source.ID + "/body")
			desc.Owner = owner
			body, err = pl.world.CreateBody(desc)
			if err != nil {
				return fail(fmt.Errorf("collider %s body: %w", plan.source.ID, err))
			}
			placement.bodies = append(placement.bodies, body)
			local = p.Identity()
		}
		desc := plan.desc
		desc.ID = p.ContentID(id + "/" + plan.source.ID)
		desc.Owner, desc.Body, desc.Shape, desc.LocalPose = owner, body, shape, local
		handle, err := pl.world.CreateCollider(desc)
		if err != nil {
			return fail(fmt.Errorf("collider %s: %w", plan.source.ID, err))
		}
		placement.colliders = append(placement.colliders, PlacedCollider{Handle: handle, Body: body, Source: plan.source})
	}
	pl.placements[id] = placement
	return placement, nil
}

// shapeFor acquires the shared shape for a collider whose collider-frame
// placement scale is m. Spheres and capsules follow Unity's approximation of
// non-uniform scale instead of becoming ellipsoids.
func (pl *Placer3D) shapeFor(plan *colliderPlan, m mat3, scale p.Vec3, unit bool) (string, p.ShapeHandle, error) {
	switch plan.shape.Kind {
	case p.HeightField:
		if !unit {
			return "", p.ShapeHandle{}, fmt.Errorf("%w: height fields cannot be scaled", p.ErrUnsupported)
		}
		return pl.acquireBase(plan.key)
	case p.Sphere:
		if unit {
			return pl.acquireBase(plan.key)
		}
		factor := max(scale.X, scale.Y, scale.Z)
		desc := plan.shape
		desc.Radius *= factor
		key := plan.key + "@" + vecKey(p.Vec3{X: factor, Y: factor, Z: factor})
		desc.ID = p.ContentID(key)
		return pl.acquire(key, "", func() (p.ShapeHandle, error) { return pl.world.CreateShape(desc) })
	case p.Capsule, p.Box:
		if unit {
			return pl.acquireBase(plan.key)
		}
		d, ok := m.diagonal()
		if !ok {
			return "", p.ShapeHandle{}, fmt.Errorf("%w: placement scale would shear a primitive", p.ErrUnsupported)
		}
		desc, err := scaledPrimitive(plan.shape, d, plan.source.Axis)
		if err != nil {
			return "", p.ShapeHandle{}, err
		}
		key := plan.key + "@" + vecKey(d)
		desc.ID = p.ContentID(key)
		return pl.acquire(key, "", func() (p.ShapeHandle, error) { return pl.world.CreateShape(desc) })
	case p.Convex, p.TriangleMesh:
		sigma := plan.shape.ScaleOrUnit()
		if !plan.shared {
			if unit {
				return pl.acquireBase(plan.key)
			}
			return pl.acquireBaked(plan, m, unitVec)
		}
		if !unit {
			d, ok := m.diagonal()
			if !ok {
				return pl.acquireBaked(plan, m, sigma)
			}
			sigma = scaleVec(d, sigma)
		}
		return pl.acquireScaled(plan, sigma)
	}
	return "", p.ShapeHandle{}, fmt.Errorf("%w: shape kind %v", p.ErrUnsupported, plan.shape.Kind)
}

var unitVec = p.Vec3{X: 1, Y: 1, Z: 1}

// scaledPrimitive scales a primitive by the per-axis collider-frame scale d.
// A capsule keeps Unity's shape: its radius takes the larger scale across
// axis and its total height the scale along it, never shorter than a sphere.
func scaledPrimitive(shape p.ShapeDesc, d p.Vec3, axis int) (p.ShapeDesc, error) {
	switch shape.Kind {
	case p.Capsule:
		scale := [3]float32{d.X, d.Y, d.Z}
		if axis < 0 || axis > 2 {
			return shape, fmt.Errorf("%w: capsule axis %d", p.ErrInvalid, axis)
		}
		radius := shape.Radius * max(scale[(axis+1)%3], scale[(axis+2)%3])
		half := max(0, (shape.PointB.Length()+shape.Radius)*scale[axis]-radius)
		var end [3]float32
		end[axis] = half
		shape.Radius, shape.PointB = radius, p.Vec3{X: end[0], Y: end[1], Z: end[2]}
		shape.PointA = shape.PointB.Mul(-1)
	case p.Box:
		shape.HalfExtents = scaleVec(shape.HalfExtents, d)
	}
	return shape, nil
}

func (pl *Placer3D) acquireBase(key string) (string, p.ShapeHandle, error) {
	return pl.acquire(key, "", func() (p.ShapeHandle, error) {
		if pl.cooked != nil {
			if prepared, ok := pl.cooked.shapes[key]; ok {
				return pl.world.(p.PreparedShapeWorld).CreatePreparedShape(prepared)
			}
		}
		return pl.world.CreateShape(pl.catalog.bases[key])
	})
}

// acquireScaled reuses the asset's cooked hull or BVH with its own scale.
func (pl *Placer3D) acquireScaled(plan *colliderPlan, sigma p.Vec3) (string, p.ShapeHandle, error) {
	if unitScale(sigma) {
		return pl.acquireBase(plan.key)
	}
	key := plan.key + "@" + vecKey(sigma)
	scaled, ok := pl.world.(p.ScaledShapeWorld)
	if !ok {
		desc := pl.catalog.bases[plan.key]
		desc.ID, desc.Scale = p.ContentID(key), sigma
		return pl.acquire(key, "", func() (p.ShapeHandle, error) { return pl.world.CreateShape(desc) })
	}
	return pl.acquire(key, plan.key, func() (p.ShapeHandle, error) {
		_, root, err := pl.acquireBase(plan.key)
		if err != nil {
			return p.ShapeHandle{}, err
		}
		handle, err := scaled.CreateScaledShape(root, p.ContentID(key), sigma)
		if err != nil {
			return p.ShapeHandle{}, errors.Join(err, pl.release(plan.key))
		}
		return handle, nil
	})
}

// acquireBaked transforms vertices when the scale cannot stay per-axis.
func (pl *Placer3D) acquireBaked(plan *colliderPlan, m mat3, sigma p.Vec3) (string, p.ShapeHandle, error) {
	key := plan.key + "@" + vecKey(sigma) + "/" + matKey(m)
	desc := pl.catalog.bases[plan.key]
	vertices := make([]p.Vec3, len(desc.Vertices))
	for i, v := range desc.Vertices {
		vertices[i] = m.apply(scaleVec(v, sigma))
	}
	desc.ID, desc.Vertices, desc.Scale = p.ContentID(key), vertices, p.Vec3{}
	return pl.acquire(key, "", func() (p.ShapeHandle, error) { return pl.world.CreateShape(desc) })
}

// acquire returns a counted reference to a registered shape, creating it on
// first use. parent names a shape the new one holds a reference to.
func (pl *Placer3D) acquire(key, parent string, create func() (p.ShapeHandle, error)) (string, p.ShapeHandle, error) {
	if entry, ok := pl.shapes[key]; ok {
		entry.refs++
		return key, entry.handle, nil
	}
	handle, err := create()
	if err != nil {
		return "", p.ShapeHandle{}, err
	}
	pl.shapes[key] = &sharedShape{handle: handle, refs: 1, parent: parent}
	return key, handle, nil
}

func (pl *Placer3D) release(key string) error {
	entry, ok := pl.shapes[key]
	if !ok {
		return nil
	}
	if entry.refs > 0 {
		entry.refs--
	}
	if entry.refs > 0 {
		return nil
	}
	if err := pl.world.DestroyShape(entry.handle); err != nil && !errors.Is(err, p.ErrStale) {
		return err
	}
	delete(pl.shapes, key)
	if entry.parent != "" {
		return pl.release(entry.parent)
	}
	return nil
}

// ID returns the placement ID.
func (pl *Placement3D) ID() string { return pl.id }

// Template returns the placed template.
func (pl *Placement3D) Template() *Template { return pl.template }

// Transform returns the root transform the placement was created with.
func (pl *Placement3D) Transform() Transform { return pl.transform }

// Bodies returns the live body handles.
func (pl *Placement3D) Bodies() []p.BodyHandle { return append([]p.BodyHandle(nil), pl.bodies...) }

// Colliders returns the live colliders with their template sources.
func (pl *Placement3D) Colliders() []PlacedCollider {
	return append([]PlacedCollider(nil), pl.colliders...)
}

// Remove destroys the placement's bodies and colliders and releases shapes no
// other placement uses. It is idempotent after success; a failed call keeps the
// remaining handles so a later call can retry.
func (pl *Placement3D) Remove() error {
	if pl == nil || pl.removed {
		return nil
	}
	if err := pl.destroy(); err != nil {
		return err
	}
	delete(pl.placer.placements, pl.id)
	return nil
}

func (pl *Placement3D) destroy() error {
	var errs []error
	for i := len(pl.bodies) - 1; i >= 0; i-- {
		if pl.bodies[i] == (p.BodyHandle{}) {
			continue
		}
		if err := pl.placer.world.DestroyBody(pl.bodies[i], p.Unloaded); err != nil && !errors.Is(err, p.ErrStale) {
			errs = append(errs, err)
			continue
		}
		pl.bodies[i] = p.BodyHandle{}
	}
	if len(errs) != 0 {
		return errors.Join(errs...)
	}
	for len(pl.shapes) > 0 {
		last := len(pl.shapes) - 1
		if err := pl.placer.release(pl.shapes[last]); err != nil {
			return err
		}
		pl.shapes = pl.shapes[:last]
	}
	pl.bodies, pl.colliders, pl.removed = nil, nil, true
	return nil
}
