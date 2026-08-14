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

func TestEntitiesProtoRendersExplicitEnvelopeTags(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/proto/entities.proto.tmpl")
	if err != nil {
		t.Fatalf("load entities proto template: %v", err)
	}
	var out bytes.Buffer
	err = tmpl.Execute(&out, schema.ProtoTemplateData{
		Package:   "game",
		GoPackage: "example.com/game",
		EntityUpdateFields: []schema.EntityUpdateField{
			{MessageType: "PlayerState", SnakeName: "player_state", Tag: 1},
			{MessageType: "InteractableState", SnakeName: "interactable_state", Tag: 8},
		},
		EntityRemovedTag: 7,
		ClientMessageFields: []schema.ClientMessageField{
			{MessageType: "MoveCommand", SnakeName: "move", Tag: 1},
			{MessageType: "InteractionCloseCommand", SnakeName: "interaction_close", Tag: 22},
		},
		ServerEventFields: []schema.ServerEventField{
			{MessageType: "DialogMessageEvent", SnakeName: "dialog_message", Tag: 1},
			{MessageType: "InteractionUpdateEvent", SnakeName: "interaction_update", Tag: 13},
		},
	})
	if err != nil {
		t.Fatalf("execute entities proto template: %v", err)
	}

	content := out.String()
	for _, want := range []string{
		"InteractableState interactable_state = 8;",
		"EntityRemoved entity_removed = 7;",
		"InteractionCloseCommand interaction_close = 22;",
		"InteractionUpdateEvent interaction_update = 13;",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated entities.proto missing %q\n%s", want, content)
		}
	}
}
