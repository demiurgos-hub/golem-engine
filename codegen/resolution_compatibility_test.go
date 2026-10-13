package codegen

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

// These hashes were captured before the schema/codec extraction. Ignore only
// comments and whitespace, so changes to emitted operations still need review.
func TestResolutionGeneratedCompatibility(t *testing.T) {
	got := map[string]string{}
	for _, dimensions := range []int{2, 3} {
		pd := resolutionFixture(dimensions)
		root := t.TempDir()
		for _, language := range []string{"go", "js", "cs"} {
			out := filepath.Join(root, language)
			if err := os.MkdirAll(out, 0755); err != nil {
				t.Fatal(err)
			}
			var err error
			switch language {
			case "go":
				err = generateGoProto(out, "github.com/demiurgos-hub/golem-engine/golem", "generated", pd.Entities, pd.Commands, pd.WorldTypes, pd.Events, pd)
			case "js":
				err = generateJSProto(out, "golem-engine", pd.Entities, pd.Commands, pd.WorldTypes, pd.Events, pd)
			case "cs":
				err = generateCSharpProto(out, "GolemEngine.Unity", "Generated", pd.Entities, pd.Commands, pd.WorldTypes, pd.Events, pd)
			}
			if err != nil {
				t.Fatal(err)
			}
			files, err := os.ReadDir(out)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range files {
				data, err := os.ReadFile(filepath.Join(out, file.Name()))
				if err != nil {
					t.Fatal(err)
				}
				got[fmt.Sprintf("%d/%s/%s", dimensions, language, file.Name())] = sourceCompatibilityHash(string(data))
			}
		}
		for _, name := range []string{"proto/entities.proto.tmpl", "go_server/server.go.tmpl", "csharp/client.cs.tmpl"} {
			tmpl, err := loadEmbeddedTemplate("templates/" + name)
			if err != nil {
				t.Fatal(err)
			}
			var b bytes.Buffer
			var data any = pd.Entities[0]
			if strings.HasPrefix(name, "proto/") {
				data = pd
			}
			if err := tmpl.Execute(&b, data); err != nil {
				t.Fatal(err)
			}
			got[fmt.Sprintf("%d/%s", dimensions, name)] = sourceCompatibilityHash(b.String())
		}
		got[fmt.Sprintf("%d/fingerprint", dimensions)] = ComputeSchemaFingerprint(pd.Entities)
	}
	path := filepath.Join("testdata", "resolution-baseline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("baseline entries: got %d, want %d", len(got), len(want))
	}
	for name, hash := range got {
		if hash != want[name] {
			t.Errorf("generated operations changed for %s: got %s, want %s", name, hash, want[name])
		}
	}
}

func sourceCompatibilityHash(source string) string {
	source = regexp.MustCompile(`(?m)//[^\n]*`).ReplaceAllString(source, "")
	source = regexp.MustCompile(`\s+`).ReplaceAllString(source, "")
	return fmt.Sprintf("%x", sha256.Sum256([]byte(source)))
}

func resolutionFixture(dimensions int) schema.ProtoTemplateData {
	item := schema.BuildCustomTypeData(schema.TypeSchemaFile{Type: "Item", Fields: map[string]schema.TypeFieldDef{
		"id": {Type: "int32", Tag: 1}, "label": {Type: "string", Tag: 3}, "value": {Type: "float", Tag: 5},
	}})
	types := map[string]schema.CustomTypeData{"Item": item}
	explicit := 8
	actor := schema.BuildEntityData(schema.SchemaFile{Entity: "Actor", Vars: map[string]schema.SchemaVarDef{
		"health": {Type: "sint32", Tag: 1}, "secret": {Type: "string", Tag: 3, Visibility: "owner"},
		"token": {Type: "string", Tag: 5, Sync: "once", Visibility: "owner"},
		"items": {Type: "list<Item>", Tag: 7}, "scores": {Type: "dict<string, int32>", Tag: 8},
		"labels": {Type: "list<string>", Tag: 9}, "flags": {Type: "list<bool>", Tag: 10},
	}}, dimensions, types)
	actor.GoPackage = "generated"
	actor.GolemImport = "github.com/demiurgos-hub/golem-engine/golem"
	beacon := schema.BuildEntityData(schema.SchemaFile{Entity: "Beacon", Tag: &explicit}, dimensions, types)
	move := schema.BuildCommandData(schema.CommandSchemaFile{Command: "Move", Target: "entity", EntityType: "Actor", Fields: map[string]schema.CommandFieldDef{
		"speed": {Type: "float"}, "item": {Type: "Item"}, "blob": {Type: "bytes"},
	}}, types)
	ping := schema.BuildCommandData(schema.CommandSchemaFile{Command: "Ping"}, types)
	hit := schema.BuildEventData(schema.EventSchemaFile{Event: "Hit", Target: "entity", EntityType: "Actor", Fields: map[string]schema.EventFieldDef{
		"amount": {Type: "int32"}, "item": {Type: "Item"}, "blob": {Type: "bytes"},
	}}, types)
	notice := schema.BuildEventData(schema.EventSchemaFile{Event: "Notice", Target: "global"}, types)
	world := schema.BuildWorldTypeData(schema.WorldSchemaFile{World: "Arena", Fields: map[string]schema.WorldFieldDef{
		"names": {Type: "list<string>"}, "items": {Type: "dict<string, Item>"}, "counts": {Type: "list<int32>"},
	}}, types)
	catalog := schema.BuildWorldTypeData(schema.WorldSchemaFile{World: "Catalog", Source: &schema.WorldSourceDef{Format: "catalog", File: "items.yaml", Type: "Item", Key: "id"}}, types)
	return schema.BuildProtoData(&schema.Config{Simulation: schema.SimulationConfig{Dimensions: dimensions}}, []schema.EntityData{actor, beacon}, []schema.CommandData{move, ping}, []schema.WorldTypeData{world, catalog}, []schema.CustomTypeData{item}, []schema.EventData{hit, notice})
}
