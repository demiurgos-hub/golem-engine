package box3d

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"runtime"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

// CookVersion identifies backend artifact layout and cooking rules.
const CookVersion = "golem-box3d-cook-v1"

// RequiredFork identifies the minimum query, joint and motion patch set.
// UpstreamCommit identifies the original library baseline separately.
const RequiredFork = "216274347bc87c788ed05654366e4aed467fc294"

// CookSettings bounds source expansion and backend image retention. Edge identity
// must be enabled so coplanar mesh seams are not treated as physical walls.
type CookSettings struct {
	MaxVertices, MaxTriangles, MaxLeaves int
	MaxBytes                             int64
	IdentifyEdges                        bool
	// SourceSnapshot is the qualification capture's exact backend source SHA256.
	// Empty means only the minimum fork pin is known; it is not release evidence.
	SourceSnapshot string
}

// DefaultCookSettings returns bounded geometry cooking with mesh adjacency.
func DefaultCookSettings() CookSettings {
	return CookSettings{MaxVertices: 1_000_000, MaxTriangles: 1_000_000, MaxLeaves: 100_000, MaxBytes: 256 << 20, IdentifyEdges: true}
}

type cookedLeaf struct {
	desc p.ShapeDesc
	pose p.Pose
	hull *b.HullData
	mesh *b.MeshData
	blob []byte
}

// CookedShape privately owns backend hull/BVH data. It is immutable after cooking
// and may be shared between tick-owned worlds. Source and native images remain
// separate; no public API interprets untrusted native images as live pointers.
type CookedShape struct {
	source                  p.ShapeDesc
	settings                CookSettings
	leaves                  []cookedLeaf
	fingerprint, sourceHash string
	bytes                   int64
}

// CookShape validates and cooks portable geometry without creating world objects.
// It never substitutes bounding boxes, drops triangles, or silently simplifies a
// convex hull whose submitted point count exceeds the backend's exact limit.
func CookShape(source p.ShapeDesc, settings CookSettings) (*CookedShape, error) {
	if err := source.Validate(); err != nil {
		return nil, err
	}
	if settings.MaxVertices < 1 || settings.MaxTriangles < 1 || settings.MaxLeaves < 1 || settings.MaxBytes < 1 || !settings.IdentifyEdges {
		return nil, fmt.Errorf("%w: cook settings", p.ErrInvalid)
	}
	if settings.SourceSnapshot != "" {
		digest, err := hex.DecodeString(settings.SourceSnapshot)
		if err != nil || len(digest) != sha256.Size {
			return nil, fmt.Errorf("%w: backend source snapshot SHA256", p.ErrInvalid)
		}
	}
	cooked := &CookedShape{source: copyShape(source), settings: settings}
	vertices, triangles := 0, 0
	var capture func(p.ShapeDesc, p.Pose) error
	capture = func(shape p.ShapeDesc, pose p.Pose) error {
		if shape.Kind == p.Compound {
			for _, child := range shape.Children {
				if err := capture(child.Shape, pose.Compose(child.Pose)); err != nil {
					return err
				}
			}
			return nil
		}
		vertices += len(shape.Vertices)
		triangles += len(shape.Indices) / 3
		if vertices > settings.MaxVertices || triangles > settings.MaxTriangles || len(cooked.leaves) >= settings.MaxLeaves {
			return fmt.Errorf("%w: cook geometry limit", p.ErrInvalid)
		}
		leaf := cookedLeaf{desc: shape, pose: pose}
		switch shape.Kind {
		case p.Box:
			hull := b.MakeBoxHull(shape.HalfExtents.X, shape.HalfExtents.Y, shape.HalfExtents.Z)
			leaf.hull = b.CloneHull(&hull.Base)
		case p.Convex:
			if len(shape.Vertices) > b.MaxHullVertices {
				return fmt.Errorf("%w: convex source exceeds %d exact hull vertices", p.ErrUnsupported, b.MaxHullVertices)
			}
			points := backendVertices(shape.Vertices, p.Identity())
			leaf.hull = b.CreateHull(points, len(points), len(points))
			if leaf.hull == nil {
				return fmt.Errorf("%w: convex cooking failed", p.ErrInvalid)
			}
		case p.TriangleMesh:
			var err error
			leaf.mesh, err = cookMesh(shape, p.Identity())
			if err != nil {
				return err
			}
		}
		var byteCount int64
		if leaf.hull != nil {
			byteCount = int64(leaf.hull.ByteCount)
		} else if leaf.mesh != nil {
			byteCount = int64(leaf.mesh.ByteCount)
		}
		if byteCount < 0 || byteCount > settings.MaxBytes-cooked.bytes {
			return fmt.Errorf("%w: cooked image byte limit", p.ErrInvalid)
		}
		if leaf.hull != nil {
			leaf.blob = b.MarshalHullData(leaf.hull)
		} else if leaf.mesh != nil {
			leaf.blob = b.MarshalMeshData(leaf.mesh)
		}
		cooked.bytes += byteCount
		cooked.leaves = append(cooked.leaves, leaf)
		return nil
	}
	if err := capture(cooked.source, p.Identity()); err != nil {
		return nil, err
	}
	data, err := json.Marshal(cooked.source)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > settings.MaxBytes {
		return nil, fmt.Errorf("%w: portable cook source byte limit", p.ErrInvalid)
	}
	cooked.sourceHash = cookDigest(data)
	identity, _ := json.Marshal(struct {
		Version, Upstream, Fork, Architecture, Source string
		HullVersion, MeshVersion                      uint64
		Settings                                      CookSettings
	}{CookVersion, UpstreamCommit, RequiredFork, runtime.GOARCH, cooked.sourceHash, b.HullVersion, b.MeshVersion, settings})
	cooked.fingerprint = cookDigest(identity)
	return cooked, nil
}

func backendVertices(vertices []p.Vec3, pose p.Pose) []b.Vec3 {
	points := make([]b.Vec3, len(vertices))
	for i, vertex := range vertices {
		points[i] = bv(pose.Transform(vertex))
	}
	return points
}

func cookMesh(shape p.ShapeDesc, pose p.Pose) (*b.MeshData, error) {
	points := backendVertices(shape.Vertices, pose)
	indices := make([]int32, len(shape.Indices))
	for i, index := range shape.Indices {
		indices[i] = int32(index)
	}
	d := b.MeshDef{Vertices: points, Indices: indices, VertexCount: int32(len(points)), TriangleCount: int32(len(indices) / 3), IdentifyEdges: true}
	mesh := b.CreateMesh(&d, nil)
	if mesh == nil || mesh.DegenerateCount != 0 || mesh.TriangleCount != d.TriangleCount {
		return nil, fmt.Errorf("%w: triangle cooking changed geometry", p.ErrInvalid)
	}
	return mesh, nil
}

func cookDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Fingerprint includes source, settings, backend pin, format versions and architecture.
func (s *CookedShape) Fingerprint() string { return s.fingerprint }

// Source returns an independent portable description, without backend arrays.
func (s *CookedShape) Source() p.ShapeDesc { return copyShape(s.source) }

// NativeBytes reports retained hull/BVH image bytes, not total solver memory.
func (s *CookedShape) NativeBytes() int64 { return s.bytes }

// MarshalBinary emits a versioned JSON envelope containing actual backend image
// bytes (base64), not portable source disguised as acceleration data. Loading a
// foreign image is intentionally unsupported; recook from Source after upgrades.
func (s *CookedShape) MarshalBinary() ([]byte, error) {
	if s == nil {
		return nil, p.ErrInvalid
	}
	type leafImage struct {
		Kind  p.ShapeKind
		Pose  p.Pose
		Image []byte
	}
	images := make([]leafImage, len(s.leaves))
	for i, leaf := range s.leaves {
		images[i] = leafImage{leaf.desc.Kind, leaf.pose, leaf.blob}
	}
	return json.Marshal(struct {
		Version, Backend, Fork, Architecture, SourceHash, Fingerprint string
		Settings                                                      CookSettings
		Leaves                                                        []leafImage
	}{CookVersion, UpstreamCommit, RequiredFork, runtime.GOARCH, s.sourceHash, s.fingerprint, s.settings, images})
}

// CreateCookedShape registers an immutable prepared shape without recooking it.
func (w *World) CreateCookedShape(cooked *CookedShape) (p.ShapeHandle, error) {
	if err := w.guard(); err != nil {
		return p.ShapeHandle{}, err
	}
	if cooked == nil {
		return p.ShapeHandle{}, p.ErrInvalid
	}
	identity, err := w.claimHandle()
	if err != nil {
		return p.ShapeHandle{}, err
	}
	h := p.ShapeHandle(identity)
	w.shapes[h] = &shape{desc: cooked.Source(), cooked: cooked}
	w.mutation++
	return h, nil
}
