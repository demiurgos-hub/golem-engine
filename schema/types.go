package schema

import "strings"

// Compatibility projections of the scalar catalog.
var protoToGo = scalarProperty(func(s ScalarInfo) string { return s.GoType })
var protoToProtoHelper = scalarProperty(func(s ScalarInfo) string { return s.PointerHelper })
var protoToTS = scalarProperty(func(s ScalarInfo) string { return s.TSType })
var protoToCS = scalarProperty(func(s ScalarInfo) string { return s.CSType })
var tsDefaults = scalarDefaults(false)
var csDefaults = scalarDefaults(true)

// ParseCollectionType parses a collection type string of the form "list<T>" or
// "dict<K, V>" and returns the component parts. Returns ok=false for plain scalar
// types, which should be looked up in protoToGo as usual.
func ParseCollectionType(raw string) (isRepeated, isMap bool, mapKeyType, elemType string, ok bool) {
	if len(raw) > 5 && raw[:5] == "list<" && raw[len(raw)-1] == '>' {
		return true, false, "", strings.TrimSpace(raw[5 : len(raw)-1]), true
	}
	if len(raw) > 5 && raw[:5] == "dict<" && raw[len(raw)-1] == '>' {
		inner := raw[5 : len(raw)-1]
		comma := strings.Index(inner, ",")
		if comma < 0 {
			return false, false, "", "", false
		}
		key := strings.TrimSpace(inner[:comma])
		val := strings.TrimSpace(inner[comma+1:])
		return false, true, key, val, true
	}
	return false, false, "", "", false
}
