package footprint

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Supported footprints.golem.yaml major version.
const FormatVersion = 1

// Shape kinds stored in Footprint.Shapes.
const (
	ShapeCircle = "circle"
	ShapeAABB   = "aabb"
	ShapeSphere = "sphere"
)

// Set is a loaded footprints.golem.yaml document.
// Footprints must not be mutated after Load/Parse.
type Set struct {
	Version    int
	Dimensions int
	byGUID     map[string]*Footprint
	byAlias    map[string]*Footprint
}

// Footprint is one prefab's collision geometry, keyed by Unity asset GUID.
// Dimensions is 2 or 3 and must match the placer (Placer2D / Placer3D).
type Footprint struct {
	GUID       string
	Dimensions int
	Name       string // diagnostic; need not be unique
	AssetPath  string // diagnostic; need not be unique
	Alias      string // optional unique lookup key; empty when unset
	Shapes     []Shape
}

// Shape is one root-local collision primitive.
// For AABB, W/H/(D) are full extents (not half-extents).
type Shape struct {
	Type                      string
	R, W, H, D                float64
	OffsetX, OffsetY, OffsetZ float64
	Trigger                   bool
	Layer                     string
}

// LookupGUID returns the footprint for a Unity asset GUID.
// GUID matching is case-insensitive; the stored key is lowercase.
func (s *Set) LookupGUID(guid string) (*Footprint, bool) {
	if s == nil {
		return nil, false
	}
	fp, ok := s.byGUID[normalizeGUID(guid)]
	return fp, ok
}

// LookupAlias returns the footprint for an optional unique alias.
func (s *Set) LookupAlias(alias string) (*Footprint, bool) {
	if s == nil || alias == "" {
		return nil, false
	}
	fp, ok := s.byAlias[alias]
	return fp, ok
}

type fileDoc struct {
	Version    int                     `yaml:"version"`
	Dimensions int                     `yaml:"dimensions"`
	Footprints map[string]footprintDoc `yaml:"footprints"`
}

type footprintDoc struct {
	Name      string     `yaml:"name"`
	AssetPath string     `yaml:"asset_path"`
	Alias     string     `yaml:"alias"`
	Shapes    []shapeDoc `yaml:"shapes"`
}

type shapeDoc struct {
	Type    string             `yaml:"type"`
	R       float64            `yaml:"r"`
	W       float64            `yaml:"w"`
	H       float64            `yaml:"h"`
	D       float64            `yaml:"d"`
	Offset  map[string]float64 `yaml:"offset"`
	Trigger bool               `yaml:"trigger"`
	Layer   string             `yaml:"layer"`
}

func buildSet(doc fileDoc) (*Set, error) {
	if doc.Version == 0 {
		return nil, fmt.Errorf("footprint: version is required (supported: %d)", FormatVersion)
	}
	if doc.Version != FormatVersion {
		return nil, fmt.Errorf("footprint: unsupported version %d (supported: %d)", doc.Version, FormatVersion)
	}
	if doc.Dimensions == 0 {
		return nil, fmt.Errorf("footprint: dimensions is required (want 2 or 3)")
	}
	if doc.Dimensions != 2 && doc.Dimensions != 3 {
		return nil, fmt.Errorf("footprint: dimensions must be 2 or 3, got %d", doc.Dimensions)
	}
	if doc.Footprints == nil {
		return nil, fmt.Errorf("footprint: footprints map is required")
	}

	set := &Set{
		Version:    doc.Version,
		Dimensions: doc.Dimensions,
		byGUID:     make(map[string]*Footprint, len(doc.Footprints)),
		byAlias:    make(map[string]*Footprint),
	}

	// Sort GUID keys so validation errors are deterministic across runs.
	keys := make([]string, 0, len(doc.Footprints))
	for rawGUID := range doc.Footprints {
		keys = append(keys, rawGUID)
	}
	sort.Strings(keys)

	for _, rawGUID := range keys {
		fdoc := doc.Footprints[rawGUID]
		guid, err := validateGUID(rawGUID)
		if err != nil {
			return nil, err
		}
		if _, exists := set.byGUID[guid]; exists {
			return nil, fmt.Errorf("footprint: duplicate GUID %q", guid)
		}

		alias := strings.TrimSpace(fdoc.Alias)
		if fdoc.Alias != "" && alias == "" {
			return nil, fmt.Errorf("footprint: GUID %q: alias must not be whitespace-only", guid)
		}
		if alias != "" {
			if _, exists := set.byAlias[alias]; exists {
				return nil, fmt.Errorf("footprint: duplicate alias %q", alias)
			}
		}

		shapes, err := parseShapes(guid, fdoc.Shapes, doc.Dimensions)
		if err != nil {
			return nil, err
		}

		fp := &Footprint{
			GUID:       guid,
			Dimensions: doc.Dimensions,
			Name:       fdoc.Name,
			AssetPath:  fdoc.AssetPath,
			Alias:      alias,
			Shapes:     shapes,
		}
		set.byGUID[guid] = fp
		if alias != "" {
			set.byAlias[alias] = fp
		}
	}
	return set, nil
}

func parseShapes(guid string, docs []shapeDoc, dimensions int) ([]Shape, error) {
	if docs == nil {
		return nil, fmt.Errorf("footprint: GUID %q: shapes list is required", guid)
	}
	out := make([]Shape, 0, len(docs))
	for i, sd := range docs {
		sh, err := parseShape(guid, i, sd, dimensions)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, nil
}

func parseShape(guid string, index int, sd shapeDoc, dimensions int) (Shape, error) {
	prefix := fmt.Sprintf("footprint: GUID %q shape[%d]", guid, index)
	if strings.TrimSpace(sd.Type) == "" {
		return Shape{}, fmt.Errorf("%s: type is required", prefix)
	}
	typ := strings.ToLower(strings.TrimSpace(sd.Type))
	layer := strings.TrimSpace(sd.Layer)
	if layer == "" {
		return Shape{}, fmt.Errorf("%s: layer is required", prefix)
	}
	if sd.Offset == nil {
		return Shape{}, fmt.Errorf("%s: offset is required", prefix)
	}

	ox, oy, oz, err := parseOffset(sd.Offset, dimensions)
	if err != nil {
		return Shape{}, fmt.Errorf("%s: %w", prefix, err)
	}

	sh := Shape{
		Type:    typ,
		OffsetX: ox,
		OffsetY: oy,
		OffsetZ: oz,
		Trigger: sd.Trigger,
		Layer:   layer,
	}

	switch dimensions {
	case 2:
		switch typ {
		case ShapeCircle:
			if err := requirePositive(prefix, "r", sd.R); err != nil {
				return Shape{}, err
			}
			if sd.W != 0 || sd.H != 0 || sd.D != 0 {
				return Shape{}, fmt.Errorf("%s: circle must not set w/h/d", prefix)
			}
			sh.R = sd.R
		case ShapeAABB:
			if err := requirePositive(prefix, "w", sd.W); err != nil {
				return Shape{}, err
			}
			if err := requirePositive(prefix, "h", sd.H); err != nil {
				return Shape{}, err
			}
			if sd.R != 0 || sd.D != 0 {
				return Shape{}, fmt.Errorf("%s: 2D aabb must not set r/d", prefix)
			}
			sh.W = sd.W
			sh.H = sd.H
		case ShapeSphere:
			return Shape{}, fmt.Errorf("%s: sphere is not valid when dimensions=2", prefix)
		default:
			return Shape{}, fmt.Errorf("%s: unsupported type %q for dimensions=2", prefix, sd.Type)
		}
	case 3:
		switch typ {
		case ShapeSphere:
			if err := requirePositive(prefix, "r", sd.R); err != nil {
				return Shape{}, err
			}
			if sd.W != 0 || sd.H != 0 || sd.D != 0 {
				return Shape{}, fmt.Errorf("%s: sphere must not set w/h/d", prefix)
			}
			sh.R = sd.R
		case ShapeAABB:
			if err := requirePositive(prefix, "w", sd.W); err != nil {
				return Shape{}, err
			}
			if err := requirePositive(prefix, "h", sd.H); err != nil {
				return Shape{}, err
			}
			if err := requirePositive(prefix, "d", sd.D); err != nil {
				return Shape{}, err
			}
			if sd.R != 0 {
				return Shape{}, fmt.Errorf("%s: 3D aabb must not set r", prefix)
			}
			sh.W = sd.W
			sh.H = sd.H
			sh.D = sd.D
		case ShapeCircle:
			return Shape{}, fmt.Errorf("%s: circle is not valid when dimensions=3", prefix)
		default:
			return Shape{}, fmt.Errorf("%s: unsupported type %q for dimensions=3", prefix, sd.Type)
		}
	}
	return sh, nil
}

func parseOffset(m map[string]float64, dimensions int) (x, y, z float64, err error) {
	switch dimensions {
	case 2:
		if len(m) != 2 {
			return 0, 0, 0, fmt.Errorf("offset must have exactly keys x,y for dimensions=2")
		}
		x, okX := m["x"]
		y, okY := m["y"]
		if !okX || !okY {
			return 0, 0, 0, fmt.Errorf("offset must have exactly keys x,y for dimensions=2")
		}
		if err := requireFinite("offset.x", x); err != nil {
			return 0, 0, 0, err
		}
		if err := requireFinite("offset.y", y); err != nil {
			return 0, 0, 0, err
		}
		return x, y, 0, nil
	case 3:
		if len(m) != 3 {
			return 0, 0, 0, fmt.Errorf("offset must have exactly keys x,y,z for dimensions=3")
		}
		x, okX := m["x"]
		y, okY := m["y"]
		z, okZ := m["z"]
		if !okX || !okY || !okZ {
			return 0, 0, 0, fmt.Errorf("offset must have exactly keys x,y,z for dimensions=3")
		}
		if err := requireFinite("offset.x", x); err != nil {
			return 0, 0, 0, err
		}
		if err := requireFinite("offset.y", y); err != nil {
			return 0, 0, 0, err
		}
		if err := requireFinite("offset.z", z); err != nil {
			return 0, 0, 0, err
		}
		return x, y, z, nil
	default:
		return 0, 0, 0, fmt.Errorf("invalid dimensions %d", dimensions)
	}
}

func validateGUID(raw string) (string, error) {
	guid := normalizeGUID(raw)
	if len(guid) != 32 {
		return "", fmt.Errorf("footprint: invalid GUID %q: want 32 hex characters", raw)
	}
	for _, c := range guid {
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' {
			continue
		}
		return "", fmt.Errorf("footprint: invalid GUID %q: want 32 hex characters", raw)
	}
	return guid, nil
}

func normalizeGUID(guid string) string {
	return strings.ToLower(strings.TrimSpace(guid))
}

func requirePositive(prefix, name string, v float64) error {
	if err := requireFinite(name, v); err != nil {
		return fmt.Errorf("%s: %w", prefix, err)
	}
	if v <= 0 {
		return fmt.Errorf("%s: %s must be > 0", prefix, name)
	}
	return nil
}

func requireFinite(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("%s must be a finite number", name)
	}
	return nil
}
