package codegen

import (
	"fmt"
	"os/exec"

	"github.com/demiurgos-hub/golem-engine/schema"
)

// SharedTemplateEntry pairs an embedded template path with its output filename.
type SharedTemplateEntry struct {
	Template string
	File     string
}

// Integration describes a built-in code generation target (e.g. Go server, JS client).
type Integration struct {
	Name            string
	Template        string // path inside the embedded templates/ FS, e.g. "go_server/server.go.tmpl"
	FileNamer       func(entityName string) string
	PostProcess     func(path string) error
	SharedTemplates []SharedTemplateEntry
	// GenerateExtra runs after templates, before Finalize (e.g. emit protobuf stubs).
	GenerateExtra func(outDir, golemImport, goPackage string, entities []schema.EntityData, commands []schema.CommandData, worldTypes []schema.WorldTypeData, events []schema.EventData, proto schema.ProtoTemplateData) error
	// Finalize runs once per integration after all files are written (e.g. compile TS to JS).
	Finalize func(projectRoot string, outDir string) error
}

var builtinIntegrations = map[string]Integration{
	"go-server": {
		Name:     "go-server",
		Template: "go_server/server.go.tmpl",
		FileNamer: func(name string) string {
			return schema.ToSnakeCase(name) + "_synced.go"
		},
		PostProcess: gofmtFile,
		SharedTemplates: []SharedTemplateEntry{
			{Template: "go_server/shared.go.tmpl", File: "golem_helpers.go"},
		},
		GenerateExtra: func(outDir, golemImport, goPackage string, entities []schema.EntityData, commands []schema.CommandData, worldTypes []schema.WorldTypeData, events []schema.EventData, proto schema.ProtoTemplateData) error {
			return generateGoProto(outDir, golemImport, goPackage, entities, commands, worldTypes, events, proto)
		},
	},
	"go-client": {
		Name:     "go-client",
		Template: "go_client/client.go.tmpl",
		FileNamer: func(name string) string {
			return schema.ToSnakeCase(name) + "_synced.go"
		},
		PostProcess: gofmtFile,
		SharedTemplates: []SharedTemplateEntry{
			{Template: "go_client/manager.go.tmpl", File: "entity_manager.go"},
			{Template: "go_client/create_client.go.tmpl", File: "client.go"},
		},
		GenerateExtra: func(outDir, golemImport, goPackage string, entities []schema.EntityData, commands []schema.CommandData, worldTypes []schema.WorldTypeData, events []schema.EventData, proto schema.ProtoTemplateData) error {
			return generateGoProto(outDir, golemImport, goPackage, entities, commands, worldTypes, events, proto)
		},
	},
	"js-client": {
		Name:     "js-client",
		Template: "js/client.ts.tmpl",
		FileNamer: func(name string) string {
			return name + "Synced.ts"
		},
		SharedTemplates: []SharedTemplateEntry{
			{Template: "js/manager.ts.tmpl", File: "EntityManager.ts"},
			{Template: "js/create_client.ts.tmpl", File: "client.ts"},
		},
		GenerateExtra: func(outDir, golemImport, goPackage string, entities []schema.EntityData, commands []schema.CommandData, worldTypes []schema.WorldTypeData, events []schema.EventData, proto schema.ProtoTemplateData) error {
			return generateJSProto(outDir, golemImport, entities, commands, worldTypes, events, proto)
		},
		Finalize: compileJSIntegration,
	},
	"csharp-client": {
		Name:     "csharp-client",
		Template: "csharp/client.cs.tmpl",
		FileNamer: func(name string) string {
			return "Synced" + name + ".cs"
		},
		SharedTemplates: []SharedTemplateEntry{
			{Template: "csharp/manager.cs.tmpl", File: "EntityManager.cs"},
			{Template: "csharp/create_client.cs.tmpl", File: "Client.cs"},
		},
		GenerateExtra: func(outDir, golemImport, goPackage string, entities []schema.EntityData, commands []schema.CommandData, worldTypes []schema.WorldTypeData, events []schema.EventData, proto schema.ProtoTemplateData) error {
			return generateCSharpProto(outDir, golemImport, goPackage, entities, commands, worldTypes, events, proto)
		},
	},
	// phaser emits raw .ts bridge scaffolds per entity (no tsc step — the
	// consumer's own bundler compiles them alongside game code).
	"phaser": {
		Name:     "phaser",
		Template: "phaser/entity_bridge.ts.tmpl",
		FileNamer: func(name string) string {
			return name + "Bridge.ts"
		},
	},
	"unity": {
		Name:     "unity",
		Template: "unity/entity_bridge.cs.tmpl",
		FileNamer: func(name string) string {
			return name + "View.cs"
		},
	},
	"ebiten": {
		Name:     "ebiten",
		Template: "ebiten/entity_bridge.go.tmpl",
		FileNamer: func(name string) string {
			return schema.ToSnakeCase(name) + "_view.go"
		},
		PostProcess: gofmtFile,
	},
}

// GetIntegration returns a built-in integration by config key, or an error if unknown.
func GetIntegration(name string) (Integration, error) {
	integ, ok := builtinIntegrations[name]
	if !ok {
		return Integration{}, fmt.Errorf("unknown integration %q (available: go-server, go-client, js-client, csharp-client, phaser, unity, ebiten)", name)
	}
	return integ, nil
}

func gofmtFile(path string) error {
	cmd := exec.Command("gofmt", "-w", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gofmt %s: %w\n%s", path, err, out)
	}
	return nil
}
