package schema

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// The existence result preserves optional loaders' missing-versus-empty slices.
// Validate each decoded file before reading another to retain diagnostic order.
func visitYAMLDirectory[T any](dir, kind string, optional bool, visit func(string, T) error) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if optional && os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s dir %s: %w", kind, dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return false, fmt.Errorf("reading %s: %w", entry.Name(), err)
		}
		var value T
		if err := yaml.Unmarshal(data, &value); err != nil {
			return false, fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}
		if err := visit(entry.Name(), value); err != nil {
			return true, err
		}
	}
	return true, nil
}
