package objecttemplate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/geometry"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/box3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func identityMatrix() *[16]float32 {
	return &[16]float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
}

func staticBody(id, kind string) content.Body {
	return content.Body{ID: id, Kind: kind, CollisionDetection: "discrete", Rotation: p.Quat{W: 1}, Enabled: true, Active: true, Mass: 1,
		AutomaticCenterOfMass: true, AutomaticInertiaTensor: true, InertiaRotation: p.Quat{W: 1}}
}

func collider(id, body, shape string) content.Collider {
	return content.Collider{ID: id, BodyID: body, Shape: shape, Enabled: true, Active: true, Solid: true, Query: true, MaterialID: "default", Rotation: p.Quat{W: 1}}
}

func cubeAsset(id string, kind geometry.Kind) geometry.Asset {
	a := geometry.Asset{ID: id, Kind: kind}
	for _, z := range []float64{-.5, .5} {
		for _, y := range []float64{-.5, .5} {
			for _, x := range []float64{-.5, .5} {
				a.Vertices = append(a.Vertices, geometry.Vec3{X: x, Y: y, Z: z})
			}
		}
	}
	for _, f := range [][3]uint32{{0, 2, 1}, {1, 2, 3}, {4, 5, 6}, {5, 7, 6}, {0, 1, 4}, {1, 5, 4}, {2, 6, 3}, {3, 6, 7}, {0, 4, 2}, {2, 4, 6}, {1, 3, 5}, {3, 7, 5}} {
		a.Triangles = append(a.Triangles, geometry.Triangle{Indices: f, Area: 1})
	}
	return a
}

// testCatalog returns a decoded 3D catalog with rock, rock-copy, terrain,
// building and portal templates.
func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	c := testCatalogDocument()
	return writeCatalog(t, c, testAssets())
}

func testAssets() []geometry.Asset {
	plane := geometry.Asset{ID: "nav-plane", Kind: geometry.MeshFloat64, Vertices: []geometry.Vec3{{X: -2, Z: -2}, {X: 2, Z: -2}, {X: 2, Z: 2}, {X: -2, Z: 2}},
		Triangles: []geometry.Triangle{{Indices: [3]uint32{0, 2, 1}, Area: 1}, {Indices: [3]uint32{0, 3, 2}, Area: 1}}}
	ground := geometry.Asset{ID: "ground", Kind: geometry.HeightfieldFloat32, Heightfield: &geometry.Heightfield{Resolution: 3, Size: [3]float32{8, 4, 8}, Samples: make([]float32, 9), Surface: []byte{0x0f}, Area: 1}}
	return []geometry.Asset{cubeAsset("cube", geometry.MeshFloat32), plane, ground}
}

func testCatalogDocument() Catalog {
	masks := make([]uint32, 32)
	for i := range masks {
		masks[i] = math.MaxUint32
	}
	convex := collider("root/convex", "root", "convex")
	convex.AssetID = "cube"
	convex.GeometryTransform = &[16]float32{2, 0, 0, 0, 0, 1, 0, .5, 0, 0, 2, 0, 0, 0, 0, 1}
	capsule := collider("root/capsule", "root", "capsule")
	capsule.Center, capsule.Radius, capsule.Height, capsule.Axis = p.Vec3{X: 3, Y: 1}, .5, 2, 1
	terrain := collider("root/terrain", "root", "terrain")
	terrain.AssetID, terrain.GeometryTransform = "ground", identityMatrix()
	terrain.Metadata = []content.Property{{Key: "game.terrain", Value: "true"}}
	mesh := collider("root/mesh", "root", "mesh")
	mesh.AssetID, mesh.GeometryTransform, mesh.Center = "cube", identityMatrix(), p.Vec3{Z: 5}
	sensor := collider("root/sensor", "root", "sphere")
	sensor.Radius, sensor.Solid, sensor.Sensor = 1, false, true
	tiltedCapsule := capsule
	tiltedCapsule.Center, tiltedCapsule.Rotation = p.Vec3{}, p.Quat{Z: .38268343, W: .9238795}
	tiltedConvex := convex
	tiltedConvex.GeometryTransform = &[16]float32{.70710677, -.70710677, 0, 0, .70710677, .70710677, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	return Catalog{
		Generator: Generator, Version: Version, CatalogID: "test", Revision: 1, Dimensions: 3, LayerCollisionMasks: masks,
		Materials: []content.Material{{ID: "default", FrictionCombine: "average", BounceCombine: "average", StaticFriction: .5, DynamicFriction: .5}},
		Templates: []Template{
			{ID: "rock", Bodies: []content.Body{staticBody("root", "static")}, Colliders: []content.Collider{convex, capsule},
				Navigation: &Navigation{
					Instances: []geom.Instance{{ID: "root/nav", AssetID: "nav-plane", Transform: geom.Identity()}},
					Volumes:   []geom.Volume{{ID: "root/area", Shape: "box", Bounds: geom.Bounds{Min: geom.Vec3{X: -1, Z: -2}, Max: geom.Vec3{X: 1, Y: 2, Z: 2}}, Area: 3}},
					Links:     []geom.Link{{ID: "root/link", End: geom.Vec3{X: 2}, Radius: .5, Area: 1, Flags: 1}},
				}},
			{ID: "rock-copy", Bodies: []content.Body{staticBody("root", "static")}, Colliders: []content.Collider{convex}},
			{ID: "terrain", Bodies: []content.Body{staticBody("root", "static")}, Colliders: []content.Collider{terrain},
				Navigation: &Navigation{Instances: []geom.Instance{{ID: "root/terrain", AssetID: "ground", Transform: geom.Identity()}}}},
			{ID: "building", Bodies: []content.Body{staticBody("root", "static")}, Colliders: []content.Collider{mesh}},
			{ID: "portal", Bodies: []content.Body{staticBody("root", "kinematic")}, Colliders: []content.Collider{sensor}},
			{ID: "tilted-capsule", Bodies: []content.Body{staticBody("root", "static")}, Colliders: []content.Collider{tiltedCapsule}},
			{ID: "tilted-rock", Bodies: []content.Body{staticBody("root", "static")}, Colliders: []content.Collider{tiltedConvex}},
		},
	}
}

func writeCatalog(t *testing.T, c Catalog, assets []geometry.Asset) *Catalog {
	t.Helper()
	catalog, err := decodeCatalog(t, c, assets)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func decodeCatalog(t *testing.T, c Catalog, assets []geometry.Asset) (*Catalog, error) {
	t.Helper()
	dir := t.TempDir()
	if len(assets) != 0 {
		var sidecar bytes.Buffer
		if err := geometry.Encode(&sidecar, assets, geometry.DefaultLimits()); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "templates.golemgeo"), sidecar.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(sidecar.Bytes())
		c.Geometry = &geometry.Reference{Path: "templates.golemgeo", SHA256: hex.EncodeToString(sum[:])}
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "templates.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	catalog, _, err := DecodeFile(path, DefaultLimits())
	return catalog, err
}

func newWorld(t *testing.T) *box3d.World {
	t.Helper()
	world, err := box3d.New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { world.Close() })
	return world
}

func yaw(degrees float64) p.Quat {
	half := degrees * math.Pi / 360
	return p.Quat{Y: float32(math.Sin(half)), W: float32(math.Cos(half))}
}

// rayDown returns the nearest hit below (x, 20, z), or false.
func rayDown(t *testing.T, world p.World, x, z float32) (p.Hit, bool) {
	t.Helper()
	if err := world.Synchronize(); err != nil {
		t.Fatal(err)
	}
	hits := make([]p.Hit, 1)
	result, err := world.Query(p.Query{Kind: p.Ray, Pose: p.Pose{Position: p.Vec3{X: x, Y: 20, Z: z}, Rotation: p.Quat{W: 1}}, Translation: p.Vec3{Y: -40},
		Filter: p.QueryFilter{Mask: math.MaxUint64}}, hits)
	if err != nil {
		t.Fatal(err)
	}
	return hits[0], result.Count > 0
}

func near(a, b float32) bool { return math.Abs(float64(a-b)) < 1e-3 }
