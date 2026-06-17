package schema

import (
	"testing"
)

// fieldProtoTags extracts ProtoTag values from a CommandData in field slice order.
func commandProtoTags(cd CommandData) []int {
	tags := make([]int, len(cd.Fields))
	for i, f := range cd.Fields {
		tags[i] = f.ProtoTag
	}
	return tags
}

// commandFieldNames extracts SnakeName values from a CommandData in field slice order.
func commandFieldNames(cd CommandData) []string {
	names := make([]string, len(cd.Fields))
	for i, f := range cd.Fields {
		names[i] = f.SnakeName
	}
	return names
}

// eventProtoTags extracts ProtoTag values from an EventData in field slice order.
func eventProtoTags(ed EventData) []int {
	tags := make([]int, len(ed.Fields))
	for i, f := range ed.Fields {
		tags[i] = f.ProtoTag
	}
	return tags
}

// eventFieldNames extracts SnakeName values from an EventData in field slice order.
func eventFieldNames(ed EventData) []string {
	names := make([]string, len(ed.Fields))
	for i, f := range ed.Fields {
		names[i] = f.SnakeName
	}
	return names
}

func intsEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestBuildCommandData_sessionProtoTags verifies that a session-targeted command's
// fields are assigned proto tags 1, 2, 3... in alphabetical field-name order,
// regardless of the order they appear in the YAML map.
func TestBuildCommandData_sessionProtoTags(t *testing.T) {
	cf := CommandSchemaFile{
		Command: "ReadyUp",
		Target:  "session",
		// Map iteration is non-deterministic; use names that would sort z→a to expose ordering bugs.
		Fields: map[string]CommandFieldDef{
			"team":    {Type: "int32"},
			"message": {Type: "string"},
			"flag":    {Type: "bool"},
		},
	}
	cd := BuildCommandData(cf, nil)

	// Alphabetical order: flag, message, team → proto tags 1, 2, 3.
	wantNames := []string{"flag", "message", "team"}
	wantTags := []int{1, 2, 3}

	if !stringsEqual(commandFieldNames(cd), wantNames) {
		t.Errorf("field names = %v, want %v", commandFieldNames(cd), wantNames)
	}
	if !intsEqual(commandProtoTags(cd), wantTags) {
		t.Errorf("proto tags = %v, want %v", commandProtoTags(cd), wantTags)
	}
}

// TestBuildCommandData_entityProtoTags verifies that entity-targeted command fields
// start at proto tag 2 (tag offset = 1 reserves field 1 for entity_id).
func TestBuildCommandData_entityProtoTags(t *testing.T) {
	cf := CommandSchemaFile{
		Command:    "Move",
		Target:     "entity",
		EntityType: "Player",
		Fields: map[string]CommandFieldDef{
			"dy": {Type: "float"},
			"dx": {Type: "float"},
		},
	}
	cd := BuildCommandData(cf, nil)

	// Alphabetical order: dx, dy → proto tags 2, 3 (offset 1 for entity_id at field 1).
	wantNames := []string{"dx", "dy"}
	wantTags := []int{2, 3}

	if !stringsEqual(commandFieldNames(cd), wantNames) {
		t.Errorf("field names = %v, want %v", commandFieldNames(cd), wantNames)
	}
	if !intsEqual(commandProtoTags(cd), wantTags) {
		t.Errorf("proto tags = %v, want %v", commandProtoTags(cd), wantTags)
	}
}

// TestBuildEventData_globalProtoTags verifies that global event fields are assigned
// proto tags 1, 2, 3... in alphabetical field-name order.
func TestBuildEventData_globalProtoTags(t *testing.T) {
	ef := EventSchemaFile{
		Event:  "ChatMessage",
		Target: "global",
		Fields: map[string]EventFieldDef{
			"text":        {Type: "string"},
			"sender_name": {Type: "string"},
		},
	}
	ed := BuildEventData(ef, nil)

	// Alphabetical order: sender_name, text → proto tags 1, 2.
	wantNames := []string{"sender_name", "text"}
	wantTags := []int{1, 2}

	if !stringsEqual(eventFieldNames(ed), wantNames) {
		t.Errorf("field names = %v, want %v", eventFieldNames(ed), wantNames)
	}
	if !intsEqual(eventProtoTags(ed), wantTags) {
		t.Errorf("proto tags = %v, want %v", eventProtoTags(ed), wantTags)
	}
}

// TestBuildEventData_entityProtoTags verifies that entity-targeted event fields
// start at proto tag 2 (tag offset = 1 reserves field 1 for entity_id).
func TestBuildEventData_entityProtoTags(t *testing.T) {
	ef := EventSchemaFile{
		Event:      "BuffApplied",
		Target:     "entity",
		EntityType: "Player",
		Fields: map[string]EventFieldDef{
			"duration": {Type: "int32"},
			"buff_id":  {Type: "int32"},
		},
	}
	ed := BuildEventData(ef, nil)

	// Alphabetical order: buff_id, duration → proto tags 2, 3 (offset 1 for entity_id at field 1).
	wantNames := []string{"buff_id", "duration"}
	wantTags := []int{2, 3}

	if !stringsEqual(eventFieldNames(ed), wantNames) {
		t.Errorf("field names = %v, want %v", eventFieldNames(ed), wantNames)
	}
	if !intsEqual(eventProtoTags(ed), wantTags) {
		t.Errorf("proto tags = %v, want %v", eventProtoTags(ed), wantTags)
	}
}
