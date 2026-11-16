package content

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func TestUnitySourceFixture(t *testing.T) {
	data, e := os.ReadFile("../../../golem-unity/Tests/Fixtures/physics-source.json")
	if e != nil {
		t.Fatal(e)
	}
	s, hash, e := Decode(bytes.NewReader(data), DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	if len(hash) != 64 || s.WorldID != "physics-fixture" || len(s.Colliders) != 1 {
		t.Fatalf("%+v %s", s, hash)
	}
}
func TestBoundedStrictDecode(t *testing.T) {
	data, e := os.ReadFile("../../../golem-unity/Tests/Fixtures/physics-source.json")
	if e != nil {
		t.Fatal(e)
	}
	l := DefaultLimits()
	l.Bytes = 8
	if _, _, e = Decode(bytes.NewReader(data), l); e == nil {
		t.Fatal("byte bound ignored")
	}
	var doc map[string]any
	if e = json.Unmarshal(data, &doc); e != nil {
		t.Fatal(e)
	}
	doc["Unknown"] = 1
	bad, _ := json.Marshal(doc)
	if _, _, e = Decode(bytes.NewReader(bad), DefaultLimits()); e == nil {
		t.Fatal("unknown field accepted")
	}
	delete(doc, "Unknown")
	doc["LayerCollisionMasks"] = []uint32{1}
	bad, _ = json.Marshal(doc)
	if _, _, e = Decode(bytes.NewReader(bad), DefaultLimits()); e == nil {
		t.Fatal("invalid matrix accepted")
	}
	if _, _, e = Decode(bytes.NewReader(append(data, []byte(" {}")...)), DefaultLimits()); e == nil {
		t.Fatal("trailing document accepted")
	}
}

func TestHullVertexLimitReachesConvexShapes(t *testing.T) {
	data, e := os.ReadFile("../../../golem-unity/Tests/Fixtures/physics-source.json")
	if e != nil {
		t.Fatal(e)
	}
	var doc map[string]any
	if e = json.Unmarshal(data, &doc); e != nil {
		t.Fatal(e)
	}
	doc["Colliders"].([]any)[0].(map[string]any)["HullVertexLimit"] = 255
	bad, _ := json.Marshal(doc)
	if _, _, e = Decode(bytes.NewReader(bad), DefaultLimits()); e == nil {
		t.Fatal("hull vertex limit accepted on a primitive collider")
	}
	asset := Asset{ID: "hull", Vertices: []p.Vec3{{}, {X: 1}, {Y: 1}, {Z: 1}}, Indices: []uint32{0, 2, 1, 0, 1, 3, 0, 3, 2, 1, 2, 3}}
	collider := Collider{ID: "c", Shape: "convex", AssetID: "hull", Rotation: p.Quat{W: 1}, HullVertexLimit: 255}
	assets := map[string]Asset{"hull": asset}
	geometry, e := collider.Geometry(assets)
	if e != nil || geometry.HullVertexLimit != 255 {
		t.Fatalf("authored geometry dropped the hull vertex limit: %+v %v", geometry, e)
	}
	identity := [16]float32{1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1}
	collider.GeometryTransform = &identity
	shape, _, shared, e := collider.Placement(assets, Body{Kind: "static", Rotation: p.Quat{W: 1}})
	if e != nil || !shared || shape.HullVertexLimit != 255 {
		t.Fatalf("shared placement dropped the hull vertex limit: %+v shared=%v %v", shape, shared, e)
	}
}
