package golem

import (
	"runtime/debug"
	"testing"
)

func TestEngineVersionNonEmpty(t *testing.T) {
	if v := EngineVersion(); v == "" {
		t.Fatal("EngineVersion returned an empty string")
	}
}

func TestEngineVersionFromBuildInfo(t *testing.T) {
	tests := []struct {
		name string
		info debug.BuildInfo
		want string
	}{
		{name: "main module versioned", info: debug.BuildInfo{Main: debug.Module{Path: engineModulePath, Version: "v1.2.3"}}, want: "v1.2.3"},
		{name: "main module unversioned", info: debug.BuildInfo{Main: debug.Module{Path: engineModulePath}}, want: develVersion},
		{name: "main module devel", info: debug.BuildInfo{Main: debug.Module{Path: engineModulePath, Version: "(devel)"}}, want: develVersion},
		{name: "dependency", info: debug.BuildInfo{
			Main: debug.Module{Path: "example.com/game", Version: "v0.1.0"},
			Deps: []*debug.Module{{Path: "example.com/other", Version: "v9.9.9"}, {Path: engineModulePath, Version: "v0.4.0"}},
		}, want: "v0.4.0"},
		{name: "dependency replaced by version", info: debug.BuildInfo{
			Main: debug.Module{Path: "example.com/game"},
			Deps: []*debug.Module{{Path: engineModulePath, Version: "v0.4.0", Replace: &debug.Module{Path: "example.com/fork", Version: "v0.4.1"}}},
		}, want: "v0.4.1"},
		{name: "dependency replaced by directory", info: debug.BuildInfo{
			Main: debug.Module{Path: "example.com/game"},
			Deps: []*debug.Module{{Path: engineModulePath, Version: "v0.0.0-00010101000000-000000000000", Replace: &debug.Module{Path: "../golem-engine"}}},
		}, want: develVersion},
		{name: "not linked", info: debug.BuildInfo{Main: debug.Module{Path: "example.com/game"}}, want: develVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := engineVersionFromBuildInfo(&tt.info); got != tt.want {
				t.Fatalf("engineVersionFromBuildInfo = %q, want %q", got, tt.want)
			}
		})
	}
}
