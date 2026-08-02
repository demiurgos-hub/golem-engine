package codegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsGeneratedJSIntermediateTS(t *testing.T) {
	wantRemove := []string{
		"EntityManager.ts",
		"EventManager.ts",
		"WorldManager.ts",
		"entities_pb.ts",
		"events_pb.ts",
		"world_pb.ts",
		"client.ts",
		"PlayerSynced.ts",
		"BattleViewSynced.ts",
		"NpcSynced.ts",
	}
	for _, name := range wantRemove {
		if !isGeneratedJSIntermediateTS(name) {
			t.Fatalf("%s: want generated intermediate", name)
		}
	}

	wantKeep := []string{
		"helpers.ts",               // authored / unknown
		"custom_utils.ts",          // authored / unknown
		"PlayerSynced.d.ts",        // declaration output
		"entities_pb.d.ts",         // declaration output
		"client.js",                // not .ts
		"tsconfig.golem-bake.json", // handled separately
	}
	for _, name := range wantKeep {
		if isGeneratedJSIntermediateTS(name) {
			t.Fatalf("%s: must not be treated as generated intermediate", name)
		}
	}
}

func TestRemoveGeneratedJSIntermediates(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"EntityManager.ts":          "export {}",
		"EventManager.ts":           "export {}",
		"WorldManager.ts":           "export {}",
		"entities_pb.ts":            "export {}",
		"events_pb.ts":              "export {}",
		"world_pb.ts":               "export {}",
		"client.ts":                 "export {}",
		"PlayerSynced.ts":           "export {}",
		"helpers.ts":                "export const authored = 1;",
		"PlayerSynced.d.ts":         "export {}",
		"entities_pb.d.ts":          "export {}",
		"client.js":                 "export {}",
		"tsconfig.golem-bake.json":  "{}",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	if err := removeGeneratedJSIntermediates(dir); err != nil {
		t.Fatalf("removeGeneratedJSIntermediates: %v", err)
	}

	mustGone := []string{
		"EntityManager.ts", "EventManager.ts", "WorldManager.ts",
		"entities_pb.ts", "events_pb.ts", "world_pb.ts",
		"client.ts", "PlayerSynced.ts", "tsconfig.golem-bake.json",
	}
	for _, name := range mustGone {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("%s: want removed, err=%v", name, err)
		}
	}

	mustRemain := []string{"helpers.ts", "PlayerSynced.d.ts", "entities_pb.d.ts", "client.js"}
	for _, name := range mustRemain {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s: want kept: %v", name, err)
		}
	}
}
