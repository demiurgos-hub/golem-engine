package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/demiurgos-hub/golem-engine/golem"
	"github.com/demiurgos-hub/golem-engine/golem/nav/surface"
	"github.com/demiurgos-hub/navmesh-go/dynamic"
	"github.com/demiurgos-hub/navmesh-go/geom"
	"github.com/demiurgos-hub/navmesh-go/qualification/worldgen"
)

func TestTerminalEvidenceRetainsBlockedAndFailedStatus(t *testing.T) {
	for _, state := range []golem.NavigationState{
		{Mode: golem.NavigationBlocked, Status: surface.Status{Code: surface.Partial, Reason: surface.Unreachable}, Generation: 7, WorldRevision: 12},
		{Mode: golem.NavigationBlocked, Status: surface.Status{Code: surface.Partial, Reason: surface.ResourceExhausted, Err: errors.New("node budget exhausted")}, Generation: 8, WorldRevision: 13},
		{Mode: golem.NavigationUnsupported, Status: surface.Status{Code: surface.NoSurface, Reason: surface.NoSurface, Err: errors.New("support missing")}, Generation: 9, WorldRevision: 14},
	} {
		m := &measurements{}
		a := &actor{ID: 2, requestAt: time.Unix(1, 0), requestQuarantined: true}
		wantError := ""
		if state.Status.Err != nil {
			wantError = state.Status.Err.Error()
		}
		observation := m.requestOutcome(a, "failed", 42, a.requestAt.Add(time.Millisecond)).withState(state)
		// The evidence owns diagnostic values, not the mutable observed state.
		state.Status.Err = nil
		var encoded bytes.Buffer
		m.writeRequestOutcome(json.NewEncoder(&encoded), observation)
		var decoded struct{ Observation outcomeObservation }
		if err := json.Unmarshal(encoded.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		got := decoded.Observation.State
		if got == nil || *got != *observation.State || got.Mode != state.Mode || got.Code != state.Status.Code || got.Reason != state.Status.Reason || got.Error != wantError || got.Generation != state.Generation || got.WorldRevision != state.WorldRevision {
			t.Fatalf("lost exact terminal status: %+v", decoded)
		}
		if m.RequestOutcomes.Failed.ObservedQuarantine != 1 || !decoded.Observation.Quarantined {
			t.Fatal("status evidence changed outcome/quarantine classification")
		}
	}
	if got := (&measurements{}).requestOutcome(&actor{}, "superseded", 1, time.Time{}); got.State != nil {
		t.Fatal("unobserved state must not fabricate Idle/NotReady status")
	}
}

func TestTerminalEvidencePreservesQuarantineAndMeasurementBoundary(t *testing.T) {
	issued := time.Unix(1, 0)
	a := &actor{ID: 7, requested: true, requestAt: issued, requestQuarantined: true}
	m := &measurements{}
	boundary := issued.Add(100 * time.Millisecond)
	observation := m.requestOutcome(a, "pending_at_measurement_end", 13, boundary)
	var encoded bytes.Buffer
	m.writeRequestOutcome(json.NewEncoder(&encoded), observation)
	var decoded struct {
		Event       string
		Observation outcomeObservation
	}
	if err := json.Unmarshal(encoded.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Event != "request_outcome" || decoded.Observation.EntityID != a.ID || decoded.Observation.Elapsed != 100*time.Millisecond || !decoded.Observation.Quarantined || decoded.Observation.Outcome != "pending_at_measurement_end" {
		t.Fatalf("lost boundary or quarantine evidence: %+v", decoded)
	}
	if m.RequestOutcomes.PendingAtMeasurementEnd.ObservedQuarantine != 1 || m.RequestOutcomes.PendingAtMeasurementEnd.NoObservedQuarantine != 0 || !a.requested || !a.requestAt.Equal(issued) {
		t.Fatal("outcome observation changed intent or misclassified quarantine")
	}
	a.requestQuarantined = false
	m.requestOutcome(a, "admitted", 14, issued.Add(time.Second))
	if m.RequestOutcomes.Admitted.NoObservedQuarantine != 1 || m.RequestOutcomes.Admitted.ObservedQuarantine != 0 {
		t.Fatal("unquarantined admission merged with quarantined pending request")
	}
}

func TestMotorUsesCurrentPhysicalBlockerSet(t *testing.T) {
	bodies := &collisionWorld{volumes: []geom.Volume{{ID: "wall", Shape: "box", Exclude: true, Bounds: geom.Bounds{Min: geom.Vec3{X: 2, Y: -1, Z: -1}, Max: geom.Vec3{X: 3, Y: 3, Z: 1}}}}}
	m := bodyMotor{actor: &actor{}, metrics: &measurements{}, tick: func() uint64 { return 1 }, radius: .4, height: 1.8, bodies: bodies, measured: func() bool { return false }}
	move := golem.NavigationMotion{From: surface.Vec3{}, Target: surface.Vec3{X: 5}, Delta: .05}
	hit, e := m.MoveNavigation(move)
	if e != nil || hit.X >= 1.6 {
		t.Fatalf("body crossed blocker: %+v, %v", hit, e)
	}
	bodies.volumes[0].Disabled = true
	hit, e = m.MoveNavigation(move)
	if e != nil || hit.X != 5 {
		t.Fatalf("removed blocker retained collision: %+v, %v", hit, e)
	}
}

func TestPhysicalActivationFollowsBothProfileQuarantines(t *testing.T) {
	config := worldgen.DefaultConfig()
	config.Family = "construction"
	config.HalfExtent = 16
	config.GridStep = 2
	f, err := worldgen.Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	var worlds []*dynamic.World
	for _, profile := range f.Profiles {
		world, err := dynamic.New(context.Background(), f.Scene, profile)
		if err != nil {
			t.Fatal(err)
		}
		worlds = append(worlds, world)
	}
	change := changes(f, 1)
	activated := false
	err = applyProfileEdits(f, worlds, 1, change, func() error {
		for profile, world := range worlds {
			if !world.Pending() || world.Safety().SafeBounds(change.Volumes[0].Bounds.Min, change.Volumes[0].Bounds.Max) {
				t.Fatalf("profile %d not quarantined before physical activation", profile)
			}
		}
		activated = true
		return nil
	})
	if err != nil || !activated {
		t.Fatalf("activation missing: %v", err)
	}
	activated = false
	err = applyProfileEdits(f, []*dynamic.World{worlds[0], nil}, 2, changes(f, 2), func() error { activated = true; return nil })
	if err == nil || activated {
		t.Fatal("physical geometry activated despite second-profile failure")
	}
}

func TestSupportPolicyDerivesFidelityAndRejectsFloorAliasing(t *testing.T) {
	config := worldgen.DefaultConfig()
	config.HalfExtent = 16
	config.GridStep = 2
	f, err := worldgen.Generate(config)
	if err != nil {
		t.Fatal(err)
	}
	p, err := fixtureSupportPolicy(f)
	if err != nil {
		t.Fatal(err)
	}
	if p.Tolerances["human"] != .2 || p.Tolerances["large"] != .2 {
		t.Fatalf("unexpected policy %+v", p)
	}
	for _, height := range []float64{0, .1} {
		bad := f
		bad.Profiles = append(bad.Profiles[:0:0], f.Profiles...)
		bad.Profiles[0].Profile.Height = height
		if _, err := fixtureSupportPolicy(bad); err == nil {
			t.Fatal("accepted tolerance at or above half profile height")
		}
	}
	f.Profiles[0].Settings.DetailSampleMaxError = .4
	if _, err := fixtureSupportPolicy(f); err == nil {
		t.Fatal("accepted tolerance at half known floor separation")
	}
}

func TestHistogramDoesNotHideOverflow(t *testing.T) {
	var h histogram
	h.add(20 * time.Second)
	if h.percentile(.99) != 20000 {
		t.Fatal(h.summary())
	}
	var near histogram
	near.add(5 * time.Millisecond)
	if near.percentile(.99) < 5 {
		t.Fatal("upper bound understated")
	}
}

func TestHistogramGateUsesExactThresholdInsteadOfUpperBin(t *testing.T) {
	var h histogram
	for i := 0; i < 99; i++ {
		h.add(100 * time.Millisecond)
	}
	h.add(101 * time.Millisecond)
	if !h.p99Within(100 * time.Millisecond) {
		t.Fatal("exact p99 of100ms was rejected by display-bin rounding")
	}
	h.add(101 * time.Millisecond)
	if h.p99Within(100 * time.Millisecond) {
		t.Fatal("more than1percent over100ms passed")
	}
}

func TestHostDislodgesOverlappingVolumesBeforeReprojection(t *testing.T) {
	w := collisionWorld{volumes: []geom.Volume{
		{ID: "a", Exclude: true, Bounds: geom.Bounds{Min: geom.Vec3{X: -3, Y: -1, Z: -2}, Max: geom.Vec3{X: 1, Y: 4, Z: 2}}},
		{ID: "b", Exclude: true, Bounds: geom.Bounds{Min: geom.Vec3{X: -1, Y: -1, Z: -2}, Max: geom.Vec3{X: 3, Y: 4, Z: 2}}},
	}}
	for _, p := range []surface.Vec3{{}, {X: 2.7669, Y: .1, Z: -2.4078}} {
		after, moved, clear := w.dislodge(p, .75, 1.8)
		if !moved || !clear || len(w.overlaps(after, .75, 1.8)) != 0 || after.Y != p.Y {
			t.Fatalf("bad ejection from %+v to %+v moved=%v clear=%v", p, after, moved, clear)
		}
	}
	above := surface.Vec3{Y: 5}
	if after, moved, clear := w.dislodge(above, .75, 1.8); after != above || moved || !clear {
		t.Fatal("changed body on unrelated upper floor")
	}
}

func TestFallingBodyUsesRetainedSourceAndDisabledFloor(t *testing.T) {
	triangles := []geom.ResolvedTriangle{
		{ObjectID: "ground", Vertices: [3]geom.Vec3{{X: -10, Z: -10}, {X: 0, Z: 10}, {X: 10, Z: -10}}, Bounds: geom.Bounds{Min: geom.Vec3{X: -10, Z: -10}, Max: geom.Vec3{X: 10, Z: 10}}},
		{ObjectID: "bridge", Vertices: [3]geom.Vec3{{X: -10, Y: 7, Z: -10}, {X: 0, Y: 7, Z: 10}, {X: 10, Y: 7, Z: -10}}, Bounds: geom.Bounds{Min: geom.Vec3{X: -10, Y: 7, Z: -10}, Max: geom.Vec3{X: 10, Y: 7, Z: 10}}},
	}
	w := collisionWorld{triangles: triangles, index: geom.NewIndex(triangles), disabled: map[string]bool{}}
	if y, hit := w.landing(surface.Vec3{Y: 6}, 8); !hit || y != 7 {
		t.Fatalf("missed bridge landing %v %v", y, hit)
	}
	w.disabled["bridge"] = true
	if _, hit := w.landing(surface.Vec3{Y: 6}, 8); hit {
		t.Fatal("landed on removed bridge")
	}
	if y, hit := w.landing(surface.Vec3{Y: -.1}, .1); !hit || y != 0 {
		t.Fatalf("missed ground landing %v %v", y, hit)
	}
}
