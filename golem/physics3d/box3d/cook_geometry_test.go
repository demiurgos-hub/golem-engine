package box3d

import (
	"errors"
	"math"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func TestCookConvexUsesHullComplexity(t *testing.T) {
	source := p.ShapeDesc{Kind: p.Convex}
	for x := -1; x <= 1; x += 2 {
		for y := -1; y <= 1; y += 2 {
			for z := -1; z <= 1; z += 2 {
				source.Vertices = append(source.Vertices, p.Vec3{X: float32(x), Y: float32(y), Z: float32(z)})
			}
		}
	}
	for range 200 {
		source.Vertices = append(source.Vertices, source.Vertices[:8]...)
		source.Vertices = append(source.Vertices, p.Vec3{})
	}
	cooked, err := CookShape(source, DefaultCookSettings())
	if err != nil {
		t.Fatal(err)
	}
	if cooked.leaves[0].hull.VertexCount != 8 || len(cooked.Source().Vertices) != len(source.Vertices) {
		t.Fatal("input ownership or unreduced hull geometry changed")
	}
}

func TestCookConvexHullVertexLimit(t *testing.T) {
	source := p.ShapeDesc{Kind: p.Convex}
	for i := range 2000 {
		// A Fibonacci sphere places every point on the hull.
		y := 1 - 2*(float64(i)+.5)/2000
		r, angle := math.Sqrt(1-y*y), float64(i)*math.Pi*(3-math.Sqrt(5))
		source.Vertices = append(source.Vertices, p.Vec3{X: float32(r * math.Cos(angle)), Y: float32(y), Z: float32(r * math.Sin(angle))})
	}
	if _, err := CookShape(source, DefaultCookSettings()); !errors.Is(err, p.ErrUnsupported) {
		t.Fatalf("unreduced hull of 2000 surface points cooked: %v", err)
	}
	source.HullVertexLimit = 255
	cooked, err := CookShape(source, DefaultCookSettings())
	if err != nil {
		t.Fatal(err)
	}
	if count := cooked.leaves[0].hull.VertexCount; count < p.MinHullVertexLimit || count > 255 {
		t.Fatalf("reduced hull has %d vertices", count)
	}
	unlimited := source
	unlimited.HullVertexLimit = 0
	if shapeDigest(source, cooked.geometry) == nil || string(shapeDigest(source, cooked.geometry)) == string(shapeDigest(unlimited, cooked.geometry)) {
		t.Fatal("hull vertex limit is not part of the shape identity")
	}
}

func TestCookRetainsSmallNonzeroTriangles(t *testing.T) {
	source := p.ShapeDesc{Kind: p.TriangleMesh,
		Vertices: []p.Vec3{{}, {X: 1}, {Z: 1}, {X: 2}, {X: 2.0001}, {X: 2, Z: .0001}},
		Indices:  []uint32{0, 2, 1, 3, 5, 4},
	}
	cooked, err := CookShape(source, DefaultCookSettings())
	if err != nil {
		t.Fatal(err)
	}
	if cooked.leaves[0].mesh.TriangleCount != 2 || cooked.leaves[0].mesh.DegenerateCount != 0 {
		t.Fatal("small authored triangle was discarded")
	}
}
