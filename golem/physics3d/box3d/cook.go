package box3d

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"math"
	"runtime"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	b "github.com/gataengine/box3d-go"
)

// CookVersion identifies backend artifact layout and cooking rules.
const CookVersion = "golem-box3d-cook-v3"

// RequiredFork identifies the minimum query, joint, motion and geometry patch set.
// UpstreamCommit identifies the original library baseline separately.
const RequiredFork = "8a6d2ef383d0426902390f15b4544074295dbc2b"

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
	desc   p.ShapeDesc
	pose   p.Pose
	hull   *b.HullData
	mesh   *b.MeshData
	height *b.HeightFieldData
}

// cookHeightField stores samples verbatim and splits cells along the portable
// main diagonal; clear solid bits become native holes.
func cookHeightField(h *p.HeightFieldDesc) *b.HeightFieldData {
	materials := make([]uint8, (h.Resolution-1)*(h.Resolution-1))
	for cell := range materials {
		if h.Solid[cell/8]&(1<<uint(cell%8)) == 0 {
			materials[cell] = b.HeightFieldHole
		}
	}
	return b.CreateHeightField(&b.HeightFieldDef{CompressedHeights: h.Heights, HeightStep: h.HeightStep, MaterialIndices: materials,
		Scale: b.Vec3{X: h.SpacingX, Y: 1, Z: h.SpacingZ}, CountX: int32(h.Resolution), CountZ: int32(h.Resolution), MainDiagonal: true})
}

// CookedShape privately owns backend hull/BVH data. It is immutable after cooking
// and may be shared between tick-owned worlds. Source and native images remain
// separate; no public API interprets untrusted native images as live pointers.
// Shapes that differ only by ID and Scale share one hull or BVH.
type CookedShape struct {
	source                  p.ShapeDesc
	settings                CookSettings
	leaves                  []cookedLeaf
	geometry                [sha256.Size]byte
	fingerprint, sourceHash string
	bytes                   int64
}

// PreparedShapeID implements physics3d.PreparedShape.
func (s *CookedShape) PreparedShapeID() p.ContentID { return s.source.ID }

// Cooker prepares shapes for any Box3D World with fixed cook settings.
type Cooker struct{ Settings CookSettings }

// PrepareShape implements physics3d.ShapePreparer and is safe for concurrent use.
func (c Cooker) PrepareShape(source p.ShapeDesc) (p.PreparedShape, error) {
	return CookShape(source, c.Settings)
}

// CookShape validates and cooks portable geometry without creating world objects.
// It preserves every nonzero-area triangle and constructs unreduced convex
// hulls. Native hull limits apply to topology, not the submitted point count;
// exceeding those limits is an error, never implicit simplification. Only a
// shape's explicit HullVertexLimit admits a hull reduced to that vertex count.
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
		if shape.Height != nil {
			vertices += len(shape.Height.Heights)
			triangles += 2 * (shape.Height.Resolution - 1) * (shape.Height.Resolution - 1)
		}
		if vertices > settings.MaxVertices || triangles > settings.MaxTriangles || len(cooked.leaves) >= settings.MaxLeaves {
			return fmt.Errorf("%w: cook geometry limit", p.ErrInvalid)
		}
		leaf := cookedLeaf{desc: shape, pose: pose}
		switch shape.Kind {
		case p.Box:
			hull := b.MakeBoxHull(shape.HalfExtents.X, shape.HalfExtents.Y, shape.HalfExtents.Z)
			leaf.hull = b.CloneHull(&hull.Base)
		case p.Convex:
			points := backendVertices(shape.Vertices, p.Identity())
			points = uniqueHullPoints(points)
			if shape.HullVertexLimit > 0 {
				leaf.hull = b.CreateHull(points, len(points), shape.HullVertexLimit)
				if leaf.hull == nil {
					return fmt.Errorf("%w: convex cooking failed (%d unique input points; hull vertex limit %d)", p.ErrUnsupported, len(points), shape.HullVertexLimit)
				}
			} else {
				leaf.hull = b.CreateHullUnreduced(points, len(points))
			}
			if leaf.hull == nil {
				return fmt.Errorf("%w: unreduced convex cooking failed (%d unique input points; native limits %d vertices/%d faces/%d edges)", p.ErrUnsupported, len(points), b.MaxHullVertices, b.MaxHullFaces, b.MaxHullEdges)
			}
		case p.TriangleMesh:
			var err error
			leaf.mesh, err = cookMesh(shape, p.Identity())
			if err != nil {
				return err
			}
		case p.HeightField:
			leaf.height = cookHeightField(shape.Height)
		}
		var byteCount int64
		if leaf.hull != nil {
			byteCount = int64(leaf.hull.ByteCount)
		} else if leaf.mesh != nil {
			byteCount = int64(leaf.mesh.ByteCount)
		} else if leaf.height != nil {
			byteCount = int64(leaf.height.ByteCount)
		}
		if byteCount < 0 || byteCount > settings.MaxBytes-cooked.bytes {
			return fmt.Errorf("%w: cooked image byte limit", p.ErrInvalid)
		}
		cooked.bytes += byteCount
		cooked.leaves = append(cooked.leaves, leaf)
		return nil
	}
	if err := capture(cooked.source, p.Identity()); err != nil {
		return nil, err
	}
	var size int64
	cooked.geometry, size = geometryDigest(cooked.source)
	if size > settings.MaxBytes {
		return nil, fmt.Errorf("%w: portable cook source byte limit", p.ErrInvalid)
	}
	cooked.identify()
	return cooked, nil
}

// identify derives the source hash from the shape header and the retained
// geometry digest, so rescaled copies never rehash shared vertices.
func (s *CookedShape) identify() {
	s.sourceHash = hex.EncodeToString(shapeDigest(s.source, s.geometry))
	identity, _ := json.Marshal(struct {
		Version, Upstream, Fork, Architecture, Source string
		HullVersion, MeshVersion                      uint64
		Settings                                      CookSettings
	}{CookVersion, UpstreamCommit, RequiredFork, runtime.GOARCH, s.sourceHash, b.HullVersion, b.MeshVersion, s.settings})
	s.fingerprint = cookDigest(identity)
}

// rescaled shares s's native geometry under a new top-level ID and Scale.
func (s *CookedShape) rescaled(id p.ContentID, scale p.Vec3) (*CookedShape, error) {
	if s.source.Kind != p.Convex && s.source.Kind != p.TriangleMesh || len(s.leaves) != 1 {
		return nil, fmt.Errorf("%w: only convex and mesh shapes can be rescaled", p.ErrInvalid)
	}
	desc := s.source
	desc.ID, desc.Scale = id, scale
	if err := desc.Validate(); err != nil {
		return nil, err
	}
	leaf := s.leaves[0]
	leaf.desc = desc
	next := &CookedShape{source: desc, settings: s.settings, leaves: []cookedLeaf{leaf}, geometry: s.geometry, bytes: s.bytes}
	next.identify()
	return next, nil
}

// digestWriter batches small fixed-width fields into a streaming SHA256.
type digestWriter struct {
	h    hash.Hash
	buf  []byte
	size int64
}

func newDigestWriter() *digestWriter {
	return &digestWriter{h: sha256.New(), buf: make([]byte, 0, 8192)}
}
func (d *digestWriter) u32(v uint32) {
	if len(d.buf)+4 > cap(d.buf) {
		d.flush()
	}
	d.buf = binary.LittleEndian.AppendUint32(d.buf, v)
}
func (d *digestWriter) f32(v float32) { d.u32(math.Float32bits(v)) }
func (d *digestWriter) vec(v p.Vec3)  { d.f32(v.X); d.f32(v.Y); d.f32(v.Z) }
func (d *digestWriter) bytes(v []byte) {
	d.u32(uint32(len(v)))
	d.flush()
	_, _ = d.h.Write(v)
	d.size += int64(len(v))
}
func (d *digestWriter) flush() {
	_, _ = d.h.Write(d.buf)
	d.size += int64(len(d.buf))
	d.buf = d.buf[:0]
}
func (d *digestWriter) sum() []byte { d.flush(); return d.h.Sum(nil) }

// geometryDigest hashes top-level vertex and index arrays and reports the
// streamed byte count used for the portable source size bound.
func geometryDigest(s p.ShapeDesc) ([sha256.Size]byte, int64) {
	d := newDigestWriter()
	d.u32(uint32(len(s.Vertices)))
	for _, v := range s.Vertices {
		d.vec(v)
	}
	d.u32(uint32(len(s.Indices)))
	for _, index := range s.Indices {
		d.u32(index)
	}
	if h := s.Height; h != nil {
		d.u32(uint32(h.Resolution))
		d.f32(h.SpacingX)
		d.f32(h.SpacingZ)
		d.f32(h.HeightStep)
		for i := 0; i+1 < len(h.Heights); i += 2 {
			d.u32(uint32(h.Heights[i]) | uint32(h.Heights[i+1])<<16)
		}
		if len(h.Heights)%2 != 0 {
			d.u32(uint32(h.Heights[len(h.Heights)-1]))
		}
		d.bytes(h.Solid)
	}
	var sum [sha256.Size]byte
	copy(sum[:], d.sum())
	size := d.size
	for _, child := range s.Children {
		_, n := geometryDigest(child.Shape)
		size += n
	}
	return sum, size
}

func shapeDigest(s p.ShapeDesc, geometry [sha256.Size]byte) []byte {
	d := newDigestWriter()
	d.bytes([]byte("golem-box3d-shape-v2"))
	d.bytes([]byte(s.ID))
	d.u32(uint32(s.Kind))
	d.f32(s.Radius)
	d.vec(s.PointA)
	d.vec(s.PointB)
	d.vec(s.HalfExtents)
	d.vec(s.Scale)
	d.u32(uint32(s.HullVertexLimit))
	d.bytes(geometry[:])
	d.u32(uint32(len(s.Children)))
	for _, child := range s.Children {
		d.bytes([]byte(child.ID))
		d.vec(child.Pose.Position)
		d.f32(child.Pose.Rotation.X)
		d.f32(child.Pose.Rotation.Y)
		d.f32(child.Pose.Rotation.Z)
		d.f32(child.Pose.Rotation.W)
		nested, _ := geometryDigest(child.Shape)
		d.bytes(shapeDigest(child.Shape, nested))
	}
	return d.sum()
}

func backendVertices(vertices []p.Vec3, pose p.Pose) []b.Vec3 {
	points := make([]b.Vec3, len(vertices))
	for i, vertex := range vertices {
		points[i] = bv(pose.Transform(vertex))
	}
	return points
}

func uniqueHullPoints(points []b.Vec3) []b.Vec3 {
	seen := make(map[b.Vec3]struct{}, len(points))
	unique := points[:0]
	for _, point := range points {
		if _, exists := seen[point]; !exists {
			seen[point] = struct{}{}
			unique = append(unique, point)
		}
	}
	return unique
}

func cookMesh(shape p.ShapeDesc, pose p.Pose) (*b.MeshData, error) {
	points := backendVertices(shape.Vertices, pose)
	indices := make([]int32, len(shape.Indices))
	for i, index := range shape.Indices {
		indices[i] = int32(index)
	}
	d := b.MeshDef{Vertices: points, Indices: indices, VertexCount: int32(len(points)), TriangleCount: int32(len(indices) / 3), IdentifyEdges: true}
	mesh := b.CreateMeshPreserveSmallTriangles(&d, nil)
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
		images[i] = leafImage{Kind: leaf.desc.Kind, Pose: leaf.pose}
		if leaf.hull != nil {
			images[i].Image = b.MarshalHullData(leaf.hull)
		} else if leaf.mesh != nil {
			images[i].Image = b.MarshalMeshData(leaf.mesh)
		} else if leaf.height != nil {
			images[i].Image = b.MarshalHeightFieldData(leaf.height)
		}
	}
	return json.Marshal(struct {
		Version, Backend, Fork, Architecture, SourceHash, Fingerprint string
		Settings                                                      CookSettings
		Leaves                                                        []leafImage
	}{CookVersion, UpstreamCommit, RequiredFork, runtime.GOARCH, s.sourceHash, s.fingerprint, s.settings, images})
}

// ShapePreparer returns a concurrent-safe cooker using DefaultCookSettings.
func (w *World) ShapePreparer() p.ShapePreparer { return Cooker{Settings: DefaultCookSettings()} }

// CreatePreparedShape registers a shape returned by a Box3D ShapePreparer.
func (w *World) CreatePreparedShape(prepared p.PreparedShape) (p.ShapeHandle, error) {
	cooked, ok := prepared.(*CookedShape)
	if !ok {
		return p.ShapeHandle{}, fmt.Errorf("%w: foreign prepared shape", p.ErrInvalid)
	}
	return w.CreateCookedShape(cooked)
}

// CreateScaledShape registers a shape that shares source's hull or BVH and
// differs only by ID and Scale. Source must be a top-level convex or mesh shape.
func (w *World) CreateScaledShape(source p.ShapeHandle, id p.ContentID, scale p.Vec3) (p.ShapeHandle, error) {
	if err := w.guard(); err != nil {
		return p.ShapeHandle{}, err
	}
	base, ok := w.shapes[source]
	if !ok {
		return p.ShapeHandle{}, p.ErrStale
	}
	cooked, err := base.cooked.rescaled(id, scale)
	if err != nil {
		return p.ShapeHandle{}, err
	}
	return w.CreateCookedShape(cooked)
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
	w.shapes[h] = &shape{desc: cooked.source, cooked: cooked}
	w.handleBindings[h.Index].shape = w.shapes[h]
	w.mutation++
	return h, nil
}
