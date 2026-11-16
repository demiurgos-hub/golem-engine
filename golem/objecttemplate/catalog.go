package objecttemplate

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/demiurgos-hub/golem-engine/golem/geometry"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

// Generator and Version identify the catalog document format.
const (
	Generator = "Golem Scribe object templates v1"
	Version   = 1
)

// Shape kinds stored in Shape2D.Kind.
const (
	ShapeCircle = "circle"
	ShapeBox    = "box"
)

// ErrInvalid reports a malformed catalog, template or placement request.
var ErrInvalid = errors.New("objecttemplate: invalid input")

// Catalog is one decoded template set. It is immutable after Decode.
type Catalog struct {
	Generator  string
	Version    int
	CatalogID  string
	Revision   uint64
	Dimensions int
	// LayerCollisionMasks is the 3D 32x32 layer matrix, symmetric like Unity's.
	LayerCollisionMasks []uint32           `json:",omitempty"`
	Materials           []content.Material `json:",omitempty"`
	Templates           []Template
	// Geometry references the binary sidecar holding every physics and
	// navigation asset of the catalog. 2D catalogs have none.
	Geometry *geometry.Reference `json:",omitempty"`

	byID       map[string]*Template
	physics    map[string]content.Asset
	geometry   map[string]geometry.Asset
	materials  map[string]p.Material
	navigation []string
	bases      map[string]p.ShapeDesc
	baseOrder  []string
}

// Template is one reusable object definition. 3D templates use Bodies,
// Colliders and Navigation in root-local space; 2D templates use Shapes.
type Template struct {
	ID string
	// SourceID identifies the authoring asset for diagnostics.
	SourceID   string             `json:",omitempty"`
	Bodies     []content.Body     `json:",omitempty"`
	Colliders  []content.Collider `json:",omitempty"`
	Shapes     []Shape2D          `json:",omitempty"`
	Navigation *Navigation        `json:",omitempty"`

	catalog   *Catalog
	bodies    []bodyPlan
	colliders []colliderPlan
}

// Navigation is root-local navigation source geometry. Instance asset IDs
// reference catalog geometry; Template.PlaceNavigation returns world copies.
type Navigation struct {
	Instances []geom.Instance `json:",omitempty"`
	Volumes   []geom.Volume   `json:",omitempty"`
	Links     []geom.Link     `json:",omitempty"`
}

// Shape2D is one root-local 2D collision primitive. Box Width and Height are
// full extents.
type Shape2D struct {
	ID               string
	Kind             string
	OffsetX, OffsetY float64
	Radius           float64 `json:",omitempty"`
	Width, Height    float64 `json:",omitempty"`
	Layer            string
	Trigger          bool `json:",omitempty"`
}

// Limits bounds decoding and geometry expansion.
type Limits struct {
	Bytes                                                     int64
	Templates, Bodies, Colliders, Assets, Vertices, Triangles int
}

// DefaultLimits admits a full-map catalog including several terrain heightfields.
func DefaultLimits() Limits {
	return Limits{Bytes: 512 << 20, Templates: 100_000, Bodies: 200_000, Colliders: 500_000, Assets: 100_000, Vertices: 50_000_000, Triangles: 100_000_000}
}

// Template returns a template by ID.
func (c *Catalog) Template(id string) (*Template, bool) {
	if c == nil {
		return nil, false
	}
	t, ok := c.byID[id]
	return t, ok
}

// Catalog returns the catalog that owns the template.
func (t *Template) Catalog() *Catalog { return t.catalog }

// DecodeFile reads a catalog and verifies its geometry sidecar inside the
// catalog's folder. The returned hash covers the exact catalog bytes.
func DecodeFile(path string, limits Limits) (*Catalog, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	return Decode(file, geometry.FileResolver(filepath.Dir(path)), limits)
}

// Decode strictly reads one catalog and resolves its geometry sidecar. Unknown
// fields, trailing data and invalid templates fail before any world is touched.
func Decode(r io.Reader, resolve geometry.Resolver, limits Limits) (*Catalog, string, error) {
	if r == nil || limits.Bytes < 1 {
		return nil, "", ErrInvalid
	}
	data, err := io.ReadAll(io.LimitReader(r, limits.Bytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > limits.Bytes {
		return nil, "", invalid("catalog byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var c Catalog
	if err = decoder.Decode(&c); err != nil {
		return nil, "", err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, "", invalid("trailing catalog data")
	}
	if err = c.load(resolve, limits); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return &c, hex.EncodeToString(sum[:]), nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func (c *Catalog) load(resolve geometry.Resolver, limits Limits) error {
	if c.Generator != Generator || c.Version != Version || c.CatalogID == "" || c.Revision == 0 {
		return invalid("catalog identity or version")
	}
	if c.Dimensions != 2 && c.Dimensions != 3 {
		return invalid("dimensions must be 2 or 3, got %d", c.Dimensions)
	}
	if len(c.Templates) > limits.Templates {
		return invalid("template limit")
	}
	c.byID = make(map[string]*Template, len(c.Templates))
	for i := range c.Templates {
		t := &c.Templates[i]
		if t.ID == "" || strings.TrimSpace(t.ID) != t.ID {
			return invalid("template identity %q", t.ID)
		}
		if _, ok := c.byID[t.ID]; ok {
			return invalid("duplicate template %s", t.ID)
		}
		t.catalog = c
		c.byID[t.ID] = t
	}
	if c.Dimensions == 2 {
		return c.load2D()
	}
	return c.load3D(resolve, limits)
}

func (c *Catalog) load2D() error {
	if len(c.LayerCollisionMasks) != 0 || len(c.Materials) != 0 || c.Geometry != nil {
		return invalid("2D catalogs carry only shapes")
	}
	for i := range c.Templates {
		t := &c.Templates[i]
		if len(t.Bodies) != 0 || len(t.Colliders) != 0 || t.Navigation != nil {
			return invalid("2D template %s has 3D content", t.ID)
		}
		if len(t.Shapes) == 0 {
			return invalid("template %s has no shapes", t.ID)
		}
		seen := map[string]bool{}
		for _, s := range t.Shapes {
			if s.ID == "" || seen[s.ID] {
				return invalid("template %s shape identity %q", t.ID, s.ID)
			}
			seen[s.ID] = true
			if err := s.validate(); err != nil {
				return fmt.Errorf("template %s shape %s: %w", t.ID, s.ID, err)
			}
		}
	}
	return nil
}

func (s Shape2D) validate() error {
	if !finite(s.OffsetX) || !finite(s.OffsetY) || strings.TrimSpace(s.Layer) == "" {
		return invalid("offset or layer")
	}
	switch s.Kind {
	case ShapeCircle:
		if !finite(s.Radius) || s.Radius <= 0 || s.Width != 0 || s.Height != 0 {
			return invalid("circle dimensions")
		}
	case ShapeBox:
		if !finite(s.Width) || s.Width <= 0 || !finite(s.Height) || s.Height <= 0 || s.Radius != 0 {
			return invalid("box dimensions")
		}
	default:
		return invalid("shape kind %q", s.Kind)
	}
	return nil
}

func (c *Catalog) load3D(resolve geometry.Resolver, limits Limits) error {
	if len(c.LayerCollisionMasks) != 32 {
		return invalid("32 layer masks required")
	}
	c.geometry = map[string]geometry.Asset{}
	c.physics = map[string]content.Asset{}
	if c.Geometry != nil {
		assets, err := geometry.Read(*c.Geometry, resolve, geometry.Limits{Bytes: limits.Bytes, Assets: limits.Assets, Vertices: limits.Vertices, Triangles: limits.Triangles})
		if err != nil {
			return err
		}
		for _, asset := range assets {
			c.geometry[asset.ID] = asset
		}
	}
	c.materials = make(map[string]p.Material, len(c.Materials))
	for _, m := range c.Materials {
		if m.StaticFriction != m.DynamicFriction {
			return fmt.Errorf("%w: material %s combination/static friction", p.ErrUnsupported, m.ID)
		}
		combine := map[string]p.CombineMode{"average": p.Average, "minimum": p.Minimum, "multiply": p.Multiply, "maximum": p.Maximum}
		hash := sha256.Sum256([]byte(c.CatalogID + "\x00" + m.ID))
		c.materials[m.ID] = p.Material{ID: binary.LittleEndian.Uint64(hash[:8]), Friction: m.DynamicFriction, Restitution: m.Restitution,
			FrictionCombine: combine[m.FrictionCombine], RestitutionCombine: combine[m.BounceCombine]}
	}
	bodies, colliders := 0, 0
	navigation := map[string]bool{}
	for i := range c.Templates {
		t := &c.Templates[i]
		bodies += len(t.Bodies)
		colliders += len(t.Colliders)
		if bodies > limits.Bodies || colliders > limits.Colliders {
			return invalid("catalog object limit")
		}
		if len(t.Shapes) != 0 {
			return invalid("3D template %s has 2D shapes", t.ID)
		}
		if len(t.Colliders) == 0 && t.Navigation.empty() {
			return invalid("template %s has no colliders or navigation", t.ID)
		}
		if err := c.prepare(t, limits); err != nil {
			return fmt.Errorf("template %s: %w", t.ID, err)
		}
		if t.Navigation != nil {
			if err := c.validateNavigation(t); err != nil {
				return fmt.Errorf("template %s navigation: %w", t.ID, err)
			}
			for _, instance := range t.Navigation.Instances {
				navigation[instance.AssetID] = true
			}
		}
	}
	for id := range navigation {
		c.navigation = append(c.navigation, id)
	}
	sort.Strings(c.navigation)
	return nil
}

// physicsAsset converts sidecar geometry to the physics content contract once.
func (c *Catalog) physicsAsset(id string) (content.Asset, bool) {
	if asset, ok := c.physics[id]; ok {
		return asset, true
	}
	raw, ok := c.geometry[id]
	if !ok {
		return content.Asset{}, false
	}
	var asset content.Asset
	switch raw.Kind {
	case geometry.HeightfieldFloat32:
		asset = content.Asset{ID: raw.ID, SourceID: raw.SourceID, Heightfield: raw.Heightfield}
	case geometry.MeshFloat32:
		asset = content.Asset{ID: raw.ID, SourceID: raw.SourceID, Vertices: make([]p.Vec3, len(raw.Vertices)), Indices: make([]uint32, 0, len(raw.Triangles)*3)}
		for i, v := range raw.Vertices {
			asset.Vertices[i] = p.Vec3{X: float32(v.X), Y: float32(v.Y), Z: float32(v.Z)}
		}
		for _, triangle := range raw.Triangles {
			asset.Indices = append(asset.Indices, triangle.Indices[:]...)
		}
	default:
		return content.Asset{}, false
	}
	c.physics[id] = asset
	return asset, true
}

func (n *Navigation) empty() bool {
	return n == nil || len(n.Instances) == 0 && len(n.Volumes) == 0 && len(n.Links) == 0
}

func (c *Catalog) validateNavigation(t *Template) error {
	n := t.Navigation
	if n.empty() {
		return invalid("empty navigation")
	}
	ids := map[string]bool{}
	for _, instance := range n.Instances {
		if instance.ID == "" || ids["i"+instance.ID] {
			return invalid("instance identity %q", instance.ID)
		}
		ids["i"+instance.ID] = true
		if _, ok := c.geometry[instance.AssetID]; !ok {
			return invalid("instance %s references missing asset %s", instance.ID, instance.AssetID)
		}
		for _, v := range instance.Transform {
			if !finite(v) {
				return invalid("instance %s transform", instance.ID)
			}
		}
	}
	for _, volume := range n.Volumes {
		if volume.ID == "" || ids["v"+volume.ID] {
			return invalid("volume identity %q", volume.ID)
		}
		ids["v"+volume.ID] = true
	}
	for _, link := range n.Links {
		if link.ID == "" || ids["l"+link.ID] {
			return invalid("link identity %q", link.ID)
		}
		ids["l"+link.ID] = true
	}
	return nil
}
