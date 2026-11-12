package main

import (
	"fmt"
	"math"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

type capacityContact struct {
	Segment                 int
	Obstacle                string
	From, To                surface.Vec3
	Radius, Height          float64
	ContactFraction         float64
	MinimumSeparationMetres float64
}

type capacityClearanceEvidence struct {
	SegmentsChecked, ObstacleTests int
	HasMinimum                     bool
	MinimumSeparationMetres        float64
	Violations                     int
	FirstViolation                 *capacityContact
}

// segmentCylinderSeparation minimizes the motor's rounded-box signed distance
// during vertical overlap. Finite candidates cover faces, corner disks and
// interior nearest-face ties; no spatial or temporal sampling is involved.
func segmentCylinderSeparation(from, to surface.Vec3, radius, height float64, box geom.Bounds) (float64, bool) {
	delta := surface.Vec3{X: to.X - from.X, Y: to.Y - from.Y, Z: to.Z - from.Z}
	enter, leave := 0.0, 1.0
	if !clipMotorSlab(from.Y, delta.Y, box.Min.Y-height, box.Max.Y, &enter, &leave) {
		return 0, false
	}
	minimum := math.Inf(1)
	evaluate := func(t float64) {
		if t < enter || t > leave {
			return
		}
		p := surface.Vec3{X: from.X + delta.X*t, Z: from.Z + delta.Z*t}
		distance, _ := circleBoxSeparation(p, surface.Vec3{}, radius, box)
		minimum = math.Min(minimum, distance)
	}
	evaluate(enter)
	evaluate(leave)
	speedSquared := delta.X*delta.X + delta.Z*delta.Z
	if speedSquared > 0 {
		for _, corner := range [4][2]float64{{box.Min.X, box.Min.Z}, {box.Min.X, box.Max.Z}, {box.Max.X, box.Min.Z}, {box.Max.X, box.Max.Z}} {
			evaluate(((corner[0]-from.X)*delta.X + (corner[1]-from.Z)*delta.Z) / speedSquared)
		}
	}
	planes := [4][2]float64{{box.Min.X - from.X, -delta.X}, {from.X - box.Max.X, delta.X}, {box.Min.Z - from.Z, -delta.Z}, {from.Z - box.Max.Z, delta.Z}}
	for i, plane := range planes {
		if plane[1] != 0 {
			evaluate(-plane[0] / plane[1])
		}
		for _, other := range planes[i+1:] {
			if plane[1] != other[1] {
				evaluate((other[0] - plane[0]) / (plane[1] - other[1]))
			}
		}
	}
	return minimum, true
}

func capacityPhysicalClearance(start navigation.Vec3, corners []navigation.Corner, profile geom.Profile, volumes []geom.Volume) (capacityClearanceEvidence, error) {
	var evidence capacityClearanceEvidence
	for _, obstacle := range volumes {
		if obstacle.Exclude && !obstacle.Disabled && obstacle.Shape != "box" {
			return evidence, fmt.Errorf("capacity physical clearance requires the test motor's box obstacles: %s shape %q", obstacle.ID, obstacle.Shape)
		}
	}
	from := surface.Vec3{X: start.X, Y: start.Y, Z: start.Z}
	for i, corner := range corners {
		to := surface.Vec3{X: corner.Position.X, Y: corner.Position.Y, Z: corner.Position.Z}
		evidence.SegmentsChecked++
		for _, obstacle := range volumes {
			if !obstacle.Exclude || obstacle.Disabled {
				continue
			}
			evidence.ObstacleTests++
			separation, verticalOverlap := segmentCylinderSeparation(from, to, profile.Radius, profile.Height, obstacle.Bounds)
			if !verticalOverlap {
				continue
			}
			if !evidence.HasMinimum || separation < evidence.MinimumSeparationMetres {
				evidence.HasMinimum, evidence.MinimumSeparationMetres = true, separation
			}
			fraction := sweepCylinderBox(from, to, profile.Radius, profile.Height, obstacle.Bounds)
			initialOverlap := cylinderOverlapsBox(from, profile.Radius, profile.Height, obstacle.Bounds)
			if !initialOverlap && fraction >= 1 {
				continue
			}
			if initialOverlap {
				fraction = 0
			}
			evidence.Violations++
			if evidence.FirstViolation == nil {
				evidence.FirstViolation = &capacityContact{Segment: i, Obstacle: obstacle.ID, From: from, To: to, Radius: profile.Radius, Height: profile.Height, ContactFraction: fraction, MinimumSeparationMetres: separation}
			}
		}
		from = to
	}
	return evidence, nil
}
