package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGenerateJSWorldProtoIncludesCustomTypeHelpers(t *testing.T) {
	outDir := t.TempDir()
	proto := schema.ProtoTemplateData{
		CustomTypes: []schema.CustomTypeData{
			{
				Name: "ItemType",
				Fields: []schema.CustomFieldInfo{
					{
						FieldName: "id",
						ProtoType: "int32",
						ProtoTag:  1,
					},
					{
						FieldName: "name",
						ProtoType: "string",
						ProtoTag:  2,
					},
				},
			},
		},
		WorldTypes: []schema.WorldTypeData{
			{
				Name:      "ItemList",
				DataName:  "ItemListData",
				SnakeName: "item_list_data",
				Fields: []schema.WorldFieldInfo{
					{
						SnakeName:       "items",
						FieldName:       "items",
						TSProtoField:    "items",
						ProtoTag:        1,
						IsMap:           true,
						MapKeyProtoType: "int32",
						MapKeyTSType:    "number",
						ElemProtoType:   "ItemType",
						ElemIsCustom:    true,
						ElemTSType:      "ItemType",
					},
				},
			},
		},
		WorldUpdateFields: []schema.WorldUpdateField{
			{
				MessageType: "ItemListData",
				SnakeName:   "item_list_data",
				Tag:         1,
			},
		},
	}

	if err := generateJSWorldProto(outDir, "golem-engine", proto); err != nil {
		t.Fatalf("generateJSWorldProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "world_pb.ts"))
	if err != nil {
		t.Fatalf("read world_pb.ts: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"export interface ItemType {",
		"function encodeItemType(m: ItemType): Uint8Array {",
		"function decodeItemType(b: Uint8Array): ItemType {",
		"items: Record<number, ItemType> = {};",
		"_kv.tag(2, 2).bytes(encodeItemType(_v as any));",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("world_pb.ts missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoEventProtoUsesRealFormatVerbInErrors(t *testing.T) {
	outDir := t.TempDir()
	proto := schema.ProtoTemplateData{
		Events: []schema.EventData{
			{
				Name:      "ChatMessage",
				LowerName: "chatMessage",
				Target:    "global",
				Fields: []schema.EventFieldInfo{
					{
						SnakeName: "text",
						GoName:    "Text",
						FieldName: "text",
						GoType:    "string",
						ProtoType: "string",
						ProtoTag:  1,
					},
				},
			},
		},
		ServerEventFields: []schema.ServerEventField{
			{
				MessageType: "ChatMessageEvent",
				SnakeName:   "chat_message",
				Tag:         1,
			},
		},
	}

	if err := generateGoEventProto(outDir, "github.com/demiurgos-hub/golem-engine/golem", "synced", proto); err != nil {
		t.Fatalf("generateGoEventProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "events_pb.go"))
	if err != nil {
		t.Fatalf("read events_pb.go: %v", err)
	}
	content := string(data)

	if strings.Contains(content, "%%%%v") {
		t.Fatalf("generated events_pb.go still contains escaped percent verb:\n%s", content)
	}
	for _, want := range []string{
		`fmt.Errorf("pb: decode ChatMessageEvent: %v", r)`,
		`fmt.Errorf("pb: decode ServerEvent: %v", r)`,
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated events_pb.go missing %q\n%s", want, content)
		}
	}
}

func TestGenerateJSProtoIncludesClientPacket(t *testing.T) {
	outDir := t.TempDir()
	commands := []schema.CommandData{
		{
			Name:      "Ping",
			LowerName: "ping",
			Target:    "session",
		},
	}
	proto := schema.ProtoTemplateData{
		ClientMessageFields: []schema.ClientMessageField{
			{
				MessageType: "PingCommand",
				SnakeName:   "ping",
				Tag:         1,
			},
		},
	}

	if err := generateJSProto(outDir, "golem-engine", nil, commands, nil, nil, proto); err != nil {
		t.Fatalf("generateJSProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "entities_pb.ts"))
	if err != nil {
		t.Fatalf("read entities_pb.ts: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"export class ClientMessage {",
		"export class ClientPacket {",
		"static encode(frames: Uint8Array[]): Uint8Array {",
		"for (const frame of frames) w.tag(1, 2).bytes(frame);",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("entities_pb.ts missing %q\n%s", want, content)
		}
	}
}

func TestGenerateJSProtoIncludesEntityRevisions(t *testing.T) {
	outDir := t.TempDir()
	entities := []schema.EntityData{
		{
			Name: "Player",
			AllVars: []schema.VarInfo{
				{TSProtoField: "health", ProtoType: "int32", UserTag: 1, ProtoTag: 4},
			},
			TickVars: []schema.VarInfo{
				{TSProtoField: "health", ProtoType: "int32", UserTag: 1, ProtoTag: 4},
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

	if err := generateJSProto(outDir, "golem-engine", entities, nil, nil, nil, proto); err != nil {
		t.Fatalf("generateJSProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "entities_pb.ts"))
	if err != nil {
		t.Fatalf("read entities_pb.ts: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"revision: number = 0;",
		"if (m.revision !== 0) w.tag(1000, 0).uint64(m.revision);",
		"case 1000: m.revision = r.uint64(); break;",
		"export class EntityRemoved {",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("entities_pb.ts missing %q\n%s", want, content)
		}
	}
}

func TestGenerateJSProtoUsesFloatEntityPositions(t *testing.T) {
	outDir := t.TempDir()
	entities := []schema.EntityData{{Name: "Player"}}
	proto := schema.ProtoTemplateData{
		EntityUpdateFields: []schema.EntityUpdateField{
			{MessageType: "PlayerState", SnakeName: "player_state", Tag: 1},
			{MessageType: "PlayerDelta", SnakeName: "player_delta", Tag: 2},
		},
		EntityRemovedTag: 3,
	}

	if err := generateJSProto(outDir, "golem-engine", entities, nil, nil, nil, proto); err != nil {
		t.Fatalf("generateJSProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "entities_pb.ts"))
	if err != nil {
		t.Fatalf("read entities_pb.ts: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"if (m.posX !== 0) w.tag(2, 5).float(m.posX);",
		"if (m.posY !== 0) w.tag(3, 5).float(m.posY);",
		"if (m.posX !== undefined) w.tag(2, 5).float(m.posX);",
		"case 2: m.posX = r.float(); break;",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("entities_pb.ts missing %q\n%s", want, content)
		}
	}
}

func TestGenerateJSProtoIncludes3DEntityPositions(t *testing.T) {
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

	if err := generateJSProto(outDir, "golem-engine", entities, nil, nil, nil, proto); err != nil {
		t.Fatalf("generateJSProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "entities_pb.ts"))
	if err != nil {
		t.Fatalf("read entities_pb.ts: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"posZ: number = 0;",
		"if (m.posZ !== 0) w.tag(4, 5).float(m.posZ);",
		"if (m.posZ !== undefined) w.tag(4, 5).float(m.posZ);",
		"case 4: m.posZ = r.float(); break;",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("entities_pb.ts missing 3D position %q\n%s", want, content)
		}
	}
}
