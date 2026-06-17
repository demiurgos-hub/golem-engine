package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golem-engine/schema"
)

func TestGenerateGoProtoIncludesClientPacket(t *testing.T) {
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

	if err := generateGoProto(outDir, "golem-engine/golem", "generated", nil, commands, nil, nil, proto); err != nil {
		t.Fatalf("generateGoProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "entities_pb.go"))
	if err != nil {
		t.Fatalf("read entities_pb.go: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"type ClientPacket struct{ Frames [][]byte }",
		"func (m *ClientPacket) Marshal() []byte {",
		"func (m *ClientPacket) Unmarshal(data []byte) (retErr error) {",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("entities_pb.go missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoProtoIncludesEntityRevisions(t *testing.T) {
	outDir := t.TempDir()
	entities := []schema.EntityData{
		{
			Name: "Player",
			AllVars: []schema.VarInfo{
				{GoName: "Health", GoType: "int32", ProtoType: "int32", UserTag: 1, ProtoTag: 4},
			},
			TickVars: []schema.VarInfo{
				{GoName: "Health", GoType: "int32", ProtoType: "int32", UserTag: 1, ProtoTag: 4},
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

	if err := generateGoProto(outDir, "golem-engine/golem", "generated", entities, nil, nil, nil, proto); err != nil {
		t.Fatalf("generateGoProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "entities_pb.go"))
	if err != nil {
		t.Fatalf("read entities_pb.go: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"Revision uint64",
		"w.Tag(1000, 0).Uint64(m.Revision)",
		"case 1000:\n\t\t\tm.Revision = rd.Uint64()",
		"type EntityRemoved struct {",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("entities_pb.go missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoProtoUsesFloat32EntityPositions(t *testing.T) {
	outDir := t.TempDir()
	entities := []schema.EntityData{{Name: "Player"}}
	proto := schema.ProtoTemplateData{
		EntityUpdateFields: []schema.EntityUpdateField{
			{MessageType: "PlayerState", SnakeName: "player_state", Tag: 1},
			{MessageType: "PlayerDelta", SnakeName: "player_delta", Tag: 2},
		},
		EntityRemovedTag: 3,
	}

	if err := generateGoProto(outDir, "golem-engine/golem", "generated", entities, nil, nil, nil, proto); err != nil {
		t.Fatalf("generateGoProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "entities_pb.go"))
	if err != nil {
		t.Fatalf("read entities_pb.go: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"PosX     float32",
		"PosY     float32",
		"w.Tag(2, 5).Float32(m.PosX)",
		"w.Tag(3, 5).Float32(m.PosY)",
		"case 2:\n\t\t\tm.PosX = rd.Float32()",
		"v := rd.Float32()",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("entities_pb.go missing %q\n%s", want, content)
		}
	}
}

func TestGenerateGoProtoIncludes3DEntityPositions(t *testing.T) {
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

	if err := generateGoProto(outDir, "golem-engine/golem", "generated", entities, nil, nil, nil, proto); err != nil {
		t.Fatalf("generateGoProto: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(outDir, "entities_pb.go"))
	if err != nil {
		t.Fatalf("read entities_pb.go: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"PosZ     float32",
		"w.Tag(4, 5).Float32(m.PosZ)",
		"case 4:\n\t\t\tm.PosZ = rd.Float32()",
		"PosZ     *float32",
		"m.PosZ = &v",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("entities_pb.go missing 3D position %q\n%s", want, content)
		}
	}
}
