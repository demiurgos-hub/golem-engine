package codegen

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"golem-engine/schema"
)

// ComputeSchemaFingerprint returns a lowercase hex SHA-256 that covers only
// the parts of the entity schemas that can cause silent data corruption when
// loading a snapshot: entity type names (used by RestoreEntity dispatch) and
// the implicit position encoding and the (tag → protoType) mapping for each
// variable.
//
// Notably excluded: variable names, sync modes, and the persistent flag.
// This means renaming a variable, adding or removing variables, or changing
// sync/persistent settings does NOT change the fingerprint and does NOT
// invalidate existing snapshots. Only reassigning a tag number or changing
// a variable's type at an existing tag will produce a new fingerprint.
func ComputeSchemaFingerprint(entities []schema.EntityData) string {
	// Sort entities by name for a deterministic canonical string.
	sorted := make([]schema.EntityData, len(entities))
	copy(sorted, entities)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var b strings.Builder
	b.WriteString("implicit-position:float32\n")
	for _, e := range sorted {
		b.WriteString(e.Name)
		b.WriteByte('\n')

		// AllVars are already in user-tag order (set by BuildEntityData).
		// Only tag and protoType are included — variable names and sync mode
		// do not affect how snapshot bytes are encoded or decoded.
		for _, v := range e.AllVars {
			fmt.Fprintf(&b, "%d:%s\n", v.UserTag, v.ProtoType)
		}
	}

	sum := sha256.Sum256([]byte(b.String()))
	return fmt.Sprintf("%x", sum)
}
