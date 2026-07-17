package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGenerateJSManagerTemplateUsesRevisionChecksForAllEntityUpdates(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/js/manager.ts.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "golem-engine",
		Entities: []schema.EntityData{
			{Name: "Player", LowerName: "player"},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"private _lastRevisions = new Map<number, number>();",
		"return last !== undefined && revision < last;",
		"if (this._isStale(id, revision)) return;",
		"const revision = Number(update.entityRemoved.revision);",
		"const revision = Number(s.revision);",
		"const revision = Number(d.revision);",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated EntityManager missing %q\n%s", want, content)
		}
	}
}

func TestGenerateJSManagerTemplateIncludesCompactUpdateDispatch(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/js/manager.ts.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "golem-engine",
		Entities: []schema.EntityData{
			{Name: "Player", LowerName: "player"},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		`import { PbReader } from "golem-engine";`,
		`import type { EntityUpdate, PlayerState } from "./entities_pb.js";`,
		`import { PlayerDelta } from "./entities_pb.js";`,
		"applyCompactUpdate(frame: Uint8Array) {",
		"const id = Number(r.int64());",
		"const revision = Number(r.uint64());",
		"const mask = Number(r.uint64());",
		"const body = r.remaining();",
		"if (entity instanceof SyncedPlayer) {",
		"const d = PlayerDelta.decode(body);",
		"d.entityId = id;",
		"d.revision = revision;",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated EntityManager missing %q\n%s", want, content)
		}
	}
}

func TestGenerateJSManagerTemplateUsesMulticastSubscriptions(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/js/manager.ts.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "golem-engine",
		Entities: []schema.EntityData{
			{Name: "Player", LowerName: "player"},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"private _onSpawn = new Set<(entity: SyncedEntity) => void>();",
		"private _onUpdate = new Set<(entity: SyncedEntity) => void>();",
		"private _onRemove = new Set<(entityId: number) => void>();",
		"getPlayer(entityId: number): SyncedPlayer | undefined",
		"onSpawn(fn: (entity: SyncedEntity) => void): () => void",
		"return () => { this._onSpawn.delete(fn); };",
		"for (const fn of this._onSpawn) fn(e!);",
		"for (const fn of this._onUpdate) fn(e);",
		"for (const fn of this._onRemove) fn(id);",
		"Phaser presentation should use an entity-view registry instead.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated EntityManager missing %q\n%s", want, content)
		}
	}
}
