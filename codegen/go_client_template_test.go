package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGenerateGoClientManagerTemplateWiresCompactUpdatesAndCommands(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_client/manager.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}
	data := schema.SharedData{
		GolemImport: "github.com/demiurgos-hub/golem-engine/golem-go-client",
		GoPackage:   "client",
		Entities: []schema.EntityData{{
			Name:      "Player",
			LowerName: "player",
		}},
		Commands: []schema.CommandData{{
			Name:      "Move",
			LowerName: "move",
			Target:    "entity",
			Fields: []schema.CommandFieldInfo{{
				GoName:    "Dx",
				FieldName: "dx",
				GoType:    "float32",
			}},
		}},
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()
	for _, want := range []string{
		`golemclient "github.com/demiurgos-hub/golem-engine/golem-go-client"`,
		`"github.com/demiurgos-hub/golem-engine/golem-go-client/pb"`,
		"lastRevisions map[int64]uint64",
		"func (m *EntityManager) ApplyCompactUpdate(frame []byte)",
		"revision := r.Uint64()",
		"mask := r.Uint64()",
		"d.EntityId = id",
		"d.Revision = revision",
		"// Command builders create typed client messages for GameClient.Send and GameClient.SendOrdered.",
		"// BuildMoveCommand builds a Move command targeting an entity for GameClient.Send or GameClient.SendOrdered.",
		"func BuildMoveCommand(entityID int64, dx float32) *ClientMessage",
		"Payload: &ClientMessage_MoveCommand{",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated go client manager missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoClientCreateClientTemplateWiresGeneratedCodecs(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/go_client/create_client.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}
	data := schema.SharedData{
		GolemImport: "github.com/demiurgos-hub/golem-engine/golem-go-client",
		GoPackage:   "client",
		Commands: []schema.CommandData{{
			Name:   "Move",
			Target: "entity",
			Fields: []schema.CommandFieldInfo{{
				FieldName: "dx",
				GoType:    "float32",
			}},
		}},
		WorldTypes: []schema.WorldTypeData{{Name: "Zone", DataName: "ZoneData"}},
		Events:     []schema.EventData{{Name: "Toast", Target: "global"}},
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()
	for _, want := range []string{
		`import golemclient "github.com/demiurgos-hub/golem-engine/golem-go-client"`,
		"entities := NewEntityManager()",
		"world := NewWorldManager()",
		"events := NewEventManager(entities)",
		"msg := &EntityUpdate{}",
		"return command.(*ClientMessage).Marshal(), nil",
		"return (&ClientPacket{Frames: frames}).Marshal(), nil",
		"msg := &WorldUpdate{}",
		"func SendMove(client *golemclient.GameClient, entityID int64, dx float32) error",
		"return client.Send(BuildMoveCommand(entityID, dx))",
		"func SendMoveOrdered(client *golemclient.GameClient, entityID int64, dx float32) error",
		"return client.SendOrdered(BuildMoveCommand(entityID, dx))",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated go client factory missing %q\n%s", want, content)
		}
	}
	for _, legacy := range []string{
		"SendMoveReliableUnordered",
		"SendMoveReliableOrdered",
		"client.SendReliableUnordered",
		"client.SendReliableOrdered",
	} {
		if strings.Contains(content, legacy) {
			t.Fatalf("generated go client factory still contains legacy helper %q\n%s", legacy, content)
		}
	}
}

func TestGenerateEbitenBridgeTemplateSyncsDrawable(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/ebiten/entity_bridge.go.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}
	data := schema.EntityData{
		Name:           "Player",
		GoPackage:      "views",
		GolemImport:    "github.com/demiurgos-hub/golem-engine/golem-ebiten",
		ProtocolImport: "example.com/game/generated",
		Events:         []schema.EventData{{Name: "Hit", Target: "entity"}},
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()
	for _, want := range []string{
		`golemebiten "github.com/demiurgos-hub/golem-engine/golem-ebiten"`,
		`protocol "example.com/game/generated"`,
		"type PlayerView struct {",
		"*protocol.SyncedPlayer",
		"func NewPlayerView(entityID int64) *PlayerView",
		"func (v *PlayerView) OnHit(event *protocol.HitEvent) {}",
		"func (v *PlayerView) ApplyDelta(d *protocol.PlayerDelta)",
		"positioned.SetPosition(v.PosX(), v.PosY())",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated ebiten bridge missing %q\n%s", want, content)
		}
	}
}
