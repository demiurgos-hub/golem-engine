package schema

import "strings"

// Type mapping tables: protobuf scalar type name -> language-native equivalents.

var protoToGo = map[string]string{
	"double": "float64",
	"float":  "float32",
	"int32":  "int32",
	"int64":  "int64",
	"uint32": "uint32",
	"uint64": "uint64",
	"sint32": "int32",
	"sint64": "int64",
	"bool":   "bool",
	"string": "string",
	"bytes":  "[]byte",
}

var protoToProtoHelper = map[string]string{
	"double": "Float64",
	"float":  "Float32",
	"int32":  "Int32",
	"int64":  "Int64",
	"uint32": "Uint32",
	"uint64": "Uint64",
	"sint32": "Int32",
	"sint64": "Int64",
	"bool":   "Bool",
	"string": "String",
}

var protoToTS = map[string]string{
	"double": "number",
	"float":  "number",
	"int32":  "number",
	"int64":  "number",
	"uint32": "number",
	"uint64": "number",
	"sint32": "number",
	"sint64": "number",
	"bool":   "boolean",
	"string": "string",
	"bytes":  "Uint8Array",
}

var protoToCS = map[string]string{
	"double": "double",
	"float":  "float",
	"int32":  "int",
	"int64":  "long",
	"uint32": "uint",
	"uint64": "ulong",
	"sint32": "int",
	"sint64": "long",
	"bool":   "bool",
	"string": "string",
	"bytes":  "byte[]",
}

var tsDefaults = map[string]string{
	"number":     "0",
	"boolean":    "false",
	"string":     `""`,
	"Uint8Array": "new Uint8Array()",
}

var csDefaults = map[string]string{
	"double": "0",
	"float":  "0f",
	"int":    "0",
	"long":   "0",
	"uint":   "0",
	"ulong":  "0",
	"bool":   "false",
	"string": `""`,
	"byte[]": "System.Array.Empty<byte>()",
}

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
