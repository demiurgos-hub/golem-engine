package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGenerateCSharpProtoIncludesClientPacket(t *testing.T) {
	outDir := t.TempDir()
	commands := []schema.CommandData{
		{Name: "Ping", LowerName: "ping", Target: "session"},
	}
	proto := schema.ProtoTemplateData{
		ClientMessageFields: []schema.ClientMessageField{
			{MessageType: "PingCommand", SnakeName: "ping", Tag: 1},
		},
	}

	if err := generateCSharpProto(outDir, "GolemEngine.Unity", "Game.Generated", nil, commands, nil, nil, proto); err != nil {
		t.Fatalf("generateCSharpProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "EntitiesPb.cs"))
	if err != nil {
		t.Fatalf("read EntitiesPb.cs: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"namespace Game.Generated",
		"public sealed class ClientMessage",
		"public static class ClientPacket",
		"public static byte[] Encode(IReadOnlyList<byte[]> frames)",
		"foreach (var frame in frames) w.Tag(1, 2).Bytes(frame);",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("EntitiesPb.cs missing %q\n%s", want, content)
		}
	}
}

func TestGenerateCSharpProtoIncludesEntityRevisions(t *testing.T) {
	outDir := t.TempDir()
	entities := []schema.EntityData{
		{
			Name: "Player",
			AllVars: []schema.VarInfo{
				{CSProtoField: "Health", ProtoType: "int32", UserTag: 1, ProtoTag: 4},
			},
			TickVars: []schema.VarInfo{
				{CSProtoField: "Health", ProtoType: "int32", UserTag: 1, ProtoTag: 4},
			},
		},
	}
	proto := schema.ProtoTemplateData{
		EntityUpdateFields: []schema.EntityUpdateField{
			{MessageType: "PlayerState", SnakeName: "player_state", Tag: 1},
			{MessageType: "PlayerDelta", SnakeName: "player_delta", Tag: 2},
		},
		EntityRemovedTag: 3,
	}

	if err := generateCSharpProto(outDir, "GolemEngine.Unity", "Game.Generated", entities, nil, nil, nil, proto); err != nil {
		t.Fatalf("generateCSharpProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "EntitiesPb.cs"))
	if err != nil {
		t.Fatalf("read EntitiesPb.cs: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"public ulong Revision { get; set; } = 0;",
		"if (m.Revision != 0) w.Tag(1000, 0).Uint64(m.Revision);",
		"case 1000: m.Revision = r.Uint64(); break;",
		"public sealed class EntityRemoved",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("EntitiesPb.cs missing %q\n%s", want, content)
		}
	}
}

func TestGenerateCSharpWorldProtoIncludesMapCustomTypes(t *testing.T) {
	outDir := t.TempDir()
	proto := schema.ProtoTemplateData{
		CustomTypes: []schema.CustomTypeData{
			{
				Name: "ItemType",
				Fields: []schema.CustomFieldInfo{
					{SnakeName: "id", ProtoType: "int32", ProtoTag: 1},
					{SnakeName: "name", ProtoType: "string", ProtoTag: 2},
				},
			},
		},
		WorldTypes: []schema.WorldTypeData{
			{
				Name:     "ItemList",
				DataName: "ItemListData",
				Fields: []schema.WorldFieldInfo{
					{
						CSProtoField:    "Items",
						ProtoTag:        1,
						IsMap:           true,
						MapKeyProtoType: "int32",
						MapKeyCSType:    "int",
						ElemProtoType:   "ItemType",
						ElemIsCustom:    true,
						ElemCSType:      "ItemType",
					},
				},
			},
		},
		WorldUpdateFields: []schema.WorldUpdateField{
			{MessageType: "ItemListData", SnakeName: "item_list_data", Tag: 1},
		},
	}

	if err := generateCSharpWorldProto(outDir, "GolemEngine.Unity", "Game.Generated", proto); err != nil {
		t.Fatalf("generateCSharpWorldProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "WorldPb.cs"))
	if err != nil {
		t.Fatalf("read WorldPb.cs: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"public Dictionary<int, ItemType> Items { get; set; } = new Dictionary<int, ItemType>();",
		"kv.Tag(2, 2).Bytes(ItemType.Encode(pair.Value));",
		"case 1: m.ItemListData = ItemListData.Decode(r.Bytes()); break;",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("WorldPb.cs missing %q\n%s", want, content)
		}
	}
}

func TestGenerateCSharpProtoIncludes3DEntityPositions(t *testing.T) {
	outDir := t.TempDir()
	entities := []schema.EntityData{{Name: "Player", Dimensions: 3, Is3D: true}}
	proto := schema.ProtoTemplateData{
		Dimensions: 3,
		Is3D:       true,
		EntityUpdateFields: []schema.EntityUpdateField{
			{MessageType: "PlayerState", SnakeName: "player_state", Tag: 1},
			{MessageType: "PlayerDelta", SnakeName: "player_delta", Tag: 2},
		},
		EntityRemovedTag: 3,
	}

	if err := generateCSharpProto(outDir, "GolemEngine.Unity", "Game.Generated", entities, nil, nil, nil, proto); err != nil {
		t.Fatalf("generateCSharpProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "EntitiesPb.cs"))
	if err != nil {
		t.Fatalf("read EntitiesPb.cs: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"public float PosZ { get; set; } = 0f;",
		"if (m.PosZ != 0f) w.Tag(4, 5).Float(m.PosZ);",
		"if (m.PosZ.HasValue) w.Tag(4, 5).Float(m.PosZ.Value);",
		"case 4: m.PosZ = r.Float(); break;",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("EntitiesPb.cs missing 3D position %q\n%s", want, content)
		}
	}
}
