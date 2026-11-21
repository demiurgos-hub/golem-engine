package schema

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

// worldManagerFixedMembers are the methods every generated world manager
// declares next to the per-world-type members, in their Go and C# spelling.
// The JS manager uses the same names with a lower-case first letter.
var worldManagerFixedMembers = []string{"ApplyUpdate", "ApplyWorldUpdate", "ClearWorld"}

// validateWorldManagerNames rejects world type names whose generated
// world-manager members clash with a fixed manager method or with a member of
// another world type. For a world type X the generated Go, JS and C# managers
// declare an accessor X, the listeners OnXUpdate and OnXCleared and, for a
// keyed catalog, the lookup GetXById. A world type named ClearWorld would
// therefore declare ClearWorld twice and the generated client would not
// compile. Names are compared with an upper-case first letter, which also
// covers the JS spelling (clearWorld) and two types that differ only in the
// case of their first letter.
func validateWorldManagerNames(worlds []WorldTypeData) error {
	claimed := make(map[string]string, len(worldManagerFixedMembers)+4*len(worlds))
	for _, name := range worldManagerFixedMembers {
		claimed[name] = "the generated world manager method " + name
	}
	for _, world := range worlds {
		pascal := ucFirstRune(world.Name)
		members := []struct{ name, kind string }{
			{pascal, "accessor"},
			{"On" + pascal + "Update", "update listener"},
			{"On" + pascal + "Cleared", "cleared listener"},
		}
		if world.IsCatalog && world.CatalogKeyField != "" {
			members = append(members, struct{ name, kind string }{"Get" + pascal + "ById", "catalog lookup"})
		}
		for _, member := range members {
			if prev, exists := claimed[member.name]; exists {
				return fmt.Errorf("world type %q generates the world manager %s %s, which clashes with %s; rename the world type", world.Name, member.kind, member.name, prev)
			}
		}
		for _, member := range members {
			claimed[member.name] = fmt.Sprintf("the %s of world type %q", member.kind, world.Name)
		}
	}
	return nil
}

// ucFirstRune returns s with its first letter in upper case.
func ucFirstRune(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 || unicode.IsUpper(r) {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}
