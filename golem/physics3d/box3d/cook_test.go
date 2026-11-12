package box3d

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
	b "github.com/gataengine/box3d-go"
)

func cookWorld(t *testing.T) *World {
	t.Helper()
	w, err := New(p.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	return w
}

func TestCookedShapeOwnsSourceAndNativeImages(t *testing.T) {
	source := p.ShapeDesc{Kind: p.TriangleMesh, Vertices: []p.Vec3{{X: -1, Z: -1}, {X: -1, Z: 1}, {X: 1, Z: -1}}, Indices: []uint32{0, 1, 2}}
	cooked, err := CookShape(source, DefaultCookSettings())
	if err != nil {
		t.Fatal(err)
	}
	source.Vertices[0].Y = 100
	copy := cooked.Source()
	copy.Vertices[0].Y = -100
	if cooked.Source().Vertices[0].Y != 0 || cooked.NativeBytes() == 0 {
		t.Fatal("cooking did not own geometry")
	}
	encoded, err := cooked.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var image struct {
		Fingerprint string
		Leaves      []struct{ Image []byte }
	}
	if err = json.Unmarshal(encoded, &image); err != nil {
		t.Fatal(err)
	}
	if image.Fingerprint != cooked.Fingerprint() || len(image.Leaves) != 1 || binary.LittleEndian.Uint64(image.Leaves[0].Image) != b.MeshVersion {
		t.Fatal("artifact does not contain actual mesh BVH")
	}
	image.Leaves[0].Image[0] ^= 255
	again, _ := cooked.MarshalBinary()
	if !bytes.Equal(encoded, again) {
		t.Fatal("artifact aliases private data")
	}
	settings := DefaultCookSettings()
	settings.MaxBytes = 1
	if _, err = CookShape(cooked.Source(), settings); !errors.Is(err, p.ErrInvalid) {
		t.Fatal("cooked bound", err)
	}
}

func TestCookedMeshLocalPoseAndCompound(t *testing.T) {
	w := cookWorld(t)
	mesh := p.ShapeDesc{Kind: p.TriangleMesh, Vertices: []p.Vec3{{X: -2, Z: -2}, {X: -2, Z: 2}, {X: 2, Z: -2}, {X: 2, Z: 2}}, Indices: []uint32{0, 1, 2, 2, 1, 3}}
	source := p.ShapeDesc{Kind: p.Compound, Children: []p.ShapeChild{{ID: "floor", Pose: p.Pose{Position: p.Vec3{Y: 1}, Rotation: p.Quat{W: 1}}, Shape: mesh}}}
	shape, err := w.CreateShape(source)
	if err != nil {
		t.Fatal(err)
	}
	body, err := w.CreateBody(p.DefaultBody(p.Static))
	if err != nil {
		t.Fatal(err)
	}
	d := p.DefaultCollider(body, shape)
	d.LocalPose.Position.Y = 2
	collider, err := w.CreateCollider(d)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Synchronize(); err != nil {
		t.Fatal(err)
	}
	hit, err := w.ClosestPoint(collider, p.Vec3{X: .2, Y: 4, Z: .1})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(float64(hit.Point.Y-3)) > .0001 {
		t.Fatal("local mesh/compound pose lost", hit)
	}
}

func TestManualMassPropertiesSurviveColliderChanges(t *testing.T) {
	w := cookWorld(t)
	d := p.DefaultBody(p.FreeDynamic)
	d.Mass = 5
	d.ManualCenterOfMass = true
	d.CenterOfMass = p.Vec3{X: .25}
	d.ManualInertia = true
	d.InertiaTensor = p.Vec3{X: 2, Y: 4, Z: 8}
	d.InertiaRotation = p.Quat{Z: float32(math.Sqrt(.5)), W: float32(math.Sqrt(.5))}
	body, err := w.CreateBody(d)
	if err != nil {
		t.Fatal(err)
	}
	shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: .5})
	if err != nil {
		t.Fatal(err)
	}
	first, err := w.CreateCollider(p.DefaultCollider(body, shape))
	if err != nil {
		t.Fatal(err)
	}
	dc := p.DefaultCollider(body, shape)
	dc.LocalPose.Position.X = 2
	second, err := w.CreateCollider(dc)
	if err != nil {
		t.Fatal(err)
	}
	if err = w.DestroyCollider(first, p.Deleted); err != nil {
		t.Fatal(err)
	}
	m := w.bodies[body].id.GetMassData()
	if m.Mass != 5 || m.Center.X != .25 || math.Abs(float64(m.Inertia.Cx.X-4)) > 1e-5 || math.Abs(float64(m.Inertia.Cy.Y-2)) > 1e-5 {
		t.Fatal("manual mass drifted", m)
	}
	if err = w.DestroyCollider(second, p.Deleted); err != nil {
		t.Fatal(err)
	}
	if m = w.bodies[body].id.GetMassData(); m.Mass != 5 || m.Center.X != .25 {
		t.Fatal("empty body lost authored mass", m)
	}
}

func TestUnityPortableFixtureLoadsIntoSolver(t *testing.T) {
	data, err := os.ReadFile("../../../golem-unity/Tests/Fixtures/physics-source.json")
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := content.Decode(bytes.NewReader(data), content.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	w := cookWorld(t)
	instance, err := content.Load(w, source, func(string) (int64, error) { return 71, nil })
	if err != nil {
		t.Fatal(err)
	}
	body, ok := instance.Body("actor")
	if !ok {
		t.Fatal("source body identity")
	}
	collider, ok := instance.Collider("actor/sphere")
	if !ok {
		t.Fatal("source collider identity")
	}
	state, err := w.Body(body)
	if err != nil || state.Owner != 71 || state.Pose.Position.Y != 2 {
		t.Fatal(state, err)
	}
	hit, err := w.ClosestPoint(collider, p.Vec3{Y: 4})
	if err != nil || math.Abs(float64(hit.Point.Y-2.5)) > .0001 {
		t.Fatal(hit, err)
	}
	if err = instance.Remove(); err != nil {
		t.Fatal(err)
	}
	if m := w.Metrics(); m.Bodies != 0 || m.Colliders != 0 || m.Shapes != 0 {
		t.Fatal("load/remove leaked", m)
	}
}

func TestLoadedJointUsesBodyOriginFramesAndWorldAnchor(t *testing.T) {
	data, err := os.ReadFile("../../../golem-unity/Tests/Fixtures/physics-source.json")
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := content.Decode(bytes.NewReader(data), content.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	source.Joints = []content.Joint{{ID: "actor/weld", Kind: "fixed", BodyID: "actor", Active: true, EnablePreprocessing: true,
		MassScale: 1, ConnectedMassScale: 1, Axis: p.Vec3{X: 1}, SecondaryAxis: p.Vec3{Y: 1}, ReferenceRotation: p.Quat{W: 1}, ConnectedAnchor: p.Vec3{Y: 2}}}
	w := cookWorld(t)
	instance, err := content.Load(w, source, func(string) (int64, error) { return 71, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := instance.Joint("actor/weld"); !ok {
		t.Fatal("joint identity missing")
	}
	body, _ := instance.Body("actor")
	for n := uint64(1); n <= 5; n++ {
		if _, err = w.Simulate(p.Frame{ID: n, Delta: .05}); err != nil {
			t.Fatal(err)
		}
		if err = w.Accept(n); err != nil {
			t.Fatal(err)
		}
	}
	state, err := w.Body(body)
	if err != nil || math.Abs(float64(state.Pose.Position.Y-2)) > .001 {
		t.Fatal("authored world anchor moved", state, err)
	}
	if err = instance.Remove(); err != nil {
		t.Fatal(err)
	}
	if w.Metrics().Bodies != 0 || w.Metrics().Joints != 0 {
		t.Fatal("world-anchor joint cleanup leaked")
	}
}
