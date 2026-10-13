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
	if err := validateThrelteIntegration(projectRoot, cfg); err != nil {
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

	if err := schema.ValidateEntityColliders(entities, cfg.ResolvedCollision); err != nil {
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
	protoData, err := schema.ResolveProtoData(cfg, entities, commands, worldTypes, customTypesList, events)
	if err != nil {
		return err
	}

	var protocolConstantSets []schema.ProtocolConstantSetData
	if cfg.ProtocolConstants != "" {
		constantsPath := cfg.ProtocolConstants
		if !filepath.IsAbs(constantsPath) {
			constantsPath = filepath.Join(projectRoot, constantsPath)
		}
		protocolConstantSets, err = schema.LoadProtocolConstants(constantsPath)
		if err != nil {
			return err
		}
	}

	for _, wt := range worldTypes {
		if wt.Source == nil || wt.Source.Format != "catalog" {
			continue
		}
		catalogPath := filepath.Join(projectRoot, wt.Source.File)
		if err := schema.ValidateCatalogFile(catalogPath, wt.Source.Key); err != nil {
			return fmt.Errorf("catalog %s: %w", wt.Source.File, err)
		}
	}

	if err := generateProto(projectRoot, cfg, protoData); err != nil {
		return err
	}

	protoData.ProtocolConstantSets = protocolConstantSets

	// Populate each entity's Events field with events that target it.
	entityEventMap := make(map[string][]schema.EventData, len(entities))
	for _, ev := range events {
		if ev.EntityType != "" {
			entityEventMap[ev.EntityType] = append(entityEventMap[ev.EntityType], ev)
		}
	}

	for integName, integCfg := range cfg.Integrations {
		integ, err := GetIntegration(integName)
		if err != nil {
			return err
		}

		spec := integrationSpecs[integName]
		golemImport := integCfg.GolemImport
		if golemImport == "" {
			golemImport = spec.defaultImport
		}
		goPackage := integCfg.Package
		if goPackage == "" {
			goPackage = spec.defaultPackage
		}
		// Template-only decorations must not leak to another target or into the
		// resolved schema descriptors shared by codec generators.
		entities := append([]schema.EntityData(nil), entities...)

		for i := range entities {
			entities[i].Events = entityEventMap[entities[i].Name]
			entities[i].ProtocolImport = integCfg.ProtocolImport
			entities[i].GolemImport = golemImport
			if spec.defaultPackage != "" {
				entities[i].GoPackage = goPackage
			} else {
				entities[i].GoPackage = ""
			}

		}

		sd := schema.SharedData{
			ProtocolImport:          integCfg.ProtocolImport,
			GolemImport:             golemImport,
			GoPackage:               goPackage,
			Dimensions:              cfg.Simulation.Dimensions,
			Is3D:                    cfg.Simulation.Dimensions == 3,
			Collision:               cfg.ResolvedCollision,
			Entities:                entities,
			Commands:                commands,
			WorldTypes:              worldTypes,
			Events:                  events,
			CustomTypes:             customTypesList,
			WorldCatalogCustomTypes: schema.WorldCatalogCustomTypeNames(worldTypes),
			Fingerprint:             ComputeSchemaFingerprint(entities),
		}

		for _, output := range selectIntegrationOutputs(integName, sd) {
			switch output.role {
			case entityOutput:
				if err := generateIntegration(projectRoot, integCfg, integ, entities[output.entityIndex]); err != nil {
					return err
				}
			case sharedOutput:
				if err := generateShared(projectRoot, integCfg, integ, sd, SharedTemplateEntry{Template: output.template, File: output.file}); err != nil {
					return err
				}
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

	fmt.Printf("golem-bake: generated code for %d entity type(s), %d command(s), %d world type(s), %d event(s), %d custom type(s), and %d protocol constant set(s)\n", len(entities), len(commands), len(worldTypes), len(events), len(customTypesList), len(protocolConstantSets))
	return nil
}

func generateProto(projectRoot string, cfg *schema.Config, data schema.ProtoTemplateData) error {
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
		"FullStateProjection": goFullStateProjection,
		"LowerFirst":          schema.LcFirst,
		"Quote":               func(s string) string { return fmt.Sprintf("%q", s) },
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
