package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGeneratePhaserRegistryIncludesTypedCompleteDefinitions(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/phaser/golem_phaser.ts.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	hit := schema.EventData{
		Name:       "Hit",
		LowerName:  "hit",
		Target:     "entity",
		EntityType: "Player",
		FOIOnly:    true,
	}
	data := schema.SharedData{
		GolemImport:    "golem-phaser",
		ProtocolImport: "../protocol/",
		Entities: []schema.EntityData{
			{Name: "Player", Events: []schema.EventData{hit}},
			{Name: "Enemy"},
		},
		Events: []schema.EventData{hit},
	}

	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()

	for _, want := range []string{
		`import { SyncedPlayer } from "../protocol/PlayerSynced.js";`,
		`import { SyncedEnemy } from "../protocol/EnemySynced.js";`,
		"Player: EntityViewBuilder<",
		"Hit: HitEvent;",
		"Enemy: EntityViewBuilder<",
		"Player: EntityViewDefinition<SyncedPlayer>;",
		"Enemy: EntityViewDefinition<SyncedEnemy>;",
		"build: (views: EntityViewBuilders) => EntityViewDefinitions",
		"Player: createEntityViewBuilder<",
		"Enemy: createEntityViewBuilder<",
		"entity instanceof SyncedPlayer",
		"client.entities.getAll().values()",
		"client.entities.onSpawn",
		"client.events.onHit",
		`dispatch(entity, "Hit", event)`,
		"golem: GolemPlugin<Client>;",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated Phaser registry missing %q\n%s", want, content)
		}
	}
	if strings.Contains(content, "Bridge") {
		t.Fatalf("generated Phaser registry still references bridges\n%s", content)
	}
}

func TestPhaserIntegrationUsesOnlySharedRegistryTemplate(t *testing.T) {
	integ, err := GetIntegration("phaser")
	if err != nil {
		t.Fatalf("GetIntegration: %v", err)
	}
	if integ.Template != "" || integ.FileNamer != nil {
		t.Fatalf(
			"phaser integration should be shared-only, got template %q and file namer %v",
			integ.Template,
			integ.FileNamer != nil,
		)
	}
	if len(integ.SharedTemplates) != 1 {
		t.Fatalf("shared template count = %d, want 1", len(integ.SharedTemplates))
	}
	if got := integ.SharedTemplates[0].File; got != "GolemPhaser.ts" {
		t.Fatalf("shared template file = %q, want GolemPhaser.ts", got)
	}
}
