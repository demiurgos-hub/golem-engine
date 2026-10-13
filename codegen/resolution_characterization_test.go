package codegen

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestResolutionDeterministicGeneration(t *testing.T) {
	generate := func() map[string]string {
		t.Helper()
		pd := resolutionFixture(3) // New maps exercise independent iteration order.
		root := t.TempDir()
		for _, emit := range []func() error{
			func() error {
				return generateGoProto(root, "github.com/demiurgos-hub/golem-engine/golem", "generated", pd.Entities, pd.Commands, pd.WorldTypes, pd.Events, pd)
			},
			func() error {
				return generateJSProto(root, "golem-engine", pd.Entities, pd.Commands, pd.WorldTypes, pd.Events, pd)
			},
			func() error {
				return generateCSharpProto(root, "GolemEngine.Unity", "Generated", pd.Entities, pd.Commands, pd.WorldTypes, pd.Events, pd)
			},
		} {
			if err := emit(); err != nil {
				t.Fatal(err)
			}
		}
		files, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		result := make(map[string]string)
		for _, file := range files {
			data, err := os.ReadFile(filepath.Join(root, file.Name()))
			if err != nil {
				t.Fatal(err)
			}
			result[file.Name()] = string(data)
		}
		return result
	}
	if !reflect.DeepEqual(generate(), generate()) {
		t.Fatal("generation is not byte-for-byte deterministic")
	}
}

func TestResolutionDeferredFingerprintCoverage(t *testing.T) {
	pd := resolutionFixture(2)
	want := ComputeSchemaFingerprint(pd.Entities)
	if got := ComputeSchemaFingerprint(resolutionFixture(3).Entities); got != want {
		t.Fatal("dimension exclusion changed")
	}
	// The legacy preimage only observes each variable's UserTag and flat
	// ProtoType. New descriptors must not accidentally widen fingerprint scope.
	pd.Entities[0].AllVars[3].ElemProtoType = "Other"
	pd.Entities[0].AllVars[3].MapKeyProtoType = "bytes"
	pd.Entities[0].AllVars[3].Collection = nil
	pd.Entities[0].Wire = nil
	pd.CustomTypes[0].Fields[0].ProtoType = "uint64"
	if got := ComputeSchemaFingerprint(pd.Entities); got != want {
		t.Fatal("fingerprint preimage widened")
	}
	pd.Entities[0].AllVars = pd.Entities[0].AllVars[:len(pd.Entities[0].AllVars)-1]
	if got := ComputeSchemaFingerprint(pd.Entities); got == want {
		t.Fatal("removing a variable must still change the legacy fingerprint")
	}
}

func TestResolutionDeferredGeneratedLimitations(t *testing.T) {
	world := schema.BuildWorldTypeData(schema.WorldSchemaFile{World: "Numbers", Fields: map[string]schema.WorldFieldDef{"values": {Type: "dict<string, uint32>"}}}, nil)
	var cs strings.Builder
	writeCSMessageClass(&cs, world.DataName, worldDataCSFields(world))
	if !strings.Contains(cs.String(), "var value = 0;") || !strings.Contains(cs.String(), "value = itemReader.Uint32();") {
		t.Fatal("C# map default inference changed; fix separately with compatibility review")
	}
	entity := schema.BuildEntityData(schema.SchemaFile{Entity: "Blob", Vars: map[string]schema.SchemaVarDef{"data": {Type: "bytes", Tag: 1}}}, 2, nil)
	entity.GoPackage, entity.GolemImport = "generated", "github.com/demiurgos-hub/golem-engine/golem"
	tmpl, err := loadEmbeddedTemplate("templates/go_server/server.go.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	var goSource bytes.Buffer
	if err := tmpl.Execute(&goSource, entity); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(goSource.String(), "if s.data != v") || entity.TickVars[0].ProtoHelper != "" {
		t.Fatal("tick bytes limitation changed; fix separately")
	}
	if tsType("Unknown") != "number" || csType("Unknown") != "Unknown" {
		t.Fatal("unknown native-type fallback changed")
	}
	if got := goSourcePropertyExpr("bytes", "p"); got != "/* unsupported type bytes */ p.StringValue()" {
		t.Fatal("map-source fallback changed")
	}
}
