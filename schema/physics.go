package schema

import "fmt"

// PhysicsComponent binds an ordinary variable to one accepted physics component.
type PhysicsComponent struct {
	VarInfo
	Component string // X, Y, Z or W in the portable physics value
}

// PhysicsBindingData declares optional server writers without adding implicit wire fields.
type PhysicsBindingData struct {
	Rotation        []PhysicsComponent
	LinearVelocity  []PhysicsComponent
	AngularVelocity []PhysicsComponent
	Active          *VarInfo
}

// ResolvePhysicsBindings validates explicit writable physics roles and complete groups.
// Binding metadata leaves tags, owner visibility, position encoding and fingerprints unchanged.
func ResolvePhysicsBindings(entity EntityData) (*PhysicsBindingData, error) {
	bound := make(map[string]VarInfo)
	roles := map[string]string{"active": "bool"}
	for _, prefix := range []string{"orientation", "velocity", "angular_velocity"} {
		axes := []string{"x", "y", "z"}
		if prefix == "orientation" {
			axes = append(axes, "w")
		}
		for _, axis := range axes {
			roles[prefix+"_"+axis] = "float32"
		}
	}
	for _, v := range entity.AllVars {
		if v.Physics == "" {
			continue
		}
		switch v.GoName {
		case "EntityId", "EntityID", "PosX", "PosY", "PosZ", "Revision", "Server", "BindServer":
			return nil, fmt.Errorf("entity %q: physics var %q is reserved metadata", entity.Name, v.SnakeName)
		}
		kind, ok := roles[v.Physics]
		if !ok {
			return nil, fmt.Errorf("entity %q: var %q: unknown physics role %q", entity.Name, v.SnakeName, v.Physics)
		}
		if entity.Dimensions != 3 {
			return nil, fmt.Errorf("entity %q: physics bindings require simulation.dimensions: 3", entity.Name)
		}
		if entity.Collider != nil {
			return nil, fmt.Errorf("entity %q: physics bindings cannot share a legacy collider", entity.Name)
		}
		if v.GoType != kind || v.IsRepeated || v.IsMap {
			return nil, fmt.Errorf("entity %q: physics role %q requires %s", entity.Name, v.Physics, kind)
		}
		if v.Sync != "tick" {
			return nil, fmt.Errorf("entity %q: physics role %q requires writable sync: tick", entity.Name, v.Physics)
		}
		if previous, exists := bound[v.Physics]; exists {
			return nil, fmt.Errorf("entity %q: vars %q and %q duplicate physics role %q", entity.Name, previous.SnakeName, v.SnakeName, v.Physics)
		}
		// TickVars carries the authoritative dirty-bit projection, unlike legacy AllVars.
		for _, tick := range entity.TickVars {
			if tick.SnakeName == v.SnakeName {
				v = tick
				break
			}
		}
		bound[v.Physics] = v
	}
	if len(bound) == 0 {
		return nil, nil
	}
	result := &PhysicsBindingData{}
	group := func(prefix string, axes []string) ([]PhysicsComponent, error) {
		var values []PhysicsComponent
		for _, axis := range axes {
			if v, ok := bound[prefix+"_"+axis]; ok {
				values = append(values, PhysicsComponent{VarInfo: v, Component: map[string]string{"x": "X", "y": "Y", "z": "Z", "w": "W"}[axis]})
			}
		}
		if len(values) != 0 && len(values) != len(axes) {
			return nil, fmt.Errorf("entity %q: physics %s group must bind every component", entity.Name, prefix)
		}
		return values, nil
	}
	var err error
	if result.Rotation, err = group("orientation", []string{"x", "y", "z", "w"}); err != nil {
		return nil, err
	}
	if result.LinearVelocity, err = group("velocity", []string{"x", "y", "z"}); err != nil {
		return nil, err
	}
	if result.AngularVelocity, err = group("angular_velocity", []string{"x", "y", "z"}); err != nil {
		return nil, err
	}
	if (len(result.LinearVelocity) == 0) != (len(result.AngularVelocity) == 0) {
		return nil, fmt.Errorf("entity %q: physics velocity binding requires both linear and angular groups", entity.Name)
	}
	if active, ok := bound["active"]; ok {
		result.Active = &active
	}
	methods := map[string]bool{"SetPhysicsRotation": len(result.Rotation) > 0, "SetPhysicsVelocity": len(result.LinearVelocity) > 0, "SetPhysicsActive": result.Active != nil}
	for _, v := range entity.AllVars {
		if methods[v.GoName] || methods["Set"+v.GoName] {
			return nil, fmt.Errorf("entity %q: var %q conflicts with a generated physics writer", entity.Name, v.SnakeName)
		}
	}
	return result, nil
}
