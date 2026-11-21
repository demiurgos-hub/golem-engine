package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildProtoDataExplicitEnvelopeTagsPreserveLegacyAssignments(t *testing.T) {
	data := BuildProtoData(
		&Config{},
		[]EntityData{
			{Name: "Npc"},
			{Name: "Player"},
			{Name: "Interactable", UpdateTag: 8},
		},
		[]CommandData{
			{Name: "Move"},
			{Name: "Interact"},
			{Name: "InteractionClose", UpdateTag: 22},
		},
		nil,
		nil,
		[]EventData{
			{Name: "DialogMessage"},
			{Name: "InteractionUpdate", UpdateTag: 13},
		},
	)

	assertEnvelopeFields(t, data.EntityUpdateFields, []EntityUpdateField{
		{MessageType: "NpcState", SnakeName: "npc_state", Tag: 1},
		{MessageType: "NpcDelta", SnakeName: "npc_delta", Tag: 2},
		{MessageType: "PlayerState", SnakeName: "player_state", Tag: 3},
		{MessageType: "PlayerDelta", SnakeName: "player_delta", Tag: 4},
		{MessageType: "InteractableState", SnakeName: "interactable_state", Tag: 8},
		{MessageType: "InteractableDelta", SnakeName: "interactable_delta", Tag: 9},
	})
	if data.EntityRemovedTag != 5 {
		t.Fatalf("EntityRemovedTag = %d, want 5", data.EntityRemovedTag)
	}
	if got, want := data.ClientMessageFields[0].Tag, 1; got != want {
		t.Fatalf("Move tag = %d, want %d", got, want)
	}
	if got, want := data.ClientMessageFields[1].Tag, 2; got != want {
		t.Fatalf("Interact tag = %d, want %d", got, want)
	}
	if got, want := data.ClientMessageFields[2].Tag, 22; got != want {
		t.Fatalf("InteractionClose tag = %d, want %d", got, want)
	}
	if got, want := data.ServerEventFields[0].Tag, 1; got != want {
		t.Fatalf("DialogMessage tag = %d, want %d", got, want)
	}
	if got, want := data.ServerEventFields[1].Tag, 13; got != want {
		t.Fatalf("InteractionUpdate tag = %d, want %d", got, want)
	}
}

func TestValidateEnvelopeTagsRejectsIllegalAndOverlappingTags(t *testing.T) {
	tests := []struct {
		name     string
		entities []EntityData
		commands []CommandData
		events   []EventData
		contains string
	}{
		{
			name:     "entity overlaps removed",
			entities: []EntityData{{Name: "Player"}, {Name: "Object", UpdateTag: 3}},
			contains: "EntityRemoved",
		},
		{
			name:     "entity delta overlaps another state",
			entities: []EntityData{{Name: "One", UpdateTag: 8}, {Name: "Two", UpdateTag: 9}},
			contains: "overlaps",
		},
		{
			name:     "entity state overflow",
			entities: []EntityData{{Name: "Object", UpdateTag: maxProtoFieldNumber}},
			contains: "no valid delta tag",
		},
		{
			name:     "entity delta reserved",
			entities: []EntityData{{Name: "Object", UpdateTag: reservedProtoFieldStart - 1}},
			contains: "reserved protobuf tag",
		},
		{
			name:     "command overlaps implicit",
			commands: []CommandData{{Name: "Move"}, {Name: "Interact", UpdateTag: 1}},
			contains: "overlaps",
		},
		{
			name:     "event duplicate explicit",
			events:   []EventData{{Name: "One", UpdateTag: 13}, {Name: "Two", UpdateTag: 13}},
			contains: "overlaps",
		},
		{
			name:     "event outside protobuf range",
			events:   []EventData{{Name: "One", UpdateTag: maxProtoFieldNumber + 1}},
			contains: "invalid envelope tag",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateEnvelopeTags(test.entities, test.commands, test.events)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("ValidateEnvelopeTags() error = %v, want containing %q", err, test.contains)
			}
		})
	}
}

func TestLoadCommandsRejectsExplicitNonPositiveEnvelopeTag(t *testing.T) {
	directory := t.TempDir()
	for _, tag := range []string{"0", "-1"} {
		path := filepath.Join(directory, "command.yaml")
		contents := "command: Test\ntag: " + tag + "\ntarget: session\nfields: {}\n"
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("write command schema: %v", err)
		}
		if _, err := LoadCommands(directory, nil); err == nil || !strings.Contains(err.Error(), "at least 1") {
			t.Fatalf("LoadCommands(tag=%s) error = %v, want minimum-tag error", tag, err)
		}
	}
}

func assertEnvelopeFields(t *testing.T, got, want []EntityUpdateField) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("field count = %d, want %d: %#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("field %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}

func TestResolveProtoDataRejectsClashingMessageNames(t *testing.T) {
	tests := []struct {
		name     string
		entities []EntityData
		commands []CommandData
		worlds   []WorldTypeData
		types    []CustomTypeData
		events   []EventData
		want     []string // substrings of the error; nil means success
	}{
		{
			name:     "normal schema",
			entities: []EntityData{{Name: "Player"}},
			commands: []CommandData{{Name: "Move"}},
			worlds:   []WorldTypeData{{Name: "Map", DataName: "MapData"}},
			types:    []CustomTypeData{{Name: "Vec"}},
			events:   []EventData{{Name: "Hit"}},
		},
		{
			name:  "custom type SpaceChanged",
			types: []CustomTypeData{{Name: "SpaceChanged"}},
			want:  []string{`"SpaceChanged"`, "custom type SpaceChanged", "built-in SpaceChanged"},
		},
		{
			name:  "custom type ServerMessage",
			types: []CustomTypeData{{Name: "ServerMessage"}},
			want:  []string{`"ServerMessage"`, "custom type ServerMessage", "built-in ServerMessage"},
		},
		{
			name:   "world data clashes with custom type",
			types:  []CustomTypeData{{Name: "MapData"}},
			worlds: []WorldTypeData{{Name: "Map", DataName: "MapData"}},
			want:   []string{`"MapData"`, "world type Map", "custom type MapData"},
		},
		{
			name:     "entity state clashes with custom type",
			entities: []EntityData{{Name: "Player"}},
			types:    []CustomTypeData{{Name: "PlayerState"}},
			want:     []string{`"PlayerState"`, "entity Player", "custom type PlayerState"},
		},
		{
			name:     "event clashes with command",
			commands: []CommandData{{Name: "Hit", Target: "entity"}},
			events:   []EventData{{Name: "Hit"}},
			types:    []CustomTypeData{{Name: "HitEvent"}},
			want:     []string{`"HitEvent"`, "event Hit", "custom type HitEvent"},
		},
		{
			name:     "built-in name reserved without its envelope",
			entities: []EntityData{{Name: "Player"}},
			types:    []CustomTypeData{{Name: "ClientPacket"}},
			want:     []string{`"ClientPacket"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveProtoData(&Config{}, tt.entities, tt.commands, tt.worlds, tt.types, tt.events)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("ResolveProtoData: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("ResolveProtoData accepted clashing message names")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}
