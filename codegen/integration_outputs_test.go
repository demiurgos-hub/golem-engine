package codegen

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestIntegrationOutputInventories(t *testing.T) {
	for _, tc := range []struct {
		target, defaultImport, defaultPackage string
		base, world, events                   []string
	}{
		{"go-server", "github.com/demiurgos-hub/golem-engine/golem", "synced", []string{"test_actor_synced.go", "golem_helpers.go", "entities_pb.go"}, []string{"world_pb.go"}, []string{"events_pb.go"}},
		{"go-client", "github.com/demiurgos-hub/golem-engine/golem-go-client", "synced", []string{"test_actor_synced.go", "entity_manager.go", "client.go", "entities_pb.go"}, []string{"world_manager.go", "world_pb.go"}, []string{"event_manager.go", "events_pb.go"}},
		{"js-client", "golem-engine", "", []string{"TestActorSynced.ts", "EntityManager.ts", "client.ts", "entities_pb.ts"}, []string{"WorldManager.ts", "world_pb.ts"}, []string{"EventManager.ts", "events_pb.ts"}},
		{"csharp-client", "GolemEngine.Unity", "Golem.Generated", []string{"SyncedTestActor.cs", "EntityManager.cs", "Client.cs", "EntitiesPb.cs"}, []string{"WorldManager.cs", "WorldPb.cs"}, []string{"EventManager.cs", "EventsPb.cs"}},
		{"phaser", "golem-phaser", "", []string{"GolemPhaser.ts"}, nil, nil},
		{"threlte", "golem-threlte", "", []string{"GolemThrelte.ts"}, nil, nil},
		{"unity", "GolemEngine.Unity", "Golem.Generated", []string{"TestActorView.cs"}, nil, nil},
		{"ebiten", "github.com/demiurgos-hub/golem-engine/golem-ebiten", "synced", []string{"test_actor_view.go"}, nil, nil},
	} {
		t.Run(tc.target, func(t *testing.T) {
			spec := integrationSpecs[tc.target]
			if spec.defaultImport != tc.defaultImport || spec.defaultPackage != tc.defaultPackage {
				t.Fatal("changed defaults")
			}
			for flags := 0; flags < 4; flags++ {
				data := schema.SharedData{Entities: []schema.EntityData{{Name: "TestActor"}}}
				want := append([]string(nil), tc.base...)
				if flags&1 != 0 {
					data.WorldTypes = []schema.WorldTypeData{{}}
					want = append(want, tc.world...)
				}
				if flags&2 != 0 {
					data.Events = []schema.EventData{{}}
					want = append(want, tc.events...)
				}
				var got []string
				for _, output := range selectIntegrationOutputs(tc.target, data) {
					got = append(got, output.file)
				}
				sort.Strings(want)
				sort.Strings(got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("flags %d: got %v, want %v", flags, got, want)
				}
			}
			first, _ := GetIntegration(tc.target)
			if len(first.SharedTemplates) != 0 {
				first.SharedTemplates[0].File = "mutated"
				next, _ := GetIntegration(tc.target)
				if next.SharedTemplates[0].File == "mutated" {
					t.Fatal("public projection mutated registry")
				}
			}
		})
	}
}

func TestContainedRelativePath(t *testing.T) {
	for _, rel := range []string{".", "child", filepath.Join("child", "nested"), "..suffix"} {
		if !isContainedRelativePath(rel) {
			t.Errorf("rejected %q", rel)
		}
	}
	for _, rel := range []string{"..", filepath.Join("..", "sibling"), t.TempDir()} {
		if isContainedRelativePath(rel) {
			t.Errorf("accepted %q", rel)
		}
	}
}
