package box3d

import (
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"sort"
)

func (w *World) orderedActiveBodies() []p.BodyHandle {
	keys := make([]p.BodyHandle, 0, len(w.activeBodies))
	for key := range w.activeBodies {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Index < keys[j].Index })
	return keys
}

func geometryInBounds(s p.ShapeDesc, pose p.Pose) bool {
	switch s.Kind {
	case p.Sphere:
		return roundInBounds(pose.Position, s.Radius)
	case p.Capsule:
		return roundInBounds(pose.Transform(s.PointA), s.Radius) && roundInBounds(pose.Transform(s.PointB), s.Radius)
	case p.Box:
		for _, x := range []float32{-1, 1} {
			for _, y := range []float32{-1, 1} {
				for _, z := range []float32{-1, 1} {
					if !pose.Transform(p.Vec3{X: x * s.HalfExtents.X, Y: y * s.HalfExtents.Y, Z: z * s.HalfExtents.Z}).InBounds() {
						return false
					}
				}
			}
		}
	case p.Convex, p.TriangleMesh:
		for _, v := range s.Vertices {
			if !pose.Transform(v).InBounds() {
				return false
			}
		}
	case p.Compound:
		for _, child := range s.Children {
			if !geometryInBounds(child.Shape, pose.Compose(child.Pose)) {
				return false
			}
		}
	default:
		return false
	}
	return true
}
func roundInBounds(center p.Vec3, radius float32) bool {
	r := p.Vec3{X: radius, Y: radius, Z: radius}
	return center.Add(r).InBounds() && center.Sub(r).InBounds()
}
func (w *World) bodyInBounds(v *body, pose p.Pose) bool {
	if !pose.Valid() || !pose.Compose(v.desc.Anchor).Position.InBounds() {
		return false
	}
	for _, h := range v.colliders {
		c := w.colliders[h]
		if c.desc.Enabled && !geometryInBounds(w.shapes[c.desc.Shape].desc, pose.Compose(c.desc.LocalPose)) {
			return false
		}
	}
	return true
}
