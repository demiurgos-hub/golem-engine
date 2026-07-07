package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestCreateClientTemplateIncludesDatagramCommandHelpers(t *testing.T) {
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
		"export function sendMoveReliableUnordered(client: GameClient, entityId: number, dx: number, dy: number): void {",
		"client.sendReliableUnorderedCommand(buildMoveCommand(entityId, dx, dy));",
		"export function sendMoveReliableOrdered(client: GameClient, entityId: number, dx: number, dy: number): void {",
		"client.sendReliableOrderedCommand(buildMoveCommand(entityId, dx, dy));",
		"export function sendReadyUpReliableUnordered(client: GameClient): void {",
		"export function sendReadyUpReliableOrdered(client: GameClient): void {",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated create_client helper missing %q\n%s", want, content)
		}
	}
}
