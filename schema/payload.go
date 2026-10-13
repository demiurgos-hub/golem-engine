package schema

import (
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// PayloadFieldInfo holds the shared resolved command/event field metadata.
type PayloadFieldInfo struct {
	SnakeName string
	GoName    string
	FieldName string // camelCase
	GoType    string
	ProtoType string
	TSType    string
	TSDefault string // TS default value expression; for custom types this is an object literal
	CSType    string
	CSDefault string // C# default value expression; for custom types this is a constructor expression
	IsCustom  bool   // true when the field type is a custom composite type, not a proto scalar
	ProtoTag  int
}

type payloadFieldDef struct {
	Type string `yaml:"type"`
	Tag  *int   `yaml:"tag"`
}

func unmarshalPayloadField(value *yaml.Node, field *payloadFieldDef) error {
	if value.Kind == yaml.ScalarNode {
		field.Type = value.Value
		return nil
	}
	return value.Decode(field)
}
func payloadUsesExplicitFieldTags(fields map[string]payloadFieldDef) bool {
	for _, field := range fields {
		if field.Tag != nil {
			return true
		}
	}
	return false
}
func validatePayloadFieldTags(kind, name, target string, fields map[string]payloadFieldDef) error {
	if !payloadUsesExplicitFieldTags(fields) {
		return nil
	}
	occupied := make(map[int]string, len(fields)+1)
	if strings.TrimSpace(target) == "entity" {
		occupied[1] = "entity_id"
	}
	for _, fieldName := range SortedKeys(fields) {
		def := fields[fieldName]
		if def.Tag == nil {
			return fmt.Errorf("%s %q: field %q: tag is required when any field sets tag", kind, name, fieldName)
		}
		if err := claimEnvelopeTag(occupied, kind+" "+name, fieldName, *def.Tag); err != nil {
			return err
		}
	}
	return nil
}
func resolvePayloadFields(kind, name, target string, fields map[string]payloadFieldDef, customTypes map[string]CustomTypeData) ([]PayloadFieldInfo, error) {
	var result []PayloadFieldInfo
	// offset: entity-targeted payloads reserve proto field 1 for entity_id.
	tagOffset := 0
	if target == "entity" {
		tagOffset = 1
	}

	keys := SortedKeys(fields)
	explicitTags := payloadUsesExplicitFieldTags(fields)
	if explicitTags {
		slices.SortFunc(keys, func(a, b string) int { return *fields[a].Tag - *fields[b].Tag })
	} else {
		// Sort alphabetically for stable, deterministic legacy assignment.
		slices.SortFunc(keys, func(a, b string) int { return strings.Compare(a, b) })
	}

	for i, k := range keys {
		def := fields[k]

		// Collection types are not supported on payloads; reject with a clear message.
		if _, _, _, _, isCollection := ParseCollectionType(def.Type); isCollection {
			return nil, fmt.Errorf("%s %q: field %q: collection types (list/dict) are not supported on %ss; use a flat custom type", kind, name, k, kind)
		}

		var fi PayloadFieldInfo
		fi.SnakeName = k
		fi.GoName = SnakeToPascal(k)
		fi.FieldName = SnakeToCamel(k)
		if explicitTags {
			fi.ProtoTag = *def.Tag
		} else {
			fi.ProtoTag = (i + 1) + tagOffset
		}

		if goType, ok := protoToGo[def.Type]; ok {
			// Proto scalar type.
			tsType := protoToTS[def.Type]
			csType := protoToCS[def.Type]
			fi.GoType = goType
			fi.ProtoType = def.Type
			fi.TSType = tsType
			fi.TSDefault = tsDefaults[tsType]
			fi.CSType = csType
			fi.CSDefault = csDefaults[csType]
		} else if ct, ok := customTypes[def.Type]; ok {
			// Custom composite type. TSDefault is an inline TS object literal built
			// from the custom type's scalar fields. This assumes custom type fields
			// are scalars only (enforced by BuildCustomTypeData); if nested custom
			// types are ever allowed, TSDefault construction would need to be recursive.
			parts := make([]string, 0, len(ct.Fields))
			for _, cf := range ct.Fields {
				parts = append(parts, cf.FieldName+": "+tsDefaults[cf.TSType])
			}
			fi.GoType = def.Type
			fi.ProtoType = def.Type
			fi.TSType = def.Type
			fi.TSDefault = "{ " + strings.Join(parts, ", ") + " }"
			fi.CSType = def.Type
			fi.CSDefault = "new " + def.Type + "()"
			fi.IsCustom = true
		} else {
			return nil, fmt.Errorf("unknown type %q for field %q in %s %q", def.Type, k, kind, name)
		}

		result = append(result, fi)
	}
	return result, nil
}
