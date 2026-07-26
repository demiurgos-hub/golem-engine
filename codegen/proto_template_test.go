package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestEntitiesProtoRendersWorldCollectionFields(t *testing.T) {
	itemType := schema.BuildCustomTypeData(schema.TypeSchemaFile{
		Type: "Item",
		Fields: map[string]schema.TypeFieldDef{
			"id": {Type: "uint32", Tag: 1},
		},
	})
	customTypes := map[string]schema.CustomTypeData{"Item": itemType}
	listCatalog := schema.BuildWorldTypeData(schema.WorldSchemaFile{
		World: "ItemList",
		Source: &schema.WorldSourceDef{
			Format: "catalog",
			Type:   "Item",
		},
	}, customTypes)
	mapCatalog := schema.BuildWorldTypeData(schema.WorldSchemaFile{
		World: "ItemCatalog",
		Source: &schema.WorldSourceDef{
			Format: "catalog",
			Type:   "Item",
			Key:    "id",
		},
	}, customTypes)

	tmpl, err := loadEmbeddedTemplate("templates/proto/entities.proto.tmpl")
	if err != nil {
		t.Fatalf("load entities proto template: %v", err)
	}
	var out bytes.Buffer
	err = tmpl.Execute(&out, schema.ProtoTemplateData{
		Package:          "game",
		GoPackage:        "example.com/game",
		CustomTypes:      []schema.CustomTypeData{itemType},
		WorldTypes:       []schema.WorldTypeData{listCatalog, mapCatalog},
		EntityRemovedTag: 1,
	})
	if err != nil {
		t.Fatalf("execute entities proto template: %v", err)
	}

	content := out.String()
	for _, want := range []string{
		"repeated Item items = 1;",
		"map<uint32, Item> items = 1;",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated entities.proto missing %q\n%s", want, content)
		}
	}
}
