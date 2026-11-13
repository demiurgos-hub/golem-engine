package content

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// PreparationVersion identifies portable source-to-contract conversion rules.
const PreparationVersion = "golem-physics-content-v2"

// Settings makes solver-policy differences from Unity explicit and hashable.
// Backend contact margins never enlarge source or query geometry. Sweep CCD is
// the Golem policy; speculative CCD requires a different implementation.
// Authored characters require an explicit CharacterPolicy; its controller base
// owns support, gravity and work budgets independently of navigation geometry.
type Settings struct {
	Limits              Limits
	ContactMarginPolicy string
	CCDPolicy           string
	CharacterPolicy     CharacterPolicy
	CharacterController p.CharacterControllerConfig
}

// DefaultSettings selects fixed backend contact margins and swept CCD, while
// refusing implicit authored-character conversion. The opt-in controller base
// has gravity disabled because Unity CharacterController does not supply it.
func DefaultSettings() Settings {
	controller := p.DefaultCharacterController(p.Kinematic)
	controller.Gravity = false
	return Settings{Limits: DefaultLimits(), ContactMarginPolicy: "backend-fixed", CCDPolicy: "swept", CharacterController: controller}
}

// Diagnostic identifies authored semantics that cannot currently be constructed.
// Such a capture can be inspected, but Load refuses it before changing a world.
type Diagnostic struct{ ID, Field, Reason string }

// Prepared privately owns a validated source capture and the exact policy hash.
// It can be shared read-only; returned metadata and source bytes are copies.
type Prepared struct {
	source      Source
	settings    Settings
	sourceBytes []byte
	sourceHash  string
	fingerprint string
	diagnostics []Diagnostic
	characters  map[string]preparedCharacter
}

// Prepare captures portable source independently of any backend acceleration
// data. Unsupported authored settings remain visible in Diagnostics and Source.
func Prepare(source *Source, settings Settings) (*Prepared, error) {
	if settings.ContactMarginPolicy != "backend-fixed" || settings.CCDPolicy != "swept" {
		return nil, fmt.Errorf("%w: explicit contact/CCD policy required", p.ErrUnsupported)
	}
	if settings.CharacterPolicy != "" && settings.CharacterPolicy != GolemKinematicCharacters {
		return nil, fmt.Errorf("%w: character conversion policy", p.ErrUnsupported)
	}
	if err := source.Validate(settings.Limits); err != nil {
		return nil, err
	}
	// Canonical prepared sources are self-contained. The resolved geometry and
	// instance matrices determine identity; sidecar location is transport detail.
	resolved := *source
	resolved.Geometry = nil
	data, err := json.Marshal(&resolved)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > settings.Limits.Bytes {
		return nil, invalid("prepared source byte limit")
	}
	var copy Source
	if err = json.Unmarshal(data, &copy); err != nil {
		return nil, err
	}
	prepared := &Prepared{source: copy, settings: settings, sourceBytes: data, sourceHash: digest(data)}
	identity, err := json.Marshal(struct {
		Version, Source string
		Settings        Settings
	}{PreparationVersion, prepared.sourceHash, settings})
	if err != nil {
		return nil, fmt.Errorf("%w: conversion settings cannot be hashed: %v", p.ErrInvalid, err)
	}
	prepared.fingerprint = digest(identity)
	prepared.characters, prepared.diagnostics = prepareCharacters(copy, settings)
	for _, body := range copy.Bodies {
		if body.CollisionDetection == "continuousSpeculative" {
			prepared.diagnostics = append(prepared.diagnostics, Diagnostic{body.ID, "CollisionDetection", "speculative CCD is not implemented by the swept CCD policy"})
		}
	}
	for _, material := range copy.Materials {
		if material.StaticFriction != material.DynamicFriction {
			prepared.diagnostics = append(prepared.diagnostics, Diagnostic{material.ID, "StaticFriction", "separate static/dynamic friction requires backend support"})
		}
	}
	for _, joint := range copy.Joints {
		if !joint.Active {
			continue
		}
		if _, err := jointDescription(joint); err != nil {
			prepared.diagnostics = append(prepared.diagnostics, Diagnostic{joint.ID, "Joint", err.Error()})
		}
	}
	sort.Slice(prepared.diagnostics, func(i, j int) bool {
		a, b := prepared.diagnostics[i], prepared.diagnostics[j]
		return a.ID < b.ID || a.ID == b.ID && a.Field < b.Field
	})
	return prepared, nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Fingerprint identifies canonical source, conversion version and all settings.
func (p *Prepared) Fingerprint() string { return p.fingerprint }

// SourceHash hashes canonical portable source; Decode separately hashes original bytes.
func (p *Prepared) SourceHash() string { return p.sourceHash }

// Source returns an independent copy, including unimplemented authored settings.
func (p *Prepared) Source() *Source {
	var source Source
	_ = json.Unmarshal(p.sourceBytes, &source)
	return &source
}

// SourceBytes returns owned canonical portable source, never cooked backend bytes.
func (p *Prepared) SourceBytes() []byte { return append([]byte(nil), p.sourceBytes...) }

// Diagnostics returns the unsupported controls that block construction.
func (p *Prepared) Diagnostics() []Diagnostic { return append([]Diagnostic(nil), p.diagnostics...) }
