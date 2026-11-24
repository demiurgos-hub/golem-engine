package golem

import "runtime/debug"

// engineModulePath is the Go module path of Golem Engine.
const engineModulePath = "github.com/demiurgos-hub/golem-engine"

// develVersion is reported when the engine version cannot be determined, as
// for a local checkout or a replace directive pointing at a directory.
const develVersion = "(devel)"

// EngineVersion returns the Golem Engine module version compiled into the
// running binary, read from runtime/debug.ReadBuildInfo: the main module's
// version when the binary is built inside the engine module, otherwise the
// version of the engine dependency (the replacement's version when a replace
// directive applies). It returns "(devel)" when the version is unknown: no
// build information, a replace to a local directory, or an unversioned local
// build. It never returns an empty string.
func EngineVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return develVersion
	}
	return engineVersionFromBuildInfo(info)
}

func engineVersionFromBuildInfo(info *debug.BuildInfo) string {
	if info.Main.Path == engineModulePath {
		return versionOrDevel(info.Main.Version)
	}
	for _, dep := range info.Deps {
		if dep.Path != engineModulePath {
			continue
		}
		if dep.Replace != nil {
			return versionOrDevel(dep.Replace.Version)
		}
		return versionOrDevel(dep.Version)
	}
	return develVersion
}

func versionOrDevel(v string) string {
	if v == "" {
		return develVersion
	}
	return v
}
