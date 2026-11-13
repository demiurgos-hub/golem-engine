// Package geometry reads bounded portable mesh and heightfield sidecars shared
// by Scribe physics and navigation sources. It contains no runtime or backend.
package geometry

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const Magic = "GOLEMGE1"

type Kind uint8

const (
	MeshFloat32        Kind = 1
	HeightfieldFloat32 Kind = 2
	MeshFloat64        Kind = 3
)

type Vec3 struct{ X, Y, Z float64 }
type Triangle struct {
	Indices [3]uint32
	Area    uint8
}
type Heightfield struct {
	Resolution uint32
	Size       [3]float32
	Samples    []float32
	Surface    []byte
	Area       uint8
}
type Asset struct {
	ID, SourceID string
	Kind         Kind
	Vertices     []Vec3
	Triangles    []Triangle
	Heightfield  *Heightfield
}
type Reference struct{ Path, SHA256 string }
type Limits struct {
	Bytes                       int64
	Assets, Vertices, Triangles int
}
type Resolver func(path string) (io.ReadCloser, error)

// DefaultLimits bounds serialized bytes and complete expanded source geometry.
func DefaultLimits() Limits { return Limits{256 << 20, 100_000, 10_000_000, 10_000_000} }

// FileResolver confines sidecars, including resolved symlinks, to a source folder.
func FileResolver(directory string) Resolver {
	return func(path string) (io.ReadCloser, error) {
		if err := validPath(path); err != nil {
			return nil, err
		}
		root, err := filepath.EvalSymlinks(directory)
		if err != nil {
			return nil, err
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		target, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return nil, errors.New("geometry sidecar escapes source folder")
		}
		return os.Open(target)
	}
}
func validPath(path string) error {
	if path == "" || strings.ContainsAny(path, "\\:\x00") || strings.HasPrefix(path, "/") {
		return errors.New("geometry needs a contained relative sidecar path")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("geometry needs a contained relative sidecar path")
		}
	}
	return nil
}

// Read resolves one sidecar, verifies exact bytes, and strictly decodes it.
func Read(reference Reference, resolve Resolver, limits Limits) ([]Asset, error) {
	if err := validPath(reference.Path); err != nil {
		return nil, err
	}
	expected, err := hex.DecodeString(reference.SHA256)
	if err != nil || len(expected) != sha256.Size || resolve == nil {
		return nil, errors.New("geometry sidecar needs SHA256 and resolver")
	}
	r, err := resolve(reference.Path)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := readBounded(r, limits)
	if err != nil {
		return nil, err
	}
	actual := sha256.Sum256(data)
	if !bytes.Equal(actual[:], expected) {
		return nil, errors.New("geometry sidecar hash mismatch")
	}
	return decode(data, limits)
}

// Decode reads one complete binary geometry sidecar with strict resource bounds.
func Decode(reader io.Reader, limits Limits) ([]Asset, error) {
	data, err := readBounded(reader, limits)
	if err != nil {
		return nil, err
	}
	return decode(data, limits)
}
func readBounded(reader io.Reader, limits Limits) ([]byte, error) {
	if reader == nil || limits.Bytes < 1 || limits.Bytes == math.MaxInt64 || limits.Assets < 0 || limits.Vertices < 0 || limits.Triangles < 0 {
		return nil, errors.New("invalid geometry limits")
	}
	data, err := io.ReadAll(io.LimitReader(reader, limits.Bytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limits.Bytes {
		return nil, errors.New("geometry byte limit")
	}
	return data, nil
}

type cursor struct {
	data []byte
	at   int
	err  error
}

func (r *cursor) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || n > len(r.data)-r.at {
		r.err = io.ErrUnexpectedEOF
		return nil
	}
	p := r.data[r.at : r.at+n]
	r.at += n
	return p
}
func (r *cursor) u8() uint8 {
	p := r.take(1)
	if p == nil {
		return 0
	}
	return p[0]
}
func (r *cursor) u32() uint32 {
	p := r.take(4)
	if p == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(p)
}
func (r *cursor) f32() float32 { return math.Float32frombits(r.u32()) }
func (r *cursor) f64() float64 {
	p := r.take(8)
	if p == nil {
		return 0
	}
	return math.Float64frombits(binary.LittleEndian.Uint64(p))
}
func (r *cursor) str() string {
	n := r.u32()
	if n > 4096 {
		r.err = errors.New("geometry identity byte limit")
		return ""
	}
	p := r.take(int(n))
	if !utf8.Valid(p) {
		r.err = errors.New("invalid geometry UTF8")
	}
	return string(p)
}
func decode(data []byte, limits Limits) ([]Asset, error) {
	r := cursor{data: data}
	if string(r.take(8)) != Magic {
		return nil, errors.New("invalid geometry magic/version")
	}
	count := r.u32()
	if uint64(count) > uint64(limits.Assets) {
		return nil, errors.New("geometry asset limit")
	}
	assets := make([]Asset, 0, int(count))
	seen := map[string]bool{}
	var vertices, triangles uint64
	for i := uint32(0); i < count; i++ {
		a := Asset{ID: r.str(), SourceID: r.str(), Kind: Kind(r.u8())}
		if a.ID == "" || seen[a.ID] {
			return nil, errors.New("invalid or duplicate geometry identity")
		}
		seen[a.ID] = true
		switch a.Kind {
		case MeshFloat32, MeshFloat64:
			nv, nt := r.u32(), r.u32()
			vertices += uint64(nv)
			triangles += uint64(nt)
			if nv < 3 || nt < 1 || vertices > uint64(limits.Vertices) || triangles > uint64(limits.Triangles) {
				return nil, errors.New("geometry expansion limit")
			}
			width := uint64(12)
			if a.Kind == MeshFloat64 {
				width = 24
			}
			if uint64(nv)*width+uint64(nt)*13 > uint64(len(data)-r.at) {
				return nil, io.ErrUnexpectedEOF
			}
			a.Vertices = make([]Vec3, int(nv))
			a.Triangles = make([]Triangle, int(nt))
			for j := range a.Vertices {
				v := Vec3{}
				if a.Kind == MeshFloat32 {
					v = Vec3{float64(r.f32()), float64(r.f32()), float64(r.f32())}
				} else {
					v = Vec3{r.f64(), r.f64(), r.f64()}
				}
				if !finite(v.X) || !finite(v.Y) || !finite(v.Z) {
					return nil, errors.New("nonfinite geometry vertex")
				}
				a.Vertices[j] = v
			}
			for j := range a.Triangles {
				t := Triangle{[3]uint32{r.u32(), r.u32(), r.u32()}, r.u8()}
				if t.Area > 63 || t.Indices[0] >= nv || t.Indices[1] >= nv || t.Indices[2] >= nv {
					return nil, errors.New("invalid geometry triangle")
				}
				a.Triangles[j] = t
			}
		case HeightfieldFloat32:
			h := &Heightfield{Resolution: r.u32(), Size: [3]float32{r.f32(), r.f32(), r.f32()}}
			a.Heightfield = h
			n := uint64(h.Resolution)
			if n < 2 || n > uint64(limits.Vertices) {
				return nil, errors.New("invalid heightfield resolution")
			}
			cells := (n - 1) * (n - 1)
			samples := r.u32()
			vertices += n * n
			triangles += cells * 2
			if uint64(samples) != n*n || vertices > uint64(limits.Vertices) || triangles > uint64(limits.Triangles) {
				return nil, errors.New("heightfield expansion limit")
			}
			for _, v := range h.Size {
				if !finite(float64(v)) || v <= 0 {
					return nil, errors.New("invalid heightfield size")
				}
			}
			if uint64(samples)*4 > uint64(len(data)-r.at) {
				return nil, io.ErrUnexpectedEOF
			}
			h.Samples = make([]float32, int(samples))
			for j := range h.Samples {
				v := r.f32()
				if !finite(float64(v)) || v < 0 || v > 1 {
					return nil, errors.New("invalid normalized height")
				}
				h.Samples[j] = v
			}
			bytes := r.u32()
			if uint64(bytes) != (cells+7)/8 {
				return nil, errors.New("invalid heightfield surface bytes")
			}
			h.Surface = append([]byte(nil), r.take(int(bytes))...)
			h.Area = r.u8()
			if r.err != nil {
				return nil, r.err
			}
			if h.Area > 63 || cells%8 != 0 && h.Surface[len(h.Surface)-1]>>uint(cells%8) != 0 {
				return nil, errors.New("invalid heightfield area or bit padding")
			}
		default:
			return nil, errors.New("unknown geometry asset kind")
		}
		if r.err != nil {
			return nil, r.err
		}
		assets = append(assets, a)
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.at != len(data) {
		return nil, errors.New("trailing geometry bytes")
	}
	return assets, nil
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// ExpandHeightfield derives indexed terrain from a validated Decode/Read asset
// without changing samples or holes. Physical terrain uses the a-d diagonal;
// navigation retains b-c. Non-heightfield assets pass through unchanged.
func ExpandHeightfield(asset Asset, physical bool) Asset {
	if asset.Kind != HeightfieldFloat32 {
		return asset
	}
	h := asset.Heightfield
	n := int(h.Resolution)
	asset.Kind = MeshFloat64
	asset.Vertices = make([]Vec3, n*n)
	asset.Triangles = make([]Triangle, 0, 2*(n-1)*(n-1))
	asset.Heightfield = nil
	for z := 0; z < n; z++ {
		for x := 0; x < n; x++ {
			asset.Vertices[z*n+x] = Vec3{float64(x) * float64(h.Size[0]) / float64(n-1), float64(h.Samples[z*n+x]) * float64(h.Size[1]), float64(z) * float64(h.Size[2]) / float64(n-1)}
		}
	}
	for z := 0; z < n-1; z++ {
		for x := 0; x < n-1; x++ {
			cell := z*(n-1) + x
			if h.Surface[cell/8]&(1<<uint(cell%8)) == 0 {
				continue
			}
			a := uint32(z*n + x)
			b, c, d := a+1, a+uint32(n), a+uint32(n)+1
			if physical {
				asset.Triangles = append(asset.Triangles, Triangle{[3]uint32{a, c, d}, h.Area}, Triangle{[3]uint32{a, d, b}, h.Area})
			} else {
				asset.Triangles = append(asset.Triangles, Triangle{[3]uint32{a, c, b}, h.Area}, Triangle{[3]uint32{b, c, d}, h.Area})
			}
		}
	}
	return asset
}

// Encode writes the canonical little-endian portable binary format after full
// validation, preserving float32 and float64 geometry precision separately.
func Encode(writer io.Writer, assets []Asset, limits Limits) error {
	if writer == nil || limits.Bytes < 1 || limits.Assets < 0 || len(assets) > limits.Assets {
		return errors.New("invalid geometry output limits")
	}
	// Reject oversized authoring input before allocating its serialized image.
	size := uint64(12)
	for _, asset := range assets {
		size += uint64(9 + len(asset.ID) + len(asset.SourceID))
		switch asset.Kind {
		case MeshFloat32, MeshFloat64:
			width := uint64(12)
			if asset.Kind == MeshFloat64 {
				width = 24
			}
			size += 8 + uint64(len(asset.Vertices))*width + uint64(len(asset.Triangles))*13
		case HeightfieldFloat32:
			if asset.Heightfield == nil {
				return errors.New("missing heightfield")
			}
			size += 25 + uint64(len(asset.Heightfield.Samples))*4 + uint64(len(asset.Heightfield.Surface))
		default:
			return errors.New("unknown geometry asset kind")
		}
		if size > uint64(limits.Bytes) {
			return errors.New("geometry byte limit")
		}
	}
	var b bytes.Buffer
	b.WriteString(Magic)
	put := func(value any) { _ = binary.Write(&b, binary.LittleEndian, value) }
	str := func(value string) { put(uint32(len(value))); b.WriteString(value) }
	put(uint32(len(assets)))
	for _, a := range assets {
		str(a.ID)
		str(a.SourceID)
		put(uint8(a.Kind))
		switch a.Kind {
		case MeshFloat32, MeshFloat64:
			put(uint32(len(a.Vertices)))
			put(uint32(len(a.Triangles)))
			for _, v := range a.Vertices {
				if a.Kind == MeshFloat32 {
					put(float32(v.X))
					put(float32(v.Y))
					put(float32(v.Z))
				} else {
					put(v.X)
					put(v.Y)
					put(v.Z)
				}
			}
			for _, t := range a.Triangles {
				put(t.Indices)
				put(t.Area)
			}
		case HeightfieldFloat32:
			if a.Heightfield == nil {
				return errors.New("missing heightfield")
			}
			h := a.Heightfield
			put(h.Resolution)
			put(h.Size)
			put(uint32(len(h.Samples)))
			for _, v := range h.Samples {
				put(v)
			}
			put(uint32(len(h.Surface)))
			b.Write(h.Surface)
			put(h.Area)
		default:
			return errors.New("unknown geometry asset kind")
		}
	}
	if _, err := Decode(bytes.NewReader(b.Bytes()), limits); err != nil {
		return fmt.Errorf("encode geometry: %w", err)
	}
	_, err := writer.Write(b.Bytes())
	return err
}
