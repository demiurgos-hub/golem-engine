package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDebugSampleMatchesUnityFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "debug.json")
	if err := run(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../../../../golem-unity/Tests/Fixtures/physics-debug.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n")), bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))) {
		t.Fatal("actual Go sample differs from the canonical Unity-reader fixture")
	}
	if err := run(path); err == nil {
		t.Fatal("sample overwrote existing capture")
	}
}
