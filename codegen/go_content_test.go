package codegen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/golem/content"
	"github.com/demiurgos-hub/golem-engine/schema"
)

const contentConsumerSchema = `{"version":1,"types":[
{"name":"Drop","kind":"record","definition":true,"fields":[{"name":"label","type":"string"}]},
{"name":"Element","kind":"enum","values":["fire","ice"]},
{"name":"Abilities","kind":"flags","values":["melee","ranged"]},
{"name":"Stats","kind":"record","fields":[{"name":"hp","type":"int32","default":100},{"name":"drop","type":"ref:Drop"}]},
{"name":"Monster","kind":"record","definition":true,"entity":"Actor","fields":[
{"name":"stats","type":"Stats"},{"name":"phases","type":"[]Stats"},{"name":"drops","type":"[]ref:Drop"},
{"name":"element","type":"Element","default":"fire"},{"name":"abilities","type":"Abilities"},
{"name":"nickname","type":"string","optional":true},{"name":"optional_stats","type":"Stats","optional":true},
{"name":"optional_drop","type":"ref:Drop","optional":true},{"name":"friends","type":"[]ref:Monster"},
{"name":"levels","type":"[]int32"},{"name":"enabled","type":"bool","default":true}]}]}`

const contentConsumerBundle = `{"version":1,"definitions":[
{"id":"sample/Drop/coin","type":"Drop","values":{"label":"Coin"}},
{"id":"sample/Monster/red","type":"Monster","factory":"monster","values":{"stats":{"drop":"sample/Drop/coin"},"phases":[{"hp":20,"drop":"sample/Drop/coin"}],"drops":["sample/Drop/coin"],"abilities":["melee"],"friends":["sample/Monster/blue"],"levels":[1,2]}},
{"id":"sample/Monster/blue","type":"Monster","factory":"monster","values":{"stats":{"hp":50,"drop":"sample/Drop/coin"},"phases":[],"drops":["sample/Drop/coin"],"abilities":["ranged"],"friends":["sample/Monster/red"],"levels":[3],"nickname":"","enabled":false}}]}`

func contentFixture(t *testing.T) *schema.ContentData {
	t.Helper()
	dir := t.TempDir()
	writeConsumerFile(t, dir, "schema.json", contentConsumerSchema)
	writeConsumerFile(t, dir, "source.json", contentConsumerBundle)
	data, err := schema.LoadContent(dir, &schema.ContentConfig{Schema: "schema.json", Bundle: "source.json"}, []schema.EntityData{{Name: "Actor"}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestGeneratedContentConsumer(t *testing.T) {
	data := contentFixture(t)
	code, err := renderGoContent(data, "generated", schema.ProtoTemplateData{})
	if err != nil {
		t.Fatal(err)
	}
	again, err := renderGoContent(data, "generated", schema.ProtoTemplateData{})
	if err != nil || !bytes.Equal(code, again) {
		t.Fatal("non-deterministic content generation", err)
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeConsumerFile(t, dir, "go.mod", fmt.Sprintf(`module contentconsumer
go 1.25.4
require github.com/demiurgos-hub/golem-engine v0.0.0
replace github.com/demiurgos-hub/golem-engine => %q
replace github.com/demiurgos-hub/golem-engine/golem/collision => %q
replace github.com/demiurgos-hub/golem-engine/golem/nav => %q
replace github.com/demiurgos-hub/golem-engine/golem/physics3d => %q
`, filepath.ToSlash(root), filepath.ToSlash(filepath.Join(root, "golem/collision")), filepath.ToSlash(filepath.Join(root, "golem/nav")), filepath.ToSlash(filepath.Join(root, "golem/physics3d"))))
	writeConsumerFile(t, dir, "generated/Definitions.go", string(code))
	writeConsumerFile(t, dir, "generated/content_test.go", goContentConsumerContract)
	writeConsumerFile(t, dir, "generated/ContentDefinitions.json", string(data.BundleJSON))
	writeConsumerFile(t, dir, "generated/source.json", contentConsumerBundle)
	runConsumer(t, dir, "go", "mod", "tidy")
	runConsumer(t, dir, "go", "test", "-count=1", "./...")
}

const goContentConsumerContract = `package generated_test
import (
 "os"
 "testing"
 generated "contentconsumer/generated"
 "github.com/demiurgos-hub/golem-engine/golem/content"
)
func TestTypedContent(t *testing.T) {
 f,err:=os.Open("ContentDefinitions.json");if err!=nil {t.Fatal(err)};defer f.Close()
 defs,err:=generated.LoadDefinitions(f,content.Options{});if err!=nil {t.Fatal(err)}
 red,ok:=defs.LookupMonster("sample/Monster/red");if !ok {t.Fatal("missing red")}
 blue,ok:=defs.LookupMonster("sample/Monster/blue");if !ok {t.Fatal("missing blue")}
 if red.ID()==blue.ID() || red.Factory()!="monster" || red.Stats().Hp()!=100 || blue.Stats().Hp()!=50 {t.Fatal("definitions/defaults")}
 if red.Stats().Drop().Label()!="Coin" || red.Drops()[0].ID()!=blue.Drops()[0].ID() {t.Fatal("typed shared reference")}
 if red.Friends()[0].Friends()[0].ID()!=red.ID() {t.Fatal("reference cycle")}
 if red.Element()!=generated.ContentElementFire || !red.Abilities().Has(generated.ContentAbilitiesMelee) {t.Fatal("enum/flags")}
 if _,ok:=red.Nickname();ok {t.Fatal("absent optional")};if v,ok:=blue.Nickname();!ok||v!="" {t.Fatal("present empty optional")}
 if _,ok:=red.OptionalStats();ok {t.Fatal("optional record")};if _,ok:=red.OptionalDrop();ok {t.Fatal("optional reference")}
 if !red.Enabled() || blue.Enabled() {t.Fatal("default overwrote authored false")}
 levels:=red.Levels();levels[0]=99;if red.Levels()[0]!=1 {t.Fatal("slice escape")}
 phases:=red.Phases();phases[0]=generated.ContentStats{};if red.Phases()[0].Hp()!=20 {t.Fatal("record slice escape")}
 flags:=red.Abilities().Values();flags[0]=generated.ContentAbilitiesRanged;if !red.Abilities().Has(generated.ContentAbilitiesMelee) {t.Fatal("flag slice escape")}
 all:=defs.AllMonster();if len(all)!=2||all[0].ID()!="sample/Monster/blue" {t.Fatal("stable ordering")};all[0]=generated.ContentMonster{};if again,_:=defs.LookupMonster("sample/Monster/blue");again.ID()=="" {t.Fatal("index escape")}
 if defs.Hash()=="" || defs.Catalog().SchemaHash()!=generated.ContentSchemaHash {t.Fatal("identity")}
 if _,ok:=defs.LookupDrop(red.ID());ok {t.Fatal("wrong-type lookup")}
 var zero generated.ContentMonster;if zero.Stats().Drop().ID()!="" {t.Fatal("zero view")}
 raw,err:=os.Open("source.json");if err!=nil {t.Fatal(err)};defer raw.Close();if _,err=generated.LoadDefinitions(raw,content.Options{});err==nil {t.Fatal("accepted unfinalized schema identity")}
}
`

func TestContentGoNameCollisions(t *testing.T) {
	data := contentFixture(t)
	data.Schema.Types = append(data.Schema.Types, content.Type{Name: "Monster_fire", Kind: "enum", Values: []string{"value"}})
	data.Schema.Types = append(data.Schema.Types, content.Type{Name: "MonsterFire", Kind: "record"})
	if _, err := renderGoContent(data, "generated", schema.ProtoTemplateData{}); err == nil {
		t.Fatal("accepted colliding type names")
	}
	data = contentFixture(t)
	proto := schema.ProtoTemplateData{CustomTypes: []schema.CustomTypeData{{Name: "ContentMonster"}}}
	if _, err := renderGoContent(data, "generated", proto); err == nil {
		t.Fatal("accepted collision with protocol type")
	}
	proto = schema.ProtoTemplateData{ProtocolConstantSets: []schema.ProtocolConstantSetData{{Name: "ContentSchema", Members: []schema.ProtocolConstantData{{Name: "Hash"}}}}}
	if _, err := renderGoContent(data, "generated", proto); err == nil {
		t.Fatal("accepted collision with protocol constant")
	}
}

func TestBakeContentPreservesWireOutputs(t *testing.T) {
	dir := t.TempDir()
	base := `entity_schema: entities
proto:
  package: fixture
  out: entities.proto
integrations:
  go-server:
    out: generated
    golem_import: example.com/game/runtime
`
	writeConsumerFile(t, dir, "golem.yaml", base)
	writeConsumerFile(t, dir, "entities/actor.yaml", "entity: Actor\nvars:\n  hp: {tag: 1, type: int32, sync: tick}\n")
	if err := Bake(dir); err != nil {
		t.Fatal(err)
	}
	before := map[string][]byte{}
	for _, name := range []string{"entities.proto", "generated/entities_pb.go", "generated/actor_synced.go", "generated/golem_helpers.go"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = b
	}
	writeConsumerFile(t, dir, "schema.json", contentConsumerSchema)
	writeConsumerFile(t, dir, "source.json", contentConsumerBundle)
	writeConsumerFile(t, dir, "golem.yaml", base+"content:\n  schema: schema.json\n  bundle: source.json\n")
	if err := Bake(dir); err != nil {
		t.Fatal(err)
	}
	for name, want := range before {
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(want, got) {
			t.Fatalf("content changed %s: %v", name, err)
		}
	}
	for _, name := range []string{"Definitions.go", "ContentDefinitions.json"} {
		if _, err := os.Stat(filepath.Join(dir, "generated", name)); err != nil {
			t.Fatal(err)
		}
	}
	definitions, err := os.ReadFile(filepath.Join(dir, "generated/Definitions.go"))
	if err != nil || !bytes.Contains(definitions, []byte(`"github.com/demiurgos-hub/golem-engine/golem/content"`)) || bytes.Contains(definitions, []byte("example.com/game/runtime/content")) {
		t.Fatalf("content import followed consumer runtime facade: %v", err)
	}
	var final content.Bundle
	b, err := os.ReadFile(filepath.Join(dir, "generated/ContentDefinitions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(b, &final); err != nil || final.SchemaHash == "" {
		t.Fatal("bundle was not finalized", err)
	}
	outputs := selectIntegrationOutputs("go-client", schema.SharedData{Content: &schema.ContentData{}})
	for _, output := range outputs {
		if strings.Contains(output.file, "Definitions") {
			t.Fatal("server definitions leaked to client output")
		}
	}
}
