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

// TestUnityResolvedCatalog consumes an actual bounded Unity capture, including
// inactive geometry and host state recipes. It is not a dynamics comparison.
func TestUnityResolvedCatalog(t *testing.T) {
	dir := os.Getenv("GOLEM_PHYSICS_CATALOG_DIR")
	if dir == "" {
		t.Skip("resolved catalog evidence requires GOLEM_PHYSICS_CATALOG_DIR")
	}
	output := os.Getenv("GOLEM_PHYSICS_CATALOG_REPORT_DIR")
	if output == "" {
		t.Fatal("set independent GOLEM_PHYSICS_CATALOG_REPORT_DIR")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "catalog-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var input struct {
		Version string
		Results []struct {
			Key, Error string
			Captures   []struct {
				State, Error                                 string
				Bodies, Colliders, ActiveColliders, Queries  int
				StableColliderIdentities, StableEnabledFlags bool
			}
		}
	}
	if err := json.Unmarshal(data, &input); err != nil || input.Version != "unity-resolved-catalog-v1" || len(input.Results) == 0 || len(input.Results) > 16 {
		t.Fatalf("invalid bounded catalog: %v", err)
	}
	type stateResult struct {
		Key, State, Error, SourceHash                 string
		Bodies, Colliders, ActiveColliders            int
		Loaded, IdentityActivationPassed, QueryPassed bool
	}
	report := struct {
		Version, InputHash, Fork string
		Results                  []stateResult
		Qualified                bool
	}{Version: "golem-resolved-catalog-v1", InputHash: cookDigest(data), Fork: RequiredFork}
	for _, root := range input.Results {
		if root.Error != "" {
			report.Results = append(report.Results, stateResult{Key: root.Key, Error: root.Error})
			t.Errorf("Unity root %s: %s", root.Key, root.Error)
			continue
		}
		for _, state := range root.Captures {
			result := stateResult{Key: root.Key, State: state.State}
			t.Run(root.Key+"/"+state.State, func(t *testing.T) {
				if state.Error != "" {
					result.Error = state.Error
					t.Error(state.Error)
					return
				}
				folder := filepath.Join(dir, root.Key, state.State)
				data, err := os.ReadFile(filepath.Join(folder, "unity-physics.source.json"))
				if err != nil {
					result.Error = err.Error()
					t.Error(err)
					return
				}
				source, hash, err := content.Decode(bytes.NewReader(data), content.DefaultLimits())
				result.SourceHash = hash
				if err != nil {
					result.Error = err.Error()
					t.Error(err)
					return
				}
				w := cookWorld(t)
				instance, err := content.Load(w, source, func(string) (int64, error) { return 0, nil })
				if err != nil {
					result.Error = err.Error()
					t.Error(err)
					return
				}
				result.Loaded = true
				defer func() {
					if err := instance.Remove(); err != nil {
						t.Error(err)
					}
				}()
				result.Bodies, result.Colliders = len(w.bodies), len(w.colliders)
				if result.Bodies != state.Bodies || result.Colliders != state.Colliders || !state.StableColliderIdentities || !state.StableEnabledFlags {
					t.Error("captured counts/identity assertions differ from constructed world")
				}
				for _, body := range source.Bodies {
					h, ok := instance.Body(body.ID)
					if !ok {
						t.Errorf("missing body %s", body.ID)
						continue
					}
					got, err := w.Body(h)
					if err != nil || got.ID != p.ContentID(body.ID) || got.Enabled != (body.Enabled && body.Active) || got.Pose.Position != body.Position {
						t.Errorf("body identity/activation/position %s: %+v %v", body.ID, got, err)
					}
				}
				for _, leaf := range source.Colliders {
					h, ok := instance.Collider(leaf.ID)
					if !ok {
						t.Errorf("missing collider %s", leaf.ID)
						continue
					}
					got := w.colliders[h].desc
					body, bodyOK := instance.Body(leaf.BodyID)
					if !bodyOK || got.Body != body || got.ID != p.ContentID(leaf.ID) || got.Enabled != (leaf.Enabled && leaf.Active) || got.Roles != (p.Roles{Solid: leaf.Solid, Sensor: leaf.Sensor, Query: leaf.Query}) || got.Filter.Category != uint64(1)<<leaf.Layer || got.Filter.Mask != uint64(source.LayerCollisionMasks[leaf.Layer]) {
						t.Errorf("collider membership/activation/roles/filter %s", leaf.ID)
					}
					if got.Enabled {
						result.ActiveColliders++
					}
				}
				if result.ActiveColliders != state.ActiveColliders {
					t.Errorf("active leaves %d want %d", result.ActiveColliders, state.ActiveColliders)
				}
				result.IdentityActivationPassed = !t.Failed()
				if state.Queries > 0 {
					t.Setenv("GOLEM_PHYSICS_UNITY_REFERENCE_DIR", folder)
					t.Setenv("GOLEM_PHYSICS_REFERENCE_REPORT", filepath.Join(output, root.Key+"-"+state.State+"-queries.json"))
					result.QueryPassed = t.Run("native-query-reference", TestUnityReferenceScene)
				} else {
					result.QueryPassed = true
				}
				if t.Failed() && result.Error == "" {
					result.Error = "identity/activation or strict Unity query comparison failed; see query report and test log"
				}
			})
			report.Results = append(report.Results, result)
		}
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(output, "catalog-go.json")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal(fmt.Errorf("preserve prior report: %s", path))
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
