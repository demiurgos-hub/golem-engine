package content

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
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
