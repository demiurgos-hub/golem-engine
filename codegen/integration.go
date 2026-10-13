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
	Template        string // optional per-entity template path inside the embedded templates/ FS
	FileNamer       func(entityName string) string
	PostProcess     func(path string) error
	SharedTemplates []SharedTemplateEntry
	// GenerateExtra runs after templates, before Finalize (e.g. emit protobuf stubs).
	GenerateExtra func(outDir, golemImport, goPackage string, entities []schema.EntityData, commands []schema.CommandData, worldTypes []schema.WorldTypeData, events []schema.EventData, proto schema.ProtoTemplateData) error
	// Finalize runs once per integration after all files are written (e.g. compile TS to JS).
	Finalize func(projectRoot string, outDir string) error
}

// GetIntegration returns a built-in integration by config key, or an error if unknown.
func GetIntegration(name string) (Integration, error) {
	spec, ok := integrationSpecs[name]
	if !ok {
		return Integration{}, fmt.Errorf("unknown integration %q (available: go-server, go-client, js-client, csharp-client, phaser, threlte, unity, ebiten)", name)
	}
	return spec.integration(name), nil
}

func gofmtFile(path string) error {
	cmd := exec.Command("gofmt", "-w", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gofmt %s: %w\n%s", path, err, out)
	}
	return nil
}
