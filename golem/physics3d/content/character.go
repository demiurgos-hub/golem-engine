package content

import p "github.com/demiurgos-hub/golem-engine/golem/physics3d"

// CharacterPolicy explicitly chooses how authored Unity controller settings
// become server movement policy. The zero value refuses implicit conversion.
type CharacterPolicy string

// GolemKinematicCharacters opts upright authored capsules into Golem's kinematic
// controller, using StepOffset, SlopeLimit and SkinWidth as step, slope and skin
// settings. Remaining motion/support budgets come from Settings.CharacterController.
// This does not promise Unity CharacterController behavioural equivalence.
const GolemKinematicCharacters CharacterPolicy = "golem-kinematic-v1"

type preparedCharacter struct {
	config p.CharacterControllerConfig
	active bool
}

func prepareCharacters(source Source, settings Settings) (map[string]preparedCharacter, []Diagnostic) {
	result := make(map[string]preparedCharacter)
	var diagnostics []Diagnostic
	bodies := make(map[string]Body, len(source.Bodies))
	counts := make(map[string]int)
	for _, body := range source.Bodies {
		bodies[body.ID] = body
	}
	for _, collider := range source.Colliders {
		counts[collider.BodyID]++
	}
	for _, collider := range source.Colliders {
		authored := collider.Character
		if authored == nil {
			continue
		}
		start := len(diagnostics)
		diagnose := func(field, reason string) {
			diagnostics = append(diagnostics, Diagnostic{collider.ID, "Character." + field, reason})
		}
		if settings.CharacterPolicy == "" {
			diagnose("Policy", "explicit GolemKinematicCharacters conversion is required")
		}
		if authored.MinimumMoveDistance != 0 {
			diagnose("MinimumMoveDistance", "nonzero movement deadband is unsupported")
		}
		if !authored.DetectCollisions {
			diagnose("DetectCollisions", "collision-disabled character movement is unsupported")
		}
		if !authored.EnableOverlapRecovery {
			diagnose("EnableOverlapRecovery", "disabling authored overlap recovery is unsupported")
		}
		body := bodies[collider.BodyID]
		up := body.Rotation.Mul(collider.Rotation).Rotate(p.Vec3{Y: 1})
		if body.Kind != "kinematic" || counts[body.ID] != 1 || collider.Shape != "capsule" || collider.Axis != 1 || !collider.Solid || collider.Sensor || up.Sub(p.Vec3{Y: 1}).Length() > 1e-5 {
			diagnose("Geometry", "requires one upright solid capsule on its own kinematic body")
		}
		cfg := settings.CharacterController
		cfg.StepHeight, cfg.MaxSlopeDegrees, cfg.Skin = authored.StepOffset, authored.SlopeLimit, authored.SkinWidth
		if settings.CharacterPolicy == GolemKinematicCharacters && (!cfg.Enabled || cfg.MaxDepenetration == 0 || cfg.Validate(p.Kinematic) != nil) {
			diagnose("Settings", "authored step/slope/skin and explicit server controller budgets are unsupported or invalid")
		}
		if len(diagnostics) == start {
			result[body.ID] = preparedCharacter{config: cfg, active: collider.Enabled && collider.Active}
		}
	}
	return result, diagnostics
}
