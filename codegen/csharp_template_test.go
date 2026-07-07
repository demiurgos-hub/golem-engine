package codegen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGenerateCSharpManagerTemplateUsesRevisionChecks(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/csharp/manager.cs.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GoPackage: "Game.Generated",
		Entities:  []schema.EntityData{{Name: "Player", LowerName: "player"}},
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()
	for _, want := range []string{
		"private readonly Dictionary<long, ulong> _lastRevisions",
		"public void ApplyCompactUpdate(byte[] frame)",
		"var mask = reader.Uint64();",
		"var delta = PlayerDelta.Decode(body);",
		"delta.EntityId = id;",
		"delta.Revision = revision;",
		"return _lastRevisions.TryGetValue(entityId, out var last) && revision < last;",
		"if (IsStale(id, revision)) return;",
		"var revision = update.EntityRemoved.Revision;",
		"var revision = state.Revision;",
		"var revision = delta.Revision;",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated EntityManager.cs missing %q\n%s", want, content)
		}
	}
}

func TestGenerateCSharpCreateClientTemplateWiresGeneratedCodecs(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/csharp/create_client.cs.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.SharedData{
		GoPackage: "Game.Generated",
		Commands: []schema.CommandData{{
			Name:   "Move",
			Target: "entity",
			Fields: []schema.CommandFieldInfo{
				{FieldName: "dx", CSType: "float"},
				{FieldName: "dy", CSType: "float"},
			},
		}},
		WorldTypes: []schema.WorldTypeData{{Name: "Zone"}},
		Events:     []schema.EventData{{Name: "Toast"}},
	}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()
	for _, want := range []string{
		"using System;",
		"var entities = new EntityManager();",
		"var world = new WorldManager();",
		"var events = new EventManager(entities);",
		"public static GameClient CreateClient()",
		"return CreateClient(() => new GolemWebSocketTransport());",
		"public static GameClient CreateClient(Func<IGolemTransport> transportFactory)",
		"bytes => EntityUpdate.Decode(bytes)",
		"command => ClientMessage.Encode((ClientMessage)command)",
		"frames => ClientPacket.Encode(frames)",
		"transportFactory",
		"bytes => WorldUpdate.Decode(bytes)",
		"public static void SendMoveReliableUnordered(GameClient client, long entityId, float dx, float dy)",
		"client.SendReliableUnordered(CommandBuilders.BuildMoveCommand(entityId, dx, dy));",
		"public static void SendMoveReliableOrdered(GameClient client, long entityId, float dx, float dy)",
		"client.SendReliableOrdered(CommandBuilders.BuildMoveCommand(entityId, dx, dy));",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated Client.cs missing %q\n%s", want, content)
		}
	}
}

func TestGenerateUnityBridgeTemplateBindsTransform(t *testing.T) {
	tmpl, err := loadEmbeddedTemplate("templates/unity/entity_bridge.cs.tmpl")
	if err != nil {
		t.Fatalf("loadEmbeddedTemplate: %v", err)
	}

	data := schema.EntityData{Name: "Player", GoPackage: "Game.Generated"}
	var out bytes.Buffer
	if err := tmpl.Execute(&out, data); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	content := out.String()
	for _, want := range []string{
		"public class PlayerView : GolemEntityView<SyncedPlayer>",
		"[SerializeField] private GolemTransformBinding transformBinding;",
		"transformBinding?.Bind(entity);",
		"protected virtual void OnSpawned(SyncedPlayer entity) {}",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated PlayerView.cs missing %q\n%s", want, content)
		}
	}
}
