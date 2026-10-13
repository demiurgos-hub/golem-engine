package codegen

import (
	"path/filepath"
	"strings"

	"github.com/demiurgos-hub/golem-engine/schema"
)

type outputRole uint8

const (
	entityOutput outputRole = iota
	sharedOutput
	entityCodecOutput
	worldCodecOutput
	eventCodecOutput
)

type outputCondition uint8

const (
	alwaysOutput outputCondition = iota
	withWorld
	withEvents
)

type outputRule struct {
	role           outputRole
	file           string
	entityName     func(string) string
	template       string
	condition      outputCondition
	jsIntermediate bool
}

type integrationOutput struct {
	role        outputRole
	file        string
	template    string
	entityIndex int // meaningful only for entityOutput
}

type integrationSpec struct {
	hooks          Integration
	defaultImport  string
	defaultPackage string
	outputs        []outputRule
}

// Keep the public integration description as a compatibility projection. Each
// lookup owns its slice, so selecting optional outputs cannot mutate the registry.
func (s integrationSpec) integration(name string) Integration {
	integration := s.hooks
	integration.Name = name
	for _, rule := range s.outputs {
		if rule.role == entityOutput {
			integration.Template, integration.FileNamer = rule.template, rule.entityName
		} else if rule.role == sharedOutput && rule.condition == alwaysOutput {
			integration.SharedTemplates = append(integration.SharedTemplates, SharedTemplateEntry{Template: rule.template, File: rule.file})
		}
	}
	return integration
}

func selectIntegrationOutputs(target string, data schema.SharedData) []integrationOutput {
	var outputs []integrationOutput
	for _, rule := range integrationSpecs[target].outputs {
		if rule.condition == withWorld && len(data.WorldTypes) == 0 || rule.condition == withEvents && len(data.Events) == 0 {
			continue
		}
		if rule.role == entityOutput {
			for i, entity := range data.Entities {
				outputs = append(outputs, integrationOutput{role: rule.role, file: rule.entityName(entity.Name), template: rule.template, entityIndex: i})
			}
		} else {
			outputs = append(outputs, integrationOutput{role: rule.role, file: rule.file, template: rule.template})
		}
	}
	return outputs
}

func codecOutputName(target string, role outputRole) string {
	for _, rule := range integrationSpecs[target].outputs {
		if rule.role == role {
			return rule.file
		}
	}
	panic("missing integration codec output")
}

func isGeneratedJSIntermediateName(name string) bool {
	if !strings.HasSuffix(name, ".ts") || strings.HasSuffix(name, ".d.ts") {
		return false
	}
	for _, rule := range integrationSpecs["js-client"].outputs {
		if !rule.jsIntermediate {
			continue
		}
		if rule.entityName != nil {
			// Recognize entities that no longer occur in the current schema as well.
			if strings.HasSuffix(name, rule.entityName("")) {
				return true
			}
		} else if name == rule.file {
			return true
		}
	}
	return false
}

// Callers own path resolution and their policy for filepath.Rel errors.
func isContainedRelativePath(rel string) bool {
	return !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

var integrationSpecs map[string]integrationSpec

func init() {
	integrationSpecs = map[string]integrationSpec{
		"go-server": {defaultImport: "github.com/demiurgos-hub/golem-engine/golem", defaultPackage: "synced",
			hooks: Integration{PostProcess: gofmtFile, GenerateExtra: generateGoProto},
			outputs: []outputRule{
				{role: entityOutput, entityName: func(name string) string { return schema.ToSnakeCase(name) + "_synced.go" }, template: "go_server/server.go.tmpl"},
				{role: sharedOutput, file: "golem_helpers.go", template: "go_server/shared.go.tmpl", condition: alwaysOutput},
				{role: entityCodecOutput, file: "entities_pb.go", condition: alwaysOutput},
				{role: worldCodecOutput, file: "world_pb.go", condition: withWorld},
				{role: eventCodecOutput, file: "events_pb.go", condition: withEvents},
			}},
		"go-client": {defaultImport: "github.com/demiurgos-hub/golem-engine/golem-go-client", defaultPackage: "synced",
			hooks: Integration{PostProcess: gofmtFile, GenerateExtra: generateGoProto},
			outputs: []outputRule{
				{role: entityOutput, entityName: func(name string) string { return schema.ToSnakeCase(name) + "_synced.go" }, template: "go_client/client.go.tmpl"},
				{role: sharedOutput, file: "entity_manager.go", template: "go_client/manager.go.tmpl", condition: alwaysOutput},
				{role: sharedOutput, file: "client.go", template: "go_client/create_client.go.tmpl", condition: alwaysOutput},
				{role: sharedOutput, file: "world_manager.go", template: "go_client/world_manager.go.tmpl", condition: withWorld},
				{role: sharedOutput, file: "event_manager.go", template: "go_client/event_manager.go.tmpl", condition: withEvents},
				{role: entityCodecOutput, file: "entities_pb.go", condition: alwaysOutput},
				{role: worldCodecOutput, file: "world_pb.go", condition: withWorld},
				{role: eventCodecOutput, file: "events_pb.go", condition: withEvents},
			}},
		"js-client": {defaultImport: "golem-engine", defaultPackage: "",
			hooks: Integration{GenerateExtra: func(outDir, golemImport, goPackage string, entities []schema.EntityData, commands []schema.CommandData, worldTypes []schema.WorldTypeData, events []schema.EventData, proto schema.ProtoTemplateData) error {
				return generateJSProto(outDir, golemImport, entities, commands, worldTypes, events, proto)
			}, Finalize: compileJSIntegration},
			outputs: []outputRule{
				{role: entityOutput, entityName: func(name string) string { return name + "Synced.ts" }, template: "js/client.ts.tmpl", jsIntermediate: true},
				{role: sharedOutput, file: "EntityManager.ts", template: "js/manager.ts.tmpl", condition: alwaysOutput, jsIntermediate: true},
				{role: sharedOutput, file: "client.ts", template: "js/create_client.ts.tmpl", condition: alwaysOutput, jsIntermediate: true},
				{role: sharedOutput, file: "WorldManager.ts", template: "js/world_manager.ts.tmpl", condition: withWorld, jsIntermediate: true},
				{role: sharedOutput, file: "EventManager.ts", template: "js/event_manager.ts.tmpl", condition: withEvents, jsIntermediate: true},
				{role: entityCodecOutput, file: "entities_pb.ts", condition: alwaysOutput, jsIntermediate: true},
				{role: worldCodecOutput, file: "world_pb.ts", condition: withWorld, jsIntermediate: true},
				{role: eventCodecOutput, file: "events_pb.ts", condition: withEvents, jsIntermediate: true},
			}},
		"csharp-client": {defaultImport: "GolemEngine.Unity", defaultPackage: "Golem.Generated",
			hooks: Integration{GenerateExtra: generateCSharpProto},
			outputs: []outputRule{
				{role: entityOutput, entityName: func(name string) string { return "Synced" + name + ".cs" }, template: "csharp/client.cs.tmpl"},
				{role: sharedOutput, file: "EntityManager.cs", template: "csharp/manager.cs.tmpl", condition: alwaysOutput},
				{role: sharedOutput, file: "Client.cs", template: "csharp/create_client.cs.tmpl", condition: alwaysOutput},
				{role: sharedOutput, file: "WorldManager.cs", template: "csharp/world_manager.cs.tmpl", condition: withWorld},
				{role: sharedOutput, file: "EventManager.cs", template: "csharp/event_manager.cs.tmpl", condition: withEvents},
				{role: entityCodecOutput, file: "EntitiesPb.cs", condition: alwaysOutput},
				{role: worldCodecOutput, file: "WorldPb.cs", condition: withWorld},
				{role: eventCodecOutput, file: "EventsPb.cs", condition: withEvents},
			}},
		"phaser": {defaultImport: "golem-phaser", defaultPackage: "",
			outputs: []outputRule{
				{role: sharedOutput, file: "GolemPhaser.ts", template: "phaser/golem_phaser.ts.tmpl", condition: alwaysOutput},
			}},
		"threlte": {defaultImport: "golem-threlte", defaultPackage: "",
			outputs: []outputRule{
				{role: sharedOutput, file: "GolemThrelte.ts", template: "threlte/golem_threlte.ts.tmpl", condition: alwaysOutput},
			}},
		"unity": {defaultImport: "GolemEngine.Unity", defaultPackage: "Golem.Generated",
			outputs: []outputRule{
				{role: entityOutput, entityName: func(name string) string { return name + "View.cs" }, template: "unity/entity_bridge.cs.tmpl"},
			}},
		"ebiten": {defaultImport: "github.com/demiurgos-hub/golem-engine/golem-ebiten", defaultPackage: "synced",
			hooks: Integration{PostProcess: gofmtFile},
			outputs: []outputRule{
				{role: entityOutput, entityName: func(name string) string { return schema.ToSnakeCase(name) + "_view.go" }, template: "ebiten/entity_bridge.go.tmpl"},
			}},
	}
}
