package schema

import "fmt"

// CollectionInfo describes a resolved collection independently of field layout.
// Treat it and its referenced custom type as read-only after schema resolution.
type CollectionInfo struct {
	IsRepeated, IsMap                            bool
	ElemProtoType, MapKeyProtoType               string
	CustomType                                   *CustomTypeData
	ElemGoType, ElemTSType, ElemCSType           string
	MapKeyGoType, MapKeyTSType, MapKeyCSType     string
	GoType, TSType, CSType, TSDefault, CSDefault string
}

type collectionPrecedence bool

const (
	scalarFirst collectionPrecedence = false
	customFirst collectionPrecedence = true
)

func resolveCollection(raw string, types map[string]CustomTypeData, precedence collectionPrecedence) (*CollectionInfo, error) {
	list, dict, key, elem, ok := ParseCollectionType(raw)
	if !ok {
		return nil, nil
	}
	c := &CollectionInfo{IsRepeated: list, IsMap: dict, ElemProtoType: elem, MapKeyProtoType: key}
	if dict {
		s, ok := LookupScalar(key)
		if !ok {
			return nil, fmt.Errorf("unknown map key type %q", key)
		}
		c.MapKeyGoType, c.MapKeyTSType, c.MapKeyCSType = s.GoType, s.TSType, s.CSType
	}
	s, scalar := LookupScalar(elem)
	ct, custom := types[elem]
	if custom && (precedence == customFirst || !scalar) {
		c.CustomType = &ct
		c.ElemGoType, c.ElemTSType, c.ElemCSType = elem, elem, elem
	} else if scalar {
		c.ElemGoType, c.ElemTSType, c.ElemCSType = s.GoType, s.TSType, s.CSType
	} else {
		return nil, fmt.Errorf("unknown element type %q", elem)
	}
	completeCollection(c)
	return c, nil
}

func completeCollection(c *CollectionInfo) {
	if c.IsRepeated {
		c.GoType, c.TSType, c.CSType = "[]"+c.ElemGoType, c.ElemTSType+"[]", "List<"+c.ElemCSType+">"
		c.TSDefault = "[]"
	} else {
		c.GoType = "map[" + c.MapKeyGoType + "]" + c.ElemGoType
		c.TSType = "Record<" + c.MapKeyTSType + ", " + c.ElemTSType + ">"
		c.CSType = "Dictionary<" + c.MapKeyCSType + ", " + c.ElemCSType + ">"
		c.TSDefault = "{}"
	}
	c.CSDefault = "new " + c.CSType + "()"
}

func (c *CollectionInfo) applyVar(v *VarInfo) {
	v.Collection = c
	v.IsRepeated, v.IsMap = c.IsRepeated, c.IsMap
	v.ElemProtoType, v.MapKeyProtoType = c.ElemProtoType, c.MapKeyProtoType
	v.ElemIsCustom = c.CustomType != nil
	v.ElemGoType, v.ElemTSType, v.ElemCSType = c.ElemGoType, c.ElemTSType, c.ElemCSType
	v.MapKeyGoType, v.MapKeyTSType, v.MapKeyCSType = c.MapKeyGoType, c.MapKeyTSType, c.MapKeyCSType
	v.GoType, v.TSType, v.CSType, v.TSDefault, v.CSDefault = c.GoType, c.TSType, c.CSType, c.TSDefault, c.CSDefault
}

func (c *CollectionInfo) applyWorld(v *WorldFieldInfo) {
	v.Collection = c
	v.IsRepeated, v.IsMap = c.IsRepeated, c.IsMap
	v.ElemProtoType, v.MapKeyProtoType = c.ElemProtoType, c.MapKeyProtoType
	v.ElemIsCustom = c.CustomType != nil
	v.ElemGoType, v.ElemTSType, v.ElemCSType = c.ElemGoType, c.ElemTSType, c.ElemCSType
	v.MapKeyGoType, v.MapKeyTSType, v.MapKeyCSType = c.MapKeyGoType, c.MapKeyTSType, c.MapKeyCSType
	v.GoType, v.TSType, v.CSType, v.TSDefault, v.CSDefault = c.GoType, c.TSType, c.CSType, c.TSDefault, c.CSDefault
}

// CatalogInfo holds schema metadata; it does not validate catalog file contents.
type CatalogInfo struct {
	Item       CustomTypeData
	Key        *CustomFieldInfo
	Collection CollectionInfo
}

// ResolveCatalog resolves an item type and optional key without reading a file.
// src must be a non-nil catalog source; format/file validation belongs to its loader.
func ResolveCatalog(world string, src *WorldSourceDef, types map[string]CustomTypeData) (*CatalogInfo, error) {
	ct, ok := types[src.Type]
	if !ok {
		return nil, fmt.Errorf("world %q: catalog source references unknown custom type %q", world, src.Type)
	}
	info := &CatalogInfo{Item: ct}
	c := &info.Collection
	c.ElemProtoType, c.ElemGoType, c.ElemTSType, c.ElemCSType = src.Type, src.Type, src.Type, src.Type
	c.CustomType = &info.Item
	if src.Key == "" {
		c.IsRepeated = true
	} else {
		for i := range info.Item.Fields {
			if info.Item.Fields[i].SnakeName == src.Key {
				field := info.Item.Fields[i]
				// Catalog loader keys historically use the authored field name,
				// including when adapting DTOs without native-name metadata.
				field.GoName = SnakeToPascal(src.Key)
				info.Key = &field
				break
			}
		}
		if info.Key == nil || info.Key.ProtoType == "" {
			return nil, fmt.Errorf("world %q: catalog key %q not found on type %q", world, src.Key, src.Type)
		}
		c.IsMap = true
		c.MapKeyProtoType, c.MapKeyGoType, c.MapKeyTSType, c.MapKeyCSType = info.Key.ProtoType, info.Key.GoType, info.Key.TSType, info.Key.CSType
	}
	completeCollection(c)
	return info, nil
}
