package box3d

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"testing"

	p "github.com/demiurgos-hub/golem-engine/golem/physics3d"
	"github.com/demiurgos-hub/golem-engine/golem/physics3d/content"
)

type unityDynamicsSample struct {
	Owner                                     string
	Frame                                     int
	Position, LinearVelocity, AngularVelocity p.Vec3
	Rotation                                  p.Quat
	Sleeping                                  bool
}

type unityDynamicsReference struct {
	Version, UnityVersion, SourceSHA256                                string
	FrameSeconds                                                       float32
	Substeps, Frames, UnityPositionIterations, UnityVelocityIterations int
	Gravity                                                            p.Vec3
	Commands                                                           []struct {
		Owner, Kind            string
		FirstFrame, FrameCount int
		Value                  p.Vec3
	}
	Samples []unityDynamicsSample
}

// This gate requires a real saved Unity simulation, including the exact source
// bytes. A missing capture is explicitly unexecuted, never comparison evidence.
func TestUnityDynamicsReference(t *testing.T) {
	dir := os.Getenv("GOLEM_PHYSICS_UNITY_DYNAMICS_DIR")
	if dir == "" {
		t.Skip("Unity simulation gate not executed: GOLEM_PHYSICS_UNITY_DYNAMICS_DIR is required")
	}
	sourceData, err := os.ReadFile(filepath.Join(dir, "dynamics.source.json"))
	if err != nil {
		t.Fatal(err)
	}
	referenceData, err := os.ReadFile(filepath.Join(dir, "dynamics.reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	var reference unityDynamicsReference
	if err := json.Unmarshal(referenceData, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Version != "unity-physics-dynamics-v1" || reference.Frames != 120 || reference.Substeps != 4 || reference.FrameSeconds != .05 || reference.Gravity != (p.Vec3{Y: -9.81}) {
		t.Fatal("unexpected dynamics fixture version, duration or step settings")
	}
	if reference.SourceSHA256 != cookDigest(sourceData) {
		t.Fatal("Unity source hash mismatch")
	}
	manifestData, err := os.ReadFile(os.Getenv("GOLEM_PHYSICS_SOURCE_MANIFEST"))
	if err != nil {
		t.Fatalf("qualification requires GOLEM_PHYSICS_SOURCE_MANIFEST identifying the frozen Go/backend source: %v", err)
	}
	source, sourceHash, err := content.Decode(bytes.NewReader(sourceData), content.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	config := p.DefaultConfig()
	config.Gravity, config.Substeps = reference.Gravity, reference.Substeps
	w, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	instance, err := content.Load(w, source, func(string) (int64, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	handles := map[string]p.BodyHandle{}
	for _, body := range source.Bodies {
		if body.OwnerID == "" {
			continue
		}
		if _, duplicate := handles[body.OwnerID]; duplicate {
			t.Fatalf("duplicate owner %s", body.OwnerID)
		}
		handle, ok := instance.Body(body.ID)
		if !ok {
			t.Fatalf("missing exported body %s", body.ID)
		}
		handles[body.OwnerID] = handle
	}
	owners := []string{"bounce", "force", "freefall", "hinge", "impulse", "push-heavy", "push-light", "stack-0", "stack-1", "stack-2", "stack-3", "stack-4", "torque"}
	if len(handles) != len(owners) {
		t.Fatalf("dynamic bodies %d, want %d", len(handles), len(owners))
	}
	for _, owner := range owners {
		if _, ok := handles[owner]; !ok {
			t.Fatalf("missing case %s", owner)
		}
	}
	unity, err := indexDynamicsSamples(reference.Samples, owners, reference.Frames)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range reference.Commands {
		if _, ok := handles[command.Owner]; !ok || command.FirstFrame < 0 || command.FrameCount < 1 || command.FirstFrame+command.FrameCount > reference.Frames || !command.Value.Valid() {
			t.Fatal("invalid simulation command")
		}
		if command.Kind != "force" && command.Kind != "torque" && command.Kind != "impulse" && command.Kind != "velocity" {
			t.Fatal("unknown simulation command")
		}
	}
	var actual []unityDynamicsSample
	observe := func(frame int) {
		for _, owner := range owners {
			state, err := w.Body(handles[owner])
			if err != nil {
				t.Fatal(err)
			}
			actual = append(actual, unityDynamicsSample{Owner: owner, Frame: frame, Position: state.Pose.Position, Rotation: state.Pose.Rotation,
				LinearVelocity: state.LinearVelocity, AngularVelocity: state.AngularVelocity, Sleeping: !state.Awake})
		}
	}
	observe(-1)
	for frame := 0; frame < reference.Frames; frame++ {
		for _, command := range reference.Commands {
			if frame < command.FirstFrame || frame >= command.FirstFrame+command.FrameCount {
				continue
			}
			handle := handles[command.Owner]
			state, err := w.Body(handle)
			if err != nil {
				t.Fatal(err)
			}
			switch command.Kind {
			case "force":
				err = w.ApplyForce(handle, command.Value, state.CenterOfMass)
			case "torque":
				err = w.ApplyTorque(handle, command.Value)
			case "impulse":
				err = w.ApplyImpulse(handle, command.Value, state.CenterOfMass)
			case "velocity":
				err = w.SetVelocity(handle, command.Value, p.Vec3{})
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		result, err := w.Simulate(p.Frame{ID: uint64(frame + 1), Delta: reference.FrameSeconds})
		if err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if result.Substeps != reference.Substeps || result.Microsteps != reference.Substeps {
			t.Fatalf("frame %d changed time integration: %+v", frame, result)
		}
		if err := w.Accept(result.ID); err != nil {
			t.Fatal(err)
		}
		observe(frame)
	}
	got, err := indexDynamicsSamples(actual, owners, reference.Frames)
	if err != nil {
		t.Fatal(err)
	}
	type comparison struct {
		Owner                                                             string
		MaxPositionError, MaxLinearVelocityError, MaxAngularVelocityError float32
		Passed                                                            bool
	}
	report := struct {
		Version, UnityVersion, SourceHash, ReferenceHash, MinimumFork, SourceManifestSHA256 string
		Scope                                                                               string
		Tolerances                                                                          map[string]float32
		Config                                                                              p.Config
		Comparisons                                                                         []comparison
		Samples                                                                             []unityDynamicsSample
		Passed                                                                              bool
	}{Version: "golem-unity-dynamics-comparison-v1", UnityVersion: reference.UnityVersion, SourceHash: sourceHash, ReferenceHash: cookDigest(referenceData), MinimumFork: RequiredFork, SourceManifestSHA256: cookDigest(manifestData), Config: config, Samples: actual, Passed: true,
		Scope: "Captured simulation fixtures only; not PhysX bit equivalence, full-workload qualification or published dependency resolution.",
		Tolerances: map[string]float32{"free_motion_position_m": .002, "free_motion_linear_velocity_m_s": .0002, "free_motion_angular_velocity_rad_s": .0002, "free_motion_orientation_rad": .02,
			"bounce_apex_difference_m": .1, "bounce_first_response_difference_s": .05, "stack_final_position_difference_m": .08, "stack_settled_speed": .05, "hinge_anchor_drift_m": .02, "hinge_limit_allowance_degrees": 1, "push_momentum_error_kg_m_s": .01}}
	defer func() {
		report.Passed = !t.Failed()
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		output := os.Getenv("GOLEM_PHYSICS_UNITY_DYNAMICS_OUTPUT")
		if output == "" {
			output = dir
		}
		file, err := os.OpenFile(filepath.Join(output, "dynamics-go-comparison.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Error(err)
			return
		}
		defer file.Close()
		if _, err := file.Write(data); err != nil {
			t.Error(err)
		}
	}()
	for _, owner := range owners {
		item := comparison{Owner: owner}
		item.Passed = t.Run(owner, func(t *testing.T) {
			for frame := -1; frame < reference.Frames; frame++ {
				a, b := got[owner][frame+1], unity[owner][frame+1]
				position, linear, angular := a.Position.Sub(b.Position).Length(), a.LinearVelocity.Sub(b.LinearVelocity).Length(), a.AngularVelocity.Sub(b.AngularVelocity).Length()
				item.MaxPositionError = max(item.MaxPositionError, position)
				item.MaxLinearVelocityError = max(item.MaxLinearVelocityError, linear)
				item.MaxAngularVelocityError = max(item.MaxAngularVelocityError, angular)
				if frame == -1 && (position > .0001 || rotationError(a.Rotation, b.Rotation) > .0001) {
					t.Fatalf("source pose mismatch: %+v / %+v", a, b)
				}
				if owner == "freefall" || owner == "impulse" || owner == "force" || owner == "torque" {
					if position > .002 || linear > .0002 || angular > .0002 || rotationError(a.Rotation, b.Rotation) > .02 {
						t.Errorf("frame %d simulation errors position=%g velocity=%g angular=%g rotation=%g", frame, position, linear, angular, rotationError(a.Rotation, b.Rotation))
						break
					}
				}
			}
			for _, engine := range []struct {
				name    string
				samples map[string][]unityDynamicsSample
			}{{"Unity", unity}, {"Go", got}} {
				t.Run(engine.name, func(t *testing.T) { checkDynamicsOutcome(t, owner, engine.samples) })
			}
			if owner == "bounce" {
				ga, gt := firstBounce(got[owner])
				ua, ut := firstBounce(unity[owner])
				if absFloat(ga-ua) > .1 || absFloat(gt-ut) > .05 {
					t.Errorf("bounce apex/time Go %g/%g Unity %g/%g", ga, gt, ua, ut)
				}
			}
			if len(owner) > 6 && owner[:6] == "stack-" {
				if got[owner][reference.Frames].Position.Sub(unity[owner][reference.Frames].Position).Length() > .08 {
					t.Error("settled stack differs from Unity by over 8cm")
				}
			}
		})
		report.Comparisons = append(report.Comparisons, item)
	}
}

func indexDynamicsSamples(samples []unityDynamicsSample, owners []string, frames int) (map[string][]unityDynamicsSample, error) {
	if len(samples) != len(owners)*(frames+1) {
		return nil, fmt.Errorf("sample count %d, want %d", len(samples), len(owners)*(frames+1))
	}
	result := map[string][]unityDynamicsSample{}
	for _, owner := range owners {
		result[owner] = nil
	}
	seen := map[string]bool{}
	for _, s := range samples {
		key := fmt.Sprintf("%s/%d", s.Owner, s.Frame)
		if _, ok := result[s.Owner]; !ok || s.Frame < -1 || s.Frame >= frames || seen[key] || !s.Position.InBounds() || !s.LinearVelocity.Valid() || !s.AngularVelocity.Valid() || !s.Rotation.Valid() {
			return nil, fmt.Errorf("invalid/duplicate sample %s", key)
		}
		seen[key] = true
		result[s.Owner] = append(result[s.Owner], s)
	}
	for _, owner := range owners {
		sort.Slice(result[owner], func(i, j int) bool { return result[owner][i].Frame < result[owner][j].Frame })
		if len(result[owner]) != frames+1 {
			return nil, fmt.Errorf("missing owner samples: %s", owner)
		}
	}
	return result, nil
}

func rotationError(a, b p.Quat) float32 {
	dot := math.Abs(float64(a.X*b.X + a.Y*b.Y + a.Z*b.Z + a.W*b.W))
	return float32(2 * math.Acos(min(1, dot)))
}

func firstBounce(samples []unityDynamicsSample) (float32, float32) {
	apex, first := float32(-1), float32(-1)
	for _, s := range samples {
		if first < 0 && s.LinearVelocity.Y > .1 {
			first = float32(s.Frame+1) * .05
		}
		if first >= 0 {
			apex = max(apex, s.Position.Y)
			if s.LinearVelocity.Y <= 0 {
				break
			}
		}
	}
	return apex, first
}

func checkDynamicsOutcome(t *testing.T, owner string, samples map[string][]unityDynamicsSample) {
	t.Helper()
	states := samples[owner]
	end := states[len(states)-1]
	switch owner {
	case "freefall":
		if absFloat(end.LinearVelocity.Y+9.81*6) > .001 {
			t.Errorf("freefall velocity %g", end.LinearVelocity.Y)
		}
	case "impulse":
		if absFloat(end.LinearVelocity.X-2) > .0002 || absFloat(end.Position.X-12) > .002 {
			t.Errorf("mass-scaled impulse %+v", end)
		}
	case "force":
		if absFloat(end.LinearVelocity.X-3) > .0002 {
			t.Errorf("force duration/mass %+v", end)
		}
	case "torque":
		if absFloat(end.AngularVelocity.Z-2) > .0002 {
			t.Errorf("torque duration/inertia %+v", end)
		}
	case "bounce":
		apex, at := firstBounce(states)
		if apex < .95 || apex > 1.35 || at < .65 || at > .85 {
			t.Errorf("bounce restitution apex=%g time=%g", apex, at)
		}
		for _, s := range states {
			if s.Position.Y < .45 || s.Position.Y > 3.01 {
				t.Errorf("bounce escaped/added energy: %+v", s)
				break
			}
		}
	case "push-light", "push-heavy":
		light, heavy := samples["push-light"], samples["push-heavy"]
		firstResponse := float32(-1)
		for i := range light {
			momentum := light[i].LinearVelocity.Add(heavy[i].LinearVelocity.Mul(3))
			if momentum.Sub(p.Vec3{X: 4}).Length() > .01 && light[i].Frame >= 0 {
				t.Errorf("linear momentum at frame%d", light[i].Frame)
				break
			}
			if light[i].Position.X > heavy[i].Position.X {
				t.Errorf("pushing spheres crossed centres at frame%d", light[i].Frame)
				break
			}
			if firstResponse < 0 && heavy[i].LinearVelocity.X > .01 {
				firstResponse = float32(heavy[i].Frame+1) * .05
			}
		}
		// Initial surface gap is 1m and closing speed is 4m/s. Allow the
		// captured frame cadence/contact margin, but not premature transfer.
		if firstResponse < .2 || firstResponse > .35 {
			t.Errorf("push response time %g, expected near .25s", firstResponse)
		}
		if absFloat(end.LinearVelocity.X-1) > .02 {
			t.Errorf("inelastic shared velocity %+v", end)
		}
	case "hinge":
		minAngle, maxAngle := float32(180), float32(-180)
		for _, s := range states {
			anchor := s.Position.Add(s.Rotation.Rotate(p.Vec3{Y: .75}))
			if anchor.Sub(p.Vec3{Y: 5, Z: 140}).Length() > .02 {
				t.Errorf("hinge anchor drift %+v", s)
				break
			}
			angle := float32(2*math.Atan2(float64(s.Rotation.Z), float64(s.Rotation.W))*180/math.Pi) - 20
			minAngle, maxAngle = min(minAngle, angle), max(maxAngle, angle)
			if angle < -31 || angle > 31 {
				t.Errorf("hinge limit angle %g", angle)
				break
			}
		}
		if minAngle > -10 || maxAngle-minAngle < 15 {
			t.Errorf("hinge did not swing toward gravity: angle range %g..%g", minAngle, maxAngle)
		}
	default:
		var index int
		if _, err := fmt.Sscanf(owner, "stack-%d", &index); err != nil {
			t.Fatal("unhandled outcome")
		}
		if absFloat(end.Position.Y-(.5+float32(index))) > .08 || end.LinearVelocity.Length() > .05 || end.AngularVelocity.Length() > .05 || !end.Sleeping {
			t.Errorf("stack did not settle/sleep: %+v", end)
		}
	}
}
