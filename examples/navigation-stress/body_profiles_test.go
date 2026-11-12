package main

import (
	"math"
	"testing"

	"github.com/demiurgos-hub/navmesh-go/geom"
	"github.com/demiurgos-hub/navmesh-go/qualification/worldgen"
)

func TestBodyProfilesUseIDsAndPreserveLegacyDimensions(t *testing.T) {
	cfg := worldgen.DefaultConfig()
	cfg.Family = "capacity"
	f, err := worldgen.Generate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.BodyProfiles[0], f.BodyProfiles[1] = f.BodyProfiles[1], f.BodyProfiles[0]
	bodies, err := fixtureBodyProfiles(f)
	if err != nil || bodies[0].Radius != .4 || bodies[1].Radius != .8 || bodies[0].ID != f.Profiles[0].Profile.ID || f.Profiles[0].Profile.Radius != .9 {
		t.Fatalf("physical and baked dimensions confused: %+v %v", bodies, err)
	}
	legacy := f
	legacy.BodyProfiles, legacy.BakeClearanceMargin = nil, 0
	for i := range legacy.Profiles {
		legacy.Profiles[i].Profile = bodies[i]
	}
	old, err := fixtureBodyProfiles(legacy)
	if err != nil || old[0] != bodies[0] || old[1] != bodies[1] {
		t.Fatalf("legacy body dimensions changed: %+v %v", old, err)
	}
}

func TestBodyProfilesRejectAmbiguousOrUnsafeDimensions(t *testing.T) {
	cfg := worldgen.DefaultConfig()
	cfg.Family = "capacity"
	base, err := worldgen.Generate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*worldgen.Fixture){
		"missing with margin": func(f *worldgen.Fixture) { f.BodyProfiles = nil },
		"present empty":       func(f *worldgen.Fixture) { f.BodyProfiles = []geom.Profile{} },
		"incomplete":          func(f *worldgen.Fixture) { f.BodyProfiles = f.BodyProfiles[:1] },
		"duplicate":           func(f *worldgen.Fixture) { f.BodyProfiles[1].ID = f.BodyProfiles[0].ID },
		"foreign ID":          func(f *worldgen.Fixture) { f.BodyProfiles[0].ID = "foreign" },
		"oversized radius":    func(f *worldgen.Fixture) { f.BodyProfiles[0].Radius = 2 },
		"oversized height":    func(f *worldgen.Fixture) { f.BodyProfiles[0].Height = 3 },
		"zero height":         func(f *worldgen.Fixture) { f.BodyProfiles[0].Height = 0 },
		"nonfinite radius":    func(f *worldgen.Fixture) { f.BodyProfiles[0].Radius = math.NaN() },
		"wrong margin":        func(f *worldgen.Fixture) { f.BakeClearanceMargin = .3 },
		"nonfinite margin":    func(f *worldgen.Fixture) { f.BakeClearanceMargin = math.Inf(1) },
	} {
		t.Run(name, func(t *testing.T) {
			f := base
			f.BodyProfiles = append([]geom.Profile(nil), base.BodyProfiles...)
			mutate(&f)
			if _, err := fixtureBodyProfiles(f); err == nil {
				t.Fatal("accepted invalid physical profile metadata")
			}
		})
	}
}
