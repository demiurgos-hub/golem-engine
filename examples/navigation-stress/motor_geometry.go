package main

import (
	"math"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

// clipMotorSlab intersects an open slab with the current motion interval.
// A tangent touches the boundary but never enters the blocker interior.
func clipMotorSlab(position, delta, low, high float64, enter, leave *float64) bool {
	if delta == 0 {
		return position > low && position < high
	}
	a, b := (low-position)/delta, (high-position)/delta
	if a > b {
		a, b = b, a
	}
	*enter, *leave = math.Max(*enter, a), math.Min(*leave, b)
	return *enter < *leave
}

// circleBoxSeparation returns signed horizontal separation and its directional
// derivative. Inside a rectangle, the nearest escape face controls separation;
// tied faces use the greatest derivative, as for the maximum of their planes.
func circleBoxSeparation(p, delta surface.Vec3, radius float64, box geom.Bounds) (float64, float64) {
	x := p.X - math.Max(box.Min.X, math.Min(p.X, box.Max.X))
	z := p.Z - math.Max(box.Min.Z, math.Min(p.Z, box.Max.Z))
	if x != 0 || z != 0 {
		distance := math.Hypot(x, z)
		return distance - radius, (x*delta.X + z*delta.Z) / distance
	}
	separation, derivative := math.Inf(-1), math.Inf(-1)
	for _, face := range [4][2]float64{{box.Min.X - p.X, -delta.X}, {p.X - box.Max.X, delta.X}, {box.Min.Z - p.Z, -delta.Z}, {p.Z - box.Max.Z, delta.Z}} {
		if face[0] > separation {
			separation, derivative = face[0], face[1]
		} else if face[0] == separation {
			derivative = math.Max(derivative, face[1])
		}
	}
	return separation - radius, derivative
}

func cylinderOverlapsBox(p surface.Vec3, radius, height float64, box geom.Bounds) bool {
	separation, _ := circleBoxSeparation(p, surface.Vec3{}, radius, box)
	return separation < 0 && p.Y < box.Max.Y && p.Y+height > box.Min.Y
}

// sweepCylinderBox uses the exact rounded-rectangle footprint, intersecting
// its two strips and four corner disks with the same vertical motion interval.
// Existing penetration may move strictly toward its nearest escape boundary;
// motion deeper into that penetration is blocked. This is a static test motor,
// not agent avoidance or a general rigid-body contact solver.
func sweepCylinderBox(from, target surface.Vec3, radius, height float64, box geom.Bounds) float64 {
	delta := surface.Vec3{X: target.X - from.X, Y: target.Y - from.Y, Z: target.Z - from.Z}
	separation, derivative := circleBoxSeparation(from, delta, radius, box)
	for _, face := range [2][2]float64{{box.Min.Y - height - from.Y, -delta.Y}, {from.Y - box.Max.Y, delta.Y}} {
		if face[0] > separation {
			separation, derivative = face[0], face[1]
		} else if face[0] == separation {
			derivative = math.Max(derivative, face[1])
		}
	}
	if separation < 0 && derivative > 0 {
		return 1
	}
	yEnter, yLeave := 0.0, 1.0
	if !clipMotorSlab(from.Y, delta.Y, box.Min.Y-height, box.Max.Y, &yEnter, &yLeave) {
		return 1
	}
	hit := 1.0
	for _, strip := range [2][4]float64{{box.Min.X - radius, box.Max.X + radius, box.Min.Z, box.Max.Z}, {box.Min.X, box.Max.X, box.Min.Z - radius, box.Max.Z + radius}} {
		enter, leave := yEnter, yLeave
		if clipMotorSlab(from.X, delta.X, strip[0], strip[1], &enter, &leave) && clipMotorSlab(from.Z, delta.Z, strip[2], strip[3], &enter, &leave) {
			hit = math.Min(hit, enter)
		}
	}
	if radius <= 0 {
		return hit
	}
	speedSquared := delta.X*delta.X + delta.Z*delta.Z
	for _, center := range [4][2]float64{{box.Min.X, box.Min.Z}, {box.Min.X, box.Max.Z}, {box.Max.X, box.Min.Z}, {box.Max.X, box.Max.Z}} {
		x, z := from.X-center[0], from.Z-center[1]
		c := x*x + z*z - radius*radius
		if speedSquared == 0 {
			if c < 0 {
				hit = math.Min(hit, yEnter)
			}
			continue
		}
		dot := x*delta.X + z*delta.Z
		discriminant := math.FMA(-speedSquared, c, dot*dot)
		if discriminant <= 0 {
			continue
		}
		// The product of roots avoids cancellation for a distant entry point.
		q := -dot - math.Copysign(math.Sqrt(discriminant), dot)
		enter, leave := q/speedSquared, c/q
		if enter > leave {
			enter, leave = leave, enter
		}
		enter, leave = math.Max(enter, yEnter), math.Min(leave, yLeave)
		if enter < leave {
			hit = math.Min(hit, enter)
		}
	}
	return hit
}
