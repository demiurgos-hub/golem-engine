package objecttemplate

import (
	"fmt"
	"math"
	"strings"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// Transform places a template root. Scale is per-axis in the root frame and
// must be positive; the zero value means unit scale.
type Transform struct {
	Position p.Vec3
	Rotation p.Quat
	Scale    p.Vec3
}

// At returns an unscaled transform.
func At(position p.Vec3, rotation p.Quat) Transform {
	return Transform{Position: position, Rotation: rotation}
}

func (t Transform) scale() p.Vec3 {
	if t.Scale == (p.Vec3{}) {
		return p.Vec3{X: 1, Y: 1, Z: 1}
	}
	return t.Scale
}

func (t Transform) validate() error {
	s := t.scale()
	if !t.Position.InBounds() || !t.Rotation.Valid() || !s.Valid() || s.X <= 0 || s.Y <= 0 || s.Z <= 0 {
		return invalid("placement transform")
	}
	return nil
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// mat3 is a row-major 3x3 linear map.
type mat3 [9]float64

func identity3() mat3 { return mat3{1, 0, 0, 0, 1, 0, 0, 0, 1} }

func rotation3(q p.Quat) mat3 {
	x, y, z, w := float64(q.X), float64(q.Y), float64(q.Z), float64(q.W)
	return mat3{
		1 - 2*(y*y+z*z), 2 * (x*y - w*z), 2 * (x*z + w*y),
		2 * (x*y + w*z), 1 - 2*(x*x+z*z), 2 * (y*z - w*x),
		2 * (x*z - w*y), 2 * (y*z + w*x), 1 - 2*(x*x+y*y),
	}
}

func diagonal3(v p.Vec3) mat3 {
	return mat3{float64(v.X), 0, 0, 0, float64(v.Y), 0, 0, 0, float64(v.Z)}
}

func (a mat3) mul(b mat3) mat3 {
	var out mat3
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			out[r*3+c] = a[r*3]*b[c] + a[r*3+1]*b[3+c] + a[r*3+2]*b[6+c]
		}
	}
	return out
}

func (a mat3) transpose() mat3 { return mat3{a[0], a[3], a[6], a[1], a[4], a[7], a[2], a[5], a[8]} }

func (a mat3) apply(v p.Vec3) p.Vec3 {
	x, y, z := float64(v.X), float64(v.Y), float64(v.Z)
	return p.Vec3{X: float32(a[0]*x + a[1]*y + a[2]*z), Y: float32(a[3]*x + a[4]*y + a[5]*z), Z: float32(a[6]*x + a[7]*y + a[8]*z)}
}

// conjugate expresses the root-frame scale s in a frame rotated by r.
func conjugate(r mat3, s p.Vec3) mat3 { return r.transpose().mul(diagonal3(s)).mul(r) }

const shearTolerance = 1e-5

// diagonal reports the per-axis scale when a has no off-diagonal terms.
func (a mat3) diagonal() (p.Vec3, bool) {
	limit := shearTolerance * max(math.Abs(a[0]), math.Abs(a[4]), math.Abs(a[8]))
	for _, i := range []int{1, 2, 3, 5, 6, 7} {
		if math.Abs(a[i]) > limit {
			return p.Vec3{}, false
		}
	}
	return p.Vec3{X: float32(a[0]), Y: float32(a[4]), Z: float32(a[8])}, true
}

func unitScale(v p.Vec3) bool { return v == p.Vec3{X: 1, Y: 1, Z: 1} }

func normalized(q p.Quat) p.Quat {
	n := float32(math.Sqrt(float64(q.X*q.X + q.Y*q.Y + q.Z*q.Z + q.W*q.W)))
	if n == 0 {
		return p.Quat{W: 1}
	}
	return p.Quat{X: q.X / n, Y: q.Y / n, Z: q.Z / n, W: q.W / n}
}

func scaleVec(a, b p.Vec3) p.Vec3 { return p.Vec3{X: a.X * b.X, Y: a.Y * b.Y, Z: a.Z * b.Z} }

// vecKey and matKey identify exact float32 scales for shape sharing.
func vecKey(v p.Vec3) string {
	return fmt.Sprintf("%08x%08x%08x", math.Float32bits(v.X), math.Float32bits(v.Y), math.Float32bits(v.Z))
}

func matKey(a mat3) string {
	var b strings.Builder
	for _, v := range a {
		fmt.Fprintf(&b, "%08x", math.Float32bits(float32(v)))
	}
	return b.String()
}
