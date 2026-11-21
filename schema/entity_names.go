package schema

import "fmt"

// generatedEntityMember is a member that codegen emits on an entity's
// generated types and that shares a namespace with the names generated for
// the entity's vars.
type generatedEntityMember struct {
	name string
	kind string
}

// reservedEntityPascalNames lists the generated members that share the
// PascalCase namespace of a var's generated names: the Go server getter
// GoName() and setter Set<GoName>() on Synced<E> (codegen/templates/go_server/
// server.go.tmpl), the Go client getter (go_client/client.go.tmpl), the field
// of the generated <E>State/<E>Delta Go and C# messages, and the C# client
// property (csharp/client.cs.tmpl). Methods of every 2D/3D/physics/owner-scoped
// variant are reserved regardless of the entity's own variant, so changing it
// never newly breaks a var name; only PosZ follows the dimensions, matching the
// pos_z metadata reservation.
func reservedEntityPascalNames(entity string, dimensions int) []generatedEntityMember {
	members := []generatedEntityMember{
		// Go server Synced<E> methods.
		{"EntityID", "method"}, {"SetEntityID", "method"},
		{"BindServer", "method"}, {"Server", "method"},
		{"Collider", "method"}, {"Collider3D", "method"},
		{"SetPhysicsRotation", "method"}, {"SetPhysicsVelocity", "method"}, {"SetPhysicsActive", "method"},
		{"TypeName", "method"}, {"StateRevision", "method"}, {"BumpStateRevision", "method"},
		{"IsGlobal", "method"}, {"IsPersistent", "method"},
		{"Position", "method"}, {"Position3D", "method"}, {"SetPosition", "method"}, {"SetPosition3D", "method"},
		{"GetSynced" + entity, "method"},
		{"LastFlushMask", "method"}, {"FlushDelta", "method"}, {"FullState", "method"}, {"FlushUpdate", "method"},
		{"MarshalDeltaMask", "method"}, {"MarshalCompactDeltaMask", "method"}, {"FullUpdate", "method"},
		{"PublicFullUpdate", "method"}, {"PublicReplicationMask", "method"}, {"ApplyState", "method"},
		// Go/C# client Synced<E> members.
		{"ApplyDelta", "method"}, {"PosX", "member"}, {"PosY", "member"}, {"Revision", "member"},
		// <E>State/<E>Delta message fields and methods (Go Marshal/Unmarshal,
		// C# Encode/Decode).
		{"EntityId", "message field"}, {"Marshal", "message method"}, {"Unmarshal", "message method"},
		{"Encode", "message method"}, {"Decode", "message method"},
		// A C# member may not share its enclosing type's name.
		{"Synced" + entity, "type name"}, {entity + "State", "type name"}, {entity + "Delta", "type name"},
	}
	if dimensions == 3 {
		members = append(members, generatedEntityMember{"PosZ", "member"})
	}
	return members
}

// reservedEntityCamelNames lists the generated members that share the
// camelCase namespace of a var's struct field / constructor parameter (Go
// server and client Synced<E>, NewSynced<E>, whose position and entityID
// parameters are struct field names) and its TypeScript getter
// (js/client.ts.tmpl) and message field.
func reservedEntityCamelNames(dimensions int) []generatedEntityMember {
	members := []generatedEntityMember{
		// Go server Synced<E> struct fields and unexported method.
		{"mu", "struct field"}, {"entityID", "struct field"}, {"posX", "struct field"}, {"posY", "struct field"},
		{"dirty", "struct field"}, {"lastFlushMask", "struct field"}, {"revision", "struct field"},
		{"srv", "struct field"}, {"deltaFromMaskLocked", "method"},
		// TypeScript Synced<E> members and message field.
		{"entityId", "member"}, {"applyState", "method"}, {"applyDelta", "method"}, {"constructor", "member"},
	}
	if dimensions == 3 {
		members = append(members, generatedEntityMember{"posZ", "struct field"})
	}
	return members
}

// checkReservedGeneratedNames rejects entity vars whose generated names clash
// with members codegen emits for every entity (see reservedEntityPascalNames
// and reservedEntityCamelNames) or with another var's generated names. Each
// var generates a getter/field named SnakeToPascal(name), a setter
// Set<SnakeToPascal(name)> and a struct field named SnakeToCamel(name).
func checkReservedGeneratedNames(entity string, dimensions int, vars map[string]SchemaVarDef) error {
	type claim struct{ owner, kind string }
	pascal := make(map[string]claim)
	for _, m := range reservedEntityPascalNames(entity, dimensions) {
		pascal[m.name] = claim{kind: m.kind}
	}
	camel := make(map[string]claim)
	for _, m := range reservedEntityCamelNames(dimensions) {
		camel[m.name] = claim{kind: m.kind}
	}
	describe := func(name string, c claim) string {
		if c.owner == "" {
			return fmt.Sprintf("generated %s %s", c.kind, name)
		}
		return fmt.Sprintf("%s %s of var %q", c.kind, name, c.owner)
	}
	for _, k := range SortedKeys(vars) {
		goName := SnakeToPascal(k)
		generated := []struct {
			names map[string]claim
			name  string
			kind  string
		}{
			{pascal, goName, "getter"},
			{pascal, "Set" + goName, "setter"},
			{camel, SnakeToCamel(k), "field"},
		}
		for _, g := range generated {
			if prev, ok := g.names[g.name]; ok {
				return fmt.Errorf("entity %q: var %q generates %s %s, which clashes with %s", entity, k, g.kind, g.name, describe(g.name, prev))
			}
		}
		for _, g := range generated {
			g.names[g.name] = claim{owner: k, kind: g.kind}
		}
	}
	return nil
}
