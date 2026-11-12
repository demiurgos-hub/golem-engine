package content

import p "github.com/demiurgos-hub/golem-engine/golem/physics3d"

// Bounds returns the complete source collider volume, including disabled geometry.
// It uses transformed portable geometry, not solver contact margins.
func (prepared *Prepared) Bounds() (p.Vec3, p.Vec3, bool, error) {
	if prepared == nil {
		return p.Vec3{}, p.Vec3{}, false, p.ErrInvalid
	}
	poses := map[string]p.Pose{}
	for _, body := range prepared.source.Bodies {
		poses[body.ID] = p.Pose{Position: body.Position, Rotation: body.Rotation}
	}
	return prepared.bounds(poses)
}

// Bounds returns the complete current collider volume for a live source lease.
// Dynamic bodies use accepted physical poses, not their authored spawn positions.
func (i *Instance) Bounds() (p.Vec3, p.Vec3, bool, error) {
	if i == nil || i.removed || i.removing {
		return p.Vec3{}, p.Vec3{}, false, p.ErrStale
	}
	poses := map[string]p.Pose{}
	for id, h := range i.bodies {
		state, err := i.world.Body(h)
		if err != nil {
			return p.Vec3{}, p.Vec3{}, false, err
		}
		poses[id] = state.Pose
	}
	return i.prepared.bounds(poses)
}

func (prepared *Prepared) bounds(poses map[string]p.Pose) (minimum, maximum p.Vec3, present bool, err error) {
	assets := map[string]Asset{}
	for _, asset := range prepared.source.Assets {
		assets[asset.ID] = asset
	}
	include := func(point p.Vec3) {
		if !point.InBounds() {
			err = p.ErrInvalid
			return
		}
		if !present {
			minimum, maximum, present = point, point, true
			return
		}
		minimum = p.Vec3{X: min(minimum.X, point.X), Y: min(minimum.Y, point.Y), Z: min(minimum.Z, point.Z)}
		maximum = p.Vec3{X: max(maximum.X, point.X), Y: max(maximum.Y, point.Y), Z: max(maximum.Z, point.Z)}
	}
	var visit func(p.ShapeDesc, p.Pose)
	visit = func(shape p.ShapeDesc, pose p.Pose) {
		switch shape.Kind {
		case p.Compound:
			for _, child := range shape.Children {
				visit(child.Shape, pose.Compose(child.Pose))
			}
		case p.Sphere, p.Capsule:
			points := []p.Vec3{{}}
			if shape.Kind == p.Capsule {
				points = []p.Vec3{shape.PointA, shape.PointB}
			}
			for _, point := range points {
				center := pose.Transform(point)
				radius := p.Vec3{X: shape.Radius, Y: shape.Radius, Z: shape.Radius}
				include(center.Sub(radius))
				include(center.Add(radius))
			}
		case p.Box:
			for _, x := range []float32{-1, 1} {
				for _, y := range []float32{-1, 1} {
					for _, z := range []float32{-1, 1} {
						include(pose.Transform(p.Vec3{X: x * shape.HalfExtents.X, Y: y * shape.HalfExtents.Y, Z: z * shape.HalfExtents.Z}))
					}
				}
			}
		case p.Convex, p.TriangleMesh:
			for _, point := range shape.Vertices {
				include(pose.Transform(point))
			}
		default:
			err = p.ErrUnsupported
		}
	}
	for _, collider := range prepared.source.Colliders {
		shape, e := collider.Geometry(assets)
		if e != nil {
			return minimum, maximum, present, e
		}
		pose, ok := poses[collider.BodyID]
		if !ok {
			return minimum, maximum, present, p.ErrStale
		}
		visit(shape, pose.Compose(p.Pose{Position: collider.Center, Rotation: collider.Rotation}))
		if err != nil {
			return
		}
	}
	return
}
