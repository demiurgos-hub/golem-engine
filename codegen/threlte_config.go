package codegen

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func validateThrelteIntegration(projectRoot string, cfg *schema.Config) error {
	threlte, ok := cfg.Integrations["threlte"]
	if !ok {
		return nil
	}
	js, ok := cfg.Integrations["js-client"]
	if !ok {
		return fmt.Errorf("threlte integration requires the js-client integration")
	}
	if strings.TrimSpace(threlte.Out) == "" || strings.TrimSpace(js.Out) == "" {
		return fmt.Errorf("threlte and js-client integrations require non-empty out paths")
	}
	if strings.TrimSpace(threlte.ProtocolImport) == "" || strings.TrimSpace(threlte.ProtocolImport) != threlte.ProtocolImport || strings.Contains(threlte.ProtocolImport, `\`) {
		return fmt.Errorf("threlte integration requires a non-empty protocol_import using forward slashes (for example '../protocol/')")
	}
	if !strings.HasSuffix(threlte.ProtocolImport, "/") {
		threlte.ProtocolImport += "/"
		cfg.Integrations["threlte"] = threlte
	}
	jsOut, err := filepath.Abs(filepath.Join(projectRoot, js.Out))
	if err != nil {
		return fmt.Errorf("js-client output: %w", err)
	}
	threlteOut, err := filepath.Abs(filepath.Join(projectRoot, threlte.Out))
	if err != nil {
		return fmt.Errorf("threlte output: %w", err)
	}
	rel, err := filepath.Rel(jsOut, threlteOut)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
		return fmt.Errorf("threlte output must be outside js-client output so its TypeScript is compiled by the application")
	}
	return nil
}
