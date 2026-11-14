package box3d

import (
	"math"
	"math/rand/v2"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/geometry"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
)

// Native heightfields and shared scaled geometry round grid points, scales and
// poses differently from the expanded or baked shapes they replace, so the two
// paths agree within a few float32 ulps rather than bit for bit.
const equivalenceTolerance = 1e-3

func equivalenceSource(id string) *content.Source {
	layers := make([]uint32, 32)
	for i := range layers {
		layers[i] = ^uint32(0)
	}
	return &content.Source{Generator: content.GeneratorV2, Version: 2, WorldID: id, Revision: 1, CoordinateLimit: p.CoordinateLimit, LayerCollisionMasks: layers,
		Materials: []content.Material{{ID: "default", FrictionCombine: "average", BounceCombine: "average"}}}
}

func addEquivalenceCollider(source *content.Source, id, shape, asset string, pose p.Pose, matrix [16]float32) {
	source.Bodies = append(source.Bodies, content.Body{ID: id, Kind: "static", CollisionDetection: "discrete", Mass: 1, Position: pose.Position, Rotation: pose.Rotation,
		AutomaticCenterOfMass: true, AutomaticInertiaTensor: true, Enabled: true, Active: true})
	source.Colliders = append(source.Colliders, content.Collider{ID: id, BodyID: id, Shape: shape, AssetID: asset, MaterialID: "default", Rotation: p.Quat{W: 1},
		GeometryTransform: &matrix, Enabled: true, Active: true, Solid: true, Query: true})
}

// loadEquivalencePair loads source through content.Load and, separately, as the
// fully transformed geometry Collider.Geometry produces under each authored pose.
func loadEquivalencePair(t *testing.T, source *content.Source, want p.ShapeKind) (*World, *World) {
	t.Helper()
	assets := map[string]content.Asset{}
	for _, asset := range source.Assets {
		assets[asset.ID] = asset
	}
	bodies := map[string]content.Body{}
	for _, body := range source.Bodies {
		bodies[body.ID] = body
	}
	for _, collider := range source.Colliders {
		shape, _, _, err := collider.Placement(assets, bodies[collider.BodyID])
		if err != nil || shape.Kind != want {
			t.Fatalf("collider %s placement kind %d, want %d: %v", collider.ID, shape.Kind, want, err)
		}
	}
	placed := cookWorld(t)
	if _, err := content.Load(placed, source, nil); err != nil {
		t.Fatal(err)
	}
	baked := cookWorld(t)
	for _, collider := range source.Colliders {
		shape, err := collider.Geometry(assets)
		if err != nil {
			t.Fatal(err)
		}
		body := bodies[collider.BodyID]
		bd := p.DefaultBody(p.Static)
		bd.Pose = p.Pose{Position: body.Position, Rotation: body.Rotation}
		bh, err := baked.CreateBody(bd)
		if err != nil {
			t.Fatal(err)
		}
		sh, err := baked.CreateShape(shape)
		if err != nil {
			t.Fatal(err)
		}
		cd := p.DefaultCollider(bh, sh)
		cd.LocalPose = p.Pose{Position: collider.Center, Rotation: collider.Rotation}
		if _, err = baked.CreateCollider(cd); err != nil {
			t.Fatal(err)
		}
	}
	if err := baked.Synchronize(); err != nil {
		t.Fatal(err)
	}
	return placed, baked
}

func compareEquivalentQueries(t *testing.T, placed, baked *World, low, high p.Vec3, seed uint64) {
	t.Helper()
	random := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	point := func() p.Vec3 {
		return p.Vec3{X: low.X + (high.X-low.X)*random.Float32(), Y: low.Y + (high.Y-low.Y)*random.Float32(), Z: low.Z + (high.Z-low.Z)*random.Float32()}
	}
	filter := p.QueryFilter{Mask: ^uint64(0)}
	run := func(w *World, q p.Query) (p.Hit, bool) {
		hits := make([]p.Hit, 1)
		result, err := w.Query(q, hits)
		if err != nil {
			t.Fatal(err)
		}
		return hits[0], result.Count > 0
	}
	hitsSeen, overlapsSeen := 0, 0
	for i := range 3000 {
		origin := point()
		origin.Y = high.Y
		direction := p.Vec3{X: random.Float32() - .5, Y: -1 - random.Float32(), Z: random.Float32() - .5}
		direction = direction.Mul((high.Y - low.Y) * 1.5 / direction.Length())
		q := p.Query{Kind: p.Ray, Pose: p.Pose{Position: origin, Rotation: p.Quat{W: 1}}, Translation: direction, Filter: filter}
		if i%2 == 1 {
			q.Kind, q.Shape = p.Sweep, p.ShapeDesc{Kind: p.Sphere, Radius: .3}
		}
		a, foundA := run(placed, q)
		b, foundB := run(baked, q)
		if foundA != foundB {
			t.Fatalf("query %d kind %d from %v along %v: placed hit %v, baked hit %v", i, q.Kind, origin, direction, foundA, foundB)
		}
		if !foundA {
			continue
		}
		hitsSeen++
		if math.Abs(float64(a.Distance-b.Distance)) > equivalenceTolerance || a.Point.Sub(b.Point).Length() > equivalenceTolerance || q.Kind == p.Ray && a.Normal.Sub(b.Normal).Length() > equivalenceTolerance {
			t.Fatalf("query %d kind %d from %v along %v: placed %+v, baked %+v", i, q.Kind, origin, direction, a, b)
		}
	}
	for i := range 3000 {
		q := p.Query{Kind: p.Overlap, Mode: p.Any, Shape: p.ShapeDesc{Kind: p.Sphere, Radius: .3}, Pose: p.Pose{Position: point(), Rotation: p.Quat{W: 1}}, Filter: filter}
		_, foundA := run(placed, q)
		_, foundB := run(baked, q)
		if foundA != foundB {
			t.Fatalf("overlap %d at %v: placed %v, baked %v", i, q.Pose.Position, foundA, foundB)
		}
		if foundA {
			overlapsSeen++
		}
	}
	if hitsSeen < 50 || overlapsSeen < 10 {
		t.Fatalf("query sample missed the geometry: %d cast hits, %d overlaps", hitsSeen, overlapsSeen)
	}
}

func TestNativeHeightFieldMatchesExpandedTerrain(t *testing.T) {
	const n = 33
	random := rand.New(rand.NewPCG(7, 11))
	field := &geometry.Heightfield{Resolution: n, Size: [3]float32{16, 5, 12}, Samples: make([]float32, n*n), Surface: make([]byte, ((n-1)*(n-1)+7)/8)}
	for z := range n {
		for x := range n {
			wave := .5 + .3*math.Sin(float64(x)*.4)*math.Cos(float64(z)*.3) + .15*random.Float64()
			field.Samples[z*n+x] = float32(math.Round(wave*32766) / 32766)
		}
	}
	for cell := range (n - 1) * (n - 1) {
		if random.IntN(10) != 0 {
			field.Surface[cell/8] |= 1 << uint(cell%8)
		}
	}
	source := equivalenceSource("terrain-equivalence")
	source.Assets = []content.Asset{{ID: "terrain", Heightfield: field}}
	yaw := float32(.4)
	addEquivalenceCollider(source, "terrain", "terrain", "terrain", p.Pose{Position: p.Vec3{X: 3, Y: -1, Z: 2}, Rotation: p.Quat{Y: float32(math.Sin(float64(yaw) / 2)), W: float32(math.Cos(float64(yaw) / 2))}},
		[16]float32{0: 1, 5: 1, 10: 1, 15: 1})
	placed, baked := loadEquivalencePair(t, source, p.HeightField)
	compareEquivalentQueries(t, placed, baked, p.Vec3{X: -6, Y: -1.5, Z: -4}, p.Vec3{X: 22, Y: 5, Z: 20}, 1)
}

// The Scribe fixture's synthetic heights are off Unity's 16-bit grid, so they
// keep the expanded mesh; snapped to the grid, the same holes and pose load natively.
func TestScribeTerrainFixtureHeightFieldPlacement(t *testing.T) {
	source, _, err := content.DecodeFile("../../../golem-unity/Tests/Fixtures/physics-source-v2.json", content.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for i, collider := range source.Colliders {
		if collider.Shape == "terrain" {
			source.Colliders = source.Colliders[i : i+1]
			break
		}
	}
	source.Bodies[0].OwnerID = ""
	low, high := p.Vec3{X: -.5, Y: 1, Z: -.5}, p.Vec3{X: 4.5, Y: 11, Z: 4.5}
	placed, baked := loadEquivalencePair(t, source, p.TriangleMesh)
	compareEquivalentQueries(t, placed, baked, low, high, 2)
	for i := range source.Assets {
		if field := source.Assets[i].Heightfield; field != nil {
			for j, sample := range field.Samples {
				field.Samples[j] = float32(math.Round(float64(sample)*32766) / 32766)
			}
		}
	}
	placed, baked = loadEquivalencePair(t, source, p.HeightField)
	compareEquivalentQueries(t, placed, baked, low, high, 2)
}

func TestSharedScaledGeometryMatchesBakedTransforms(t *testing.T) {
	random := rand.New(rand.NewPCG(3, 5))
	const n = 9
	mesh := content.Asset{ID: "patch"}
	for z := range n {
		for x := range n {
			mesh.Vertices = append(mesh.Vertices, p.Vec3{X: float32(x) - 4, Y: random.Float32() * .6, Z: float32(z) - 4})
		}
	}
	for z := range n - 1 {
		for x := range n - 1 {
			a := uint32(z*n + x)
			mesh.Indices = append(mesh.Indices, a, a+n, a+n+1, a, a+n+1, a+1)
		}
	}
	hull := content.Asset{ID: "rock"}
	for range 24 {
		hull.Vertices = append(hull.Vertices, p.Vec3{X: random.Float32()*2 - 1, Y: random.Float32()*2 - 1, Z: random.Float32()*2 - 1})
	}
	hull.Indices = []uint32{0, 1, 2}
	affine := func(axis p.Vec3, angle float64, scale, offset p.Vec3) [16]float32 {
		axis = axis.Mul(1 / axis.Length())
		s, c := math.Sin(angle), math.Cos(angle)
		x, y, z := float64(axis.X), float64(axis.Y), float64(axis.Z)
		r := [3][3]float64{{c + x*x*(1-c), x*y*(1-c) - z*s, x*z*(1-c) + y*s}, {y*x*(1-c) + z*s, c + y*y*(1-c), y*z*(1-c) - x*s}, {z*x*(1-c) - y*s, z*y*(1-c) + x*s, c + z*z*(1-c)}}
		k := [3]float64{float64(scale.X), float64(scale.Y), float64(scale.Z)}
		o := [3]float32{offset.X, offset.Y, offset.Z}
		var m [16]float32
		for row := range 3 {
			for column := range 3 {
				m[row*4+column] = float32(r[row][column] * k[column])
			}
			m[row*4+3] = o[row]
		}
		m[15] = 1
		return m
	}
	for _, kind := range []struct {
		shape, asset string
		want         p.ShapeKind
	}{{"mesh", "patch", p.TriangleMesh}, {"convex", "rock", p.Convex}} {
		t.Run(kind.shape, func(t *testing.T) {
			source := equivalenceSource("shared-" + kind.shape)
			source.Assets = []content.Asset{mesh, hull}
			addEquivalenceCollider(source, "a", kind.shape, kind.asset, p.Pose{Rotation: p.Quat{W: 1}}, affine(p.Vec3{X: 1, Y: 2, Z: 3}, .7, p.Vec3{X: 1.5, Y: .7, Z: 2.2}, p.Vec3{X: .25, Y: -.5, Z: 1}))
			addEquivalenceCollider(source, "b", kind.shape, kind.asset, p.Pose{Position: p.Vec3{X: 14}, Rotation: p.Quat{W: 1}}, affine(p.Vec3{X: -2, Y: 1, Z: .5}, 2.1, p.Vec3{X: -1.2, Y: 1.8, Z: .9}, p.Vec3{}))
			addEquivalenceCollider(source, "c", kind.shape, kind.asset, p.Pose{Position: p.Vec3{X: 28}, Rotation: p.Quat{W: 1}}, [16]float32{0: 1.5, 5: 1.5, 10: 1.5, 15: 1})
			placed, baked := loadEquivalencePair(t, source, kind.want)
			compareEquivalentQueries(t, placed, baked, p.Vec3{X: -10, Y: -6, Z: -10}, p.Vec3{X: 38, Y: 6, Z: 10}, 3)
		})
	}
}
