package collisioncp

import (
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/collision"
	cp "github.com/jakecoffman/cp/v2"
)

func TestRegistrationBookkeeping(t *testing.T) {
	for _, dynamic := range []bool{false, true} {
		for _, shape := range []collision.Shape{collision.Circle{R: 1}, collision.AABB{W: 2, H: 4}} {
			b := New()
			if dynamic {
				b.AddDynamic(42, shape, 4, 8, true, 3, 7, 5, 6)
			} else {
				b.Add(42, shape, 4, 8, true)
				b.Update(42, 5, 6)
			}
			e := b.entries[42]
			if e.body.UserData != int64(42) || e.shape.Body() != e.body || !e.trigger || e.dynamic != dynamic || !e.shape.Sensor() || e.shape.Filter.Categories != 4 || e.shape.Filter.Mask != 8 {
				t.Fatalf("registration=%+v shape=%+v", e, e.shape)
			}
			wantType := cp.BODY_KINEMATIC
			if dynamic {
				wantType = cp.BODY_DYNAMIC
			}
			if e.body.GetType() != wantType || e.body.Position() != (cp.Vector{X: 5, Y: 6}) {
				t.Fatal("wrong body or position")
			}
			if dynamic {
				b.SetVelocity(42, 2, 3)
				b.Set(42, collision.AABB{W: 3, H: 5}, 2, 4, false)
				e = b.entries[42]
				if e.body.Mass() != 3 || e.body.Moment() != 7 || e.body.Velocity() != (cp.Vector{X: 2, Y: 3}) || e.body.Position() != (cp.Vector{X: 5, Y: 6}) {
					t.Fatal("dynamic state lost")
				}
			}
			count := 0
			b.space.EachBody(func(*cp.Body) { count++ })
			if count != 1 {
				t.Fatalf("bodies=%d", count)
			}
			count = 0
			b.space.EachShape(func(*cp.Shape) { count++ })
			if count != 1 {
				t.Fatalf("shapes=%d", count)
			}
			b.Remove(42)
			b.Remove(42)
			b.space.EachBody(func(*cp.Body) { t.Fatal("body leaked") })
			b.space.EachShape(func(*cp.Shape) { t.Fatal("shape leaked") })
			if len(b.entries) != 0 {
				t.Fatal("entry leaked")
			}
		}
	}
	for _, shape := range []collision.Shape{nil, &collision.Circle{R: 1}, &collision.AABB{W: 1, H: 1}} {
		b := New()
		b.Add(1, shape, 1, 1, false)
		b.AddDynamic(2, shape, 1, 1, false, 1, 1, 0, 0)
		if len(b.entries) != 0 {
			t.Fatal("unsupported shape registered")
		}
		b.space.EachBody(func(*cp.Body) { t.Fatal("unsupported body leaked") })
		b.space.EachShape(func(*cp.Shape) { t.Fatal("unsupported shape leaked") })
	}
}
