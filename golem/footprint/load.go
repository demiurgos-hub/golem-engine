package footprint

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Load reads and validates a footprints.golem.yaml file from path.
func Load(path string) (*Set, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("footprint: read %s: %w", path, err)
	}
	set, err := Parse(data)
	if err != nil {
		// Parse errors already carry the "footprint:" prefix; keep a single one.
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return set, nil
}

// Parse validates footprints.golem.yaml bytes and returns a Set.
// Unknown YAML fields are rejected.
func Parse(data []byte) (*Set, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var doc fileDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	// Reject trailing documents.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("footprint: unexpected trailing YAML document")
		}
		return nil, err
	}
	return buildSet(doc)
}
