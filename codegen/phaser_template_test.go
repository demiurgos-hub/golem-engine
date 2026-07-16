package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGeneratePhaserBridgeGuardsInitialSpriteSync(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/phaser/entity_bridge.ts.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.EntityData{
		Name:           "Player",
		ProtocolImport: "../",
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	if got := strings.Count(content, "if (this.sprite) {\n      this.syncToSprite();\n    }"); got != 2 {
		t.Fatalf("guarded sprite sync count = %d, want 2\n%s", got, content)
	}
}
