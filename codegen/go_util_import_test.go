package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func TestGoGolemUtilImport(t *testing.T) {
	cases := []struct {
		golemImport string
		util        string
		want        string
	}{
		{
			"github.com/demiurgos-hub/golem-engine/golem",
			"tiled",
			"github.com/demiurgos-hub/golem-engine/golem/tiled",
		},
		{
			"github.com/demiurgos-hub/golem-engine/golem-go-client",
			"tiled",
			"github.com/demiurgos-hub/golem-engine/golem/tiled",
		},
		{
			"github.com/demiurgos-hub/golem-engine/golem-go-client",
			"ldtk",
			"github.com/demiurgos-hub/golem-engine/golem/ldtk",
		},
		{
			"github.com/demiurgos-hub/golem-engine/golem-ebiten",
			"tiled",
			"github.com/demiurgos-hub/golem-engine/golem/tiled",
		},
	}
	for _, tc := range cases {
		got := goGolemUtilImport(tc.golemImport, tc.util)
		if got != tc.want {
			t.Fatalf("goGolemUtilImport(%q,%q)=%q want %q", tc.golemImport, tc.util, got, tc.want)
		}
	}
}

func TestGenerateGoWorldProtoClientTiledImport(t *testing.T) {
	dir := t.TempDir()
	proto := schema.ProtoTemplateData{
		WorldTypes: []schema.WorldTypeData{
			{
				Name:     "Zone",
				DataName: "ZoneData",
				Source: &schema.WorldSourceDef{
					Format:    "tiled",
					File:      "maps/zone.tmj",
					URLPrefix: "/maps/",
				},
				Fields: []schema.WorldFieldInfo{
					{SnakeName: "map_url", GoName: "MapUrl", GoType: "string", ProtoType: "string", ProtoTag: 1},
				},
			},
		},
	}
	proto.WorldUpdateFields = []schema.WorldUpdateField{
		{MessageType: "ZoneData", SnakeName: "zone_data", Tag: 1},
	}

	clientImport := "github.com/demiurgos-hub/golem-engine/golem-go-client"
	if err := generateGoWorldProto(dir, clientImport, "client", proto); err != nil {
		t.Fatalf("generateGoWorldProto: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "world_pb.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	want := `"github.com/demiurgos-hub/golem-engine/golem/tiled"`
	if !strings.Contains(src, want) {
		t.Fatalf("go-client world_pb missing tiled import %s", want)
	}
	bad := clientImport + "/tiled"
	if strings.Contains(src, bad) {
		t.Fatalf("go-client must not import %s", bad)
	}
}

func TestGenerateGoWorldProtoServerTiledImport(t *testing.T) {
	dir := t.TempDir()
	proto := schema.ProtoTemplateData{
		WorldTypes: []schema.WorldTypeData{
			{
				Name:     "Zone",
				DataName: "ZoneData",
				Source: &schema.WorldSourceDef{
					Format: "ldtk",
					File:   "maps/zone.ldtk",
				},
				Fields: []schema.WorldFieldInfo{
					{SnakeName: "tile_data", GoName: "TileData", GoType: "[]byte", ProtoType: "bytes", ProtoTag: 1},
				},
			},
		},
	}
	proto.WorldUpdateFields = []schema.WorldUpdateField{
		{MessageType: "ZoneData", SnakeName: "zone_data", Tag: 1},
	}
	serverImport := "github.com/demiurgos-hub/golem-engine/golem"
	if err := generateGoWorldProto(dir, serverImport, "generated", proto); err != nil {
		t.Fatalf("generateGoWorldProto: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "world_pb.go"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	want := `"github.com/demiurgos-hub/golem-engine/golem/ldtk"`
	if !strings.Contains(src, want) {
		t.Fatalf("server world_pb missing ldtk import %s", want)
	}
}
