package content

import (
	"bytes"
	"errors"
	"os"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func fixtureSource(t *testing.T) *Source {
	t.Helper()
	data, err := os.ReadFile("../../../golem-unity/Tests/Fixtures/physics-source.json")
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := Decode(bytes.NewReader(data), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return source
}

type loadWorld struct {
	p.World
	next         uint32
	bodies       map[p.BodyHandle]p.BodyDesc
	shapes       map[p.ShapeHandle]p.ShapeDesc
	colliders    map[p.ColliderHandle]p.ColliderDesc
	createCalls  int
	failCollider bool
	failCleanup  bool
}

func newLoadWorld() *loadWorld {
	return &loadWorld{bodies: map[p.BodyHandle]p.BodyDesc{}, shapes: map[p.ShapeHandle]p.ShapeDesc{}, colliders: map[p.ColliderHandle]p.ColliderDesc{}}
}
func (w *loadWorld) handle() p.Handle {
	w.next++
	w.createCalls++
	return p.Handle{World: 1, Index: w.next, Generation: 1}
}
func (w *loadWorld) CreateBody(d p.BodyDesc) (p.BodyHandle, error) {
	h := p.BodyHandle(w.handle())
	w.bodies[h] = d
	return h, nil
}
func (w *loadWorld) CreateShape(d p.ShapeDesc) (p.ShapeHandle, error) {
	h := p.ShapeHandle(w.handle())
	w.shapes[h] = d
	return h, nil
}
func (w *loadWorld) CreateCollider(d p.ColliderDesc) (p.ColliderHandle, error) {
	if w.failCollider {
		return p.ColliderHandle{}, errors.New("injected collider failure")
	}
	h := p.ColliderHandle(w.handle())
	w.colliders[h] = d
	return h, nil
}
func (w *loadWorld) DestroyBody(h p.BodyHandle, _ p.CleanupReason) error {
	if w.failCleanup {
		return errors.New("injected cleanup failure")
	}
	delete(w.bodies, h)
	for id, c := range w.colliders {
		if c.Body == h {
			delete(w.colliders, id)
		}
	}
	return nil
}
func (w *loadWorld) DestroyShape(h p.ShapeHandle) error { delete(w.shapes, h); return nil }
func (w *loadWorld) Synchronize() error                 { return nil }

func TestPreparedCaptureAndPolicyIdentity(t *testing.T) {
	source := fixtureSource(t)
	prepared, err := Prepare(source, DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	old := prepared.SourceHash()
	source.Bodies[0].Position.Y = 99
	copy := prepared.Source()
	copy.Bodies[0].Position.Y = -99
	bytes := prepared.SourceBytes()
	bytes[0] = 'x'
	if prepared.Source().Bodies[0].Position.Y != 2 || prepared.SourceHash() != old {
		t.Fatal("capture leaked mutable source")
	}
	settings := DefaultSettings()
	settings.Limits.Bodies--
	other, err := Prepare(prepared.Source(), settings)
	if err != nil {
		t.Fatal(err)
	}
	if other.Fingerprint() == prepared.Fingerprint() || other.SourceHash() != old {
		t.Fatal("settings missing from fingerprint")
	}
	settings.CCDPolicy = "physx"
	if _, err = Prepare(prepared.Source(), settings); !errors.Is(err, p.ErrUnsupported) {
		t.Fatal(err)
	}
}

func TestLoadIdentityOwnershipAndRemoval(t *testing.T) {
	w := newLoadWorld()
	instance, err := Load(w, fixtureSource(t), func(id string) (int64, error) {
		if id != "actor-owner" {
			t.Fatal(id)
		}
		return 73, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	body, ok := instance.Body("actor")
	if !ok || w.bodies[body].Owner != 73 {
		t.Fatal("body identity/owner")
	}
	collider, ok := instance.Collider("actor/sphere")
	if !ok || w.colliders[collider].Body != body || w.colliders[collider].Owner != 73 {
		t.Fatal("collider identity/owner")
	}
	if w.colliders[collider].Material.FrictionCombine != p.Average {
		t.Fatal("authored material combination lost")
	}
	if err = instance.Remove(); err != nil {
		t.Fatal(err)
	}
	if len(w.bodies)+len(w.shapes)+len(w.colliders) != 0 {
		t.Fatal("owned objects leaked")
	}
	if _, ok = instance.Body("actor"); ok {
		t.Fatal("removed handle published")
	}
	if err = instance.Remove(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPreflightAndRollback(t *testing.T) {
	w := newLoadWorld()
	if _, err := Load(w, fixtureSource(t), func(string) (int64, error) { return 0, errors.New("unresolved") }); err == nil || w.createCalls != 0 {
		t.Fatal("owner failure mutated world")
	}
	source := fixtureSource(t)
	source.Bodies[0].CollisionDetection = "continuousSpeculative"
	prepared, err := Prepare(source, DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Diagnostics()) != 1 || prepared.Source().Bodies[0].CollisionDetection != "continuousSpeculative" {
		t.Fatal("unsupported source not retained")
	}
	if _, err = prepared.Load(w, func(string) (int64, error) { return 1, nil }); !errors.Is(err, p.ErrUnsupported) || w.createCalls != 0 {
		t.Fatal("unsupported settings changed world")
	}
	existing, _ := w.CreateBody(p.DefaultBody(p.Static))
	w.failCollider = true
	if instance, err := Load(w, fixtureSource(t), func(string) (int64, error) { return 1, nil }); err == nil || instance != nil {
		t.Fatal("construction failure not rolled back")
	}
	if len(w.bodies) != 1 || len(w.shapes) != 0 || w.bodies[existing].Mode != p.Static {
		t.Fatal("rollback affected existing object")
	}
	w.failCleanup = true
	instance, err := Load(w, fixtureSource(t), func(string) (int64, error) { return 1, nil })
	if err == nil || instance == nil {
		t.Fatal("incomplete rollback lost retry handle")
	}
	w.failCleanup = false
	if err = instance.Remove(); err != nil || len(w.bodies) != 1 {
		t.Fatal("cleanup retry failed", err)
	}
}
