// Package querygeom shares narrowphase geometry between 2D backend adapters.
// Shape extraction, broadphase filtering, and hit selection belong to adapters.
package querygeom

import (
	"math"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
)

type kind uint8

const (
	invalid kind = iota
	circle
	box
)

// Primitive is a measured circle or axis-aligned box. Its zero value is invalid.
type Primitive struct {
	kind                 kind
	radius, halfW, halfH float64
}

// Circle describes a circle without validating or normalizing its radius.
func Circle(radius float64) Primitive { return Primitive{kind: circle, radius: radius} }

// Box describes a box without validating or normalizing its half-extents.
func Box(halfW, halfH float64) Primitive { return Primitive{kind: box, halfW: halfW, halfH: halfH} }

// FromShape measures the supported value shapes. Pointer and nil shapes miss,
// matching the backend registration switches.
func FromShape(shape collision.Shape) (Primitive, bool) {
	switch s := shape.(type) {
	case collision.Circle:
		return Circle(s.R), true
	case collision.AABB:
		return Box(s.W/2, s.H/2), true
	default:
		return Primitive{}, false
	}
}

// CastSegment dispatches the adapters' existing query encoding: zero extents
// mean a ray, two positive extents mean a box, and all other combinations mean
// a circle of radius -qhw. In particular, degenerate boxes retain that fallback.
func CastSegment(px, py, qx, qy, qhw, qhh, ex, ey float64, target Primitive) (float64, collision.Vec2, bool) {
	switch {
	case qhw == 0 && qhh == 0:
		switch target.kind {
		case circle:
			return collision.SegmentVsCircle(px, py, qx, qy, ex, ey, target.radius)
		case box:
			return collision.SegmentVsAABB(px, py, qx, qy, ex, ey, target.halfW, target.halfH)
		}
	case qhw > 0 && qhh > 0:
		switch target.kind {
		case circle:
			return collision.SegmentVsRoundedRect(px, py, qx, qy, ex, ey, qhw, qhh, target.radius)
		case box:
			return collision.SegmentVsAABB(px, py, qx, qy, ex, ey, target.halfW+qhw, target.halfH+qhh)
		}
	default:
		radius := -qhw
		switch target.kind {
		case circle:
			return collision.SegmentVsCircle(px, py, qx, qy, ex, ey, radius+target.radius)
		case box:
			return collision.SegmentVsRoundedRect(px, py, qx, qy, ex, ey, target.halfW, target.halfH, radius)
		}
	}
	return 0, collision.Vec2{}, false
}

// OverlapBox tests strict overlap against a box query; touching does not overlap.
func OverlapBox(qx, qy, hw, hh, ex, ey float64, target Primitive) bool {
	switch target.kind {
	case circle:
		cx := math.Max(qx-hw, math.Min(ex, qx+hw))
		cy := math.Max(qy-hh, math.Min(ey, qy+hh))
		dx, dy := ex-cx, ey-cy
		return dx*dx+dy*dy < target.radius*target.radius
	case box:
		return math.Abs(qx-ex) < hw+target.halfW && math.Abs(qy-ey) < hh+target.halfH
	}
	return false
}

// OverlapCircle tests strict overlap against a circle query.
func OverlapCircle(qx, qy, radius, ex, ey float64, target Primitive) bool {
	switch target.kind {
	case circle:
		dx, dy := qx-ex, qy-ey
		total := radius + target.radius
		return dx*dx+dy*dy < total*total
	case box:
		cx := math.Max(ex-target.halfW, math.Min(qx, ex+target.halfW))
		cy := math.Max(ey-target.halfH, math.Min(qy, ey+target.halfH))
		dx, dy := qx-cx, qy-cy
		return dx*dx+dy*dy < radius*radius
	}
	return false
}
