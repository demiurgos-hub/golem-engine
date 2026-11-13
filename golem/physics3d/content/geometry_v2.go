package content

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
	"math/big"
	"strconv"
	"strings"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

const normalizationPrefix = "golem.physics.zeroAreaNormalization.v1."

func validateSourceAsset(asset Asset, canonical bool) error {
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
		// Explicit float32 intermediates preserve Unity MultiplyPoint3x4 rounding.
		x := float32(matrix[0] * v.X)
		x = float32(x + float32(matrix[1]*v.Y))
		x = float32(x + float32(matrix[2]*v.Z))
		x = float32(x + matrix[3])
		y := float32(matrix[4] * v.X)
		y = float32(y + float32(matrix[5]*v.Y))
		y = float32(y + float32(matrix[6]*v.Z))
		y = float32(y + matrix[7])
		z := float32(matrix[8] * v.X)
		z = float32(z + float32(matrix[9]*v.Y))
		z = float32(z + float32(matrix[10]*v.Z))
		z = float32(z + matrix[11])
		vertices[index] = p.Vec3{X: x, Y: y, Z: z}
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
		if len(provided) != 3 || provided["originalTriangleCount"] != strconv.Itoa(len(original)/3) || provided["removedTriangleOrdinals"] != strings.Join(removed, ",") || provided["originalGeometrySHA256"] != geometryHash(vertices, original) {
			return nil, nil, invalid("geometry normalization provenance mismatch")
		}
	}
	if len(indices) == 0 {
		return nil, nil, invalid("geometry has no nonzero-area triangles")
	}
	return vertices, indices, nil
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
