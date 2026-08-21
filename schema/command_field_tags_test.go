package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commandTag(value int) *int { return &value }

func TestBuildCommandDataExplicitFieldTagsPreserveWireNumbers(t *testing.T) {
	cf := CommandSchemaFile{
		Command: "FishingAction",
		Fields: map[string]CommandFieldDef{
			"action":      {Type: "string", Tag: commandTag(1)},
			"cue_seq":     {Type: "uint32", Tag: commandTag(3)},
			"control_seq": {Type: "uint32", Tag: commandTag(4)},
		},
	}
	if err := validateCommandFieldTags(cf); err != nil {
		t.Fatalf("validateCommandFieldTags: %v", err)
	}
	cd := BuildCommandData(cf, nil)
	if got, want := commandFieldNames(cd), []string{"action", "cue_seq", "control_seq"}; !stringsEqual(got, want) {
		t.Fatalf("field names = %v, want %v", got, want)
	}
	if got, want := commandProtoTags(cd), []int{1, 3, 4}; !intsEqual(got, want) {
		t.Fatalf("field tags = %v, want %v", got, want)
	}
}

func TestLoadCommandsParsesExplicitFieldTags(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fishing_action.yaml")
	content := "command: FishingAction\nfields:\n  action: { tag: 1, type: string }\n  cue_seq: { tag: 3, type: uint32 }\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	commands, err := LoadCommands(dir, nil)
	if err != nil {
		t.Fatalf("LoadCommands: %v", err)
	}
	if got, want := commandProtoTags(commands[0]), []int{1, 3}; !intsEqual(got, want) {
		t.Fatalf("field tags = %v, want %v", got, want)
	}
}

func TestValidateCommandFieldTagsRejectsMixedAndInvalidTags(t *testing.T) {
	tests := []struct {
		name     string
		target   string
		fields   map[string]CommandFieldDef
		contains string
	}{
		{
			name: "mixed",
			fields: map[string]CommandFieldDef{
				"tagged":   {Type: "string", Tag: commandTag(1)},
				"untagged": {Type: "string"},
			},
			contains: "tag is required",
		},
		{
			name: "duplicate",
			fields: map[string]CommandFieldDef{
				"one": {Type: "string", Tag: commandTag(2)},
				"two": {Type: "string", Tag: commandTag(2)},
			},
			contains: "overlaps",
		},
		{
			name: "illegal",
			fields: map[string]CommandFieldDef{
				"field": {Type: "string", Tag: commandTag(0)},
			},
			contains: "invalid envelope tag",
		},
		{
			name:   "entity reserved",
			target: "entity",
			fields: map[string]CommandFieldDef{
				"field": {Type: "string", Tag: commandTag(1)},
			},
			contains: "entity_id",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cf := CommandSchemaFile{Command: "Example", Target: tc.target, Fields: tc.fields}
			err := validateCommandFieldTags(cf)
			if err == nil || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("error = %v, want containing %q", err, tc.contains)
			}
		})
	}
}
