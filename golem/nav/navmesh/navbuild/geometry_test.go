package navbuild

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/geometry"
	"github.com/demiurgos-hub/navmesh-go/bake"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func TestCompactTerrainSourceBakesWithoutExpandedJSON(t *testing.T) {
	asset := geometry.Asset{ID: "terrain", Kind: geometry.HeightfieldFloat32, Heightfield: &geometry.Heightfield{Resolution: 3, Size: [3]float32{8, 2, 8}, Samples: make([]float32, 9), Surface: []byte{15}, Area: 1}}
	var payload bytes.Buffer
	if err := geometry.Encode(&payload, []geometry.Asset{asset}, geometry.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(payload.Bytes())
	source := Source{Generator: "Golem Scribe navigation source v2", Geometry: &geometry.Reference{Path: "terrain.golemgeo", SHA256: hex.EncodeToString(hash[:])}, Config: bake.DefaultConfig("compact", geom.Bounds{Min: geom.Vec3{X: -4, Y: -1, Z: -4}, Max: geom.Vec3{X: 4, Y: 3, Z: 4}}), Scene: geom.Scene{Instances: []geom.Instance{{ID: "tile", AssetID: "terrain", Revision: 1, Transform: geom.Translation(geom.Vec3{X: -4, Z: -4})}}}}
	directory := t.TempDir()
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "source.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "terrain.golemgeo"), payload.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeFile(filepath.Join(directory, "source.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Scene.Assets) != 1 || decoded.Scene.Assets[0].Triangles[0].Indices != [3]uint32{0, 3, 1} {
		t.Fatal("terrain topology changed")
	}
	if _, err = decoded.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = Decode(bytes.NewReader(data)); err == nil {
		t.Fatal("unresolved sidecar accepted")
	}
	if err = os.WriteFile(filepath.Join(directory, "terrain.golemgeo"), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeFile(filepath.Join(directory, "source.json")); err == nil {
		t.Fatal("corrupt sidecar accepted")
	}
}
