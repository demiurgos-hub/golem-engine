package navbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	navigation "github.com/demiurgos-hub/navmesh-go"
	"github.com/demiurgos-hub/navmesh-go/artifact"
	"github.com/demiurgos-hub/navmesh-go/bake"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

func TestActualScribeBinary32ClearancePreservesGeometryAndArtifactPolicy(t *testing.T) {
	// Actual GolemNavigationDocument output is byte-checked by its C# fixture test.
	source, err := DecodeFile("testdata/scribe-clearance-source.json")
	if err != nil {
		t.Fatal(err)
	}
	if source.Config.Settings.ClearancePrecision != artifact.ClearanceBinary32 || source.Config.Settings.CellHeight != float64(float32(.1)) {
		t.Fatal("Scribe precision provenance or exact authored cell height was lost")
	}
	if source.Scene.Assets[0].Vertices[1].Y != math.Nextafter(1, 2) {
		t.Fatal("clearance policy rounded authored geometry")
	}
	clearance, err := source.Config.Settings.QuantizeClearance(source.Config.Profile)
	if err != nil || clearance.Climb != 5 {
		t.Fatalf("Unity .5f/.1f clearance=%+v error=%v", clearance, err)
	}
	nativeHash, err := source.Config.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	legacy := source.Config
	legacy.Settings.ClearancePrecision = artifact.ClearanceBinary64
	legacyClearance, err := legacy.Settings.QuantizeClearance(legacy.Profile)
	if err != nil || legacyClearance.Climb != 4 {
		t.Fatalf("default binary64 contract changed: %+v %v", legacyClearance, err)
	}
	legacyHash, err := legacy.Fingerprint()
	if err != nil || nativeHash == legacyHash {
		t.Fatal("cache identity omitted clearance precision")
	}
	baked, err := source.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := artifact.Encode(baked.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := artifact.Decode(encoded, artifact.DefaultLimits())
	if err != nil || decoded.Settings != source.Config.Settings || decoded.SettingsFingerprint != nativeHash {
		t.Fatalf("artifact lost validated clearance policy: %v", err)
	}
	if _, err = navigation.NewSnapshot(decoded); err != nil {
		t.Fatal(err)
	}
	source.Config.Settings.ClearancePrecision = "unknown"
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Decode(bytes.NewReader(data)); err == nil {
		t.Fatal("unknown clearance mode accepted")
	}
}

func TestDecodePreservesGeometryAdmission(t *testing.T) {
	for _, mode := range []string{"valid", "degenerate", "invalid index", "missing asset", "transform overflow", "volume overflow", "link overflow"} {
		t.Run(mode, func(t *testing.T) {
			for _, strict := range []bool{false, true} {
				source := Source{Config: bake.DefaultConfig("admission", geom.Bounds{Min: geom.Vec3{X: -4, Y: -1, Z: -4}, Max: geom.Vec3{X: 4, Y: 3, Z: 4}}), Scene: geom.Scene{Assets: []geom.Asset{{ID: "floor", Vertices: []geom.Vec3{{}, {Z: 2}, {X: 2}}, Triangles: []geom.Triangle{{Indices: [3]uint32{0, 1, 2}, Area: 1}}}}, Instances: []geom.Instance{{ID: "floor", AssetID: "floor", Transform: geom.Identity()}}}}
				source.Config.Strict = strict
				switch mode {
				case "degenerate":
					source.Scene.Assets[0].Triangles[0].Indices = [3]uint32{}
				case "invalid index":
					source.Scene.Assets[0].Triangles[0].Indices[1] = 42
				case "missing asset":
					source.Scene.Instances[0].AssetID = "missing"
				case "transform overflow":
					source.Scene.Assets[0].Vertices[0].X = 1e308
					source.Scene.Instances[0].Transform[3] = 1e308
				case "volume overflow":
					source.Scene.Instances = nil
					source.Config.Settings.Origin.X = -1e308
					source.Scene.Volumes = []geom.Volume{{ID: "paint", Shape: "box", Bounds: geom.Bounds{Min: geom.Vec3{X: 1e308}, Max: geom.Vec3{X: 1e308, Y: 2, Z: 2}}, Area: 1}}
				case "link overflow":
					source.Scene.Instances = nil
					source.Config.Settings.Origin.X = -1e308
					source.Scene.Links = []geom.Link{{ID: "door", Start: geom.Vec3{X: 1e308}, End: geom.Vec3{}, Radius: .2, Flags: 1}}
				}
				data, err := json.Marshal(source)
				if err != nil {
					t.Fatal(err)
				}
				_, resolvedErr := source.Scene.Resolve(source.Config.Settings.Origin, strict)
				_, decodedErr := Decode(bytes.NewReader(data))
				if (decodedErr == nil) != (resolvedErr == nil) {
					t.Fatalf("strict=%v Decode=%v Resolve=%v", strict, decodedErr, resolvedErr)
				}
			}
		})
	}
}

func TestDecodeDoesNotAllocateEveryPlacedTriangle(t *testing.T) {
	asset := geom.Asset{ID: "shared", Vertices: []geom.Vec3{{}, {Z: 2}, {X: 2}}, Triangles: make([]geom.Triangle, 1000)}
	for i := range asset.Triangles {
		asset.Triangles[i] = geom.Triangle{Indices: [3]uint32{0, 1, 2}, Area: 1}
	}
	source := Source{Config: bake.DefaultConfig("shared", geom.Bounds{Min: geom.Vec3{X: -4, Y: -1, Z: -4}, Max: geom.Vec3{X: 4, Y: 3, Z: 4}}), Scene: geom.Scene{Assets: []geom.Asset{asset}}}
	for i := 0; i < 100; i++ {
		source.Scene.Instances = append(source.Scene.Instances, geom.Instance{ID: fmt.Sprintf("placement-%03d", i), AssetID: asset.ID, Transform: geom.Identity()})
	}
	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	decoded, err := Decode(bytes.NewReader(data))
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Scene.Assets) != 1 || len(decoded.Scene.Instances) != 100 {
		t.Fatal("decoder lost shared source or placements")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 3<<20 {
		t.Fatalf("decoding shared source allocated %d bytes for discarded placement geometry", allocated)
	}
}

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
