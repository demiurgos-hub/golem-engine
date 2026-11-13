package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const exampleSchema = `{"version":1,"types":[
{"name":"Movement","kind":"record","fields":[{"name":"speed","type":"float32","default":3},{"name":"stopping","type":"float64","default":1}]},
{"name":"Faction","kind":"enum","values":["Neutral","Friendly"]},
{"name":"Features","kind":"flags","values":["Attack","Move"]},
{"name":"Combat","kind":"record","definition":true,"fields":[{"name":"damage","type":"int32"},{"name":"next","type":"ref:Combat","optional":true}]},
{"name":"Monster","kind":"record","definition":true,"entity":"Monster","fields":[
{"name":"health","type":"int32","default":100},{"name":"movement","type":"Movement"},
{"name":"combat","type":"ref:Combat"},{"name":"faction","type":"Faction","default":"Neutral"},
{"name":"features","type":"Features","default":[]},{"name":"enabled","type":"bool","default":true},
{"name":"names","type":"[]string","default":[]},{"name":"note","type":"string","optional":true},
{"name":"alias","type":"string","optional":true,"default":"default"},
{"name":"sequence","type":"uint64","default":18446744073709551615}]}]}`

const exampleBundle = `{"version":1,"definitions":[
{"id":"demo/Combat/melee","type":"Combat","values":{"damage":7,"next":"demo/Combat/melee"}},
{"id":"demo/Monster/scout","type":"Monster","factory":"monster","source":{"guid":"abc","local_id":"9223372036854775807","path":"scout.prefab"},"values":{"movement":{"speed":5},"combat":"demo/Combat/melee","health":0,"enabled":false,"names":[],"alias":null,"features":["Move","Attack"]}},
{"id":"demo/Monster/guard","type":"Monster","factory":"monster","values":{"movement":{},"combat":"demo/Combat/melee"}}]}`

func schemaFixture(t *testing.T) Schema {
	t.Helper()
	s, err := ParseSchema(strings.NewReader(exampleSchema))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func loadFixture(t *testing.T) *Catalog {
	t.Helper()
	c, err := Load(strings.NewReader(exampleBundle), schemaFixture(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDefinitionsDefaultsReferencesAndImmutability(t *testing.T) {
	c := loadFixture(t)
	scout, ok := c.Lookup("demo/Monster/scout")
	if !ok {
		t.Fatal("scout missing")
	}
	if scout.Type() != "Monster" || scout.Factory() != "monster" {
		t.Fatal("lost type/factory")
	}
	for field, want := range map[string]string{"health": "0", "enabled": "false", "names": "[]", "alias": "null", "sequence": "18446744073709551615", "features": "[\"Attack\",\"Move\"]"} {
		if got := string(scout.Raw(field)); got != want {
			t.Errorf("%s=%s want %s", field, got, want)
		}
	}
	if scout.Raw("note") != nil {
		t.Fatal("absent optional was invented")
	}
	if string(scout.Raw("movement")) != `{"speed":5,"stopping":1}` {
		t.Fatal(string(scout.Raw("movement")))
	}
	combat, ok := scout.Ref("combat")
	if !ok || combat.ID() != "demo/Combat/melee" {
		t.Fatal("reference not resolved")
	}
	cycle, ok := combat.Ref("next")
	if !ok || cycle.ID() != combat.ID() {
		t.Fatal("ordinary ref cycle failed")
	}
	raw := scout.Raw("health")
	raw[0] = '9'
	if string(scout.Raw("health")) != "0" {
		t.Fatal("raw mutates catalog")
	}
	var movement map[string]any
	if err := scout.Decode("movement", &movement); err != nil {
		t.Fatal(err)
	}
	movement["speed"] = 99
	if string(scout.Raw("movement")) != `{"speed":5,"stopping":1}` {
		t.Fatal("decode exposed backing storage")
	}
	s := c.Schema()
	s.Types[0].Name = "Changed"
	if c.Schema().Types[0].Name == "Changed" {
		t.Fatal("schema exposed backing storage")
	}
	all := c.All("Monster")
	all[0] = View{}
	if c.All("Monster")[0].ID() == "" {
		t.Fatal("all exposed backing slice")
	}
	if err := c.ValidateFactories(map[string]bool{"monster": true}); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateFactories(nil); err == nil {
		t.Fatal("unknown factory accepted")
	}
}

func TestContentHashesAndFinalizedRoundtrip(t *testing.T) {
	c := loadFixture(t)
	final, err := c.MarshalBundle()
	if err != nil {
		t.Fatal(err)
	}
	copy, err := Load(bytes.NewReader(final), schemaFixture(t), Options{RequireSchemaHash: true, ExpectedHash: c.Hash()})
	if err != nil {
		t.Fatal(err)
	}
	if copy.Hash() != c.Hash() {
		t.Fatal("roundtrip changed semantic hash")
	}
	changed := strings.Replace(exampleBundle, "scout.prefab", "renamed.prefab", 1)
	other, err := Load(strings.NewReader(changed), schemaFixture(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if other.Hash() != c.Hash() {
		t.Fatal("provenance changed content identity")
	}
	changed = strings.Replace(exampleBundle, `"damage":7`, `"damage":8`, 1)
	other, err = Load(strings.NewReader(changed), schemaFixture(t), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if other.Hash() == c.Hash() {
		t.Fatal("data did not change hash")
	}
	if _, err = Load(strings.NewReader(exampleBundle), schemaFixture(t), Options{RequireSchemaHash: true}); err == nil {
		t.Fatal("unpinned runtime source accepted")
	}
	if _, err = Load(bytes.NewReader(final), schemaFixture(t), Options{ExpectedHash: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("content mismatch accepted")
	}
	s := schemaFixture(t)
	s.Generator = "anything"
	for i, j := 0, len(s.Types)-1; i < j; i, j = i+1, j-1 {
		s.Types[i], s.Types[j] = s.Types[j], s.Types[i]
	}
	hash, err := SchemaHash(s)
	if err != nil || hash != c.SchemaHash() {
		t.Fatal("schema ordering affected hash", err)
	}
}

func TestMalformedDefinitionsFailWithContext(t *testing.T) {
	tests := []struct{ name, old, new, want string }{
		{"duplicate identity", "demo/Monster/guard", "demo/Monster/scout", "duplicate identity"},
		{"missing reference", `"combat":"demo/Combat/melee"`, `"combat":"demo/Combat/missing"`, "missing Combat reference"},
		{"wrong kind", `"combat":"demo/Combat/melee"`, `"combat":"demo/Monster/guard"`, "wrong kind"},
		{"unknown field", `"health":0`, `"surprise":0`, "unknown field"},
		{"invalid integer", `"health":0`, `"health":2147483648`, "out-of-range"},
		{"string coercion", `"health":0`, `"health":"0"`, "without coercion"},
		{"fractional int", `"health":0`, `"health":0.5`, "integer"},
		{"null required", `"health":0`, `"health":null`, "null requires"},
		{"enum", `"health":0`, `"health":0,"faction":"Unknown"`, "unknown enum"},
		{"duplicate flags", `["Move","Attack"]`, `["Move","Move"]`, "duplicate flag"},
		{"float overflow", `"speed":5`, `"speed":1e100`, "non-finite"},
		{"null list", `"names":[]`, `"names":null`, "null requires"},
		{"null item", `"names":[]`, `"names":[null]`, "must be a string"},
		{"duplicate field", `"health":0`, `"health":0,"health":1`, "duplicate JSON"},
		{"factory", `"factory":"monster"`, `"factory":""`, "factory key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := strings.Replace(exampleBundle, tt.old, tt.new, 1)
			if input == exampleBundle {
				t.Fatal("fixture replacement not applied")
			}
			_, err := Load(strings.NewReader(input), schemaFixture(t), Options{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v want %s", err, tt.want)
			}
		})
	}
	for _, suffix := range []string{` {}`, ` garbage`} {
		if _, err := Load(strings.NewReader(exampleBundle+suffix), schemaFixture(t), Options{}); err == nil {
			t.Fatal("trailing input accepted")
		}
	}
}

func TestSchemaRejectsUnsupportedAndRecursiveTypes(t *testing.T) {
	for _, schema := range []string{
		`{"version":1,"types":[{"name":"A","kind":"record","fields":[{"name":"a","type":"A"}]}]}`,
		`{"version":1,"types":[{"name":"A","kind":"record","fields":[{"name":"a","type":"map:string"}]}]}`,
		`{"version":1,"types":[{"name":"A","kind":"record","fields":[{"name":"a","type":"[][]int32"}]}]}`,
		`{"version":1,"types":[{"name":"A","kind":"record","fields":[{"name":"a","type":"ref:Missing"}]}]}`,
		`{"version":1,"types":[{"name":"A","kind":"record","fields":[{"name":"a","type":"int32","default":null}]}]}`,
		`{"version":1,"types":[{"name":"A","kind":"union"}]}`,
		`{"version":1,"types":[{"name":"A","kind":"record","extends":"B"}]}`,
	} {
		if _, err := ParseSchema(strings.NewReader(schema)); err == nil {
			t.Errorf("accepted %s", schema)
		}
	}
}

func TestImportsAreExplicitPinnedAndImmutable(t *testing.T) {
	s := schemaFixture(t)
	base := `{"version":1,"definitions":[{"id":"demo/Combat/melee","type":"Combat","values":{"damage":7}}]}`
	imported, err := Load(strings.NewReader(base), s, Options{})
	if err != nil {
		t.Fatal(err)
	}
	local := fmt.Sprintf(`{"version":1,"imports":[{"hash":%q}],"definitions":[{"id":"demo/Monster/local","type":"Monster","factory":"monster","values":{"movement":{},"combat":"demo/Combat/melee"}}]}`, imported.Hash())
	if _, err = Load(strings.NewReader(local), s, Options{}); err == nil {
		t.Fatal("missing import accepted")
	}
	c, err := Load(strings.NewReader(local), s, Options{Imports: []*Catalog{imported}})
	if err != nil {
		t.Fatal(err)
	}
	view, _ := c.Lookup("demo/Monster/local")
	ref, ok := view.Ref("combat")
	if !ok || ref.ID() != "demo/Combat/melee" {
		t.Fatal("import not resolved")
	}
	undeclared := strings.Replace(local, fmt.Sprintf(`"imports":[{"hash":%q}],`, imported.Hash()), "", 1)
	if _, err = Load(strings.NewReader(undeclared), s, Options{Imports: []*Catalog{imported}}); err == nil {
		t.Fatal("undeclared import used")
	}
	duplicate := fmt.Sprintf(`{"version":1,"imports":[{"hash":%q}],"definitions":[{"id":"demo/Combat/melee","type":"Combat","values":{"damage":9}}]}`, imported.Hash())
	if _, err = Load(strings.NewReader(duplicate), s, Options{Imports: []*Catalog{imported}}); err == nil {
		t.Fatal("import shadowing accepted")
	}
}

func TestCatalogConcurrentReads(t *testing.T) {
	c := loadFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				v, _ := c.Lookup("demo/Monster/scout")
				var health int
				if err := v.Decode("health", &health); err != nil || health != 0 {
					t.Errorf("read: %v %d", err, health)
				}
				_, _ = c.MarshalBundle()
			}
		}()
	}
	wg.Wait()
}

func TestExpandedDefaultsHaveABudget(t *testing.T) {
	s := Schema{Version: 1, Types: []Type{{Name: "D", Kind: "record", Definition: true, Fields: []Field{{Name: "items", Type: "[]string", Default: json.RawMessage(`["a","b","c","d","e","f","g","h"]`)}}}}}
	b := `{"version":1,"definitions":[{"id":"d/D/a","type":"D","values":{}}]}`
	if _, err := Load(strings.NewReader(b), s, Options{Limits: Limits{Bytes: int64(len(b)), ListItems: 2}}); err == nil {
		t.Fatal("default bypassed list limit")
	}
	if _, err := Load(strings.NewReader(exampleBundle), schemaFixture(t), Options{Limits: Limits{Definitions: 1}}); err == nil {
		t.Fatal("definition limit bypassed")
	}
}

func TestSchemaDefaultsShareExpandedBudget(t *testing.T) {
	text, err := json.Marshal(strings.Repeat("x", 8192))
	if err != nil {
		t.Fatal(err)
	}
	s := Schema{Version: Version, Types: []Type{{Name: "Leaf", Kind: "record", Fields: []Field{{Name: "text", Type: "string", Default: text}}}}}
	for block := 0; block < 6; block++ {
		typ := Type{Name: fmt.Sprintf("Block%d", block), Kind: "record"}
		for field := 0; field < 128; field++ {
			typ.Fields = append(typ.Fields, Field{Name: fmt.Sprintf("field%d", field), Type: "Leaf", Default: json.RawMessage(`{}`)})
		}
		s.Types = append(s.Types, typ)
	}
	if err := ValidateSchema(s); err == nil || !strings.Contains(err.Error(), "expanded value budget") {
		t.Fatalf("aggregate default expansion escaped schema budget: %v", err)
	}
}

func TestDiamondImportsUseSemanticOrigin(t *testing.T) {
	s := schemaFixture(t)
	load := func(document string, imports ...*Catalog) *Catalog {
		t.Helper()
		catalog, err := Load(strings.NewReader(document), s, Options{Imports: imports})
		if err != nil {
			t.Fatal(err)
		}
		return catalog
	}
	baseJSON := `{"version":1,"definitions":[{"id":"demo/Combat/base","type":"Combat","values":{"damage":7}}]}`
	firstBase, secondBase := load(baseJSON), load(baseJSON)
	branch := func(id string, base *Catalog) *Catalog {
		return load(fmt.Sprintf(`{"version":1,"imports":[{"hash":%q}],"definitions":[{"id":"demo/Combat/%s","type":"Combat","values":{"damage":1,"next":"demo/Combat/base"}}]}`, base.Hash(), id), base)
	}
	left, right := branch("left", firstBase), branch("right", secondBase)
	diamondJSON := fmt.Sprintf(`{"version":1,"imports":[{"hash":%q},{"hash":%q}],"definitions":[]}`, left.Hash(), right.Hash())
	diamond := load(diamondJSON, left, right)
	if len(diamond.All("Combat")) != 3 {
		t.Fatal("shared origin was not deduplicated")
	}
	leftView, _ := diamond.Lookup("demo/Combat/left")
	base, ok := leftView.Ref("next")
	if !ok || base.ID() != "demo/Combat/base" {
		t.Fatal("diamond reference did not resolve")
	}
	conflict := load(strings.Replace(baseJSON, `"damage":7`, `"damage":8`, 1))
	conflictJSON := fmt.Sprintf(`{"version":1,"imports":[{"hash":%q},{"hash":%q}],"definitions":[]}`, firstBase.Hash(), conflict.Hash())
	if _, err := Load(strings.NewReader(conflictJSON), s, Options{Imports: []*Catalog{firstBase, conflict}}); err == nil || !strings.Contains(err.Error(), "duplicate imported identity") {
		t.Fatalf("conflicting origins were merged: %v", err)
	}
}

func TestScribeEmittedContract(t *testing.T) {
	root := filepath.Join("..", "..", "golem-unity", "Tests", "Fixtures")
	schemaData, err := os.ReadFile(filepath.Join(root, "content-schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	schema, err := ParseSchema(bytes.NewReader(schemaData))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "content-source.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(bytes.NewReader(data), schema, Options{})
	if err != nil {
		t.Fatal(err)
	}
	scout, ok := catalog.Lookup("demo/Monster/scout")
	if !ok {
		t.Fatal("missing scout")
	}
	guard, ok := catalog.Lookup("demo/Monster/guard")
	if !ok {
		t.Fatal("missing guard")
	}
	a, aOK := scout.Ref("movement")
	b, bOK := guard.Ref("movement")
	if !aOK || !bOK || a.ID() != b.ID() {
		t.Fatal("shared typed reference lost")
	}
	if string(scout.Raw("health")) != "80" || string(guard.Raw("health")) != "160" {
		t.Fatal("configured values lost")
	}
	if scout.Raw("note") != nil || string(guard.Raw("note")) != "null" {
		t.Fatal("presence/null distinction lost")
	}
	final, err := catalog.MarshalBundle()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Load(bytes.NewReader(final), schema, Options{RequireSchemaHash: true, ExpectedHash: catalog.Hash()}); err != nil {
		t.Fatal(err)
	}
}
