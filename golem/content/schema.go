// Package content validates immutable gameplay definitions independently of
// replication, entity lifecycle, Unity, and physics/navigation runtime handles.
package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Version is the portable definition schema and bundle format version.
const Version = 1

// Schema describes portable records and definition kinds, never wire messages.
type Schema struct {
	Generator string `json:"generator,omitempty"`
	Version   int    `json:"version"`
	Types     []Type `json:"types"`
}

// Type declares a record, enum, or flags set. Only records can be definitions.
type Type struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Definition bool     `json:"definition,omitempty"`
	Entity     string   `json:"entity,omitempty"`
	Fields     []Field  `json:"fields,omitempty"`
	Values     []string `json:"values,omitempty"`
}

// Field declares a scalar, named type, []element list, or ref:DefinitionKind.
// A nil Default means no default; explicit null requires Optional.
type Field struct {
	Name     string          `json:"name"`
	Type     string          `json:"type"`
	Optional bool            `json:"optional,omitempty"`
	Default  json.RawMessage `json:"default,omitempty"`
}

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var keyPart = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

type compiledSchema struct {
	schema Schema
	types  map[string]Type
	hash   string
}

// ParseSchema strictly decodes and validates one bounded JSON schema.
func ParseSchema(r io.Reader) (Schema, error) {
	var s Schema
	if err := decodeDocument(r, 4<<20, &s); err != nil {
		return s, fmt.Errorf("content schema: %w", err)
	}
	if err := ValidateSchema(s); err != nil {
		return Schema{}, err
	}
	return s, nil
}

// ValidateSchema checks names, supported types, defaults, and inline cycles.
func ValidateSchema(s Schema) error { _, err := compileSchema(s); return err }

// SchemaHash hashes the normalized complete schema, excluding generator metadata.
func SchemaHash(s Schema) (string, error) {
	c, err := compileSchema(s)
	if err != nil {
		return "", err
	}
	return c.hash, nil
}

func compileSchema(s Schema) (*compiledSchema, error) {
	if s.Version != Version {
		return nil, fmt.Errorf("content: unsupported schema version %d", s.Version)
	}
	if len(s.Types) == 0 || len(s.Types) > 256 {
		return nil, fmt.Errorf("content: schema requires 1..256 types")
	}
	// Own every nested slice and default before sorting or retaining it.
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	if len(b) > 4<<20 {
		return nil, fmt.Errorf("content: schema exceeds 4 MiB")
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	s.Generator = ""
	c := &compiledSchema{schema: s, types: make(map[string]Type)}
	for _, t := range s.Types {
		if !identifier.MatchString(t.Name) || scalar(t.Name) {
			return nil, fmt.Errorf("content: invalid type name %q", t.Name)
		}
		if _, ok := c.types[t.Name]; ok {
			return nil, fmt.Errorf("content: duplicate type %s", t.Name)
		}
		if t.Kind != "record" && t.Kind != "enum" && t.Kind != "flags" {
			return nil, fmt.Errorf("content %s: unsupported kind %q", t.Name, t.Kind)
		}
		if t.Entity != "" && (!t.Definition || !identifier.MatchString(t.Entity)) {
			return nil, fmt.Errorf("content %s: entity requires a definition and valid entity name", t.Name)
		}
		if t.Kind != "record" && (t.Definition || len(t.Fields) != 0 || len(t.Values) == 0) {
			return nil, fmt.Errorf("content %s: enum/flags requires values and cannot have fields or identity", t.Name)
		}
		if t.Kind == "record" && len(t.Values) != 0 {
			return nil, fmt.Errorf("content %s: records cannot declare enum values", t.Name)
		}
		if len(t.Fields) > 256 || len(t.Values) > 1024 {
			return nil, fmt.Errorf("content %s: schema member limit exceeded", t.Name)
		}
		seen := map[string]bool{}
		for _, v := range t.Values {
			if !identifier.MatchString(v) || seen[v] {
				return nil, fmt.Errorf("content %s: invalid/duplicate symbol %q", t.Name, v)
			}
			seen[v] = true
		}
		seen = map[string]bool{}
		for _, f := range t.Fields {
			if !identifier.MatchString(f.Name) || seen[f.Name] {
				return nil, fmt.Errorf("content %s: invalid/duplicate field %q", t.Name, f.Name)
			}
			seen[f.Name] = true
		}
		c.types[t.Name] = t
	}
	for _, t := range s.Types {
		for _, f := range t.Fields {
			if err := c.checkType(f.Type, 0); err != nil {
				return nil, fmt.Errorf("content %s.%s: %w", t.Name, f.Name, err)
			}
		}
	}
	visiting, done := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(name string) error {
		if visiting[name] {
			return fmt.Errorf("content %s: recursive inline records are unsupported; use definition references", name)
		}
		if done[name] {
			return nil
		}
		visiting[name] = true
		for _, f := range c.types[name].Fields {
			n := strings.TrimPrefix(f.Type, "[]")
			if t, ok := c.types[n]; ok && t.Kind == "record" {
				if err := visit(n); err != nil {
					return err
				}
			}
		}
		delete(visiting, name)
		done[name] = true
		return nil
	}
	for _, t := range s.Types {
		if err := visit(t.Name); err != nil {
			return nil, err
		}
	}
	defaultLimits := DefaultLimits()
	defaultLimits.Bytes = 4 << 20
	defaultLimits.budget = &valueBudget{remaining: defaultLimits.Bytes}
	for i, t := range c.schema.Types {
		for j, f := range t.Fields {
			if f.Default != nil {
				var value any
				if err := decodeDocument(bytes.NewReader(f.Default), 4<<20, &value); err != nil {
					return nil, fmt.Errorf("content %s.%s default: %w", t.Name, f.Name, err)
				}
				value, err = c.normalizeField(f, value, t.Name+"."+f.Name+" default", defaultLimits, nil, 0)
				if err != nil {
					return nil, err
				}
				normalized, _ := json.Marshal(value)
				c.schema.Types[i].Fields[j].Default = normalized
			}
		}
		sort.Slice(c.schema.Types[i].Fields, func(a, b int) bool { return c.schema.Types[i].Fields[a].Name < c.schema.Types[i].Fields[b].Name })
		sort.Strings(c.schema.Types[i].Values)
	}
	sort.Slice(c.schema.Types, func(i, j int) bool { return c.schema.Types[i].Name < c.schema.Types[j].Name })
	for _, t := range c.schema.Types {
		c.types[t.Name] = t
	}
	b, err = json.Marshal(c.schema)
	if err != nil {
		return nil, err
	}
	if len(b) > 4<<20 {
		return nil, fmt.Errorf("content: normalized schema exceeds 4 MiB")
	}
	c.hash = digest(b)
	return c, nil
}

func scalar(t string) bool {
	switch t {
	case "bool", "string", "int32", "uint32", "int64", "uint64", "float32", "float64":
		return true
	}
	return false
}

func (c *compiledSchema) checkType(t string, depth int) error {
	if depth > 1 {
		return fmt.Errorf("nested lists are unsupported; use a named record")
	}
	if strings.HasPrefix(t, "[]") {
		return c.checkType(t[2:], depth+1)
	}
	if strings.HasPrefix(t, "ref:") {
		target, ok := c.types[t[4:]]
		if !ok || !target.Definition {
			return fmt.Errorf("reference target %q must be a definition kind", t[4:])
		}
		return nil
	}
	if scalar(t) {
		return nil
	}
	if named, ok := c.types[t]; ok {
		if named.Definition {
			return fmt.Errorf("definition %s must be referenced using ref:%s", t, t)
		}
		return nil
	}
	return fmt.Errorf("unsupported field type %q", t)
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// ValidateID checks a case-sensitive namespace/kind/key authored identity.
func ValidateID(id string) error {
	parts := strings.Split(id, "/")
	if len(parts) != 3 || len(id) > 512 {
		return fmt.Errorf("content: invalid identity %q; expected namespace/kind/key", id)
	}
	for _, p := range parts {
		if !keyPart.MatchString(p) {
			return fmt.Errorf("content: invalid identity %q", id)
		}
	}
	if !identifier.MatchString(parts[1]) {
		return fmt.Errorf("content: invalid kind in identity %q", id)
	}
	return nil
}
