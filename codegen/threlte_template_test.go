package codegen

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGenerateThrelteAdapterUsesGeneratedManagersAndTypedComponents(t *testing.T) {
	hit := schema.EventData{Name: "Hit", LowerName: "hit", Target: "entity", EntityType: "Actor"}
	content := renderThrelte(t, schema.SharedData{
		GolemImport: "golem-threlte", ProtocolImport: "../protocol/",
		Entities: []schema.EntityData{
			{Name: "Actor", Events: []schema.EventData{hit}},
			{Name: "Beacon"},
			{Name: "Session"},
		},
		WorldTypes: []schema.WorldTypeData{
			{Name: "Arena", LowerName: "arena", DataName: "ArenaData"},
			{Name: "Species", LowerName: "species", DataName: "SpeciesData", IsCatalog: true},
		},
		Events: []schema.EventData{hit, {Name: "Notice", LowerName: "notice", Target: "global"}},
	})
	for _, want := range []string{
		`import { SyncedActor } from "../protocol/ActorSynced.js";`,
		`import type { HitEvent, NoticeEvent } from "../protocol/events_pb.js";`,
		"Actor: entityCollection(client.entities, (entity): entity is SyncedActor => entity instanceof SyncedActor)",
		"Arena: worldStore(() => client.world.arena, (listener) => client.world.onArenaUpdate(listener))",
		"Species: worldStore(() => client.world.species, (listener) => client.world.onSpeciesUpdate(listener))",
		"Hit: (listener: (entity: SyncedActor, event: HitEvent) => void) => client.events.onHit(listener)",
		"Notice: (listener: (event: NoticeEvent) => void) => client.events.onNotice(listener)",
		`client.events.onHit((entity, event) => dispatch(entity, "Hit", event))`,
		"return useGolemContext(golemAdapter)",
		"export interface ActorEvents {\n  Hit: HitEvent;",
		"export type ActorViewProps = EntityViewProps<SyncedActor, ActorEvents>",
		"Actor: EntityViewDefinition<SyncedActor, ActorEvents>",
		"Session: EntityViewDefinition<SyncedSession, SessionEvents>",
		"build: (views: EntityViewBuilders) => EntityViewDefinitions",
		"Actor: createEntityViewBuilder<SyncedActor, ActorEvents>()",
		"return createEntityViewRegistry(golemAdapter, [",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("Threlte adapter missing %q\n%s", want, content)
		}
	}
	for _, forbidden := range []string{"new GameClient", "class EntityManager", "requestAnimationFrame", "posX", "posY", "three", "dispatch(entity, \"Notice\""} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("Threlte adapter unexpectedly contains %q\n%s", forbidden, content)
		}
	}
}

func TestGenerateThrelteOptionalSchemaSections(t *testing.T) {
	for _, tc := range []struct {
		name string
		data schema.SharedData
	}{
		{name: "empty"},
		{name: "entity-only", data: schema.SharedData{Entities: []schema.EntityData{{Name: "Headless"}}}},
		{name: "world-only", data: schema.SharedData{WorldTypes: []schema.WorldTypeData{{Name: "Arena", LowerName: "arena"}}}},
		{name: "global-event-only", data: schema.SharedData{Events: []schema.EventData{{Name: "Notice", Target: "global"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.data.GolemImport = "golem-threlte"
			tc.data.ProtocolImport = "../protocol/"
			content := renderThrelte(t, tc.data)
			if len(tc.data.Events) == 0 && strings.Contains(content, "events_pb") {
				t.Fatal("imported absent event output")
			}
			if len(tc.data.WorldTypes) == 0 && strings.Contains(content, "client.world.") {
				t.Fatal("read absent world manager")
			}
			if len(tc.data.Entities) == 0 && strings.Contains(content, "Synced") {
				t.Fatal("referenced absent synchronized entity")
			}
		})
	}
}

func renderThrelte(t *testing.T, data schema.SharedData) string {
	t.Helper()
	tmpl, err := loadEmbeddedTemplate("templates/threlte/golem_threlte.ts.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestThrelteIntegrationIsRemovableRawSharedTypeScript(t *testing.T) {
	integ, err := GetIntegration("threlte")
	if err != nil {
		t.Fatal(err)
	}
	if integ.Template != "" || integ.FileNamer != nil || integ.GenerateExtra != nil || integ.Finalize != nil || len(integ.SharedTemplates) != 1 {
		t.Fatalf("Threlte must be a shared-only bridge: %+v", integ)
	}
	if got := integ.SharedTemplates[0].File; got != "GolemThrelte.ts" {
		t.Fatalf("output = %q, want GolemThrelte.ts", got)
	}
	if isGeneratedJSIntermediateTS("GolemThrelte.ts") {
		t.Fatal("JS intermediate cleanup must leave the consumer-compiled bridge intact")
	}
}

func TestValidateThrelteIntegration(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		name      string
		js        *schema.IntegrationConfig
		threlte   schema.IntegrationConfig
		wantError string
	}{
		{name: "missing-js", threlte: schema.IntegrationConfig{Out: "bridge", ProtocolImport: "../protocol/"}, wantError: "requires the js-client"},
		{name: "missing-output", js: &schema.IntegrationConfig{Out: "protocol"}, wantError: "non-empty out"},
		{name: "missing-js-output", js: &schema.IntegrationConfig{}, threlte: schema.IntegrationConfig{Out: "bridge"}, wantError: "non-empty out"},
		{name: "missing-import", js: &schema.IntegrationConfig{Out: "protocol"}, threlte: schema.IntegrationConfig{Out: "bridge"}, wantError: "protocol_import"},
		{name: "normalized-import-slash", js: &schema.IntegrationConfig{Out: "protocol"}, threlte: schema.IntegrationConfig{Out: "bridge", ProtocolImport: "../protocol"}},
		{name: "whitespace-import", js: &schema.IntegrationConfig{Out: "protocol"}, threlte: schema.IntegrationConfig{Out: "bridge", ProtocolImport: " ../protocol/ "}, wantError: "protocol_import"},
		{name: "backslash-import", js: &schema.IntegrationConfig{Out: "protocol"}, threlte: schema.IntegrationConfig{Out: "bridge", ProtocolImport: `..\protocol/`}, wantError: "protocol_import"},
		{name: "same-output", js: &schema.IntegrationConfig{Out: "protocol"}, threlte: schema.IntegrationConfig{Out: "protocol", ProtocolImport: "./"}, wantError: "outside js-client"},
		{name: "nested-output", js: &schema.IntegrationConfig{Out: "protocol"}, threlte: schema.IntegrationConfig{Out: "protocol/bridge", ProtocolImport: "../"}, wantError: "outside js-client"},
		{name: "normalized-nested-output", js: &schema.IntegrationConfig{Out: "protocol"}, threlte: schema.IntegrationConfig{Out: "bridge/../protocol/bridge", ProtocolImport: "../"}, wantError: "outside js-client"},
		{name: "sibling-outputs", js: &schema.IntegrationConfig{Out: "protocol"}, threlte: schema.IntegrationConfig{Out: "bridge", ProtocolImport: "../protocol/"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &schema.Config{Integrations: map[string]schema.IntegrationConfig{"threlte": tc.threlte}}
			if tc.js != nil {
				cfg.Integrations["js-client"] = *tc.js
			}
			err := validateThrelteIntegration(root, cfg)
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				if got := cfg.Integrations["threlte"].ProtocolImport; got != "../protocol/" {
					t.Fatalf("normalized protocol_import = %q, want ../protocol/", got)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("error = %v, want %q", err, tc.wantError)
			}
		})
	}
	if err := validateThrelteIntegration(root, &schema.Config{}); err != nil {
		t.Fatalf("unconfigured Threlte changed existing integrations: %v", err)
	}
}

func TestBakeRejectsThrelteConfigurationBeforeWriting(t *testing.T) {
	root := t.TempDir()
	config := "proto:\n  out: generated/entities.proto\nintegrations:\n  threlte:\n    out: bridge\n    protocol_import: ../protocol/\n"
	if err := os.WriteFile(filepath.Join(root, "golem.yaml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Bake(root); err == nil || !strings.Contains(err.Error(), "requires the js-client") {
		t.Fatalf("Bake error = %v", err)
	}
	files, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != "golem.yaml" {
		t.Fatalf("invalid configuration wrote files: %v", files)
	}
}
