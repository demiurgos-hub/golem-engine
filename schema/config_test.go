package schema

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaultsSimulationDimensionsTo2(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "golem.yaml"), []byte("proto:\n  out: entities.proto\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Simulation.Dimensions, 2; got != want {
		t.Fatalf("Simulation.Dimensions = %d, want %d", got, want)
	}
}

func TestLoadConfigAccepts3DSimulationDimensions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "golem.yaml"), []byte("simulation:\n  dimensions: 3\nproto:\n  out: entities.proto\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Simulation.Dimensions, 3; got != want {
		t.Fatalf("Simulation.Dimensions = %d, want %d", got, want)
	}
}

func TestLoadConfigRejectsInvalidSimulationDimensions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "golem.yaml"), []byte("simulation:\n  dimensions: 4\nproto:\n  out: entities.proto\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("expected error for invalid simulation.dimensions")
	}
}
