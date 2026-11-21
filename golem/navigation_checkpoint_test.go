package golem

import (
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

// TestNavigationCheckpointCapturesAndRestoresPerSpace checks that each space's
// navigation checkpoint holds only that space's intents and restores inside
// that space's barrier, while another space has navigation too.
func TestNavigationCheckpointCapturesAndRestoresPerSpace(t *testing.T) {
	s := NewServer(ServerConfig{})
	for _, space := range []SpaceID{0, 3} {
		if err := s.Space(space).SetSurfaceNavigation(&navigationTestBackend{revision: 1}, NavigationConfig{}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := NavigationAgentConfig{Profile: "human", Speed: 2, Projection: surface.ProjectionOptions{Horizontal: .5, Up: .5, Down: .5}}
	for _, e := range []struct {
		space SpaceID
		id    int64
		goal  float64
	}{{0, 1, 4}, {3, 2, -6}, {3, 3, 8}} {
		if err := s.CreateEntityIn(e.space, &navigationTestEntity{id: e.id}); err != nil {
			t.Fatal(err)
		}
		if err := s.AttachNavigation(e.id, cfg); err != nil {
			t.Fatal(err)
		}
		if err := s.SetNavigationDestination(e.id, surface.Vec3{X: e.goal}); err != nil {
			t.Fatal(err)
		}
	}
	space0, err := s.CaptureNavigationCheckpoint("c", "sources", 1, []byte("world-0"))
	if err != nil {
		t.Fatal(err)
	}
	space3, err := s.Space(3).CaptureNavigationCheckpoint("c", "sources-3", 2, []byte("world-3"))
	if err != nil {
		t.Fatal(err)
	}
	if len(space0.Agents) != 1 || space0.Agents[0].EntityID != 1 || len(space3.Agents) != 2 || space3.Agents[0].EntityID != 2 || space3.Agents[1].EntityID != 3 {
		t.Fatalf("captured intents: space 0 %+v, space 3 %+v", space0.Agents, space3.Agents)
	}
	if _, err := s.Space(5).CaptureNavigationCheckpoint("c", "sources", 1, []byte("world")); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("capture in a space without navigation = %v", err)
	}
	for _, id := range []int64{1, 2, 3} {
		_ = s.StopNavigation(id)
	}
	if err := s.Space(3).RestoreNavigationIntents(space3, "sources-3"); err == nil {
		t.Fatal("space 3 intents restored outside its barrier")
	}
	if err := s.Space(3).BeginNavigationRestore(); err != nil {
		t.Fatal(err)
	}
	if err := s.Space(3).RestoreNavigationIntents(space0, "sources"); err == nil || !strings.Contains(err.Error(), "entity 1 is not attached") {
		t.Fatalf("space 0 intents restored into space 3: %v", err)
	}
	if err := s.Space(3).RestoreNavigationIntents(space3, "sources-3"); err != nil {
		t.Fatal(err)
	}
	if err := s.Space(3).CompleteNavigationRestore(nil); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[int64]float64{2: -6, 3: 8} {
		if state, _ := s.NavigationAgentState(id); !state.HasDestination || state.Destination.X != want {
			t.Fatalf("entity %d intent %+v", id, state)
		}
	}
	if state, _ := s.NavigationAgentState(1); state.HasDestination {
		t.Fatal("restoring space 3 changed space 0's intent")
	}
	if s.NavigationReady().Restoring {
		t.Fatal("space 3 barrier closed space 0")
	}
}
