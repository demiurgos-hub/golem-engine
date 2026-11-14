package content

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/big"
	"math/bits"
	"strconv"
	"strings"

	"github.com/demiurgos-hub/golem-engine/golem/geometry"
	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

const normalizationPrefix = "golem.physics.zeroAreaNormalization.v1."

var identityMatrix = [16]float32{0: 1, 5: 1, 10: 1, 15: 1}

// unityHeightSteps is the Unity terrain sample range: normalized heights are
// k/32766 for integer k.
const unityHeightSteps = 32766

// nativeHeightTolerance bounds a sample's distance from the Unity 16-bit grid,
// in grid steps, before native terrain falls back to an expanded mesh.
const nativeHeightTolerance = 1e-3

// nativeHeightField quantizes Unity-authored samples. Grid spacing and step are
// rounded to float32 once, so points can differ from the expanded mesh by a few
// float32 ulps.
func nativeHeightField(h *geometry.Heightfield) (*p.HeightFieldDesc, bool) {
	n := int(h.Resolution)
	if n < 2 || n > p.MaxHeightFieldResolution || len(h.Samples) != n*n {
		return nil, false
	}
	heights := make([]uint16, len(h.Samples))
	for i, sample := range h.Samples {
		scaled := float64(sample) * unityHeightSteps
		q := math.Round(scaled)
		if math.Abs(scaled-q) > nativeHeightTolerance || q < 0 || q > unityHeightSteps {
			return nil, false
		}
		heights[i] = uint16(q)
	}
	return &p.HeightFieldDesc{Resolution: n, SpacingX: float32(float64(h.Size[0]) / float64(n-1)), SpacingZ: float32(float64(h.Size[2]) / float64(n-1)),
		HeightStep: float32(float64(h.Size[1]) / unityHeightSteps), Heights: heights, Solid: h.Surface}, true
}

// expanded returns the physical triangle mesh of a heightfield asset, or the
// asset itself when it is already a mesh.
func (a Asset) expanded() Asset {
	if a.Heightfield == nil {
		return a
	}
	mesh := geometry.ExpandHeightfield(geometry.Asset{ID: a.ID, SourceID: a.SourceID, Kind: geometry.HeightfieldFloat32, Heightfield: a.Heightfield}, true)
	value := Asset{ID: a.ID, SourceID: a.SourceID, Vertices: make([]p.Vec3, len(mesh.Vertices)), Indices: make([]uint32, 0, len(mesh.Triangles)*3)}
	for index, vertex := range mesh.Vertices {
		value.Vertices[index] = p.Vec3{X: float32(vertex.X), Y: float32(vertex.Y), Z: float32(vertex.Z)}
	}
	for _, triangle := range mesh.Triangles {
		value.Indices = append(value.Indices, triangle.Indices[:]...)
	}
	return value
}

func validateHeightfieldAsset(asset Asset) error {
	h := asset.Heightfield
	if len(asset.Vertices) != 0 || len(asset.Indices) != 0 {
		return invalid("heightfield asset with mesh data")
	}
	n := uint64(h.Resolution)
	if n < 2 || n > p.MaxHeightFieldResolution || n*n > 1_000_000 || uint64(len(h.Samples)) != n*n {
		return invalid("heightfield resolution")
	}
	for _, v := range h.Size {
		if !positive(v) {
			return invalid("heightfield size")
		}
	}
	for _, v := range h.Samples {
		if !p.Finite(v) || v < 0 || v > 1 {
			return invalid("normalized height")
		}
	}
	cells := (n - 1) * (n - 1)
	if uint64(len(h.Surface)) != (cells+7)/8 || h.Area > 63 || cells%8 != 0 && h.Surface[len(h.Surface)-1]>>uint(cells%8) != 0 {
		return invalid("heightfield surface")
	}
	solid := 0
	for _, value := range h.Surface {
		solid += bits.OnesCount8(value)
	}
	if solid == 0 || 6*solid > 3_000_000 {
		return invalid("canonical mesh counts")
	}
	return nil
}

func validateSourceAsset(asset Asset, canonical bool) error {
	if asset.Heightfield != nil {
		if !canonical {
			return invalid("heightfield asset requires version 2")
		}
		return validateHeightfieldAsset(asset)
	}
	if !canonical {
		return (p.ShapeDesc{Kind: p.TriangleMesh, Vertices: asset.Vertices, Indices: asset.Indices}).Validate()
	}
	if len(asset.Vertices) < 3 || len(asset.Vertices) > 1_000_000 || len(asset.Indices) == 0 || len(asset.Indices)%3 != 0 || len(asset.Indices) > 3_000_000 {
		return invalid("canonical mesh counts")
	}
	for _, vertex := range asset.Vertices {
		if !vertex.Valid() {
			return invalid("canonical mesh vertex")
		}
	}
	for _, index := range asset.Indices {
		if uint64(index) >= uint64(len(asset.Vertices)) {
			return invalid("canonical mesh index")
		}
	}
	return nil
}

func transformedGeometry(asset Asset, matrix [16]float32, metadata []Property) ([]p.Vec3, []uint32, error) {
	for _, value := range matrix {
		if !p.Finite(value) {
			return nil, nil, invalid("geometry matrix")
		}
	}
	if matrix[12] != 0 || matrix[13] != 0 || matrix[14] != 0 || matrix[15] != 1 {
		return nil, nil, invalid("non-affine geometry matrix")
	}
	determinant := float64(matrix[0])*(float64(matrix[5])*float64(matrix[10])-float64(matrix[6])*float64(matrix[9])) - float64(matrix[1])*(float64(matrix[4])*float64(matrix[10])-float64(matrix[6])*float64(matrix[8])) + float64(matrix[2])*(float64(matrix[4])*float64(matrix[9])-float64(matrix[5])*float64(matrix[8]))
	if math.Abs(determinant) <= 1e-12 {
		return nil, nil, invalid("singular geometry matrix")
	}
	vertices := make([]p.Vec3, len(asset.Vertices))
	for index, v := range asset.Vertices {
		// Scribe explicitly accumulates in double and stores float32 once.
		// Float32 products are exact in float64; explicit sums prevent a Go
		// implementation from fusing across the specified rounding boundaries.
		coordinate := func(row int) float32 {
			value := float64(matrix[row]) * float64(v.X)
			value = float64(value + float64(matrix[row+1])*float64(v.Y))
			value = float64(value + float64(matrix[row+2])*float64(v.Z))
			return float32(float64(value + float64(matrix[row+3])))
		}
		vertices[index] = p.Vec3{X: coordinate(0), Y: coordinate(4), Z: coordinate(8)}
		if !vertices[index].Valid() {
			return nil, nil, invalid("nonfinite transformed geometry")
		}
	}
	original := append([]uint32(nil), asset.Indices...)
	if determinant < 0 {
		for i := 0; i < len(original); i += 3 {
			original[i+1], original[i+2] = original[i+2], original[i+1]
		}
	}
	indices := make([]uint32, 0, len(original))
	var removed []string
	for i := 0; i < len(original); i += 3 {
		if exactZeroArea(vertices[original[i]], vertices[original[i+1]], vertices[original[i+2]]) {
			removed = append(removed, strconv.Itoa(i/3))
			continue
		}
		indices = append(indices, original[i:i+3]...)
	}
	provided := map[string]string{}
	for _, property := range metadata {
		if strings.HasPrefix(property.Key, normalizationPrefix) {
			provided[strings.TrimPrefix(property.Key, normalizationPrefix)] = property.Value
		}
	}
	if len(removed) > 0 || len(provided) > 0 {
		if len(provided) != 3 || provided["originalTriangleCount"] != strconv.Itoa(len(original)/3) || provided["removedTriangleOrdinals"] != strings.Join(removed, ",") {
			return nil, nil, invalid("geometry normalization triangle provenance mismatch")
		}
		if actual := geometryHash(vertices, original); provided["originalGeometrySHA256"] != actual {
			return nil, nil, invalid("geometry normalization hash mismatch: recorded %s, resolved %s", provided["originalGeometrySHA256"], actual)
		}
	}
	if len(indices) == 0 {
		return nil, nil, invalid("geometry has no nonzero-area triangles")
	}
	return vertices, indices, nil
}

// shareTolerance bounds the normalized column dot products accepted as a
// rotation with per-axis scale; larger shear keeps transformed copies.
const shareTolerance = 1e-5

func hasNormalization(metadata []Property) bool {
	for _, property := range metadata {
		if strings.HasPrefix(property.Key, normalizationPrefix) {
			return true
		}
	}
	return false
}

// shareableMesh reports whether untransformed asset triangles all have nonzero
// area, so a shared BVH keeps the same triangles a transformed copy would.
func shareableMesh(asset Asset) bool {
	v := asset.Vertices
	for i := 0; i+2 < len(asset.Indices); i += 3 {
		a, b, c := v[asset.Indices[i]], v[asset.Indices[i+1]], v[asset.Indices[i+2]]
		if b.Sub(a).Cross(c.Sub(a)).Length() == 0 || exactZeroArea(a, b, c) {
			return false
		}
	}
	return len(asset.Indices) > 0
}

// decomposeGeometryTransform splits an affine row-major matrix into a rigid
// pose and per-axis scale applied first. Mirroring negates the X scale.
func decomposeGeometryTransform(matrix [16]float32) (p.Pose, p.Vec3, bool) {
	for _, value := range matrix {
		if !p.Finite(value) {
			return p.Pose{}, p.Vec3{}, false
		}
	}
	if matrix[12] != 0 || matrix[13] != 0 || matrix[14] != 0 || matrix[15] != 1 {
		return p.Pose{}, p.Vec3{}, false
	}
	var r [3][3]float64
	var s [3]float64
	for column := 0; column < 3; column++ {
		x, y, z := float64(matrix[column]), float64(matrix[4+column]), float64(matrix[8+column])
		n := math.Sqrt(x*x + y*y + z*z)
		if n < float64(p.MinShapeScale) {
			return p.Pose{}, p.Vec3{}, false
		}
		s[column], r[column] = n, [3]float64{x / n, y / n, z / n}
	}
	dot := func(a, b [3]float64) float64 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
	if math.Abs(dot(r[0], r[1])) > shareTolerance || math.Abs(dot(r[0], r[2])) > shareTolerance || math.Abs(dot(r[1], r[2])) > shareTolerance {
		return p.Pose{}, p.Vec3{}, false
	}
	cross := [3]float64{r[1][1]*r[2][2] - r[1][2]*r[2][1], r[1][2]*r[2][0] - r[1][0]*r[2][2], r[1][0]*r[2][1] - r[1][1]*r[2][0]}
	if dot(r[0], cross) < 0 {
		s[0], r[0] = -s[0], [3]float64{-r[0][0], -r[0][1], -r[0][2]}
	}
	// m(row, column) of the rotation whose columns are r.
	m := func(row, column int) float64 { return r[column][row] }
	var x, y, z, w float64
	if trace := m(0, 0) + m(1, 1) + m(2, 2); trace > 0 {
		k := math.Sqrt(trace+1) * 2
		w, x, y, z = k/4, (m(2, 1)-m(1, 2))/k, (m(0, 2)-m(2, 0))/k, (m(1, 0)-m(0, 1))/k
	} else if m(0, 0) > m(1, 1) && m(0, 0) > m(2, 2) {
		k := math.Sqrt(1+m(0, 0)-m(1, 1)-m(2, 2)) * 2
		w, x, y, z = (m(2, 1)-m(1, 2))/k, k/4, (m(0, 1)+m(1, 0))/k, (m(0, 2)+m(2, 0))/k
	} else if m(1, 1) > m(2, 2) {
		k := math.Sqrt(1+m(1, 1)-m(0, 0)-m(2, 2)) * 2
		w, x, y, z = (m(0, 2)-m(2, 0))/k, (m(0, 1)+m(1, 0))/k, k/4, (m(1, 2)+m(2, 1))/k
	} else {
		k := math.Sqrt(1+m(2, 2)-m(0, 0)-m(1, 1)) * 2
		w, x, y, z = (m(1, 0)-m(0, 1))/k, (m(0, 2)+m(2, 0))/k, (m(1, 2)+m(2, 1))/k, k/4
	}
	n := math.Sqrt(x*x + y*y + z*z + w*w)
	pose := p.Pose{Position: p.Vec3{X: matrix[3], Y: matrix[7], Z: matrix[11]}, Rotation: p.Quat{X: float32(x / n), Y: float32(y / n), Z: float32(z / n), W: float32(w / n)}}
	scale := p.Vec3{X: float32(s[0]), Y: float32(s[1]), Z: float32(s[2])}
	if !pose.Position.InBounds() || !pose.Rotation.Valid() {
		return p.Pose{}, p.Vec3{}, false
	}
	return pose, scale, true
}

func geometryHash(vertices []p.Vec3, indices []uint32) string {
	h := sha256.New()
	var raw [4]byte
	write := func(value uint32) { binary.LittleEndian.PutUint32(raw[:], value); _, _ = h.Write(raw[:]) }
	write(uint32(len(vertices)))
	for _, v := range vertices {
		write(math.Float32bits(v.X))
		write(math.Float32bits(v.Y))
		write(math.Float32bits(v.Z))
	}
	write(uint32(len(indices)))
	for _, index := range indices {
		write(index)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// The ordinary case is separated by a conservative floating-point error bound.
// Near-collinear input falls back to exact integer arithmetic on float32 bits;
// no area epsilon may remove a small but nonzero triangle.
func exactZeroArea(a, b, c p.Vec3) bool {
	if a == b || a == c || b == c {
		return true
	}
	x := [3]float64{float64(a.X), float64(a.Y), float64(a.Z)}
	y := [3]float64{float64(b.X), float64(b.Y), float64(b.Z)}
	z := [3]float64{float64(c.X), float64(c.Y), float64(c.Z)}
	for axis := 0; axis < 3; axis++ {
		j, k := (axis+1)%3, (axis+2)%3
		cross := (y[j]-x[j])*(z[k]-x[k]) - (y[k]-x[k])*(z[j]-x[j])
		bound := 16 * 0x1p-52 * ((math.Abs(y[j])+math.Abs(x[j]))*(math.Abs(z[k])+math.Abs(x[k])) + (math.Abs(y[k])+math.Abs(x[k]))*(math.Abs(z[j])+math.Abs(x[j])))
		if math.Abs(cross) > bound {
			return false
		}
	}
	var u, v [3]big.Int
	for axis := 0; axis < 3; axis++ {
		base := exactCoordinate(float32(x[axis]))
		u[axis].Sub(exactCoordinate(float32(y[axis])), base)
		v[axis].Sub(exactCoordinate(float32(z[axis])), base)
	}
	var left, right big.Int
	for axis := 0; axis < 3; axis++ {
		j, k := (axis+1)%3, (axis+2)%3
		left.Mul(&u[j], &v[k])
		right.Mul(&u[k], &v[j])
		if left.Cmp(&right) != 0 {
			return false
		}
	}
	return true
}
func exactCoordinate(value float32) *big.Int {
	bits := math.Float32bits(value)
	exponent := (bits >> 23) & 255
	significand := bits & 0x7fffff
	if exponent != 0 {
		significand |= 1 << 23
	}
	result := new(big.Int).SetUint64(uint64(significand))
	if exponent > 0 {
		result.Lsh(result, uint(exponent-1))
	}
	if bits>>31 != 0 {
		result.Neg(result)
	}
	return result
}
