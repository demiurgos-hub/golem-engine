package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestCreateClientTemplateIncludesUnifiedCommandHelpers(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/js/create_client.ts.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "golem-engine",
		Commands: []schema.CommandData{
			{
				Name:      "Move",
				LowerName: "move",
				Target:    "entity",
				Fields: []schema.CommandFieldInfo{
					{FieldName: "dx", TSType: "number"},
					{FieldName: "dy", TSType: "number"},
				},
			},
			{
				Name:      "ReadyUp",
				LowerName: "readyUp",
				Target:    "session",
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		`import { EntityManager, buildMoveCommand, buildReadyUpCommand } from "./EntityManager.js";`,
		`export { buildMoveCommand } from "./EntityManager.js";`,
		`export { buildReadyUpCommand } from "./EntityManager.js";`,
		"export function sendMove(client: GameClient, entityId: number, dx: number, dy: number): void {",
		"client.send(buildMoveCommand(entityId, dx, dy));",
		"export function sendMoveOrdered(client: GameClient, entityId: number, dx: number, dy: number): void {",
		"client.sendOrdered(buildMoveCommand(entityId, dx, dy));",
		"export function sendReadyUp(client: GameClient): void {",
		"export function sendReadyUpOrdered(client: GameClient): void {",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated create_client helper missing %q\n%s", want, content)
		}
	}

	for _, legacy := range []string{
		"ReliableUnordered",
		"ReliableOrdered",
		"sendReliableUnorderedCommand",
		"sendReliableOrderedCommand",
	} {
		if strings.Contains(content, legacy) {
			t.Fatalf("generated create_client helper contains legacy name %q\n%s", legacy, content)
		}
	}
}

func TestCreateClientTemplateExposesConcreteGeneratedManagers(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/js/create_client.ts.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "golem-engine",
		WorldTypes:  []schema.WorldTypeData{{Name: "Zone"}},
		Events:      []schema.EventData{{Name: "Toast"}},
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	want := "export type Client = GameClient & { readonly entities: EntityManager; readonly world: WorldManager; readonly events: EventManager };"
	if !strings.Contains(content, want) {
		t.Fatalf("generated Client type missing concrete managers %q\n%s", want, content)
	}
}
