package navbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/artifact"
	"github.com/demiurgos-hub/navmesh-go/bake"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func TestSourceArtifactAndFailureAtomicity(t *testing.T) {
	source := Source{Config: bake.DefaultConfig("empty", geom.Bounds{Min: geom.Vec3{X: -2, Y: -1, Z: -2}, Max: geom.Vec3{X: 2, Y: 3, Z: 2}})}
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	input, output := filepath.Join(dir, "source.json"), filepath.Join(dir, "world.nav")
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := BakeFile(context.Background(), input, output); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := artifact.Decode(before, artifact.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, []byte(`{"unexpected":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := BakeFile(context.Background(), input, output); err == nil {
		t.Fatal("invalid source accepted")
	}
	after, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("failed bake replaced previous artifact")
	}
	if _, err := Decode(bytes.NewReader(append(data, []byte(` {}`)...))); err == nil {
		t.Fatal("trailing input accepted")
	}
}

func TestScribeEmittedSourceBakesAndQueries(t *testing.T) {
	// This ordinary-Git fixture is actual GolemNavigationDocument C# output from
	// JsonIsDeterministicInvariantAndRecognizablyOwned, not hand-written JSON.
	file, err := os.Open("testdata/scribe-source.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	source, err := Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if source.Generator != "Golem Scribe navigation source v1" {
		t.Fatal("missing exporter provenance")
	}
	result, err := source.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := navigation.NewSnapshot(result.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	query, err := snapshot.NewQuery(navigation.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	projection := query.Project(context.Background(), geom.Vec3{X: -2, Y: .1, Z: -2}, navigation.ProjectionOptions{Horizontal: .3, Up: .3, Down: .3}, navigation.Filter{})
	if projection.Code != navigation.Complete {
		t.Fatalf("translated Scribe floor is unavailable: %+v", projection.Status)
	}
}
