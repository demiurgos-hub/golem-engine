package content

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math"
	"slices"
	"sort"
	"sync"

	"github.com/demiurgos-hub/golem-engine/golem/geometry"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

// PreparationVersion identifies portable source-to-contract conversion rules.
const PreparationVersion = "golem-physics-content-v4"

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
	sourceOnce  sync.Once
	sourceBytes []byte
	sourceHash  string
	fingerprint string
	diagnostics []Diagnostic
	characters  map[string]preparedCharacter
}

// Prepare captures portable source independently of any backend acceleration
// data. Unsupported authored settings remain visible in Diagnostics and Source.
func Prepare(source *Source, settings Settings) (*Prepared, error) {
	if err := checkPolicies(settings); err != nil {
		return nil, err
	}
	if err := source.Validate(settings.Limits); err != nil {
		return nil, err
	}
	return prepareValidated(cloneSource(source), settings)
}

// DecodePrepared decodes, validates and prepares one source in a single pass.
// It is equivalent to DecodeWithGeometry followed by Prepare, without a second
// validation or a defensive copy of geometry the caller never sees.
func DecodePrepared(r io.Reader, settings Settings, resolve geometry.Resolver) (*Prepared, string, error) {
	if err := checkPolicies(settings); err != nil {
		return nil, "", err
	}
	source, hash, err := DecodeWithGeometry(r, settings.Limits, resolve)
	if err != nil {
		return nil, "", err
	}
	source.Geometry = nil
	prepared, err := prepareValidated(*source, settings)
	return prepared, hash, err
}

func checkPolicies(settings Settings) error {
	if settings.ContactMarginPolicy != "backend-fixed" || settings.CCDPolicy != "swept" {
		return fmt.Errorf("%w: explicit contact/CCD policy required", p.ErrUnsupported)
	}
	if settings.CharacterPolicy != "" && settings.CharacterPolicy != GolemKinematicCharacters {
		return fmt.Errorf("%w: character conversion policy", p.ErrUnsupported)
	}
	return nil
}

// prepareValidated takes ownership of a validated, self-contained source.
// Canonical prepared sources are self-contained. The resolved geometry and
// instance matrices determine identity; sidecar location is transport detail.
func prepareValidated(source Source, settings Settings) (*Prepared, error) {
	prepared := &Prepared{source: source, settings: settings}
	sourceHash, size, err := hashSource(&prepared.source)
	if err != nil {
		return nil, err
	}
	if size > settings.Limits.Bytes {
		data, err := json.Marshal(&prepared.source)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > settings.Limits.Bytes {
			return nil, invalid("prepared source byte limit")
		}
		prepared.sourceBytes = data
		prepared.sourceOnce.Do(func() {})
	}
	prepared.sourceHash = sourceHash
	identity, err := json.Marshal(struct {
		Version, Source string
		Settings        Settings
	}{PreparationVersion, prepared.sourceHash, settings})
	if err != nil {
		return nil, fmt.Errorf("%w: conversion settings cannot be hashed: %v", p.ErrInvalid, err)
	}
	prepared.fingerprint = digest(identity)
	prepared.characters, prepared.diagnostics = prepareCharacters(prepared.source, settings)
	for _, body := range prepared.source.Bodies {
		if body.CollisionDetection == "continuousSpeculative" {
			prepared.diagnostics = append(prepared.diagnostics, Diagnostic{body.ID, "CollisionDetection", "speculative CCD is not implemented by the swept CCD policy"})
		}
	}
	for _, material := range prepared.source.Materials {
		if material.StaticFriction != material.DynamicFriction {
			prepared.diagnostics = append(prepared.diagnostics, Diagnostic{material.ID, "StaticFriction", "separate static/dynamic friction requires backend support"})
		}
	}
	for _, joint := range prepared.source.Joints {
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

// hashSource digests canonical JSON for everything but asset arrays, then the
// assets' exact float32 and index values. It returns an upper bound on the
// canonical JSON size so the byte limit only needs exact encoding near it.
func hashSource(source *Source) (string, int64, error) {
	h := sha256.New()
	counter := &countingWriter{w: h}
	header := *source
	header.Assets = nil
	if err := json.NewEncoder(counter).Encode(&header); err != nil {
		return "", 0, err
	}
	size := counter.n
	stream := assetStream{h: h, buf: make([]byte, 0, 8192)}
	stream.u32(uint32(len(source.Assets)))
	for _, asset := range source.Assets {
		stream.text(asset.ID)
		stream.text(asset.SourceID)
		stream.u32(uint32(len(asset.Vertices)))
		for _, v := range asset.Vertices {
			stream.u32(math.Float32bits(v.X))
			stream.u32(math.Float32bits(v.Y))
			stream.u32(math.Float32bits(v.Z))
		}
		stream.u32(uint32(len(asset.Indices)))
		for _, index := range asset.Indices {
			stream.u32(index)
		}
		// Object syntax, escaped identities, 15-character float32 values and
		// 10-digit indices bound each asset's JSON encoding from above.
		size += 64 + 6*int64(len(asset.ID)+len(asset.SourceID)) + 62*int64(len(asset.Vertices)) + 11*int64(len(asset.Indices))
		if h := asset.Heightfield; h != nil {
			stream.u32(1)
			stream.u32(h.Resolution)
			for _, v := range h.Size {
				stream.u32(math.Float32bits(v))
			}
			stream.u32(uint32(len(h.Samples)))
			for _, v := range h.Samples {
				stream.u32(math.Float32bits(v))
			}
			stream.u32(uint32(len(h.Surface)))
			stream.flush()
			_, _ = stream.h.Write(h.Surface)
			stream.u32(uint32(h.Area))
			size += 128 + 16*int64(len(h.Samples)) + 2*int64(len(h.Surface))
		} else {
			stream.u32(0)
		}
	}
	stream.flush()
	return hex.EncodeToString(h.Sum(nil)), size, nil
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(data []byte) (int, error) {
	n, err := c.w.Write(data)
	c.n += int64(n)
	return n, err
}

type assetStream struct {
	h   hash.Hash
	buf []byte
}

func (s *assetStream) u32(v uint32) {
	if len(s.buf)+4 > cap(s.buf) {
		s.flush()
	}
	s.buf = binary.LittleEndian.AppendUint32(s.buf, v)
}
func (s *assetStream) text(v string) {
	s.u32(uint32(len(v)))
	s.flush()
	_, _ = s.h.Write([]byte(v))
}
func (s *assetStream) flush() { _, _ = s.h.Write(s.buf); s.buf = s.buf[:0] }

// cloneSource deep-copies every slice and pointer a caller could later mutate.
func cloneSource(source *Source) Source {
	c := *source
	c.Geometry = nil
	c.LayerCollisionMasks = slices.Clone(source.LayerCollisionMasks)
	c.Bodies = slices.Clone(source.Bodies)
	c.Materials = slices.Clone(source.Materials)
	c.Colliders = slices.Clone(source.Colliders)
	for i := range c.Colliders {
		collider := &c.Colliders[i]
		collider.Metadata = slices.Clone(collider.Metadata)
		if collider.Character != nil {
			value := *collider.Character
			collider.Character = &value
		}
		if collider.GeometryTransform != nil {
			value := *collider.GeometryTransform
			collider.GeometryTransform = &value
		}
	}
	c.Assets = slices.Clone(source.Assets)
	for i := range c.Assets {
		c.Assets[i].Vertices = slices.Clone(c.Assets[i].Vertices)
		c.Assets[i].Indices = slices.Clone(c.Assets[i].Indices)
		if h := c.Assets[i].Heightfield; h != nil {
			value := *h
			value.Samples = slices.Clone(h.Samples)
			value.Surface = slices.Clone(h.Surface)
			c.Assets[i].Heightfield = &value
		}
	}
	c.Joints = slices.Clone(source.Joints)
	for i := range c.Joints {
		joint := &c.Joints[i]
		if joint.BreakForce != nil {
			value := *joint.BreakForce
			joint.BreakForce = &value
		}
		if joint.BreakTorque != nil {
			value := *joint.BreakTorque
			joint.BreakTorque = &value
		}
	}
	return c
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
	source := cloneSource(&p.source)
	return &source
}

// SourceBytes returns owned canonical portable source, never cooked backend bytes.
// The canonical encoding is produced on first use.
func (p *Prepared) SourceBytes() []byte {
	p.sourceOnce.Do(func() { p.sourceBytes, _ = json.Marshal(&p.source) })
	return append([]byte(nil), p.sourceBytes...)
}

// Diagnostics returns the unsupported controls that block construction.
func (p *Prepared) Diagnostics() []Diagnostic { return append([]Diagnostic(nil), p.diagnostics...) }
