package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/demiurgos-hub/golem-engine/golem/content"
	"gopkg.in/yaml.v3"
)

// ContentConfig selects portable definitions separately from replicated world data.
type ContentConfig struct {
	Schema  string   `yaml:"schema"`
	Bundle  string   `yaml:"bundle"`
	Imports []string `yaml:"imports,omitempty"`
}

// UnmarshalYAML rejects unknown content options without changing legacy config decoding.
func (c *ContentConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("content: expected schema, bundle and optional imports mapping")
	}
	for i := 0; i < len(node.Content); i += 2 {
		switch node.Content[i].Value {
		case "schema", "bundle", "imports":
		default:
			return fmt.Errorf("content: unknown option %q", node.Content[i].Value)
		}
	}
	type plainContentConfig ContentConfig
	return node.Decode((*plainContentConfig)(c))
}

// ContentData holds validated, immutable content inputs for generation.
type ContentData struct {
	Schema     content.Schema
	SchemaJSON []byte
	SchemaHash string
	BundleJSON []byte
}

// LoadContent validates explicit content inputs without discovering dependencies.
// Imports are finalized bundles listed in dependency order. No content is loaded
// when cfg is nil, preserving the existing schema and wire generation paths.
func LoadContent(projectRoot string, cfg *ContentConfig, entities []EntityData) (*ContentData, error) {
	if cfg == nil {
		return nil, nil
	}
	if cfg.Schema == "" || cfg.Bundle == "" {
		return nil, fmt.Errorf("content: schema and bundle paths are both required")
	}
	open := func(path string) (*os.File, error) {
		if !filepath.IsAbs(path) {
			path = filepath.Join(projectRoot, path)
		}
		return os.Open(path)
	}
	f, err := open(cfg.Schema)
	if err != nil {
		return nil, fmt.Errorf("content schema %s: %w", cfg.Schema, err)
	}
	s, err := content.ParseSchema(f)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("content schema %s: %w", cfg.Schema, err)
	}
	entityNames := make(map[string]bool, len(entities))
	for _, entity := range entities {
		entityNames[entity.Name] = true
	}
	for _, typ := range s.Types {
		if typ.Entity != "" && !entityNames[typ.Entity] {
			return nil, fmt.Errorf("content type %q: unknown replicated entity %q", typ.Name, typ.Entity)
		}
	}
	var imports []*content.Catalog
	load := func(path string, finalized bool) (*content.Catalog, error) {
		f, err := open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return content.Load(f, s, content.Options{Imports: imports, RequireSchemaHash: finalized})
	}
	for _, path := range cfg.Imports {
		catalog, err := load(path, true)
		if err != nil {
			return nil, fmt.Errorf("content import %s: %w", path, err)
		}
		imports = append(imports, catalog)
	}
	catalog, err := load(cfg.Bundle, false)
	if err != nil {
		return nil, fmt.Errorf("content bundle %s: %w", cfg.Bundle, err)
	}
	s = catalog.Schema()
	schemaJSON, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("content schema: %w", err)
	}
	bundleJSON, err := catalog.MarshalBundle()
	if err != nil {
		return nil, fmt.Errorf("content bundle: %w", err)
	}
	return &ContentData{Schema: s, SchemaJSON: schemaJSON, SchemaHash: catalog.SchemaHash(), BundleJSON: bundleJSON}, nil
}
