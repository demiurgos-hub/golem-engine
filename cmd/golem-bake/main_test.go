package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProject(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"golem.yaml":          "entity_schema: entities\nproto:\n  package: fixture\n  out: entities.proto\nintegrations:\n  go-server:\n    out: generated\n    package: generated\n",
		"entities/actor.yaml": "entity: Actor\nvars:\n  hp: {tag: 1, type: int32, sync: tick}\n",
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func runCLI(t *testing.T, dir string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, dir, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestUsageErrorsExitTwo(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"--bogus"},
		{"--check", "--remove-integration", "phaser"},
		{"--remove-integration", "phaser", "--check"},
		{"--check", "extra"},
		{"--remove-integration"},
	} {
		if code, _, stderr := runCLI(t, dir, args...); code != exitUsage || !strings.Contains(stderr, "usage:") {
			t.Errorf("%q: code %d, stderr %q", args, code, stderr)
		}
	}
	if code, stdout, _ := runCLI(t, dir, "--help"); code != exitOK || !strings.Contains(stdout, "CRLF") {
		t.Errorf("--help: code %d, stdout %q", code, stdout)
	}
}

func TestCheckExitCodes(t *testing.T) {
	dir := t.TempDir()
	if code, _, stderr := runCLI(t, dir, "--check"); code != exitUsage || !strings.Contains(stderr, "golem.yaml") {
		t.Fatalf("missing golem.yaml: code %d, stderr %q", code, stderr)
	}
	writeProject(t, dir)
	if code, stdout, _ := runCLI(t, dir, "--check"); code != exitFailure || !strings.Contains(stdout, "missing     generated/actor_synced.go") || !strings.Contains(stdout, "Run golem-bake") {
		t.Fatalf("before bake: code %d, stdout %q", code, stdout)
	}
	if code, _, stderr := runCLI(t, dir); code != exitOK {
		t.Fatalf("bake: code %d, stderr %q", code, stderr)
	}
	code, stdout, _ := runCLI(t, dir, "--check")
	if code != exitOK || strings.Count(stdout, "\n") != 1 || !strings.Contains(stdout, "up to date") {
		t.Fatalf("after bake: code %d, stdout %q", code, stdout)
	}
	if err := os.WriteFile(filepath.Join(dir, "generated", "actor_synced.go"), []byte("package generated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, stdout, _ := runCLI(t, dir, "--check"); code != exitFailure || !strings.Contains(stdout, "stale       generated/actor_synced.go") {
		t.Fatalf("stale: code %d, stdout %q", code, stdout)
	}
}

func TestBakeFailureKeepsExitOne(t *testing.T) {
	if code, _, stderr := runCLI(t, t.TempDir()); code != exitFailure || !strings.Contains(stderr, "golem.yaml") {
		t.Fatalf("code %d, stderr %q", code, stderr)
	}
}
