package schema

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config is the top-level golem.yaml project configuration.
type Config struct {
	EntitySchemas  string                       `yaml:"entity_schema"`
	CommandSchemas string                       `yaml:"command_schema"`
	WorldSchema    string                       `yaml:"world_schema"`
	TypesSchema    string                       `yaml:"types_schema"`
	EventSchemas   string                       `yaml:"event_schema"`
	Simulation     SimulationConfig             `yaml:"simulation"`
	Proto          ProtoConfig                  `yaml:"proto"`
	Integrations   map[string]IntegrationConfig `yaml:"integrations"`
}

// SimulationConfig controls project-wide simulation semantics used by codegen.
type SimulationConfig struct {
	Dimensions int `yaml:"dimensions"`
}

// ProtoConfig controls the generated entities.proto file.
type ProtoConfig struct {
	Package   string `yaml:"package"`
	GoPackage string `yaml:"go_package"`
	Out       string `yaml:"out"`
}

// IntegrationConfig holds per-integration output path and import settings.
type IntegrationConfig struct {
	ProtocolImport string `yaml:"protocol_import"`
	GolemImport    string `yaml:"golem_import"`
	Out            string `yaml:"out"`
	Package        string `yaml:"package"`
}

// LoadConfig reads and parses golem.yaml from the given project root.
func LoadConfig(projectRoot string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(projectRoot, "golem.yaml"))
	if err != nil {
		return nil, fmt.Errorf("reading golem.yaml: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing golem.yaml: %w", err)
	}
	if cfg.EntitySchemas == "" {
		cfg.EntitySchemas = "schemas/entities/"
	}
	if cfg.CommandSchemas == "" {
		cfg.CommandSchemas = "schemas/commands/"
	}
	if cfg.WorldSchema == "" {
		cfg.WorldSchema = "schemas/world/"
	}
	if cfg.TypesSchema == "" {
		cfg.TypesSchema = "schemas/types/"
	}
	if cfg.EventSchemas == "" {
		cfg.EventSchemas = "schemas/events/"
	}
	if cfg.Simulation.Dimensions == 0 {
		cfg.Simulation.Dimensions = 2
	}
	if cfg.Simulation.Dimensions != 2 && cfg.Simulation.Dimensions != 3 {
		return nil, fmt.Errorf("simulation.dimensions must be 2 or 3, got %d", cfg.Simulation.Dimensions)
	}
	return &cfg, nil
}
