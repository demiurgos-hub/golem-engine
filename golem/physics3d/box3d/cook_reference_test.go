package box3d

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
)

// This test consumes actual Unity evidence, never a hand-recreated Go scene.
// Qualification must set the input directory and retain the resulting report.
func TestUnityReferenceScene(t *testing.T) {
	dir := os.Getenv("GOLEM_PHYSICS_UNITY_REFERENCE_DIR")
	if dir == "" {
		t.Skip("Unity reference gate not executed: GOLEM_PHYSICS_UNITY_REFERENCE_DIR is required")
	}
	data, err := os.ReadFile(filepath.Join(dir, "unity-physics.source.json"))
	if err != nil {
		t.Fatal(err)
	}
	source, sourceHash, err := content.Decode(bytes.NewReader(data), content.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := content.Prepare(source, content.DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	w := cookWorld(t)
	instance, err := prepared.Load(w, func(string) (int64, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := instance.Remove(); err != nil {
			t.Error(err)
		}
	}()
	referenceData, err := os.ReadFile(filepath.Join(dir, "unity-physics.reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Version, UnityVersion string
		Queries               []struct {
			Name, Type       string
			From, Direction  p.Vec3
			Radius, Distance float32
			Hits             []struct {
				ColliderID    string
				Distance      float32
				Point, Normal p.Vec3
			}
		}
	}
	if err = json.Unmarshal(referenceData, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Version != "unity-physics-reference-v1" || len(reference.Queries) == 0 {
		t.Fatal("invalid Unity reference")
	}
	type observation struct {
		Name                                            string
		Hits                                            int
		MaxDistanceError, MaxPointError, MaxNormalError float32
		InitialOverlap                                  bool
		ComparedSurfaceGeometry                         bool
		Passed                                          bool
	}
	report := struct {
		UnityVersion, SourceHash, ReferenceHash, PreparedFingerprint, Backend, Fork string
		Bodies, Colliders, Assets                                                   int
		Tolerance                                                                   float32
		Observations                                                                []observation
		Passed                                                                      bool
	}{UnityVersion: reference.UnityVersion, SourceHash: sourceHash, ReferenceHash: cookDigest(referenceData), PreparedFingerprint: prepared.Fingerprint(), Backend: UpstreamCommit, Fork: RequiredFork,
		Bodies: len(source.Bodies), Colliders: len(source.Colliders), Assets: len(source.Assets), Tolerance: .0001, Passed: true}
	for _, referenceQuery := range reference.Queries {
		t.Run(referenceQuery.Name, func(t *testing.T) {
			// The Unity capture includes trigger hits (the thin-wall trigger is
			// explicitly present in its sphere-cast observation).
			q := p.Query{Kind: p.Ray, Mode: p.All, Pose: p.Identity(), Translation: referenceQuery.Direction.Mul(referenceQuery.Distance), Filter: p.QueryFilter{Mask: ^uint64(0), Sensors: p.IncludeSensors}}
			q.Pose.Position = referenceQuery.From
			if referenceQuery.Type == "sphere" {
				q.Kind = p.Sweep
				q.Shape = p.ShapeDesc{Kind: p.Sphere, Radius: referenceQuery.Radius}
			} else if referenceQuery.Type != "ray" {
				t.Fatal("unrecognized reference query")
			}
			hits := make([]p.Hit, len(source.Colliders)+1)
			result, err := w.Query(q, hits)
			observation := observation{Name: referenceQuery.Name, Hits: result.Count, Passed: true}
			defer func() {
				observation.Passed = !t.Failed()
				report.Passed = report.Passed && observation.Passed
				report.Observations = append(report.Observations, observation)
			}()
			if err != nil || result.Incomplete || result.Total != len(referenceQuery.Hits) {
				t.Fatalf("query count %d, want %d; incomplete %v: %v; hits %+v", result.Total, len(referenceQuery.Hits), result.Incomplete, err, hits[:result.Count])
			}
			for n, want := range referenceQuery.Hits {
				got := hits[n]
				exact, ok := instance.Collider(want.ColliderID)
				if !ok || got.Collider != exact || got.Content != p.ContentID(want.ColliderID) {
					t.Errorf("collider identity: %+v want %s", got, want.ColliderID)
				}
				distanceError := absFloat(got.Distance - want.Distance)
				observation.MaxDistanceError = max(observation.MaxDistanceError, distanceError)
				if distanceError > report.Tolerance {
					t.Errorf("distance %g want %g", got.Distance, want.Distance)
				}
				if got.Initial {
					// Unity uses Point=(0,0,0) and -sweepDirection as sentinels for
					// initial overlaps. Golem returns geometric contacts instead.
					observation.InitialOverlap = true
					if want.Distance != 0 || !got.Penetrating || got.Depth <= 0 {
						t.Errorf("initial-overlap classification: %+v", got)
					}
					continue
				}
				pointError, normalError := got.Point.Sub(want.Point).Length(), got.Normal.Sub(want.Normal).Length()
				observation.ComparedSurfaceGeometry = true
				observation.MaxPointError = max(observation.MaxPointError, pointError)
				observation.MaxNormalError = max(observation.MaxNormalError, normalError)
				if pointError > report.Tolerance || normalError > report.Tolerance {
					t.Errorf("geometry point/normal error %g/%g; got %+v want %+v", pointError, normalError, got, want)
				}
			}
		})
	}
	if path := os.Getenv("GOLEM_PHYSICS_REFERENCE_REPORT"); path != "" {
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func absFloat(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
