package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Limits bounds bundle decoding and expanded default values. Zero selects defaults.
type Limits struct {
	Bytes                                      int64
	Definitions, ListItems, StringBytes, Depth int
	budget                                     *valueBudget
}

type valueBudget struct{ remaining int64 }

// DefaultLimits supplies finite authoring/runtime resource limits.
func DefaultLimits() Limits {
	return Limits{Bytes: 64 << 20, Definitions: 100000, ListItems: 10000, StringBytes: 1 << 20, Depth: 32}
}

// Source identifies Unity provenance for diagnostics, never gameplay identity.
type Source struct {
	GUID    string `json:"guid,omitempty"`
	LocalID string `json:"local_id,omitempty"`
	Path    string `json:"path,omitempty"`
}

// Definition is a mutable transport document. Load takes an owned immutable copy.
type Definition struct {
	ID      string                     `json:"id"`
	Type    string                     `json:"type"`
	Factory string                     `json:"factory,omitempty"`
	Values  map[string]json.RawMessage `json:"values"`
	Source  *Source                    `json:"source,omitempty"`
}

// Import pins an explicitly supplied catalog by its semantic content hash.
type Import struct {
	Hash string `json:"hash"`
}

// Bundle is the portable authored or finalized content document.
// Authoring sources may omit SchemaHash; finalized runtime bundles include it.
type Bundle struct {
	Generator   string       `json:"generator,omitempty"`
	Version     int          `json:"version"`
	SchemaHash  string       `json:"schema_hash,omitempty"`
	Definitions []Definition `json:"definitions"`
	Imports     []Import     `json:"imports,omitempty"`
}

// Options supplies explicit imports and compatibility requirements. Extra catalogs
// in Imports are available for selection but cannot resolve undeclared references.
type Options struct {
	Imports           []*Catalog
	ExpectedHash      string
	RequireSchemaHash bool
	Limits            Limits
}

type entry struct {
	id, kind, factory string
	origin            string
	fields            map[string]json.RawMessage
	source            *Source
}

// Catalog owns a fully validated, immutable definition graph. It is safe to share.
type Catalog struct {
	schema  *compiledSchema
	entries map[string]*entry
	own     []*entry
	imports []Import
	hash    string
}

// View is a read-only definition handle retaining its owning catalog.
type View struct {
	catalog *Catalog
	entry   *entry
}

// Load validates one bundle and every selected reference before publishing a catalog.
// It creates no entities and performs no filesystem or networking operations.
func Load(r io.Reader, schema Schema, options Options) (*Catalog, error) {
	c, err := compileSchema(schema)
	if err != nil {
		return nil, err
	}
	l := options.Limits
	defaults := DefaultLimits()
	if l.Bytes == 0 {
		l.Bytes = defaults.Bytes
	}
	if l.Definitions == 0 {
		l.Definitions = defaults.Definitions
	}
	if l.ListItems == 0 {
		l.ListItems = defaults.ListItems
	}
	if l.StringBytes == 0 {
		l.StringBytes = defaults.StringBytes
	}
	if l.Depth == 0 {
		l.Depth = defaults.Depth
	}
	if l.Bytes < 1 || l.Definitions < 1 || l.ListItems < 1 || l.StringBytes < 1 || l.Depth < 1 || l.Depth > 64 {
		return nil, fmt.Errorf("content: invalid resource limits")
	}
	l.budget = &valueBudget{remaining: l.Bytes}
	var bundle Bundle
	if err = decodeDocument(r, l.Bytes, &bundle); err != nil {
		return nil, fmt.Errorf("content bundle: %w", err)
	}
	if bundle.Version != Version {
		return nil, fmt.Errorf("content: unsupported bundle version %d", bundle.Version)
	}
	if (options.RequireSchemaHash || bundle.SchemaHash != "") && bundle.SchemaHash != c.hash {
		return nil, fmt.Errorf("content: schema hash mismatch: got %q, want %s", bundle.SchemaHash, c.hash)
	}
	if len(bundle.Definitions) > l.Definitions || len(bundle.Imports) > 256 {
		return nil, fmt.Errorf("content: definition/import limit exceeded")
	}
	cat := &Catalog{schema: c, entries: map[string]*entry{}}
	available := map[string]*Catalog{}
	for _, imported := range options.Imports {
		if imported != nil {
			available[imported.hash] = imported
		}
	}
	seenImports := map[string]bool{}
	for _, imp := range bundle.Imports {
		if imp.Hash == "" || seenImports[imp.Hash] {
			return nil, fmt.Errorf("content: missing/duplicate import hash %q", imp.Hash)
		}
		seenImports[imp.Hash] = true
		imported, ok := available[imp.Hash]
		if !ok {
			return nil, fmt.Errorf("content: required import %s was not supplied", imp.Hash)
		}
		if imported.SchemaHash() != c.hash {
			return nil, fmt.Errorf("content: import %s uses a different schema", imp.Hash)
		}
		for id, e := range imported.entries {
			if prior, exists := cat.entries[id]; exists && prior.origin != e.origin {
				return nil, fmt.Errorf("content: duplicate imported identity %s", id)
			}
			cat.entries[id] = e
		}
		cat.imports = append(cat.imports, imp)
	}
	if len(cat.entries)+len(bundle.Definitions) > l.Definitions {
		return nil, fmt.Errorf("content: combined definition limit exceeded")
	}
	var refs []pendingReference
	for _, def := range bundle.Definitions {
		where := def.ID
		if def.Source != nil {
			where += fmt.Sprintf(" (%s; %s:%s)", def.Source.Path, def.Source.GUID, def.Source.LocalID)
		}
		if err = ValidateID(def.ID); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if prior, exists := cat.entries[def.ID]; exists {
			return nil, fmt.Errorf("content: duplicate identity %s; first source %+v", where, prior.source)
		}
		t, ok := c.types[def.Type]
		if !ok || !t.Definition {
			return nil, fmt.Errorf("content %s: unknown definition kind %q", where, def.Type)
		}
		if strings.Split(def.ID, "/")[1] != def.Type {
			return nil, fmt.Errorf("content %s: identity kind does not match %s", where, def.Type)
		}
		if t.Entity != "" && def.Factory == "" {
			return nil, fmt.Errorf("content %s: archetype requires a factory key", where)
		}
		if len(def.Factory) > 512 {
			return nil, fmt.Errorf("content %s: factory key too long", where)
		}
		values := map[string]any{}
		if def.Values == nil {
			return nil, fmt.Errorf("content %s: values must be an object", where)
		}
		for k, raw := range def.Values {
			var v any
			if err = decodeDocument(bytes.NewReader(raw), l.Bytes, &v); err != nil {
				return nil, fmt.Errorf("content %s.%s: %w", where, k, err)
			}
			values[k] = v
		}
		normalized, err := c.normalizeRecord(t, values, where, l, &refs, 0)
		if err != nil {
			return nil, err
		}
		e := &entry{id: def.ID, kind: def.Type, factory: def.Factory, fields: map[string]json.RawMessage{}, source: def.Source}
		for k, v := range normalized {
			raw, _ := json.Marshal(v)
			e.fields[k] = raw
		}
		cat.entries[e.id] = e
		cat.own = append(cat.own, e)
	}
	for _, ref := range refs {
		target, ok := cat.entries[ref.id]
		if !ok {
			return nil, fmt.Errorf("content %s: missing %s reference %s", ref.path, ref.kind, ref.id)
		}
		if target.kind != ref.kind {
			return nil, fmt.Errorf("content %s: reference %s has kind %s, expected %s", ref.path, ref.id, target.kind, ref.kind)
		}
	}
	sort.Slice(cat.own, func(i, j int) bool { return cat.own[i].id < cat.own[j].id })
	sort.Slice(cat.imports, func(i, j int) bool { return cat.imports[i].Hash < cat.imports[j].Hash })
	semantic := cat.bundle(false)
	raw, err := json.Marshal(semantic)
	if err != nil {
		return nil, err
	}
	cat.hash = digest(raw)
	for _, e := range cat.own {
		e.origin = cat.hash
	}
	if options.ExpectedHash != "" && options.ExpectedHash != cat.hash {
		return nil, fmt.Errorf("content: content hash mismatch: got %s, want %s", cat.hash, options.ExpectedHash)
	}
	return cat, nil
}

// Lookup resolves a stable ID in this catalog's explicitly selected import closure.
func (c *Catalog) Lookup(id string) (View, bool) {
	if c == nil {
		return View{}, false
	}
	e, ok := c.entries[id]
	return View{catalog: c, entry: e}, ok
}

// All returns independently owned handles in stable ID order for one definition kind.
func (c *Catalog) All(kind string) []View {
	if c == nil {
		return nil
	}
	var result []View
	for _, e := range c.entries {
		if e.kind == kind {
			result = append(result, View{catalog: c, entry: e})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID() < result[j].ID() })
	return result
}

// Hash identifies normalized data and pinned imports, excluding provenance.
func (c *Catalog) Hash() string {
	if c == nil {
		return ""
	}
	return c.hash
}

// SchemaHash identifies the complete schema used to validate this catalog.
func (c *Catalog) SchemaHash() string {
	if c == nil {
		return ""
	}
	return c.schema.hash
}

// Schema returns an independent copy of the normalized schema.
func (c *Catalog) Schema() Schema {
	if c == nil {
		return Schema{}
	}
	data, _ := json.Marshal(c.schema.schema)
	var result Schema
	_ = json.Unmarshal(data, &result)
	return result
}

// MarshalBundle returns owned finalized JSON including the required schema hash.
func (c *Catalog) MarshalBundle() ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("content: nil catalog")
	}
	return json.MarshalIndent(c.bundle(true), "", "  ")
}

func (c *Catalog) bundle(provenance bool) Bundle {
	b := Bundle{Version: Version, SchemaHash: c.schema.hash, Definitions: make([]Definition, 0, len(c.own)), Imports: append([]Import(nil), c.imports...)}
	for _, e := range c.own {
		d := Definition{ID: e.id, Type: e.kind, Factory: e.factory, Values: e.fields}
		if provenance {
			d.Source = e.source
		}
		b.Definitions = append(b.Definitions, d)
	}
	return b
}

// ID returns the authored definition identity, or empty for an invalid view.
func (v View) ID() string {
	if v.entry == nil {
		return ""
	}
	return v.entry.id
}

// Type returns the definition kind, or empty for an invalid view.
func (v View) Type() string {
	if v.entry == nil {
		return ""
	}
	return v.entry.kind
}

// Factory returns the consumer-owned behavior/factory key.
func (v View) Factory() string {
	if v.entry == nil {
		return ""
	}
	return v.entry.factory
}

// Raw returns an owned normalized field value; nil means absent, "null" means explicit null.
func (v View) Raw(field string) json.RawMessage {
	if v.entry == nil {
		return nil
	}
	return append(json.RawMessage(nil), v.entry.fields[field]...)
}

// Decode copies a present non-null field into caller-owned storage.
func (v View) Decode(field string, target any) error {
	raw := v.Raw(field)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return fmt.Errorf("content %s.%s: absent or null", v.ID(), field)
	}
	return json.Unmarshal(raw, target)
}

// Ref resolves a direct reference field within the same immutable catalog.
func (v View) Ref(field string) (View, bool) {
	if v.entry == nil {
		return View{}, false
	}
	t := v.catalog.schema.types[v.entry.kind]
	for _, f := range t.Fields {
		if f.Name == field && strings.HasPrefix(f.Type, "ref:") {
			var id string
			if v.Decode(field, &id) != nil {
				return View{}, false
			}
			return v.catalog.Lookup(id)
		}
	}
	return View{}, false
}

// ValidateFactories checks every archetype against explicit consumer factory keys.
// It invokes no factory and does not treat exported data as executable behavior.
func (c *Catalog) ValidateFactories(keys map[string]bool) error {
	if c == nil {
		return fmt.Errorf("content: nil catalog")
	}
	ids := make([]string, 0, len(c.entries))
	for id := range c.entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		e := c.entries[id]
		if c.schema.types[e.kind].Entity != "" && !keys[e.factory] {
			return fmt.Errorf("content %s: unknown factory %q", e.id, e.factory)
		}
	}
	return nil
}
