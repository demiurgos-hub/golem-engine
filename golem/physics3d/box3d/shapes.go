package box3d

import (
	"fmt"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

// CreateCollider attaches independent collider geometry and metadata to a body.
func (w *World) CreateCollider(d p.ColliderDesc) (p.ColliderHandle, error) {
	if err := w.guard(); err != nil {
		return p.ColliderHandle{}, err
	}
	v, err := w.findBody(d.Body)
	if err != nil {
		return p.ColliderHandle{}, err
	}
	s, ok := w.shapes[d.Shape]
	if !ok {
		return p.ColliderHandle{}, p.ErrStale
	}
	if !d.LocalPose.Valid() || !p.Finite(d.Density) || d.Density < 0 || !p.Finite(d.Material.Friction) || d.Material.Friction < 0 || !p.Finite(d.Material.Restitution) || d.Material.Restitution < 0 || d.Material.Restitution > 1 || !p.Finite(d.Material.RollingResistance) || d.Material.RollingResistance < 0 {
		return p.ColliderHandle{}, p.ErrInvalid
	}
	if !geometryInBounds(s.desc, w.bodyState(d.Body, v).Pose.Compose(d.LocalPose)) {
		return p.ColliderHandle{}, fmt.Errorf("%w: collider geometry exceeds coordinate envelope", p.ErrInvalid)
	}
	if !d.Roles.Solid && !d.Roles.Sensor && !d.Roles.Query {
		return p.ColliderHandle{}, fmt.Errorf("%w: collider has no role", p.ErrInvalid)
	}
	if v.id.GetJointCount() != 0 {
		return p.ColliderHandle{}, fmt.Errorf("%w: rebuild joints before changing body mass", p.ErrBusy)
	}
	if containsMesh(s.desc) && v.desc.Mode != p.Static && (!w.config.Continuous || v.desc.Mode == p.FreeDynamic || v.desc.Mode == p.DrivenCharacter) {
		return p.ColliderHandle{}, fmt.Errorf("%w: moving triangle collision requires a kinematic body and continuous motion admission", p.ErrUnsupported)
	}
	d.Material, err = w.registerMaterial(d.Material)
	if err != nil {
		return p.ColliderHandle{}, err
	}
	c := &collider{desc: d, children: map[b.ShapeID]uint32{}}
	h := p.ColliderHandle(w.handle())
	if d.Enabled {
		if err := w.attachCookedGeometry(v, c, s.cooked, d.LocalPose); err != nil {
			for _, id := range c.shapes {
				id.Destroy(true)
			}
			return p.ColliderHandle{}, err
		}
	}
	w.colliders[h] = c
	if c.desc.Roles.Sensor {
		if w.sensorColliders == nil {
			w.sensorColliders = map[p.ColliderHandle]*collider{}
		}
		w.sensorColliders[h] = c
	}
	s.refs++
	v.colliders = append(v.colliders, h)
	for _, id := range c.shapes {
		w.shapeOwners[id] = h
	}
	w.adjustMass(v)
	w.changed()
	return h, nil
}
func containsMesh(s p.ShapeDesc) bool {
	if s.Kind == p.TriangleMesh {
		return true
	}
	for _, c := range s.Children {
		if containsMesh(c.Shape) {
			return true
		}
	}
	return false
}
func backendShapeDefinition(c *collider) b.ShapeDef {
	d := b.DefaultShapeDef()
	d.Name = string(c.desc.ID)
	d.Density = c.desc.Density
	d.Filter = b.Filter{CategoryBits: c.desc.Filter.Category, MaskBits: c.desc.Filter.Mask, GroupIndex: c.desc.Filter.Group}
	d.IsSensor = !c.desc.Roles.Solid
	d.EnableSensorEvents = c.desc.Roles.Solid || c.desc.Roles.Sensor
	d.EnableContactEvents = c.desc.Roles.Solid
	d.EnableHitEvents = c.desc.Roles.Solid
	d.BaseMaterial = b.DefaultSurfaceMaterial()
	d.BaseMaterial.Friction = c.desc.Material.Friction
	d.BaseMaterial.Restitution = c.desc.Material.Restitution
	d.BaseMaterial.RollingResistance = c.desc.Material.RollingResistance
	d.BaseMaterial.UserMaterialId = c.desc.Material.ID
	return d
}
func (w *World) adjustMass(v *body) {
	if v.desc.Mode != p.FreeDynamic && v.desc.Mode != p.DrivenCharacter {
		return
	}
	// Recompute from geometry before each override so repeated collider changes
	// cannot accumulate scaling or a manually shifted centre of mass.
	v.id.ApplyMassFromShapes()
	m := v.id.GetMassData()
	if m.Mass > 0 && v.desc.Mass > 0 {
		scale := v.desc.Mass / m.Mass
		m.Mass = v.desc.Mass
		m.Inertia.Cx = b.MulSV(scale, m.Inertia.Cx)
		m.Inertia.Cy = b.MulSV(scale, m.Inertia.Cy)
		m.Inertia.Cz = b.MulSV(scale, m.Inertia.Cz)
	}
	m.Mass = v.desc.Mass
	if v.desc.ManualCenterOfMass {
		center := bv(v.desc.CenterOfMass)
		m.Inertia = b.AddMM(m.Inertia, b.Steiner(m.Mass, b.Sub(m.Center, center)))
		m.Center = center
	}
	if v.desc.ManualInertia {
		i := v.desc.InertiaTensor
		principal := b.Matrix3{Cx: b.Vec3{X: i.X}, Cy: b.Vec3{Y: i.Y}, Cz: b.Vec3{Z: i.Z}}
		rotation := b.MakeMatrixFromQuat(bq(v.desc.InertiaRotation))
		m.Inertia = b.MulMM(b.MulMM(rotation, principal), b.Transpose(rotation))
	}
	v.id.SetMassData(m)
}

// DestroyCollider invalidates all exact child identities and emits cleanup exits.
func (w *World) DestroyCollider(h p.ColliderHandle, reason p.CleanupReason) error {
	if err := w.guard(); err != nil {
		return err
	}
	c, ok := w.colliders[h]
	if !ok {
		return p.ErrStale
	}
	v := w.bodies[c.desc.Body]
	if v.id.GetJointCount() != 0 {
		return fmt.Errorf("%w: rebuild joints before changing body mass", p.ErrBusy)
	}
	for _, id := range c.shapes {
		delete(w.shapeOwners, id)
		id.Destroy(true)
	}
	for i, x := range v.colliders {
		if x == h {
			v.colliders = append(v.colliders[:i], v.colliders[i+1:]...)
			break
		}
	}
	w.shapes[c.desc.Shape].refs--
	w.cleanup = append(w.cleanup, w.tracker.Remove(h, reason, w.revision+1)...)
	delete(w.colliders, h)
	delete(w.sensorColliders, h)
	w.adjustMass(v)
	w.changed()
	return nil
}
