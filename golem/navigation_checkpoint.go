package golem

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
)

// NavigationIntent persists only stable identity and destination, never runtime
// polygon handles, query scratch, corridors, or in-progress traversal actions.
type NavigationIntent struct {
	EntityID    int64             `json:"entity_id"`
	Profile     surface.ProfileID `json:"profile"`
	Destination surface.Vec3      `json:"destination"`
}

// NavigationCheckpoint binds desired world bytes and agent intents to one host
// checkpoint identity. The host captures entity bytes in the same tick and commits
// its checkpoint manifest last; this value alone is not an entity snapshot.
type NavigationCheckpoint struct {
	Version           uint32             `json:"version"`
	CheckpointID      string             `json:"checkpoint_id"`
	SourceFingerprint string             `json:"source_fingerprint"`
	DesiredRevision   uint64             `json:"desired_revision"`
	DesiredWorld      []byte             `json:"desired_world"`
	Agents            []NavigationIntent `json:"agents"`
}

// CaptureNavigationCheckpoint copies tick-owned intents and already captured
// immutable desired-world bytes. JSON encoding stores these opaque bytes as base64.
// The bytes should come from the geometry backend's
// desired-state checkpoint, including source objects, blockers and link states.
func (s *Server) CaptureNavigationCheckpoint(id, fingerprint string, revision uint64, desiredWorld []byte) (NavigationCheckpoint, error) {
	if s.navigation == nil {
		return NavigationCheckpoint{}, errors.New("golem: navigation is not configured")
	}
	out := NavigationCheckpoint{Version: 1, CheckpointID: id, SourceFingerprint: fingerprint, DesiredRevision: revision, DesiredWorld: append([]byte(nil), desiredWorld...)}
	for _, a := range s.navigation.agents {
		if a.state.HasDestination {
			out.Agents = append(out.Agents, NavigationIntent{EntityID: a.id, Profile: a.config.Profile, Destination: a.state.Destination})
		}
	}
	sort.Slice(out.Agents, func(i, j int) bool { return out.Agents[i].EntityID < out.Agents[j].EntityID })
	if err := validateNavigationCheckpoint(out); err != nil {
		return NavigationCheckpoint{}, err
	}
	return out, nil
}

// DecodeNavigationCheckpoint validates a bounded, versioned checkpoint before
// world or entity mutation. Unknown fields and trailing JSON are rejected.
func DecodeNavigationCheckpoint(data []byte) (NavigationCheckpoint, error) {
	if len(data) > 64<<20 {
		return NavigationCheckpoint{}, errors.New("golem: navigation checkpoint exceeds 64 MiB")
	}
	var out NavigationCheckpoint
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return out, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return out, errors.New("golem: trailing navigation checkpoint data")
	}
	if err := validateNavigationCheckpoint(out); err != nil {
		return NavigationCheckpoint{}, err
	}
	return out, nil
}

// EncodeNavigationCheckpoint validates the complete encoded envelope's size,
// including base64 desired-world data and agent intents, before serializing it.
func EncodeNavigationCheckpoint(checkpoint NavigationCheckpoint) ([]byte, error) {
	if err := validateNavigationCheckpoint(checkpoint); err != nil {
		return nil, err
	}
	return json.Marshal(checkpoint)
}

// RestoreNavigationIntents validates every required attachment before replacing
// any intent. Restore desired world state and entity identities first, while the
// readiness barrier is closed; open it only after all restoration succeeds.
func (s *Server) RestoreNavigationIntents(checkpoint NavigationCheckpoint, expectedFingerprint string) error {
	if err := validateNavigationCheckpoint(checkpoint); err != nil {
		return err
	}
	n := s.navigation
	if n == nil || !n.restoring {
		return errors.New("golem: restore navigation intents inside a restoration barrier")
	}
	if checkpoint.SourceFingerprint != expectedFingerprint {
		return errors.New("golem: navigation source fingerprint mismatch")
	}
	for _, intent := range checkpoint.Agents {
		a := n.agents[intent.EntityID]
		if a == nil {
			return fmt.Errorf("golem: required navigation entity %d is not attached", intent.EntityID)
		}
		if a.config.Profile != intent.Profile {
			return fmt.Errorf("golem: navigation profile mismatch for entity %d", intent.EntityID)
		}
	}
	for _, a := range n.agents {
		_ = s.StopNavigation(a.id)
	}
	for _, intent := range checkpoint.Agents {
		_ = s.SetNavigationDestination(intent.EntityID, intent.Destination)
	}
	return nil
}

func validateNavigationCheckpoint(c NavigationCheckpoint) error {
	if c.Version != 1 {
		return errors.New("golem: unsupported navigation checkpoint version")
	}
	if c.CheckpointID == "" || c.SourceFingerprint == "" || c.DesiredRevision == 0 {
		return errors.New("golem: navigation checkpoint identity is incomplete")
	}
	if len(c.CheckpointID) > 4096 || len(c.SourceFingerprint) > 4096 || len(c.Agents) > 100000 {
		return errors.New("golem: navigation checkpoint metadata exceeds limits")
	}
	if len(c.DesiredWorld) == 0 || len(c.DesiredWorld) > 48<<20 {
		return errors.New("golem: desired navigation world must contain at most 48 MiB")
	}
	seen := make(map[int64]bool, len(c.Agents))
	for _, a := range c.Agents {
		if a.EntityID == 0 || a.Profile == "" || len(a.Profile) > 256 || !a.Destination.Finite() || seen[a.EntityID] {
			return errors.New("golem: invalid or duplicate navigation intent")
		}
		seen[a.EntityID] = true
	}
	// Size the metadata with a null payload, then substitute the exact JSON
	// base64 string size without allocating an oversized payload encoding.
	payloadSize := base64.StdEncoding.EncodedLen(len(c.DesiredWorld)) + 2
	c.DesiredWorld = nil
	metadata, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if len(metadata)-4+payloadSize > 64<<20 {
		return errors.New("golem: navigation checkpoint exceeds 64 MiB encoded")
	}
	return nil
}
