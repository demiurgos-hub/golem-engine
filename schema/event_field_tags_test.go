package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func eventTag(value int) *int { return &value }

func TestBuildEventDataExplicitFieldTagsPreserveWireNumbers(t *testing.T) {
	ef := EventSchemaFile{
		Event:  "BattleEnd",
		Target: "session",
		Fields: map[string]EventFieldDef{
			"blackout":     {Type: "bool", Tag: eventTag(7)},
			"battle_id":    {Type: "string", Tag: eventTag(1)},
			"coins_gained": {Type: "int32", Tag: eventTag(2)},
		},
	}
	if err := validateEventFieldTags(ef); err != nil {
		t.Fatalf("validateEventFieldTags: %v", err)
	}
	ed := BuildEventData(ef, nil)
	if got, want := eventFieldNames(ed), []string{"battle_id", "coins_gained", "blackout"}; !stringsEqual(got, want) {
		t.Fatalf("field names = %v, want %v", got, want)
	}
	if got, want := eventProtoTags(ed), []int{1, 2, 7}; !intsEqual(got, want) {
		t.Fatalf("field tags = %v, want %v", got, want)
	}
}

func TestLoadEventsParsesExplicitFieldTags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "battle_end.yaml")
	content := "event: BattleEnd\ntarget: session\nfields:\n  battle_id: { tag: 1, type: string }\n  blackout: { tag: 7, type: bool }\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	events, err := LoadEvents(dir, nil)
	if err != nil {
		t.Fatalf("LoadEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if got, want := eventProtoTags(events[0]), []int{1, 7}; !intsEqual(got, want) {
		t.Fatalf("field tags = %v, want %v", got, want)
	}
}

func TestValidateEventFieldTagsRejectsMixedAndInvalidTags(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		fields   map[string]EventFieldDef
		contains string
	}{
		{
			name: "mixed",
			fields: map[string]EventFieldDef{
				"a": {Type: "string", Tag: eventTag(1)},
				"b": {Type: "string"},
			},
			contains: "tag is required when any field sets tag",
		},
		{
			name: "duplicate",
			fields: map[string]EventFieldDef{
				"a": {Type: "string", Tag: eventTag(2)},
				"b": {Type: "string", Tag: eventTag(2)},
			},
			contains: "overlaps",
		},
		{
			name:   "entity_id_reserved",
			target: "entity",
			fields: map[string]EventFieldDef{
				"a": {Type: "string", Tag: eventTag(1)},
			},
			contains: "entity_id",
		},
		{
			name: "protobuf_reserved_range",
			fields: map[string]EventFieldDef{
				"a": {Type: "string", Tag: eventTag(19000)},
			},
			contains: "reserved protobuf tag",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ef := EventSchemaFile{Event: "Example", Target: tc.target, Fields: tc.fields}
			err := validateEventFieldTags(ef)
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error = %v, want containing %q", err, tc.contains)
			}
		})
	}
}
