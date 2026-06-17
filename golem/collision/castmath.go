package collision

import "math"

// SegmentVsCircle tests the segment from (px,py) to (qx,qy) against a circle
// at (cx,cy) with radius r. Returns (fraction, normal, true) on hit.
// normal points away from the circle centre toward the cast origin.
func SegmentVsCircle(px, py, qx, qy, cx, cy, r float64) (float64, Vec2, bool) {
	dx, dy := qx-px, qy-py
	fx, fy := px-cx, py-cy
	a := dx*dx + dy*dy
	if a == 0 {
		return 0, Vec2{}, false
	}
	b := 2 * (fx*dx + fy*dy)
	c := fx*fx + fy*fy - r*r
	disc := b*b - 4*a*c
	if disc < 0 {
		return 0, Vec2{}, false
	}
	t := (-b - math.Sqrt(disc)) / (2 * a)
	if t < 0 {
		t = 0
	}
	if t > 1 {
		return 0, Vec2{}, false
	}
	hx, hy := px+t*dx, py+t*dy
	nx, ny := hx-cx, hy-cy
	len_ := math.Sqrt(nx*nx + ny*ny)
	if len_ > 0 {
		nx, ny = nx/len_, ny/len_
	}
	return t, Vec2{X: nx, Y: ny}, true
}

// SegmentVsAABB tests the segment from (px,py) to (qx,qy) against the AABB
// centred at (ex,ey) with half-extents (hw,hh) using the slab method.
// Returns (fraction, normal, true) on hit; normal points away from the hit face.
func SegmentVsAABB(px, py, qx, qy, ex, ey, hw, hh float64) (float64, Vec2, bool) {
	dx, dy := qx-px, qy-py
	tMin := 0.0
	tMax := 1.0
	var nx, ny float64

	for axis := 0; axis < 2; axis++ {
		var d, p, min, max float64
		if axis == 0 {
			d, p, min, max = dx, px, ex-hw, ex+hw
		} else {
			d, p, min, max = dy, py, ey-hh, ey+hh
		}
		if d == 0 {
			if p < min || p > max {
				return 0, Vec2{}, false
			}
			continue
		}
		t1 := (min - p) / d
		t2 := (max - p) / d
		var enterNorm float64
		if t1 > t2 {
			t1, t2 = t2, t1
			enterNorm = 1
		} else {
			enterNorm = -1
		}
		if t1 > tMin {
			tMin = t1
			if axis == 0 {
				nx, ny = enterNorm, 0
			} else {
				nx, ny = 0, enterNorm
			}
		}
		if t2 < tMax {
			tMax = t2
		}
		if tMin > tMax {
			return 0, Vec2{}, false
		}
	}

	if tMin > 1 || tMax < 0 {
		return 0, Vec2{}, false
	}

	t := math.Max(0, tMin)
	if t > 1 {
		return 0, Vec2{}, false
	}

	// Normalise the axis vector (may not be unit if start is inside).
	nlen := math.Sqrt(nx*nx + ny*ny)
	if nlen > 0 {
		nx, ny = nx/nlen, ny/nlen
	}
	return t, Vec2{X: nx, Y: ny}, true
}

// SegmentVsRoundedRect tests the segment from (px,py) to (qx,qy) against a
// rounded rectangle centred at (ex,ey) with half-extents (ew,eh) and corner
// radius r. This is the Minkowski sum of a circle and an AABB, arising from
// CircleCast-vs-AABB and BoxCast-vs-Circle queries.
// Returns (fraction, normal, true) for the earliest hit in [0,1].
func SegmentVsRoundedRect(px, py, qx, qy, ex, ey, ew, eh, r float64) (float64, Vec2, bool) {
	bestT := math.MaxFloat64
	bestN := Vec2{}
	found := false

	tryHit := func(t float64, n Vec2, ok bool) {
		if ok && t < bestT {
			bestT = t
			bestN = n
			found = true
		}
	}

	// Four corner circles at (ex±ew, ey±eh).
	for _, cx := range []float64{ex - ew, ex + ew} {
		for _, cy := range []float64{ey - eh, ey + eh} {
			t, n, ok := SegmentVsCircle(px, py, qx, qy, cx, cy, r)
			// Only accept if the hit point is in the corner quadrant (|dx|>=ew or |dy|>=eh).
			if ok {
				hx, hy := px+t*(qx-px), py+t*(qy-py)
				if math.Abs(hx-ex) >= ew-1e-9 && math.Abs(hy-ey) >= eh-1e-9 {
					tryHit(t, n, true)
				}
			}
		}
	}

	// Extended AABB slabs: x-axis expanded sides (|y| ≤ eh), x-extents ew+r.
	if t, n, ok := SegmentVsAABB(px, py, qx, qy, ex, ey, ew+r, eh); ok {
		hx := px + t*(qx-px)
		if math.Abs(hx-ex) > ew-1e-9 {
			tryHit(t, n, true)
		}
	}
	// y-axis expanded sides (|x| ≤ ew), y-extents eh+r.
	if t, n, ok := SegmentVsAABB(px, py, qx, qy, ex, ey, ew, eh+r); ok {
		hy := py + t*(qy-py)
		if math.Abs(hy-ey) > eh-1e-9 {
			tryHit(t, n, true)
		}
	}

	// Inner rectangle: start point already inside the rounded rect.
	if !found {
		// Check if the segment origin is inside; if so, t=0.
		dx, dy := px-ex, py-ey
		clampX := math.Max(math.Abs(dx)-ew, 0)
		clampY := math.Max(math.Abs(dy)-eh, 0)
		if clampX*clampX+clampY*clampY < r*r {
			bestT = 0
			bestN = Vec2{}
			found = true
		}
	}

	if !found {
		return 0, Vec2{}, false
	}
	return bestT, bestN, true
}

// SortHits sorts a slice of RaycastHit values in-place by ascending Fraction.
// Uses a simple insertion sort — cast lists are almost always short.
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
