package codegen

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

// Run after building the local npm packages. Keep Go-only environments usable.
func TestThrelteGeneratedConsumerCompiles(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tsc := filepath.Join(root, "golem-threlte", "node_modules", "typescript", "lib", "tsc.js")
	engineTypes := filepath.Join(root, "golem-js", "dist", "index.d.ts")
	threlteTypes := filepath.Join(root, "golem-threlte", "dist", "index.d.ts")
	svelteTypes := filepath.Join(root, "golem-threlte", "node_modules", "svelte", "types", "index.d.ts")
	for _, file := range []string{tsc, engineTypes, threlteTypes, svelteTypes} {
		if _, err := os.Stat(file); err != nil {
			t.Skipf("build golem-js and golem-threlte first to check the generated consumer: %s", file)
		}
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is needed for generated consumer type checks")
	}
	hit := schema.BuildEventData(schema.EventSchemaFile{Event: "Hit", Target: "entity", EntityType: "Actor", Fields: map[string]schema.EventFieldDef{"amount": {Type: "int32"}}}, nil)
	notice := schema.BuildEventData(schema.EventSchemaFile{Event: "Notice", Target: "global", Fields: map[string]schema.EventFieldDef{"message": {Type: "string"}}}, nil)
	actor := schema.BuildEntityData(schema.SchemaFile{Entity: "Actor", Vars: map[string]schema.SchemaVarDef{"health": {Type: "int32", Tag: 1}}}, 3, nil)
	actor.Events = []schema.EventData{hit}
	beacon := schema.BuildEntityData(schema.SchemaFile{Entity: "Beacon", Vars: map[string]schema.SchemaVarDef{"brightness": {Type: "float", Tag: 1}}}, 3, nil)
	session := schema.BuildEntityData(schema.SchemaFile{Entity: "Session"}, 3, nil)
	arena := schema.BuildWorldTypeData(schema.WorldSchemaFile{World: "Arena", Fields: map[string]schema.WorldFieldDef{"name": {Type: "string"}}}, nil)
	arena.UpdateTag = 1
	species := schema.BuildCustomTypeData(schema.TypeSchemaFile{Type: "Species", Fields: map[string]schema.TypeFieldDef{"id": {Type: "string", Tag: 1}}})
	catalog := schema.BuildWorldTypeData(schema.WorldSchemaFile{World: "SpeciesCatalog", Source: &schema.WorldSourceDef{Format: "catalog", File: "species.yaml", Type: "Species", Key: "id"}}, map[string]schema.CustomTypeData{"Species": species})
	catalog.UpdateTag = 2
	move := schema.BuildCommandData(schema.CommandSchemaFile{Command: "Move", Target: "entity", EntityType: "Actor", Fields: map[string]schema.CommandFieldDef{"speed": {Type: "float"}}}, nil)
	for _, tc := range []struct {
		name     string
		data     schema.SharedData
		consumer string
	}{
		{name: "full", data: schema.SharedData{
			Entities: []schema.EntityData{actor, beacon, session}, Commands: []schema.CommandData{move},
			WorldTypes: []schema.WorldTypeData{arena, catalog}, Events: []schema.EventData{hit, notice}, CustomTypes: []schema.CustomTypeData{species},
		}, consumer: threlteConsumerTypeFixture},
		{name: "empty"},
		{name: "global-event-only", data: schema.SharedData{Events: []schema.EventData{notice}}},
		{name: "world-only", data: schema.SharedData{WorldTypes: []schema.WorldTypeData{arena}}},
		{name: "entity-only", data: schema.SharedData{Entities: []schema.EntityData{session}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := t.TempDir()
			emitThrelteCompileFixture(t, fixture, tc.data)
			if tc.consumer != "" {
				if err := os.WriteFile(filepath.Join(fixture, "consumer.ts"), []byte(tc.consumer), 0600); err != nil {
					t.Fatal(err)
				}
			}
			config, err := json.Marshal(map[string]any{
				"compilerOptions": map[string]any{
					"target": "ES2022", "module": "ESNext", "moduleResolution": "bundler",
					"strict": true, "skipLibCheck": true, "noEmit": true,
					"paths": map[string][]string{"golem-engine": {engineTypes}, "golem-threlte": {threlteTypes}, "svelte": {svelteTypes}},
				},
				"include": []string{"./**/*.ts"},
			})
			if err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(fixture, "tsconfig.json")
			if err := os.WriteFile(configPath, config, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(node, tsc, "-p", configPath)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generated consumer did not typecheck: %v\n%s", err, output)
			}
		})
	}
}

func emitThrelteCompileFixture(t *testing.T, root string, data schema.SharedData) {
	t.Helper()
	js, err := GetIntegration("js-client")
	if err != nil {
		t.Fatal(err)
	}
	config := schema.IntegrationConfig{Out: "protocol"}
	data.GolemImport = "golem-engine"
	data.WorldCatalogCustomTypes = schema.WorldCatalogCustomTypeNames(data.WorldTypes)
	for _, entity := range data.Entities {
		entity.GolemImport = data.GolemImport
		if err := generateIntegration(root, config, js, entity); err != nil {
			t.Fatal(err)
		}
	}
	shared := append([]SharedTemplateEntry{}, js.SharedTemplates...)
	if len(data.WorldTypes) > 0 {
		shared = append(shared, SharedTemplateEntry{Template: "js/world_manager.ts.tmpl", File: "WorldManager.ts"})
	}
	if len(data.Events) > 0 {
		shared = append(shared, SharedTemplateEntry{Template: "js/event_manager.ts.tmpl", File: "EventManager.ts"})
	}
	for _, template := range shared {
		if err := generateShared(root, config, js, data, template); err != nil {
			t.Fatal(err)
		}
	}
	proto := schema.BuildProtoData(&schema.Config{Simulation: schema.SimulationConfig{Dimensions: 3}}, data.Entities, data.Commands, data.WorldTypes, data.CustomTypes, data.Events)
	if err := generateJSProto(filepath.Join(root, config.Out), data.GolemImport, data.Entities, data.Commands, data.WorldTypes, data.Events, proto); err != nil {
		t.Fatal(err)
	}
	bridge, err := GetIntegration("threlte")
	if err != nil {
		t.Fatal(err)
	}
	data.GolemImport = "golem-threlte"
	data.ProtocolImport = "../protocol/"
	if err := generateShared(root, schema.IntegrationConfig{Out: "bridge"}, bridge, data, bridge.SharedTemplates[0]); err != nil {
		t.Fatal(err)
	}
}

const threlteConsumerTypeFixture = `import type { Component } from 'svelte';
import { defineEntityViews, useGolem, type ActorViewProps, type BeaconViewProps } from './bridge/GolemThrelte.js';
import { sendMove } from './protocol/client.js';
declare const Actor: Component<ActorViewProps>;
declare const Beacon: Component<BeaconViewProps>;
defineEntityViews(views => ({ Actor: views.Actor.component(Actor), Beacon: views.Beacon.component(Beacon), Session: views.Session.headless() }));
// @ts-expect-error Every generated type must be registered.
defineEntityViews(views => ({ Actor: views.Actor.component(Actor) }));
defineEntityViews(views => ({
  // @ts-expect-error A Beacon component cannot present an Actor.
  Actor: views.Actor.component(Beacon),
  Beacon: views.Beacon.component(Beacon), Session: views.Session.headless()
}));
declare const actor: ActorViewProps;
actor.view.onEvent('Hit', event => {
  event.amount satisfies number;
  // @ts-expect-error The generated event payload is not a string.
  event.amount satisfies string;
});
// @ts-expect-error Actor has no Notice entity event.
actor.view.onEvent('Notice', () => {});
function hud() {
  const golem = useGolem();
  sendMove(golem.client, 1, 2);
  golem.world.SpeciesCatalog.subscribe(catalog => {
    catalog?.items['actor'].id satisfies string | undefined;
  });
}
`
