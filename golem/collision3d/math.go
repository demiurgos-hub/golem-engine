package collision3d

import "math"

const epsilon = 1e-9

func sub(a, b Vec3) Vec3 {
	return Vec3{X: a.X - b.X, Y: a.Y - b.Y, Z: a.Z - b.Z}
}

func add(a, b Vec3) Vec3 {
	return Vec3{X: a.X + b.X, Y: a.Y + b.Y, Z: a.Z + b.Z}
}

func mul(v Vec3, s float64) Vec3 {
	return Vec3{X: v.X * s, Y: v.Y * s, Z: v.Z * s}
}

func dot(a, b Vec3) float64 {
	return a.X*b.X + a.Y*b.Y + a.Z*b.Z
}

func length(v Vec3) float64 {
	return math.Sqrt(dot(v, v))
}

func normalize(v Vec3) Vec3 {
	l := length(v)
	if l <= epsilon {
		return Vec3{}
	}
	inv := 1 / l
	return Vec3{X: v.X * inv, Y: v.Y * inv, Z: v.Z * inv}
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// SegmentVsSphere tests the segment from from to to against a sphere.
// Returns the hit fraction, normal, and true on hit.
func SegmentVsSphere(from, to, center Vec3, radius float64) (float64, Vec3, bool) {
	d := sub(to, from)
	f := sub(from, center)
	a := dot(d, d)
	if a <= epsilon {
		return 0, Vec3{}, false
	}
	b := 2 * dot(f, d)
	c := dot(f, f) - radius*radius
	disc := b*b - 4*a*c
	if disc < 0 {
		return 0, Vec3{}, false
	}
	t := (-b - math.Sqrt(disc)) / (2 * a)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		return 0, Vec3{}, false
	}
	point := add(from, mul(d, t))
	return t, normalize(sub(point, center)), true
}

// SegmentVsAABB tests the segment from from to to against an AABB using the slab method.
func SegmentVsAABB(from, to, center, half Vec3) (float64, Vec3, bool) {
	d := sub(to, from)
	tMin := 0.0
	tMax := 1.0
	normal := Vec3{}

	for axis := 0; axis < 3; axis++ {
		var origin, dir, min, max float64
		var enterNormal Vec3
		switch axis {
		case 0:
			origin, dir, min, max = from.X, d.X, center.X-half.X, center.X+half.X
			enterNormal = Vec3{X: -1}
		case 1:
			origin, dir, min, max = from.Y, d.Y, center.Y-half.Y, center.Y+half.Y
			enterNormal = Vec3{Y: -1}
		default:
			origin, dir, min, max = from.Z, d.Z, center.Z-half.Z, center.Z+half.Z
			enterNormal = Vec3{Z: -1}
		}
		if math.Abs(dir) <= epsilon {
			if origin < min || origin > max {
				return 0, Vec3{}, false
			}
			continue
		}
		t1 := (min - origin) / dir
		t2 := (max - origin) / dir
		n := enterNormal
		if t1 > t2 {
			t1, t2 = t2, t1
			n = mul(enterNormal, -1)
		}
		if t1 > tMin {
			tMin = t1
			normal = n
		}
		if t2 < tMax {
			tMax = t2
		}
		if tMin > tMax {
			return 0, Vec3{}, false
		}
	}
	if tMin > 1 || tMax < 0 {
		return 0, Vec3{}, false
	}
	t := math.Max(0, tMin)
	if t > 1 {
		return 0, Vec3{}, false
	}
	return t, normal, true
}

// SortHits sorts a slice of RaycastHit values by ascending Fraction.
func SortHits(hits []RaycastHit) {
	for i := 1; i < len(hits); i++ {
		h := hits[i]
		j := i - 1
		for j >= 0 && hits[j].Fraction > h.Fraction {
			hits[j+1] = hits[j]
			j--
		}
		hits[j+1] = h
	}
}
