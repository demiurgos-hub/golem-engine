package physics3d

import "fmt"

// Validate checks portable geometry before invoking a backend. Compound nesting
// and vertex counts are bounded even when constructed directly rather than loaded.
func (s ShapeDesc) Validate() error { remaining := 1_000_000; return validateShape(s, 0, &remaining) }
func validateShape(s ShapeDesc, depth int, remaining *int) error {
	if depth > 16 || *remaining < 1 {
		return fmt.Errorf("%w: geometry resource limit", ErrInvalid)
	}
	*remaining -= 1
	if !Finite(s.Radius) || s.Radius < 0 {
		return fmt.Errorf("%w: radius", ErrInvalid)
	}
	switch s.Kind {
	case Sphere:
		if s.Radius <= 0 {
			return fmt.Errorf("%w: sphere radius", ErrInvalid)
		}
	case Capsule:
		if s.Radius <= 0 || !s.PointA.InBounds() || !s.PointB.InBounds() {
			return fmt.Errorf("%w: capsule", ErrInvalid)
		}
	case Box:
		if !s.HalfExtents.InBounds() || s.HalfExtents.X <= 0 || s.HalfExtents.Y <= 0 || s.HalfExtents.Z <= 0 {
			return fmt.Errorf("%w: box extents", ErrInvalid)
		}
	case Convex, TriangleMesh:
		if len(s.Vertices) < 3 || len(s.Vertices) > *remaining {
			return fmt.Errorf("%w: vertex count", ErrInvalid)
		}
		*remaining -= len(s.Vertices)
		for _, v := range s.Vertices {
			if !v.InBounds() {
				return fmt.Errorf("%w: vertex", ErrInvalid)
			}
		}
		if s.Kind == Convex && len(s.Vertices) < 4 {
			return fmt.Errorf("%w: hull vertices", ErrInvalid)
		}
		if s.Kind == TriangleMesh {
			if len(s.Indices) == 0 || len(s.Indices)%3 != 0 || len(s.Indices) > 3_000_000 {
				return fmt.Errorf("%w: triangle indices", ErrInvalid)
			}
			for i := 0; i < len(s.Indices); i += 3 {
				a, b, c := s.Indices[i], s.Indices[i+1], s.Indices[i+2]
				if uint64(a) >= uint64(len(s.Vertices)) || uint64(b) >= uint64(len(s.Vertices)) || uint64(c) >= uint64(len(s.Vertices)) {
					return fmt.Errorf("%w: triangle index", ErrInvalid)
				}
				if s.Vertices[b].Sub(s.Vertices[a]).Cross(s.Vertices[c].Sub(s.Vertices[a])).Length() == 0 {
					return fmt.Errorf("%w: degenerate triangle", ErrInvalid)
				}
			}
		}
	case Compound:
		if len(s.Children) == 0 {
			return fmt.Errorf("%w: empty compound", ErrInvalid)
		}
		for _, child := range s.Children {
			if !child.Pose.Valid() {
				return fmt.Errorf("%w: child transform", ErrInvalid)
			}
			if err := validateShape(child.Shape, depth+1, remaining); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("%w: shape kind", ErrInvalid)
	}
	return nil
}

// Validate rejects nonfinite, malformed and out-of-envelope queries.
func (q Query) Validate() error {
	if !q.Pose.Valid() || !q.Translation.Valid() || !q.Pose.Position.Add(q.Translation).InBounds() || q.Mode > All || q.Initial > BlockInitial || q.Backfaces > IncludeBackfaces || q.Filter.Sensors > OnlySensors {
		return ErrInvalid
	}
	switch q.Kind {
	case Ray:
		return nil
	case Sweep, Overlap:
		return q.Shape.Validate()
	default:
		return ErrInvalid
	}
}

// Validate checks body parameters; body and navigation anchor are distinct poses.
func (d BodyDesc) Validate() error {
	if d.Mode > ExternalPose || !d.Pose.Valid() || !d.Anchor.Valid() || !d.LinearVelocity.Valid() || !d.AngularVelocity.Valid() || !Finite(d.Mass) || d.Mass < 0 || ((d.Mode == FreeDynamic || d.Mode == DrivenCharacter) && d.Mass <= 0) || !Finite(d.GravityScale) || !Finite(d.LinearDamping) || d.LinearDamping < 0 || !Finite(d.AngularDamping) || d.AngularDamping < 0 {
		return ErrInvalid
	}
	return nil
}

// Validate checks finite world settings and bounded scheduling configuration.
func (c Config) Validate() error {
	if !c.Gravity.Valid() || c.Substeps < 1 || c.Substeps > 128 || c.TickRate < 1 || c.TickRate > 1000 || c.Workers < 1 || c.Workers > 64 {
		return ErrInvalid
	}
	return nil
}
