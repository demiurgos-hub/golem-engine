package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"reflect"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/geometry"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func TestCompactSourceSharedMeshPlacementAndPreparation(t *testing.T) {
	asset := geometry.Asset{ID: "canonical", SourceID: "authored", Kind: geometry.MeshFloat32, Vertices: []geometry.Vec3{{}, {X: 1}, {Z: 1}}, Triangles: []geometry.Triangle{{Indices: [3]uint32{0, 2, 1}}}}
	var binary bytes.Buffer
	if err := geometry.Encode(&binary, []geometry.Asset{asset}, geometry.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(binary.Bytes())
	layers := make([]uint32, 32)
	for i := range layers {
		layers[i] = ^uint32(0)
	}
	identity := [16]float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	reflection := identity
	reflection[0] = -2
	reflection[3] = 3
	source := Source{Generator: GeneratorV2, Version: 2, WorldID: "compact", Revision: 1, CoordinateLimit: p.CoordinateLimit, LayerCollisionMasks: layers, Geometry: &geometry.Reference{Path: "shared.golemgeo", SHA256: hex.EncodeToString(hash[:])}, Materials: []Material{{ID: "default", FrictionCombine: "average", BounceCombine: "average"}}}
	for index, matrix := range []*[16]float32{&identity, &reflection} {
		id := []string{"left", "right"}[index]
		source.Bodies = append(source.Bodies, Body{ID: id, Kind: "static", CollisionDetection: "discrete", Mass: 1, Rotation: p.Identity().Rotation, AutomaticCenterOfMass: true, AutomaticInertiaTensor: true, Enabled: true, Active: true})
		source.Colliders = append(source.Colliders, Collider{ID: id, BodyID: id, Shape: "mesh", AssetID: "canonical", MaterialID: "default", Rotation: p.Identity().Rotation, GeometryTransform: matrix, Enabled: true, Active: true, Solid: true, Query: true})
	}
	jsonSource, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Decode(bytes.NewReader(jsonSource), DefaultLimits()); err == nil {
		t.Fatal("unresolved sidecar accepted")
	}
	decoded, _, err := DecodeWithGeometry(bytes.NewReader(jsonSource), DefaultLimits(), func(string) (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(binary.Bytes())), nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Assets) != 1 {
		t.Fatal("shared asset duplicated")
	}
	shape, err := decoded.Colliders[1].Geometry(map[string]Asset{"canonical": decoded.Assets[0]})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(shape.Vertices, []p.Vec3{{X: 3}, {X: 1}, {X: 3, Z: 1}}) || !reflect.DeepEqual(shape.Indices, []uint32{0, 1, 2}) {
		t.Fatalf("affine placement/winding changed: %+v", shape)
	}
	prepared, err := Prepare(decoded, DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	decoded.Assets[0].Vertices[0].X = 99
	decoded.Colliders[1].GeometryTransform[3] = 99
	copy := prepared.Source()
	if copy.Assets[0].Vertices[0].X != 0 || copy.Colliders[1].GeometryTransform[3] != 3 {
		t.Fatal("prepared source aliases authoring data")
	}
	if _, _, err := Decode(bytes.NewReader(prepared.SourceBytes()), DefaultLimits()); err != nil {
		t.Fatal("canonical prepared source is not self-contained", err)
	}
}

func TestCanonicalExactZeroAreaProvenance(t *testing.T) {
	asset := Asset{ID: "raw", Vertices: []p.Vec3{{}, {X: 1}, {X: 2}, {Z: 1}}, Indices: []uint32{0, 1, 2, 0, 3, 1}}
	matrix := [16]float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	metadata := []Property{{normalizationPrefix + "originalTriangleCount", "2"}, {normalizationPrefix + "removedTriangleOrdinals", "0"}, {normalizationPrefix + "originalGeometrySHA256", geometryHash(asset.Vertices, asset.Indices)}}
	_, indices, err := transformedGeometry(asset, matrix, metadata)
	if err != nil || !reflect.DeepEqual(indices, []uint32{0, 3, 1}) {
		t.Fatal(indices, err)
	}
	metadata[1].Value = "1"
	if _, _, err := transformedGeometry(asset, matrix, metadata); err == nil {
		t.Fatal("forged removed triangle accepted")
	}
	if exactZeroArea(p.Vec3{}, p.Vec3{X: 1}, p.Vec3{X: 2, Z: 1e-30}) {
		t.Fatal("small nonzero triangle removed")
	}
}
