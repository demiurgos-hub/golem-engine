package main

import (
	"testing"
	"time"
)

func TestFullWorkloadCountsAndUniqueFloor(t *testing.T) {
	c, err := counts("full")
	if err != nil {
		t.Fatal(err)
	}
	if c.Characters != 1000 || c.DynamicBodies != 1000 || c.Projectiles != 500 || c.StaticColliders != 100000 || c.Triangles != 5000000 || c.Queries != 10000 {
		t.Fatalf("full workload changed: %+v", c)
	}
	if c.TilesX*c.TilesZ*c.Cells*c.Cells*2 != c.Triangles {
		t.Fatal("triangle count is not resident topology")
	}
	a := floorTile(0, 0, c, 384)
	b := floorTile(1, 0, c, 384)
	if len(a.Indices)/3 != 20000 || a.Vertices[0] == b.Vertices[0] {
		t.Fatal("floor tiles must own different geometry")
	}
	a.Vertices[0].Y = 10
	if b.Vertices[0].Y != 0 {
		t.Fatal("floor vertices alias")
	}
}

func TestLatencyUpperBoundsCannotPassFiftyMillisecondGate(t *testing.T) {
	var h histogram
	for i := 0; i < 100; i++ {
		h.add(50 * time.Millisecond)
	}
	if h.percentile(.99) < 50*time.Millisecond {
		t.Fatal("gate reports lower bin edge")
	}
	var tail histogram
	tail.add(3 * time.Second)
	if tail.percentile(.99) != 3*time.Second {
		t.Fatal("overflow must retain maximum")
	}
}
