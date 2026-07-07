package codegen

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/demiurgos-hub/golem-engine/schema"
)

//go:embed templates/*/*.tmpl
var templateFS embed.FS

// Bake is the main entry point: reads golem.yaml + schemas, generates all output files.
func Bake(projectRoot string) error {
	cfg, err := schema.LoadConfig(projectRoot)
	if err != nil {
		return err
	}

	typesDir := filepath.Join(projectRoot, cfg.TypesSchema)
	customTypesList, err := schema.LoadCustomTypes(typesDir)
	if err != nil {
		return err
	}
	customTypesMap := make(map[string]schema.CustomTypeData, len(customTypesList))
	for _, ct := range customTypesList {
		customTypesMap[ct.Name] = ct
	}

	schemasDir := filepath.Join(projectRoot, cfg.EntitySchemas)
	entities, err := schema.LoadSchemas(schemasDir, cfg.Simulation.Dimensions, customTypesMap)
	if err != nil {
		return err
	}

	commandsDir := filepath.Join(projectRoot, cfg.CommandSchemas)
	commands, err := schema.LoadCommands(commandsDir, customTypesMap)
	if err != nil {
		return err
	}

	if err := schema.ValidateCommands(commands, entities); err != nil {
		return err
	}

	worldDir := filepath.Join(projectRoot, cfg.WorldSchema)
	worldTypes, err := schema.LoadWorldSchemas(worldDir, customTypesMap)
	if err != nil {
		return err
	}

	eventsDir := filepath.Join(projectRoot, cfg.EventSchemas)
	events, err := schema.LoadEvents(eventsDir, customTypesMap)
	if err != nil {
		return err
	}

	if err := schema.ValidateEvents(events, entities); err != nil {
		return err
	}

	for _, wt := range worldTypes {
		if wt.Source == nil || wt.Source.Format != "catalog" {
			continue
		}
		// Validate that the declared key field actually exists on the custom type.
		if wt.Source.Key != "" {
			ct, ok := customTypesMap[wt.Source.Type]
			if !ok {
				return fmt.Errorf("world %q: catalog type %q not found in custom types", wt.Name, wt.Source.Type)
			}
			found := false
			for _, cf := range ct.Fields {
				if cf.SnakeName == wt.Source.Key {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("world %q: catalog key %q not found on type %q", wt.Name, wt.Source.Key, wt.Source.Type)
			}
		}
		catalogPath := filepath.Join(projectRoot, wt.Source.File)
		if err := schema.ValidateCatalogFile(catalogPath, wt.Source.Key); err != nil {
			return fmt.Errorf("catalog %s: %w", wt.Source.File, err)
		}
	}

	if err := generateProto(projectRoot, cfg, entities, commands, worldTypes, customTypesList, events); err != nil {
		return err
	}

	protoData := schema.BuildProtoData(cfg, entities, commands, worldTypes, customTypesList, events)

	// Populate each entity's Events field with events that target it.
	entityEventMap := make(map[string][]schema.EventData, len(entities))
	for _, ev := range events {
		if ev.EntityType != "" {
			entityEventMap[ev.EntityType] = append(entityEventMap[ev.EntityType], ev)
		}
	}
	for i := range entities {
		entities[i].Events = entityEventMap[entities[i].Name]
	}

	for integName, integCfg := range cfg.Integrations {
		integ, err := GetIntegration(integName)
		if err != nil {
			return err
		}

		golemImport := integCfg.GolemImport
		if golemImport == "" {
			switch integName {
			case "go-server":
				golemImport = "github.com/demiurgos-hub/golem-engine/golem"
			case "go-client":
				golemImport = "github.com/demiurgos-hub/golem-engine/golem-go-client"
			case "js-client":
				golemImport = "golem-engine"
			case "csharp-client":
				golemImport = "GolemEngine.Unity"
			case "phaser":
				golemImport = "golem-phaser"
			case "unity":
				golemImport = "GolemEngine.Unity"
			case "ebiten":
				golemImport = "github.com/demiurgos-hub/golem-engine/golem-ebiten"
			}
		}

		goPackage := integCfg.Package
		if (integName == "go-server" || integName == "go-client" || integName == "ebiten") && goPackage == "" {
			goPackage = "synced"
		}
		if (integName == "csharp-client" || integName == "unity") && goPackage == "" {
			goPackage = "Golem.Generated"
		}

		for i := range entities {
			entities[i].ProtocolImport = integCfg.ProtocolImport
			entities[i].GolemImport = golemImport
			if integName == "go-server" || integName == "go-client" || integName == "ebiten" || integName == "csharp-client" || integName == "unity" {
				entities[i].GoPackage = goPackage
			} else {
				entities[i].GoPackage = ""
			}
			if err := generateIntegration(projectRoot, integCfg, integ, entities[i]); err != nil {
				return err
			}
		}

		sd := schema.SharedData{
			ProtocolImport:          integCfg.ProtocolImport,
			GolemImport:             golemImport,
			GoPackage:               goPackage,
			Dimensions:              cfg.Simulation.Dimensions,
			Is3D:                    cfg.Simulation.Dimensions == 3,
			Entities:                entities,
			Commands:                commands,
			WorldTypes:              worldTypes,
			Events:                  events,
			CustomTypes:             customTypesList,
			WorldCatalogCustomTypes: schema.WorldCatalogCustomTypeNames(worldTypes),
			Fingerprint:             ComputeSchemaFingerprint(entities),
		}

		sharedTemplates := integ.SharedTemplates
		if integName == "js-client" && len(worldTypes) > 0 {
			sharedTemplates = append(sharedTemplates, SharedTemplateEntry{
				Template: "js/world_manager.ts.tmpl",
				File:     "WorldManager.ts",
			})
		}
		if integName == "csharp-client" && len(worldTypes) > 0 {
			sharedTemplates = append(sharedTemplates, SharedTemplateEntry{
				Template: "csharp/world_manager.cs.tmpl",
				File:     "WorldManager.cs",
			})
		}
		if integName == "go-client" && len(worldTypes) > 0 {
			sharedTemplates = append(sharedTemplates, SharedTemplateEntry{
				Template: "go_client/world_manager.go.tmpl",
				File:     "world_manager.go",
			})
		}
		if integName == "js-client" && len(events) > 0 {
			sharedTemplates = append(sharedTemplates, SharedTemplateEntry{
				Template: "js/event_manager.ts.tmpl",
				File:     "EventManager.ts",
			})
		}
		if integName == "csharp-client" && len(events) > 0 {
			sharedTemplates = append(sharedTemplates, SharedTemplateEntry{
				Template: "csharp/event_manager.cs.tmpl",
				File:     "EventManager.cs",
			})
		}
		if integName == "go-client" && len(events) > 0 {
			sharedTemplates = append(sharedTemplates, SharedTemplateEntry{
				Template: "go_client/event_manager.go.tmpl",
				File:     "event_manager.go",
			})
		}

		for _, st := range sharedTemplates {
			if err := generateShared(projectRoot, integCfg, integ, sd, st); err != nil {
				return err
			}
		}

		if integ.GenerateExtra != nil {
			outDir := filepath.Join(projectRoot, integCfg.Out)
			if err := integ.GenerateExtra(outDir, golemImport, goPackage, entities, commands, worldTypes, events, protoData); err != nil {
				return err
			}
		}

		if integ.Finalize != nil {
			outDir := filepath.Join(projectRoot, integCfg.Out)
			if err := integ.Finalize(projectRoot, outDir); err != nil {
				return err
			}
			fmt.Printf("  finalized %s in %s\n", integName, integCfg.Out)
		}
	}

	fmt.Printf("golem-bake: generated code for %d entity type(s), %d command(s), %d world type(s), %d event(s), and %d custom type(s)\n", len(entities), len(commands), len(worldTypes), len(events), len(customTypesList))
	return nil
}

func generateProto(projectRoot string, cfg *schema.Config, entities []schema.EntityData, commands []schema.CommandData, worldTypes []schema.WorldTypeData, customTypes []schema.CustomTypeData, events []schema.EventData) error {
	data := schema.BuildProtoData(cfg, entities, commands, worldTypes, customTypes, events)
	t, err := loadEmbeddedTemplate("templates/proto/entities.proto.tmpl")
	if err != nil {
		return err
	}
	outPath := filepath.Join(projectRoot, cfg.Proto.Out)
	if err := writeTemplate(outPath, t, data); err != nil {
		return err
	}
	fmt.Printf("  wrote %s\n", outPath)
	return nil
}

func generateShared(projectRoot string, integCfg schema.IntegrationConfig, integ Integration, sd schema.SharedData, st SharedTemplateEntry) error {
	t, err := loadEmbeddedTemplate("templates/" + st.Template)
	if err != nil {
		return err
	}
	outDir := filepath.Join(projectRoot, integCfg.Out)
	outPath := filepath.Join(outDir, st.File)

	if err := writeTemplate(outPath, t, sd); err != nil {
		return err
	}
	fmt.Printf("  wrote %s\n", outPath)

	if integ.PostProcess != nil {
		if err := integ.PostProcess(outPath); err != nil {
			return err
		}
	}
	return nil
}

func generateIntegration(projectRoot string, integCfg schema.IntegrationConfig, integ Integration, ent schema.EntityData) error {
	t, err := loadEmbeddedTemplate("templates/" + integ.Template)
	if err != nil {
		return err
	}
	outDir := filepath.Join(projectRoot, integCfg.Out)
	outPath := filepath.Join(outDir, integ.FileNamer(ent.Name))

	if err := writeTemplate(outPath, t, ent); err != nil {
		return err
	}
	fmt.Printf("  wrote %s\n", outPath)

	if integ.PostProcess != nil {
		if err := integ.PostProcess(outPath); err != nil {
			return err
		}
	}
	return nil
}

func loadEmbeddedTemplate(path string) (*template.Template, error) {
	funcs := template.FuncMap{
		"LowerFirst": schema.LcFirst,
		"Quote":      func(s string) string { return fmt.Sprintf("%q", s) },
		"Add": func(values ...int) int {
			sum := 0
			for _, v := range values {
				sum += v
			}
			return sum
		},
	}
	t, err := template.New(filepath.Base(path)).Funcs(funcs).ParseFS(templateFS, path)
	if err != nil {
		return nil, fmt.Errorf("parsing template %s: %w", path, err)
	}
	return t, nil
}

func writeTemplate(path string, t *template.Template, data any) error {
	if path != "" && strings.Contains(path, `\`) {
		path = filepath.Clean(path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating dir for %s: %w", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating %s: %w", path, err)
	}
	defer f.Close()
	if err := t.Execute(f, data); err != nil {
		return fmt.Errorf("executing template for %s: %w", path, err)
	}
	return nil
}
