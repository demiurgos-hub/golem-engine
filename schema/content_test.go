package schema

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/content"
)

func TestLoadContentExplicitInputs(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("schema.json", `{"version":1,"types":[{"name":"Monster","kind":"record","definition":true,"entity":"Actor","fields":[{"name":"hp","type":"int32","default":100}]}]}`)
	write("bundle.json", `{"version":1,"definitions":[{"id":"sample/Monster/red","type":"Monster","factory":"monster","values":{}}]}`)
	cfg := &ContentConfig{Schema: "schema.json", Bundle: "bundle.json"}
	data, err := LoadContent(dir, cfg, []EntityData{{Name: "Actor"}})
	if err != nil {
		t.Fatal(err)
	}
	cat, err := content.Load(bytes.NewReader(data.BundleJSON), data.Schema, content.Options{RequireSchemaHash: true})
	if err != nil {
		t.Fatal(err)
	}
	view, ok := cat.Lookup("sample/Monster/red")
	var hp int32
	if !ok || view.Decode("hp", &hp) != nil || hp != 100 {
		t.Fatalf("default not finalized: %s", data.BundleJSON)
	}
	if data.SchemaHash == "" || data.SchemaHash != cat.SchemaHash() {
		t.Fatal("missing schema hash")
	}
	if _, err := LoadContent(dir, cfg, nil); err == nil || !strings.Contains(err.Error(), "unknown replicated entity") {
		t.Fatalf("unknown entity: %v", err)
	}
	if _, err := LoadContent(dir, &ContentConfig{Schema: "schema.json"}, nil); err == nil {
		t.Fatal("accepted incomplete config")
	}
	if data, err := LoadContent(dir, nil, nil); data != nil || err != nil {
		t.Fatalf("disabled content: %v %v", data, err)
	}
}

func TestLoadContentDeclaredImports(t *testing.T) {
	dir := t.TempDir()
	s := content.Schema{Version: 1, Types: []content.Type{{Name: "Item", Kind: "record", Definition: true, Fields: []content.Field{{Name: "other", Type: "ref:Item", Optional: true}}}}}
	schemaJSON, _ := json.Marshal(s)
	base, err := content.Load(strings.NewReader(`{"version":1,"definitions":[{"id":"sample/Item/base","type":"Item","values":{}}]}`), s, content.Options{})
	if err != nil {
		t.Fatal(err)
	}
	baseJSON, err := base.MarshalBundle()
	if err != nil {
		t.Fatal(err)
	}
	child, _ := json.Marshal(content.Bundle{Version: 1, Imports: []content.Import{{Hash: base.Hash()}}, Definitions: []content.Definition{{ID: "sample/Item/child", Type: "Item", Values: map[string]json.RawMessage{"other": json.RawMessage(`"sample/Item/base"`)}}}})
	for name, data := range map[string][]byte{"schema.json": schemaJSON, "base.json": baseJSON, "child.json": child} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &ContentConfig{Schema: "schema.json", Bundle: "child.json", Imports: []string{"base.json"}}
	if _, err := LoadContent(dir, cfg, nil); err != nil {
		t.Fatal(err)
	}
	cfg.Imports = nil
	if _, err := LoadContent(dir, cfg, nil); err == nil || !strings.Contains(err.Error(), "required import") {
		t.Fatalf("undeclared filesystem discovery: %v", err)
	}
}

func TestContentConfigRejectsUnknownOptions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "golem.yaml"), []byte("content:\n  schema: content.json\n  bunlde: definitions.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(dir); err == nil || !strings.Contains(err.Error(), "bunlde") {
		t.Fatalf("unknown content option: %v", err)
	}
}
