package box3d

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

func (w *World) attachCookedGeometry(body *body, collider *collider, cooked *CookedShape, pose p.Pose) error {
	for _, leaf := range cooked.leaves {
		local := pose.Compose(leaf.pose)
		d := backendShapeDefinition(collider)
		var id b.ShapeID
		switch leaf.desc.Kind {
		case p.Sphere:
			id = body.id.CreateSphereShape(&d, &b.Sphere{Center: bv(local.Position), Radius: leaf.desc.Radius})
		case p.Capsule:
			id = body.id.CreateCapsuleShape(&d, &b.Capsule{Center1: bv(local.Transform(leaf.desc.PointA)), Center2: bv(local.Transform(leaf.desc.PointB)), Radius: leaf.desc.Radius})
		case p.Box, p.Convex:
			hull := leaf.hull
			if local != p.Identity() {
				hull = b.CloneAndTransformHull(hull, bt(local), b.Vec3One)
			}
			id = body.id.CreateHullShape(&d, hull)
		case p.TriangleMesh:
			mesh := leaf.mesh
			if local != p.Identity() {
				// MeshShape has no local rigid pose. A transformed instance must
				// cook transformed source vertices; sharing the untransformed BVH
				// would silently lose collider/compound local geometry.
				var err error
				mesh, err = cookMesh(leaf.desc, local)
				if err != nil {
					return err
				}
			}
			id = body.id.CreateMeshShape(&d, mesh, b.Vec3One)
		default:
			return p.ErrUnsupported
		}
		collider.children[id] = uint32(len(collider.shapes))
		collider.shapes = append(collider.shapes, id)
	}
	return nil
}
