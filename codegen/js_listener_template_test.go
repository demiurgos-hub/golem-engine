package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGenerateJSEventManagerUsesTypedSubscriptions(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/js/event_manager.ts.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GolemImport: "golem-engine",
		Entities: []schema.EntityData{
			{Name: "Player", LowerName: "player"},
		},
		Events: []schema.EventData{
			{
				Name:       "Hit",
				LowerName:  "hit",
				Target:     "entity",
				EntityType: "Player",
				FOIOnly:    true,
			},
			{
				Name:      "Toast",
				LowerName: "toast",
				Target:    "global",
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		`import { SyncedPlayer } from "./PlayerSynced.js";`,
		"private _onHit = new Set<(entity: SyncedPlayer, e: HitEvent) => void>();",
		"onHit(fn: (entity: SyncedPlayer, e: HitEvent) => void): () => void",
		"return () => { this._onHit.delete(fn); };",
		"if (entity instanceof SyncedPlayer)",
		"for (const fn of this._onHit) fn(entity, update.hit);",
		"onToast(fn: (e: ToastEvent) => void): () => void",
		"for (const fn of this._onToast) fn(update.toast);",
		"(entity as { onHit?(e: HitEvent): void }).onHit?.(update.hit);",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated EventManager missing %q\n%s", want, content)
		}
	}
}

func TestGenerateJSWorldManagerUsesMulticastSubscriptions(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/js/world_manager.ts.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		WorldTypes: []schema.WorldTypeData{
			{
				Name:      "Zone",
				LowerName: "zone",
				DataName:  "ZoneData",
			},
		},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		"private _onZoneUpdate = new Set<(data: ZoneData) => void>();",
		"onZoneUpdate(fn: (data: ZoneData) => void): () => void",
		"return () => { this._onZoneUpdate.delete(fn); };",
		"for (const fn of this._onZoneUpdate) fn(update.zoneData);",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated WorldManager missing %q\n%s", want, content)
		}
	}
}
