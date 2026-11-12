package box3d

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
)

type unityReferenceHit struct {
	ColliderID    string
	Distance      float32
	Point, Normal p.Vec3
}

// Unity does not promise all-hit order. Match the complete identity multiset
// before comparing unchanged geometric tolerances; native ordering is separate.
func matchUnityReferenceHits(got []p.Hit, want []unityReferenceHit, lookup func(string) (p.ColliderHandle, bool)) ([]int, error) {
	if len(got) != len(want) {
		return nil, fmt.Errorf("hit count %d, want %d", len(got), len(want))
	}
	indices := make([]int, len(want))
	used := make([]bool, len(got))
	for i, reference := range want {
		handle, ok := lookup(reference.ColliderID)
		if !ok {
			return nil, fmt.Errorf("unknown reference collider %s", reference.ColliderID)
		}
		found := -1
		for j, hit := range got {
			if !used[j] && hit.Collider == handle && hit.Content == p.ContentID(reference.ColliderID) {
				found = j
				break
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("missing reference collider/multiplicity %s", reference.ColliderID)
		}
		used[found] = true
		indices[i] = found
	}
	return indices, nil
}

func unityReferenceGeometryError(got p.Hit, want unityReferenceHit, tolerance float32) error {
	if absFloat(got.Distance-want.Distance) > tolerance {
		return fmt.Errorf("distance %g want %g", got.Distance, want.Distance)
	}
	if got.Initial {
		if want.Distance != 0 || !got.Penetrating || got.Depth <= 0 {
			return fmt.Errorf("initial-overlap classification: %+v", got)
		}
		return nil
	}
	if point, normal := got.Point.Sub(want.Point).Length(), got.Normal.Sub(want.Normal).Length(); point > tolerance || normal > tolerance {
		return fmt.Errorf("geometry point/normal error %g/%g; got %+v want %+v", point, normal, got, want)
	}
	return nil
}

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
			Name, Type                   string
			From, Direction, WorldOffset p.Vec3
			Radius, Distance             float32
			Hits                         []unityReferenceHit
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
		WorldOffset                                     p.Vec3
		Hits                                            int
		MaxDistanceError, MaxPointError, MaxNormalError float32
		InitialOverlap                                  bool
		ComparedSurfaceGeometry                         bool
		Passed                                          bool
		Actual                                          []p.Hit
	}
	report := struct {
		UnityVersion, SourceHash, ReferenceHash, PreparedFingerprint, Backend, Fork string
		IdentityMatching                                                            string
		Bodies, Colliders, Assets                                                   int
		Tolerance                                                                   float32
		Observations                                                                []observation
		Passed                                                                      bool
		Config                                                                      p.Config
		ContentSettings                                                             content.Settings
		CookSettings                                                                CookSettings
	}{UnityVersion: reference.UnityVersion, SourceHash: sourceHash, ReferenceHash: cookDigest(referenceData), PreparedFingerprint: prepared.Fingerprint(), Backend: UpstreamCommit, Fork: RequiredFork,
		IdentityMatching: "complete collider identity multiset; native stable ordering is tested independently",
		Bodies:           len(source.Bodies), Colliders: len(source.Colliders), Assets: len(source.Assets), Tolerance: .0001, Passed: true,
		Config: w.config, ContentSettings: content.DefaultSettings(), CookSettings: DefaultCookSettings()}
	for _, referenceQuery := range reference.Queries {
		t.Run(referenceQuery.Name, func(t *testing.T) {
			for _, sourceBody := range source.Bodies {
				body, ok := instance.Body(sourceBody.ID)
				if !ok {
					t.Fatal("missing source body")
				}
				pose := p.Pose{Position: sourceBody.Position.Add(referenceQuery.WorldOffset), Rotation: sourceBody.Rotation}
				if err := w.SetPose(body, pose); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Synchronize(); err != nil {
				t.Fatal(err)
			}
			// The Unity capture includes trigger hits (the thin-wall trigger is
			// explicitly present in its sphere-cast observation).
			q := p.Query{Kind: p.Ray, Mode: p.All, Pose: p.Identity(), Translation: referenceQuery.Direction.Mul(referenceQuery.Distance), Filter: p.QueryFilter{Mask: ^uint64(0), Sensors: p.IncludeSensors}}
			q.Pose.Position = referenceQuery.From
			// Unity raycasts omit colliders containing the origin; Golem reports
			// them by default, so select the matching explicit policy for comparison.
			q.Initial = p.IgnoreInitial
			if referenceQuery.Type == "sphere" {
				q.Kind = p.Sweep
				q.Initial = p.ReportInitial
				q.Shape = p.ShapeDesc{Kind: p.Sphere, Radius: referenceQuery.Radius}
			} else if referenceQuery.Type != "ray" {
				t.Fatal("unrecognized reference query")
			}
			hits := make([]p.Hit, len(source.Colliders)+1)
			result, err := w.Query(q, hits)
			observation := observation{Name: referenceQuery.Name, WorldOffset: referenceQuery.WorldOffset, Hits: result.Count, Passed: true, Actual: append([]p.Hit(nil), hits[:result.Count]...)}
			defer func() {
				observation.Passed = !t.Failed()
				report.Passed = report.Passed && observation.Passed
				report.Observations = append(report.Observations, observation)
			}()
			if err != nil || result.Incomplete || result.Total != len(referenceQuery.Hits) {
				t.Fatalf("query count %d, want %d; incomplete %v: %v; hits %+v", result.Total, len(referenceQuery.Hits), result.Incomplete, err, hits[:result.Count])
			}
			indices, err := matchUnityReferenceHits(hits[:result.Count], referenceQuery.Hits, instance.Collider)
			if err != nil {
				t.Fatal(err)
			}
			for n, want := range referenceQuery.Hits {
				got := hits[indices[n]]
				if err := unityReferenceGeometryError(got, want, report.Tolerance); err != nil {
					t.Error(err)
				}
				distanceError := absFloat(got.Distance - want.Distance)
				observation.MaxDistanceError = max(observation.MaxDistanceError, distanceError)
				if got.Initial {
					// Unity uses Point=(0,0,0) and -sweepDirection as sentinels for
					// initial overlaps. Golem returns geometric contacts instead.
					observation.InitialOverlap = true
					continue
				}
				pointError, normalError := got.Point.Sub(want.Point).Length(), got.Normal.Sub(want.Normal).Length()
				observation.ComparedSurfaceGeometry = true
				observation.MaxPointError = max(observation.MaxPointError, pointError)
				observation.MaxNormalError = max(observation.MaxNormalError, normalError)
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
