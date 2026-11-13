// Package content loads versioned portable physics geometry. It owns no server,
// world-assembly or gameplay policy. Source identities are resolved by the host.
package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	"github.com/demiurgos-hub/golem-engine/golem/geometry"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

const Generator = "Golem Scribe physics source v1"
const GeneratorV2 = "Golem Scribe physics source v2"

type Limits struct {
	Bytes                                                  int64
	Bodies, Colliders, Assets, Vertices, Triangles, Joints int
}

// DefaultLimits bounds decoding and geometry expansion independently of backend memory.
func DefaultLimits() Limits {
	return Limits{Bytes: 256 << 20, Bodies: 200_000, Colliders: 500_000, Assets: 100_000, Vertices: 10_000_000, Triangles: 10_000_000, Joints: 100_000}
}

type Source struct {
	Generator           string
	Version             int
	WorldID             string
	Revision            uint64
	CoordinateLimit     float32
	LayerCollisionMasks []uint32
	Bodies              []Body
	Colliders           []Collider
	Assets              []Asset
	Materials           []Material
	Joints              []Joint
	Geometry            *geometry.Reference `json:",omitempty"`
}
type Body struct {
	ID, OwnerID, Kind, CollisionDetection         string
	Position                                      p.Vec3
	Rotation                                      p.Quat
	Enabled, Active, UseGravity                   bool
	Mass, LinearDamping, AngularDamping           float32
	Constraints                                   int
	AutomaticCenterOfMass, AutomaticInertiaTensor bool
	CenterOfMass, InertiaTensor                   p.Vec3
	InertiaRotation                               p.Quat
}
type Collider struct {
	ID, BodyID, OwnerID, SourceID, ParentID, LayerName, Shape, AssetID, MaterialID string
	Enabled, Active, Solid, Sensor, Query                                          bool
	Layer, Axis                                                                    int
	Center, Size                                                                   p.Vec3
	Rotation                                                                       p.Quat
	Radius, Height, ContactOffset                                                  float32
	Character                                                                      *Character
	Metadata                                                                       []Property
	GeometryTransform                                                              *[16]float32 `json:",omitempty"`
}
type Character struct {
	StepOffset, SlopeLimit, SkinWidth, MinimumMoveDistance float32
	DetectCollisions, EnableOverlapRecovery                bool
}
type Property struct{ Key, Value string }
type Asset struct {
	ID, SourceID string
	Vertices     []p.Vec3
	Indices      []uint32
}
type Material struct {
	ID, FrictionCombine, BounceCombine           string
	StaticFriction, DynamicFriction, Restitution float32
}
type JointLimit struct{ Limit, Bounciness, BounceMinVelocity, ContactDistance, Spring, Damper float32 }
type JointMotor struct {
	TargetVelocity, Force float32
	FreeSpin              bool
}
type JointSpring struct{ Spring, Damper, TargetPosition, MinDistance, MaxDistance, Tolerance, RestDistance float32 }
type Joint struct {
	ID, Kind, SourceKind, BodyID, ConnectedBodyID                                                  string
	Anchor, ConnectedAnchor, Axis, SecondaryAxis                                                   p.Vec3
	ReferenceRotation                                                                              p.Quat
	Active, EnableCollision, EnablePreprocessing, UseLimits, UseMotor, UseSpring, EnableProjection bool
	MassScale, ConnectedMassScale, ProjectionDistance, ProjectionAngle                             float32
	BreakForce, BreakTorque                                                                        *float32
	LowTwist, HighTwist, Swing1, Swing2                                                            JointLimit
	Motor                                                                                          JointMotor
	Spring                                                                                         JointSpring
}

// Decode reads one strict JSON source document. Unknown fields and trailing data
// fail before any live physics world is modified. The hash covers exact source bytes.
func Decode(r io.Reader, limits Limits) (*Source, string, error) {
	return DecodeWithGeometry(r, limits, nil)
}

// DecodeFile reads a source and verifies binary sidecars contained in its folder.
func DecodeFile(path string, limits Limits) (*Source, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	return DecodeWithGeometry(file, limits, geometry.FileResolver(filepath.Dir(path)))
}

// DecodeWithGeometry resolves version-2 binary geometry before complete source
// validation. Version-1 inline sources retain their existing decoding contract.
func DecodeWithGeometry(r io.Reader, limits Limits, resolve geometry.Resolver) (*Source, string, error) {
	if r == nil || limits.Bytes < 1 {
		return nil, "", p.ErrInvalid
	}
	data, e := io.ReadAll(io.LimitReader(r, limits.Bytes+1))
	if e != nil {
		return nil, "", e
	}
	if int64(len(data)) > limits.Bytes {
		return nil, "", fmt.Errorf("%w: source byte limit", p.ErrInvalid)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var s Source
	if e = d.Decode(&s); e != nil {
		return nil, "", e
	}
	var extra any
	if e = d.Decode(&extra); e != io.EOF {
		return nil, "", fmt.Errorf("%w: trailing source data", p.ErrInvalid)
	}
	if s.Geometry != nil {
		if s.Version != 2 || len(s.Assets) != 0 {
			return nil, "", invalid("external geometry requires version 2 without inline assets")
		}
		assets, err := geometry.Read(*s.Geometry, resolve, geometry.Limits{Bytes: limits.Bytes, Assets: limits.Assets, Vertices: limits.Vertices, Triangles: limits.Triangles})
		if err != nil {
			return nil, "", err
		}
		for _, asset := range assets {
			if asset.Kind == geometry.MeshFloat64 {
				return nil, "", invalid("physics geometry requires float32 authored samples")
			}
			asset = geometry.ExpandHeightfield(asset, true)
			value := Asset{ID: asset.ID, SourceID: asset.SourceID, Vertices: make([]p.Vec3, len(asset.Vertices)), Indices: make([]uint32, 0, len(asset.Triangles)*3)}
			for index, vertex := range asset.Vertices {
				value.Vertices[index] = p.Vec3{X: float32(vertex.X), Y: float32(vertex.Y), Z: float32(vertex.Z)}
			}
			for _, triangle := range asset.Triangles {
				value.Indices = append(value.Indices, triangle.Indices[:]...)
			}
			s.Assets = append(s.Assets, value)
		}
	}
	if e = s.Validate(limits); e != nil {
		return nil, "", e
	}
	sum := sha256.Sum256(data)
	return &s, hex.EncodeToString(sum[:]), nil
}
func positive(v float32) bool    { return p.Finite(v) && v > 0 }
func nonnegative(v float32) bool { return p.Finite(v) && v >= 0 }
func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", p.ErrInvalid, fmt.Sprintf(format, args...))
}

// Validate checks complete identity, geometry and resource invariants.
func (s *Source) Validate(l Limits) error {
	if l.Bytes < 1 || l.Bodies < 0 || l.Colliders < 0 || l.Assets < 0 || l.Vertices < 0 || l.Triangles < 0 || l.Joints < 0 {
		return invalid("resource limits")
	}
	if s == nil || (s.Version != 1 || s.Generator != Generator) && (s.Version != 2 || s.Generator != GeneratorV2) || s.WorldID == "" || s.Revision == 0 || s.CoordinateLimit != p.CoordinateLimit {
		return invalid("source identity/version/envelope")
	}
	if s.Version == 1 && s.Geometry != nil {
		return invalid("version 1 cannot reference binary geometry")
	}
	if len(s.Bodies) > l.Bodies || len(s.Colliders) > l.Colliders || len(s.Assets) > l.Assets || len(s.Joints) > l.Joints {
		return invalid("source object limit")
	}
	if len(s.LayerCollisionMasks) != 32 {
		return invalid("32 layer masks required")
	}
	for i := 0; i < 32; i++ {
		for j := 0; j < 32; j++ {
			if (s.LayerCollisionMasks[i]>>j)&1 != (s.LayerCollisionMasks[j]>>i)&1 {
				return invalid("asymmetric layer matrix")
			}
		}
	}
	bodies := map[string]Body{}
	for _, v := range s.Bodies {
		if v.ID == "" {
			return invalid("body identity")
		}
		if _, ok := bodies[v.ID]; ok {
			return invalid("duplicate body %s", v.ID)
		}
		if !(p.Pose{Position: v.Position, Rotation: v.Rotation}).Valid() || !positive(v.Mass) || !nonnegative(v.LinearDamping) || !nonnegative(v.AngularDamping) || v.Constraints&^126 != 0 {
			return invalid("body %s", v.ID)
		}
		switch v.Kind {
		case "static", "kinematic", "dynamic":
		default:
			return invalid("body kind %s", v.Kind)
		}
		if v.CollisionDetection != "discrete" && v.CollisionDetection != "continuous" && v.CollisionDetection != "continuousDynamic" && v.CollisionDetection != "continuousSpeculative" {
			return invalid("CCD mode %s", v.CollisionDetection)
		}
		if !v.AutomaticCenterOfMass && !v.CenterOfMass.InBounds() {
			return invalid("body centre of mass")
		}
		if !v.AutomaticInertiaTensor && (!positive(v.InertiaTensor.X) || !positive(v.InertiaTensor.Y) || !positive(v.InertiaTensor.Z) || !v.InertiaRotation.Valid()) {
			return invalid("body inertia")
		}
		bodies[v.ID] = v
	}
	assets := map[string]Asset{}
	vertices, triangles := 0, 0
	for _, v := range s.Assets {
		if v.ID == "" {
			return invalid("asset identity")
		}
		if _, ok := assets[v.ID]; ok {
			return invalid("duplicate asset")
		}
		vertices += len(v.Vertices)
		triangles += len(v.Indices) / 3
		if vertices > l.Vertices || triangles > l.Triangles {
			return invalid("geometry limit")
		}
		if e := validateSourceAsset(v, s.Version == 2); e != nil {
			return invalid("asset %s: %v", v.ID, e)
		}
		assets[v.ID] = v
	}
	materials := map[string]Material{}
	for _, v := range s.Materials {
		if v.ID == "" {
			return invalid("material identity")
		}
		if _, ok := materials[v.ID]; ok {
			return invalid("duplicate material")
		}
		if !nonnegative(v.StaticFriction) || !nonnegative(v.DynamicFriction) || !nonnegative(v.Restitution) || v.Restitution > 1 {
			return invalid("material coefficients")
		}
		for _, combine := range []string{v.FrictionCombine, v.BounceCombine} {
			switch combine {
			case "average", "minimum", "maximum", "multiply":
			default:
				return invalid("material combination")
			}
		}
		materials[v.ID] = v
	}
	seen := map[string]bool{}
	for _, v := range s.Colliders {
		mesh := v.Shape == "mesh" || v.Shape == "convex" || v.Shape == "terrain"
		if s.Version == 1 && v.GeometryTransform != nil || s.Version == 2 && mesh && v.GeometryTransform == nil || !mesh && v.GeometryTransform != nil {
			return invalid("collider geometry transform/version")
		}
		if v.ID == "" || seen[v.ID] {
			return invalid("collider identity")
		}
		seen[v.ID] = true
		body, ok := bodies[v.BodyID]
		if !ok {
			return invalid("missing body %s", v.BodyID)
		}
		if _, ok := materials[v.MaterialID]; !ok {
			return invalid("missing material")
		}
		if v.Layer < 0 || v.Layer > 31 || !nonnegative(v.ContactOffset) || !v.Center.InBounds() || !v.Rotation.Valid() {
			return invalid("collider transform/layer")
		}
		shape, e := v.Geometry(assets)
		if e != nil {
			return fmt.Errorf("collider %s: %w", v.ID, e)
		}
		if e = shape.Validate(); e != nil {
			return e
		}
		if body.Kind == "dynamic" && shape.Kind == p.TriangleMesh {
			return invalid("dynamic nonconvex collider")
		}
		metadata := map[string]bool{}
		for _, m := range v.Metadata {
			if m.Key == "" || metadata[m.Key] {
				return invalid("metadata keys")
			}
			metadata[m.Key] = true
		}
		if v.Character != nil {
			c := v.Character
			if v.Shape != "capsule" || body.Kind != "kinematic" || !nonnegative(c.StepOffset) || !nonnegative(c.SkinWidth) || !nonnegative(c.MinimumMoveDistance) || !nonnegative(c.SlopeLimit) || c.SlopeLimit > 90 {
				return invalid("character settings")
			}
		}
		pose := (p.Pose{Position: body.Position, Rotation: body.Rotation}).Compose(p.Pose{Position: v.Center, Rotation: v.Rotation})
		if shape.Kind == p.TriangleMesh || shape.Kind == p.Convex {
			for _, point := range shape.Vertices {
				if !pose.Transform(point).InBounds() {
					return invalid("world vertex envelope")
				}
			}
		} else {
			radius := shape.Radius
			if shape.Kind == p.Capsule {
				radius += max(shape.PointA.Length(), shape.PointB.Length())
			}
			if shape.Kind == p.Box {
				radius = shape.HalfExtents.Length()
			}
			if math.Abs(float64(pose.Position.X))+float64(radius) > 4096 || math.Abs(float64(pose.Position.Y))+float64(radius) > 4096 || math.Abs(float64(pose.Position.Z))+float64(radius) > 4096 {
				return invalid("world primitive envelope")
			}
		}
	}
	seen = map[string]bool{}
	for _, j := range s.Joints {
		if j.ID == "" || seen[j.ID] || j.BodyID == j.ConnectedBodyID {
			return invalid("joint identity")
		}
		seen[j.ID] = true
		if _, ok := bodies[j.BodyID]; !ok {
			return invalid("joint body")
		}
		if j.ConnectedBodyID != "" {
			if _, ok := bodies[j.ConnectedBodyID]; !ok {
				return invalid("connected joint body")
			}
		}
		switch j.Kind {
		case "fixed", "hinge", "spring", "character":
		default:
			return invalid("joint kind")
		}
		if !j.Anchor.InBounds() || !j.ConnectedAnchor.InBounds() || !j.Axis.Valid() || j.Axis.Length() == 0 || !j.SecondaryAxis.Valid() || !j.ReferenceRotation.Valid() || !positive(j.MassScale) || !positive(j.ConnectedMassScale) {
			return invalid("joint frame")
		}
		for _, threshold := range []*float32{j.BreakForce, j.BreakTorque} {
			if threshold != nil && !nonnegative(*threshold) {
				return invalid("joint break threshold")
			}
		}
		for _, limit := range []JointLimit{j.LowTwist, j.HighTwist, j.Swing1, j.Swing2} {
			if !p.Finite(limit.Limit) || !nonnegative(limit.Bounciness) || limit.Bounciness > 1 || !nonnegative(limit.BounceMinVelocity) || !nonnegative(limit.ContactDistance) || !nonnegative(limit.Spring) || !nonnegative(limit.Damper) {
				return invalid("joint limit")
			}
		}
		if !p.Finite(j.Motor.TargetVelocity) || !nonnegative(j.Motor.Force) || !nonnegative(j.ProjectionDistance) || !nonnegative(j.ProjectionAngle) {
			return invalid("joint motor/projection")
		}
		sp := j.Spring
		if !nonnegative(sp.Spring) || !nonnegative(sp.Damper) || !p.Finite(sp.TargetPosition) || !nonnegative(sp.MinDistance) || !nonnegative(sp.MaxDistance) || sp.MaxDistance < sp.MinDistance || !nonnegative(sp.Tolerance) || !nonnegative(sp.RestDistance) {
			return invalid("joint spring")
		}
	}
	return nil
}

// Geometry converts supported authored dimensions to the portable shape contract.
func (c Collider) Geometry(assets map[string]Asset) (p.ShapeDesc, error) {
	s := p.ShapeDesc{ID: p.ContentID(c.ID + "/geometry")}
	switch c.Shape {
	case "sphere":
		s.Kind = p.Sphere
		s.Radius = c.Radius
	case "capsule":
		if c.Axis < 0 || c.Axis > 2 || c.Height < 2*c.Radius {
			return s, invalid("capsule dimensions")
		}
		s.Kind = p.Capsule
		s.Radius = c.Radius
		length := c.Height/2 - c.Radius
		switch c.Axis {
		case 0:
			s.PointB.X = length
		case 1:
			s.PointB.Y = length
		case 2:
			s.PointB.Z = length
		}
		s.PointA = s.PointB.Mul(-1)
	case "box":
		s.Kind = p.Box
		s.HalfExtents = c.Size.Mul(.5)
	case "mesh", "terrain", "convex":
		a, ok := assets[c.AssetID]
		if !ok {
			return s, invalid("missing geometry %s", c.AssetID)
		}
		s.Kind = p.TriangleMesh
		if c.Shape == "convex" {
			s.Kind = p.Convex
		}
		s.Vertices = a.Vertices
		s.Indices = a.Indices
		if c.GeometryTransform != nil {
			var err error
			s.Vertices, s.Indices, err = transformedGeometry(a, *c.GeometryTransform, c.Metadata)
			if err != nil {
				return s, err
			}
		}
	default:
		return s, invalid("shape %s", c.Shape)
	}
	return s, nil
}
