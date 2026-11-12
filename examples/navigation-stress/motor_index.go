package main

import (
	"math"
	"math/bits"

	"github.com/demiurgos-hub/golem-engine/golem"
)

const collisionBucketSize = 16.0

type collisionCell struct{ x, z int }

func (w *collisionWorld) rebuildBuckets() {
	w.buckets = nil
	// Larger custom fixtures retain the exact linear implementation. This small
	// bitset handles the canonical workloads without mutable query scratch.
	if len(w.volumes) > 64 {
		return
	}
	buckets := make(map[collisionCell]uint64)
	for i, volume := range w.volumes {
		minX, minZ, maxX, maxZ, ok := bucketRange(volume.Bounds.Min.X, volume.Bounds.Min.Z, volume.Bounds.Max.X, volume.Bounds.Max.Z)
		if !ok || uint64(maxX-minX+1)*uint64(maxZ-minZ+1) > 4096 {
			return
		}
		for z := minZ; z <= maxZ; z++ {
			for x := minX; x <= maxX; x++ {
				buckets[collisionCell{x, z}] |= uint64(1) << i
			}
		}
	}
	w.buckets = buckets
}

func bucketRange(minX, minZ, maxX, maxZ float64) (int, int, int, int, bool) {
	values := [4]float64{minX, minZ, maxX, maxZ}
	var cells [4]int
	for i, value := range values {
		cell := math.Floor(value / collisionBucketSize)
		if math.IsNaN(cell) || math.IsInf(cell, 0) || math.Abs(cell) > 1<<25 {
			return 0, 0, 0, 0, false
		}
		cells[i] = int(cell)
	}
	return cells[0], cells[1], cells[2], cells[3], cells[0] <= cells[2] && cells[1] <= cells[3]
}

func (w *collisionWorld) candidateMask(minX, minZ, maxX, maxZ float64) (uint64, bool) {
	if w.buckets == nil {
		return 0, false
	}
	x0, z0, x1, z1, ok := bucketRange(minX, minZ, maxX, maxZ)
	if !ok || uint64(x1-x0+1)*uint64(z1-z0+1) > 64 {
		return 0, false
	}
	var mask uint64
	for z := z0; z <= z1; z++ {
		for x := x0; x <= x1; x++ {
			mask |= w.buckets[collisionCell{x, z}]
		}
	}
	return mask, true
}

func (w *collisionWorld) sweepFraction(v golem.NavigationMotion, radius, height float64) float64 {
	t := 1.0
	minX, maxX := math.Min(v.From.X, v.Target.X)-radius, math.Max(v.From.X, v.Target.X)+radius
	minZ, maxZ := math.Min(v.From.Z, v.Target.Z)-radius, math.Max(v.From.Z, v.Target.Z)+radius
	mask, indexed := w.candidateMask(minX, minZ, maxX, maxZ)
	for i := 0; i < len(w.volumes); i++ {
		if indexed {
			if mask == 0 {
				break
			}
			// Ascending set bits preserve the original volume visitation order.
			i = bits.TrailingZeros64(mask)
			mask &= mask - 1
		}
		box := w.volumes[i]
		if box.Disabled || !box.Exclude || math.Min(v.From.Y, v.Target.Y) > box.Bounds.Max.Y || math.Max(v.From.Y, v.Target.Y)+height < box.Bounds.Min.Y {
			continue
		}
		if maxX < box.Bounds.Min.X || minX > box.Bounds.Max.X || maxZ < box.Bounds.Min.Z || minZ > box.Bounds.Max.Z {
			continue
		}
		hit := sweepCylinderBox(v.From, v.Target, radius, height, box.Bounds)
		if hit < t {
			t = hit
		}
	}
	return t
}
