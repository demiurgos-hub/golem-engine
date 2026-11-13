package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

type pendingReference struct{ id, kind, path string }

func (c *compiledSchema) normalizeRecord(t Type, values map[string]any, path string, l Limits, refs *[]pendingReference, depth int) (map[string]any, error) {
	if l.budget == nil {
		l.budget = &valueBudget{remaining: l.Bytes}
	}
	if err := spend(l, 2, path); err != nil {
		return nil, err
	}
	if depth > l.Depth {
		return nil, fmt.Errorf("content %s: nesting limit exceeded", path)
	}
	fields := map[string]Field{}
	for _, f := range t.Fields {
		fields[f.Name] = f
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, ok := fields[k]; !ok {
			return nil, fmt.Errorf("content %s.%s: unknown field", path, k)
		}
	}
	out := make(map[string]any, len(t.Fields))
	for _, f := range t.Fields {
		if err := spend(l, int64(len(f.Name)+4), path); err != nil {
			return nil, err
		}
		value, present := values[f.Name]
		if !present {
			if f.Default != nil {
				if err := decodeDocument(bytes.NewReader(f.Default), l.Bytes, &value); err != nil {
					return nil, err
				}
			} else if f.Optional {
				continue
			} else {
				return nil, fmt.Errorf("content %s.%s: required field is absent", path, f.Name)
			}
		}
		normalized, err := c.normalizeField(f, value, path+"."+f.Name, l, refs, depth+1)
		if err != nil {
			return nil, err
		}
		out[f.Name] = normalized
	}
	return out, nil
}

func (c *compiledSchema) normalizeField(f Field, value any, path string, l Limits, refs *[]pendingReference, depth int) (any, error) {
	if l.budget == nil {
		l.budget = &valueBudget{remaining: l.Bytes}
	}
	if value == nil {
		if f.Optional {
			if err := spend(l, 4, path); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return nil, fmt.Errorf("content %s: null requires an optional field", path)
	}
	return c.normalizeValue(f.Type, value, path, l, refs, depth)
}

func (c *compiledSchema) normalizeValue(typ string, value any, path string, l Limits, refs *[]pendingReference, depth int) (any, error) {
	if l.budget == nil {
		l.budget = &valueBudget{remaining: l.Bytes}
	}
	cost := int64(8)
	if text, ok := value.(string); ok {
		encoded, _ := json.Marshal(text)
		cost = int64(len(encoded))
	}
	if err := spend(l, cost, path); err != nil {
		return nil, err
	}
	fail := func(message string) (any, error) {
		return nil, fmt.Errorf("content %s: %s (expected %s)", path, message, typ)
	}
	if depth > l.Depth {
		return fail("nesting limit exceeded")
	}
	if strings.HasPrefix(typ, "[]") {
		list, ok := value.([]any)
		if !ok {
			return fail("must be an array")
		}
		if len(list) > l.ListItems {
			return fail("list item limit exceeded")
		}
		out := make([]any, len(list))
		for i, item := range list {
			v, err := c.normalizeValue(typ[2:], item, fmt.Sprintf("%s[%d]", path, i), l, refs, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}
	if strings.HasPrefix(typ, "ref:") {
		id, ok := value.(string)
		if !ok {
			return fail("must be an identity string")
		}
		if err := ValidateID(id); err != nil {
			return fail(err.Error())
		}
		if strings.Split(id, "/")[1] != typ[4:] {
			return fail("reference identity has the wrong kind")
		}
		if refs != nil {
			*refs = append(*refs, pendingReference{id: id, kind: typ[4:], path: path})
		}
		return id, nil
	}
	switch typ {
	case "bool":
		if b, ok := value.(bool); ok {
			return b, nil
		}
		return fail("must be a boolean")
	case "string":
		if s, ok := value.(string); ok {
			if len(s) > l.StringBytes {
				return fail("string byte limit exceeded")
			}
			return s, nil
		}
		return fail("must be a string")
	case "int32", "int64", "uint32", "uint64", "float32", "float64":
		n, ok := value.(json.Number)
		if !ok {
			return fail("must be a JSON number without coercion")
		}
		bits := 64
		if strings.HasSuffix(typ, "32") {
			bits = 32
		}
		if strings.HasPrefix(typ, "uint") {
			v, err := strconv.ParseUint(string(n), 10, bits)
			if err != nil {
				return fail("invalid or out-of-range unsigned integer")
			}
			return v, nil
		}
		if strings.HasPrefix(typ, "int") {
			v, err := strconv.ParseInt(string(n), 10, bits)
			if err != nil {
				return fail("invalid or out-of-range integer")
			}
			return v, nil
		}
		v, err := strconv.ParseFloat(string(n), bits)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return fail("invalid or non-finite float")
		}
		if v == 0 {
			v = 0
		}
		return v, nil
	}
	t, ok := c.types[typ]
	if !ok {
		return fail("unsupported type")
	}
	switch t.Kind {
	case "record":
		record, ok := value.(map[string]any)
		if !ok {
			return fail("must be a record object")
		}
		return c.normalizeRecord(t, record, path, l, refs, depth)
	case "enum":
		s, ok := value.(string)
		if !ok {
			return fail("must be an enum symbol")
		}
		for _, allowed := range t.Values {
			if s == allowed {
				return s, nil
			}
		}
		return fail("unknown enum symbol")
	case "flags":
		list, ok := value.([]any)
		if !ok {
			return fail("must be an array of flag symbols")
		}
		if len(list) > len(t.Values) {
			return fail("too many flag symbols")
		}
		allowed := map[string]bool{}
		for _, s := range t.Values {
			allowed[s] = true
		}
		seen := map[string]bool{}
		out := make([]string, 0, len(list))
		for _, item := range list {
			s, ok := item.(string)
			if !ok || !allowed[s] || seen[s] {
				return fail("unknown or duplicate flag symbol")
			}
			seen[s] = true
			if err := spend(l, int64(len(s)+3), path); err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		sort.Strings(out)
		return out, nil
	}
	return fail("unsupported type")
}

func spend(l Limits, n int64, path string) error {
	l.budget.remaining -= n
	if l.budget.remaining < 0 {
		return fmt.Errorf("content %s: expanded value budget exceeded", path)
	}
	return nil
}
