package geometry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func sampleAssets() []Asset {
	return []Asset{{ID: "mesh", SourceID: "authored-mesh", Kind: MeshFloat32, Vertices: []Vec3{{}, {X: 1}, {Z: 1}}, Triangles: []Triangle{{[3]uint32{0, 2, 1}, 1}}}, {ID: "terrain", SourceID: "authored-terrain", Kind: HeightfieldFloat32, Heightfield: &Heightfield{Resolution: 3, Size: [3]float32{4, 8, 4}, Samples: []float32{0, .1, .2, .3, .4, .5, .6, .7, .8}, Surface: []byte{0b1101}, Area: 1}}, {ID: "precise", Kind: MeshFloat64, Vertices: []Vec3{{X: 1.0000000000000002}, {X: 2}, {X: 2, Z: 1}}, Triangles: []Triangle{{[3]uint32{0, 2, 1}, 63}}}}
}

func TestBinaryRoundTripAndTerrainDiagonals(t *testing.T) {
	var encoded bytes.Buffer
	if err := Encode(&encoded, sampleAssets(), DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(bytes.NewReader(encoded.Bytes()), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, sampleAssets()) {
		t.Fatal("precision or authored geometry changed")
	}
	physics := ExpandHeightfield(decoded[1], true)
	navigation := ExpandHeightfield(decoded[1], false)
	if len(physics.Vertices) != 9 || len(physics.Triangles) != 6 || len(navigation.Triangles) != 6 {
		t.Fatal("holes or resolution changed")
	}
	if physics.Triangles[0].Indices != [3]uint32{0, 3, 4} || navigation.Triangles[0].Indices != [3]uint32{0, 3, 1} {
		t.Fatal("terrain diagonal changed")
	}
	if physics.Vertices[4].Y != float64(float32(.4))*8 {
		t.Fatal("height sample precision changed")
	}
	for _, bad := range [][]byte{encoded.Bytes()[:len(encoded.Bytes())-1], append(append([]byte(nil), encoded.Bytes()...), 0), []byte("GOLEMGE2\x00\x00\x00\x00")} {
		if _, err := Decode(bytes.NewReader(bad), DefaultLimits()); err == nil {
			t.Fatal("malformed binary accepted")
		}
	}
	limits := DefaultLimits()
	limits.Vertices = 3
	if _, err := Decode(bytes.NewReader(encoded.Bytes()), limits); err == nil {
		t.Fatal("expansion cap ignored")
	}
}

func TestInvalidGeometryRejected(t *testing.T) {
	cases := []func([]Asset){func(a []Asset) { a[0].Vertices[0].X = math.NaN() }, func(a []Asset) { a[0].Triangles[0].Indices[2] = 99 }, func(a []Asset) { a[0].Triangles[0].Area = 64 }, func(a []Asset) { a[1].Heightfield.Surface[0] = 0xf1 }, func(a []Asset) { a[1].Heightfield.Samples[0] = 2 }, func(a []Asset) { a[2].ID = a[0].ID }, func(a []Asset) { a[0].ID = "\xff" }}
	for _, change := range cases {
		assets := sampleAssets()
		change(assets)
		if err := Encode(io.Discard, assets, DefaultLimits()); err == nil {
			t.Fatal("invalid geometry encoded")
		}
	}
}

func TestSidecarHashAndContainment(t *testing.T) {
	var encoded bytes.Buffer
	if err := Encode(&encoded, sampleAssets(), DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "shared.golemgeo"), encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(encoded.Bytes())
	reference := Reference{"shared.golemgeo", hex.EncodeToString(hash[:])}
	if _, err := Read(reference, FileResolver(directory), DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside", "/absolute", "C:/absolute", "nested\\outside", "./shared.golemgeo"} {
		reference.Path = path
		if _, err := Read(reference, FileResolver(directory), DefaultLimits()); err == nil {
			t.Fatal("unsafe sidecar path accepted")
		}
	}
	reference.Path = "shared.golemgeo"
	reference.SHA256 = string(bytes.Repeat([]byte{'0'}, 64))
	if _, err := Read(reference, FileResolver(directory), DefaultLimits()); err == nil {
		t.Fatal("stale sidecar accepted")
	}
}

func TestActualScribeBinaryFixtures(t *testing.T) {
	for _, name := range []string{"geometry-physics.golemgeo", "geometry-navigation.golemgeo"} {
		data, err := os.ReadFile(filepath.Join("../../golem-unity/Tests/Fixtures", name))
		if err != nil {
			t.Fatal(err)
		}
		assets, err := Decode(bytes.NewReader(data), DefaultLimits())
		if err != nil {
			t.Fatal(name, err)
		}
		var encoded bytes.Buffer
		if err = Encode(&encoded, assets, DefaultLimits()); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, encoded.Bytes()) {
			t.Fatal("C#/Go binary encoding differs", name)
		}
		if len(assets) != 2 || assets[1].Heightfield == nil || assets[1].Heightfield.Surface[0] != 13 {
			t.Fatal("C# terrain samples/holes changed")
		}
		if name == "geometry-navigation.golemgeo" && assets[0].Vertices[0].X != 1.0000000000000002 {
			t.Fatal("C# navigation float64 precision lost")
		}
	}
}
