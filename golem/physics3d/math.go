// Package physics3d defines backend-independent, tick-owned 3D physics contracts.
// Coordinates are float32 metres in a right-handed, Y-up space. Angles are radians.
package physics3d

import (
	"errors"
	"math"
)

const CoordinateLimit float32 = 4096

var (
	ErrInvalid     = errors.New("physics3d: invalid input")
	ErrStale       = errors.New("physics3d: stale or foreign handle")
	ErrBusy        = errors.New("physics3d: simulation frame pending")
	ErrFaulted     = errors.New("physics3d: world faulted; coherent restoration required")
	ErrUnsupported = errors.New("physics3d: unsupported operation")
)

type Vec3 struct{ X, Y, Z float32 }
type Quat struct{ X, Y, Z, W float32 }
type Pose struct {
	Position Vec3
	Rotation Quat
}

// Identity returns a pose with zero translation and identity rotation.
func Identity() Pose { return Pose{Rotation: Quat{W: 1}} }

// Finite reports whether x is finite.
func Finite(x float32) bool { return !math.IsNaN(float64(x)) && !math.IsInf(float64(x), 0) }

// Valid reports whether all vector components are finite.
func (v Vec3) Valid() bool { return Finite(v.X) && Finite(v.Y) && Finite(v.Z) }

// InBounds checks the supported authoritative coordinate envelope.
func (v Vec3) InBounds() bool {
	return v.Valid() && abs(v.X) <= CoordinateLimit && abs(v.Y) <= CoordinateLimit && abs(v.Z) <= CoordinateLimit
}

// Add returns the component-wise sum.
func (v Vec3) Add(b Vec3) Vec3 { return Vec3{v.X + b.X, v.Y + b.Y, v.Z + b.Z} }

// Sub returns the component-wise difference.
func (v Vec3) Sub(b Vec3) Vec3 { return Vec3{v.X - b.X, v.Y - b.Y, v.Z - b.Z} }

// Mul scales a vector.
func (v Vec3) Mul(s float32) Vec3 { return Vec3{v.X * s, v.Y * s, v.Z * s} }

// Dot returns the scalar product.
func (v Vec3) Dot(b Vec3) float32 { return v.X*b.X + v.Y*b.Y + v.Z*b.Z }

// Cross returns the right-handed cross product.
func (v Vec3) Cross(b Vec3) Vec3 {
	return Vec3{v.Y*b.Z - v.Z*b.Y, v.Z*b.X - v.X*b.Z, v.X*b.Y - v.Y*b.X}
}

// Length returns the Euclidean length.
func (v Vec3) Length() float32 { return float32(math.Sqrt(float64(v.Dot(v)))) }

// Unit returns a normalized vector, or zero for a zero vector.
func (v Vec3) Unit() Vec3 {
	if n := v.Length(); n > 0 {
		return v.Mul(1 / n)
	}
	return Vec3{}
}

// Valid checks finiteness and unit quaternion length without silently normalizing input.
func (q Quat) Valid() bool {
	n := q.X*q.X + q.Y*q.Y + q.Z*q.Z + q.W*q.W
	return Finite(n) && abs(n-1) < 0.00001
}

// Conjugate returns the inverse of a unit quaternion.
func (q Quat) Conjugate() Quat { return Quat{-q.X, -q.Y, -q.Z, q.W} }

// Mul composes rotations, applying b first.
func (q Quat) Mul(b Quat) Quat {
	return Quat{q.W*b.X + q.X*b.W + q.Y*b.Z - q.Z*b.Y, q.W*b.Y - q.X*b.Z + q.Y*b.W + q.Z*b.X, q.W*b.Z + q.X*b.Y - q.Y*b.X + q.Z*b.W, q.W*b.W - q.X*b.X - q.Y*b.Y - q.Z*b.Z}
}

// Rotate applies a unit quaternion to a vector.
func (q Quat) Rotate(v Vec3) Vec3 {
	u := Vec3{q.X, q.Y, q.Z}
	t := u.Cross(v).Mul(2)
	return v.Add(t.Mul(q.W)).Add(u.Cross(t))
}

// Valid checks a world pose against the coordinate envelope.
func (p Pose) Valid() bool { return p.Position.InBounds() && p.Rotation.Valid() }

// Transform maps a local point into the parent space.
func (p Pose) Transform(v Vec3) Vec3 { return p.Position.Add(p.Rotation.Rotate(v)) }

// Compose applies the child transform and then this transform.
func (p Pose) Compose(b Pose) Pose { return Pose{p.Transform(b.Position), p.Rotation.Mul(b.Rotation)} }

// Inverse returns the inverse rigid transform.
func (p Pose) Inverse() Pose {
	q := p.Rotation.Conjugate()
	return Pose{q.Rotate(p.Position.Mul(-1)), q}
}
func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
