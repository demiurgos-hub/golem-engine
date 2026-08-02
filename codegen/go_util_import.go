package codegen

import "strings"

// goGolemUtilImport returns the Go import path for engine utilities that live
// under the golem package (tiled, ldtk), given a configured integration
// golem_import. Server imports end with "/golem"; other integrations map to
// the sibling "/golem/<util>" under the same module root.
func goGolemUtilImport(golemImport, util string) string {
	const golemSuffix = "/golem"
	if strings.HasSuffix(golemImport, golemSuffix) {
		return golemImport + "/" + util
	}
	if i := strings.LastIndex(golemImport, "/"); i >= 0 {
		return golemImport[:i] + golemSuffix + "/" + util
	}
	return golemImport + "/" + util
}
