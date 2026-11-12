package main

import (
	"fmt"
	"math"

	"github.com/demiurgos-hub/navmesh-go/geom"
	"github.com/demiurgos-hub/navmesh-go/qualification/worldgen"
)

// fixtureBodyProfiles returns physical dimensions in baked-profile order.
// Legacy Version1 fixtures used the bake dimensions for their bodies too.
func fixtureBodyProfiles(f worldgen.Fixture) ([]geom.Profile, error) {
	if math.IsNaN(f.BakeClearanceMargin) || math.IsInf(f.BakeClearanceMargin, 0) || f.BakeClearanceMargin < 0 {
		return nil, fmt.Errorf("invalid fixture bake clearance margin")
	}
	if f.BodyProfiles == nil && f.BakeClearanceMargin != 0 {
		return nil, fmt.Errorf("a bake clearance margin requires explicit physical body profiles")
	}
	bodyByID := make(map[string]geom.Profile, len(f.BodyProfiles))
	for _, profile := range f.BodyProfiles {
		if err := profile.Validate(); err != nil {
			return nil, fmt.Errorf("physical body profile: %w", err)
		}
		if _, exists := bodyByID[profile.ID]; exists {
			return nil, fmt.Errorf("duplicate physical body profile %q", profile.ID)
		}
		bodyByID[profile.ID] = profile
	}
	if f.BodyProfiles != nil && len(f.BodyProfiles) != len(f.Profiles) {
		return nil, fmt.Errorf("physical and baked profile IDs must match exactly")
	}
	seen := make(map[string]bool, len(f.Profiles))
	profiles := make([]geom.Profile, len(f.Profiles))
	for i, cfg := range f.Profiles {
		if err := cfg.Profile.Validate(); err != nil {
			return nil, fmt.Errorf("baked profile: %w", err)
		}
		if seen[cfg.Profile.ID] {
			return nil, fmt.Errorf("duplicate baked profile %q", cfg.Profile.ID)
		}
		seen[cfg.Profile.ID] = true
		profile := cfg.Profile
		if f.BodyProfiles != nil {
			var exists bool
			profile, exists = bodyByID[cfg.Profile.ID]
			if !exists {
				return nil, fmt.Errorf("missing physical body profile %q", cfg.Profile.ID)
			}
		}
		if profile.Radius > cfg.Profile.Radius || profile.Height > cfg.Profile.Height {
			return nil, fmt.Errorf("physical body %q exceeds its baked radius or height", profile.ID)
		}
		if f.BakeClearanceMargin > 0 && math.Abs(cfg.Profile.Radius-profile.Radius-f.BakeClearanceMargin) > 1e-12 {
			return nil, fmt.Errorf("profile %q does not match the recorded bake clearance margin", profile.ID)
		}
		profiles[i] = profile
	}
	return profiles, nil
}

func fixtureProfileEvidence(f worldgen.Fixture, bodies []geom.Profile) map[string]any {
	baked := make([]geom.Profile, len(f.Profiles))
	for i, cfg := range f.Profiles {
		baked[i] = cfg.Profile
	}
	return map[string]any{"physical_bodies": bodies, "baked_navigation_profiles": baked, "bake_clearance_margin_metres": f.BakeClearanceMargin, "legacy_body_profile_fallback": f.BodyProfiles == nil}
}
