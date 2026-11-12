package physics3d

// InBounds reports whether all of a validated shape's geometry lies within the
// supported world envelope at pose. It uses oriented geometry, not a substitute
// collision AABB. Call Validate first for untrusted shape descriptions.
func (s ShapeDesc) InBounds(pose Pose) bool {
	if !pose.Valid() {
		return false
	}
	round := func(center Vec3, radius float32) bool {
		r := Vec3{X: radius, Y: radius, Z: radius}
		return center.Add(r).InBounds() && center.Sub(r).InBounds()
	}
	switch s.Kind {
	case Sphere:
		return round(pose.Position, s.Radius)
	case Capsule:
		return round(pose.Transform(s.PointA), s.Radius) && round(pose.Transform(s.PointB), s.Radius)
	case Box:
		for _, x := range []float32{-1, 1} {
			for _, y := range []float32{-1, 1} {
				for _, z := range []float32{-1, 1} {
					if !pose.Transform(Vec3{X: x * s.HalfExtents.X, Y: y * s.HalfExtents.Y, Z: z * s.HalfExtents.Z}).InBounds() {
						return false
					}
				}
			}
		}
	case Convex, TriangleMesh:
		for _, v := range s.Vertices {
			if !pose.Transform(v).InBounds() {
				return false
			}
		}
	case Compound:
		for _, child := range s.Children {
			if !child.Shape.InBounds(pose.Compose(child.Pose)) {
				return false
			}
		}
	default:
		return false
	}
	return true
}
