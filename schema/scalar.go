package schema

// ScalarInfo is the resolved language and wire metadata for a protobuf scalar.
// PointerHelper names Go value-to-pointer helpers, not wire codec methods.
type ScalarInfo struct {
	ProtoType              string
	WireType               int
	GoType, TSType, CSType string
	TSDefault, CSDefault   string
	PointerHelper          string
	CSReference            bool
}

var scalars = map[string]ScalarInfo{
	"double": {"double", 1, "float64", "number", "double", "0", "0", "Float64", false},
	"float":  {"float", 5, "float32", "number", "float", "0", "0f", "Float32", false},
	"int32":  {"int32", 0, "int32", "number", "int", "0", "0", "Int32", false},
	"int64":  {"int64", 0, "int64", "number", "long", "0", "0", "Int64", false},
	"uint32": {"uint32", 0, "uint32", "number", "uint", "0", "0", "Uint32", false},
	"uint64": {"uint64", 0, "uint64", "number", "ulong", "0", "0", "Uint64", false},
	"sint32": {"sint32", 0, "int32", "number", "int", "0", "0", "Int32", false},
	"sint64": {"sint64", 0, "int64", "number", "long", "0", "0", "Int64", false},
	"bool":   {"bool", 0, "bool", "boolean", "bool", "false", "false", "Bool", false},
	"string": {"string", 2, "string", "string", "string", `""`, `""`, "String", true},
	"bytes":  {"bytes", 2, "[]byte", "Uint8Array", "byte[]", "new Uint8Array()", "System.Array.Empty<byte>()", "", true},
}

// LookupScalar returns a copy of the metadata for a known scalar.
func LookupScalar(name string) (ScalarInfo, bool) { info, ok := scalars[name]; return info, ok }

func scalarProperty(property func(ScalarInfo) string) map[string]string {
	result := make(map[string]string, len(scalars))
	for name, info := range scalars {
		result[name] = property(info)
	}
	return result
}

func scalarDefaults(cs bool) map[string]string {
	result := make(map[string]string)
	for _, info := range scalars {
		if cs {
			result[info.CSType] = info.CSDefault
		} else {
			result[info.TSType] = info.TSDefault
		}
	}
	return result
}

// CSDeltaPresence describes how a generated C# delta represents presence.
type CSDeltaPresence string

const (
	// NullableValue uses HasValue/Value for a nullable C# value type.
	NullableValue CSDeltaPresence = "nullable-value"
	// Reference uses a null check for strings, bytes, and collections.
	Reference CSDeltaPresence = "reference"
)

// DeltaPresenceCS returns resolved C# presence, adapting legacy DTOs if absent.
func (v VarInfo) DeltaPresenceCS() CSDeltaPresence {
	if v.CSPresence != "" {
		return v.CSPresence
	}
	if v.IsRepeated || v.IsMap {
		return Reference
	}
	info, _ := LookupScalar(v.ProtoType)
	if info.CSReference {
		return Reference
	}
	return NullableValue
}
