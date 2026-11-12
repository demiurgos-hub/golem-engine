package main

import (
	"math"
	"math/rand"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func linearSweep(volumes []geom.Volume, v golem.NavigationMotion, radius, height float64) float64 {
	t := 1.0
	minX, maxX := math.Min(v.From.X, v.Target.X)-radius, math.Max(v.From.X, v.Target.X)+radius
	minZ, maxZ := math.Min(v.From.Z, v.Target.Z)-radius, math.Max(v.From.Z, v.Target.Z)+radius
	for _, box := range volumes {
		if box.Disabled || !box.Exclude || math.Min(v.From.Y, v.Target.Y) > box.Bounds.Max.Y || math.Max(v.From.Y, v.Target.Y)+height < box.Bounds.Min.Y {
			continue
		}
		if maxX < box.Bounds.Min.X || minX > box.Bounds.Max.X || maxZ < box.Bounds.Min.Z || minZ > box.Bounds.Max.Z {
			continue
		}
		if hit := sweepCylinderBox(v.From, v.Target, radius, height, box.Bounds); hit < t {
			t = hit
		}
	}
	return t
}

func TestMotorBucketsMatchLinearGeometry(t *testing.T) {
	rng := rand.New(rand.NewSource(41))
	w := collisionWorld{}
	for i := 0; i < 34; i++ {
		x, z := rng.Float64()*256-128, rng.Float64()*256-128
		w.volumes = append(w.volumes, geom.Volume{Exclude: i%5 != 0, Disabled: i%7 == 0, Bounds: geom.Bounds{Min: geom.Vec3{X: x, Y: -1, Z: z}, Max: geom.Vec3{X: x + rng.Float64()*20, Y: rng.Float64() * 8, Z: z + rng.Float64()*20}}})
	}
	w.rebuildBuckets()
	if w.buckets == nil {
		t.Fatal("canonical-sized input unexpectedly fell back")
	}
	for i := 0; i < 10000; i++ {
		v := golem.NavigationMotion{From: surface.Vec3{X: rng.Float64()*300 - 150, Y: rng.Float64() * 10, Z: rng.Float64()*300 - 150}}
		v.Target = surface.Vec3{X: v.From.X + rng.Float64()*20 - 10, Y: v.From.Y + rng.Float64()*2 - 1, Z: v.From.Z + rng.Float64()*20 - 10}
		radius, height := rng.Float64()*3, rng.Float64()*3
		if got, want := w.sweepFraction(v, radius, height), linearSweep(w.volumes, v, radius, height); got != want {
			t.Fatalf("case%d motion%+v radius%g height%g: indexed%g linear%g", i, v, radius, height, got, want)
		}
	}
	t.Run("Allocations", func(t *testing.T) {
		if got := testing.AllocsPerRun(1000, func() {
			w.sweepFraction(golem.NavigationMotion{From: surface.Vec3{X: -16.1}, Target: surface.Vec3{X: -15.9}}, .8, 2.4)
		}); got != 0 {
			t.Fatalf("read-only bucket lookup allocated %v times", got)
		}
	})
}

func TestMotorBucketsBoundariesLongSegmentsAndActivation(t *testing.T) {
	w := collisionWorld{volumes: []geom.Volume{{Exclude: true, Bounds: geom.Bounds{Min: geom.Vec3{X: -16, Y: -2, Z: -16}, Max: geom.Vec3{X: 0, Y: 4, Z: 0}}}}}
	w.rebuildBuckets()
	for _, v := range []golem.NavigationMotion{
		{From: surface.Vec3{X: -200, Z: -8}, Target: surface.Vec3{X: 200, Z: -8}},
		{From: surface.Vec3{X: -16, Z: -17}, Target: surface.Vec3{X: -16, Z: 1}},
		{From: surface.Vec3{X: -17, Z: -16}, Target: surface.Vec3{X: -16, Z: -16}},
		{From: surface.Vec3{X: .8, Z: -8}, Target: surface.Vec3{X: .8, Z: -9}},
		{From: surface.Vec3{X: -10000, Z: -10000}, Target: surface.Vec3{X: 10000, Z: 10000}},
	} {
		for _, radius := range []float64{0, .4, .8, 17} {
			if got, want := w.sweepFraction(v, radius, 1.8), linearSweep(w.volumes, v, radius, 1.8); got != want {
				t.Fatalf("boundary/long sweep %+v radius%g: indexed%g linear%g", v, radius, got, want)
			}
		}
	}
	w.volumes[0].Bounds.Min.X, w.volumes[0].Bounds.Max.X = 32, 48
	w.rebuildBuckets()
	v := golem.NavigationMotion{From: surface.Vec3{X: 20, Z: -8}, Target: surface.Vec3{X: 40, Z: -8}}
	if got, want := w.sweepFraction(v, .4, 1.8), linearSweep(w.volumes, v, .4, 1.8); got != want || got == 1 {
		t.Fatal("accepted moved volume absent from rebuilt buckets")
	}
	for len(w.volumes) <= 64 {
		w.volumes = append(w.volumes, w.volumes[0])
	}
	w.rebuildBuckets()
	if w.buckets != nil || w.sweepFraction(v, .4, 1.8) != linearSweep(w.volumes, v, .4, 1.8) {
		t.Fatal("large custom fixture did not retain exact linear fallback")
	}
}
