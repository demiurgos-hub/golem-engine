package box3d_test

import (
	"reflect"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
)

func TestBatchMatchesSerialQueriesAndOverflow(t *testing.T) {
	w := world(t)
	for i := 0; i < 16; i++ {
		add(t, w, p.Static, p.Vec3{X: float32(i * 3)}, p.ShapeDesc{Kind: p.Sphere, Radius: 1})
	}
	var tasks []p.QueryTask
	var want []p.QueryOutcome
	var expected [][]p.Hit
	for i := 0; i < 384; i++ {
		q := p.Query{Kind: p.QueryKind(i%3 + 1), Mode: p.QueryMode(i % 3), Pose: p.Identity(), Shape: p.ShapeDesc{Kind: p.Sphere, Radius: .5}, Filter: p.QueryFilter{Mask: ^uint64(0)}, Translation: p.Vec3{X: 50}, Initial: p.InitialOverlapPolicy(i % 3)}
		q.Pose.Position = p.Vec3{X: -2 + float32(i%7), Y: float32(i%2) * .25}
		hits := make([]p.Hit, i%5)
		r, err := w.Query(q, hits)
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, p.QueryTask{Query: q, Hits: make([]p.Hit, len(hits))})
		want = append(want, p.QueryOutcome{Result: r})
		expected = append(expected, hits)
	}
	out := make([]p.QueryOutcome, len(tasks))
	before := w.Metrics().Queries
	if err := w.QueryBatch(tasks, out, 6); err != nil {
		t.Fatal(err)
	}
	for i := range tasks {
		if !reflect.DeepEqual(out[i], want[i]) || !reflect.DeepEqual(tasks[i].Hits, expected[i]) {
			t.Fatalf("query %d differs: %+v / %+v", i, out[i], want[i])
		}
	}
	if w.Metrics().Queries-before != uint64(len(tasks)) {
		t.Fatal("batch omitted query accounting")
	}
	tasks[len(tasks)-1].Query.Pose.Position.X = 5000
	before = w.Metrics().Queries
	if err := w.QueryBatch(tasks, out, 6); err == nil || w.Metrics().Queries != before {
		t.Fatal("invalid batch executed queries")
	}
}

func TestInitialPoliciesAndNearestAgreeWithAll(t *testing.T) {
	w := world(t)
	_, initial := add(t, w, p.Static, p.Vec3{}, p.ShapeDesc{Kind: p.Sphere, Radius: 1})
	_, target := add(t, w, p.Static, p.Vec3{X: 4}, p.ShapeDesc{Kind: p.Sphere, Radius: 1})
	q := p.Query{Kind: p.Sweep, Shape: p.ShapeDesc{Kind: p.Sphere, Radius: .5}, Pose: p.Identity(), Translation: p.Vec3{X: 6}, Filter: p.QueryFilter{Mask: ^uint64(0)}}
	for _, policy := range []p.InitialOverlapPolicy{p.ReportInitial, p.IgnoreInitial, p.BlockInitial} {
		q.Initial = policy
		q.Mode = p.All
		var all [8]p.Hit
		r, err := w.Query(q, all[:])
		if err != nil || r.Count == 0 {
			t.Fatalf("%+v %v", r, err)
		}
		q.Mode = p.Nearest
		var nearest [1]p.Hit
		r, err = w.Query(q, nearest[:])
		if err != nil || r.Count != 1 || nearest[0] != all[0] {
			t.Fatalf("nearest differs from all: %+v %+v %v", nearest, all, err)
		}
		want := initial
		if policy == p.IgnoreInitial {
			want = target
		}
		if nearest[0].Collider != want {
			t.Fatal("initial overlap policy ignored", nearest[0])
		}
	}
}

func TestPhysicalQueryFiltersMatchSymmetricMasksAndGroups(t *testing.T) {
	for _, tc := range []struct {
		name          string
		query, target p.CollisionFilter
		hit           bool
	}{
		{"one-way-rejected", p.CollisionFilter{Category: 1, Mask: 2}, p.CollisionFilter{Category: 2, Mask: 0}, false},
		{"symmetric", p.CollisionFilter{Category: 1, Mask: 2}, p.CollisionFilter{Category: 2, Mask: 1}, true},
		{"positive-group", p.CollisionFilter{Category: 1, Group: 7}, p.CollisionFilter{Category: 2, Group: 7}, true},
		{"negative-group", p.CollisionFilter{Category: 1, Mask: 2, Group: -7}, p.CollisionFilter{Category: 2, Mask: 1, Group: -7}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := world(t)
			shape, err := w.CreateShape(p.ShapeDesc{Kind: p.Sphere, Radius: 1})
			if err != nil {
				t.Fatal(err)
			}
			body, err := w.CreateBody(p.DefaultBody(p.Static))
			if err != nil {
				t.Fatal(err)
			}
			d := p.DefaultCollider(body, shape)
			d.Filter = tc.target
			if _, err = w.CreateCollider(d); err != nil {
				t.Fatal(err)
			}
			if err = w.Synchronize(); err != nil {
				t.Fatal(err)
			}
			q := p.Query{Kind: p.Overlap, Mode: p.Any, Shape: p.ShapeDesc{Kind: p.Sphere, Radius: .2}, Pose: p.Identity(), Filter: p.QueryFilter{Mask: ^uint64(0), PhysicalFilter: &tc.query}}
			var hits [1]p.Hit
			r, err := w.Query(q, hits[:])
			if err != nil || (r.Count > 0) != tc.hit {
				t.Fatalf("%+v %v", r, err)
			}
			q.Filter.PhysicalFilter = nil
			r, err = w.Query(q, hits[:])
			if err != nil || r.Count != 1 {
				t.Fatalf("ordinary query used target mask: %+v %v", r, err)
			}
		})
	}
}
