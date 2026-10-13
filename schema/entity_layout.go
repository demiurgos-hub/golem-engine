package schema

// EntityRevisionTag is the reserved revision field in entity wire messages.
const EntityRevisionTag = 1000

// EntityWireField describes one entity field, including delta presence.
type EntityWireField struct {
	VarInfo
	Role     string
	Optional bool
}

// EntityWireLayout is the ordered state/delta layout resolved by the schema.
// Fields retain their wire order, which is not necessarily numeric tag order.
type EntityWireLayout struct{ State, Delta []EntityWireField }

// WireLayout returns resolved metadata or adapts a legacy hand-built entity DTO.
func (ed EntityData) WireLayout() EntityWireLayout {
	if ed.Wire != nil {
		return *ed.Wire
	}
	dimensions := ed.Dimensions
	if dimensions == 0 {
		dimensions = 2
	}
	return buildEntityWireLayout(ed, dimensions)
}

// ProtoWireLayout retains the proto template's root-dimension fallback for old DTOs.
func (ed EntityData) ProtoWireLayout(dimensions int) EntityWireLayout {
	if ed.Wire != nil {
		return *ed.Wire
	}
	return buildEntityWireLayout(ed, dimensions)
}

func buildEntityWireLayout(ed EntityData, dimensions int) EntityWireLayout {
	var result EntityWireLayout
	type metadataField struct {
		name, typ, role string
		tag, bit        int
	}
	metadata := []metadataField{
		{"entity_id", "int64", "identity", 1, 0}, {"pos_x", "float", "position", 2, 0}, {"pos_y", "float", "position", 3, 1},
	}
	if ed.Is3D {
		metadata = append(metadata, metadataField{"pos_z", "float", "position", 4, 2})
	}
	metadata = append(metadata, metadataField{"revision", "uint64", "revision", EntityRevisionTag, 0})
	for _, m := range metadata {
		s, _ := LookupScalar(m.typ)
		v := VarInfo{
			SnakeName: m.name, GoName: SnakeToPascal(m.name), FieldName: SnakeToCamel(m.name),
			TSProtoField: SnakeToCamel(m.name), CSProtoField: SnakeToPascal(m.name),
			GoType: s.GoType, TSType: s.TSType, CSType: s.CSType, TSDefault: s.TSDefault, CSDefault: s.CSDefault,
			ProtoType: m.typ, ProtoHelper: s.PointerHelper, ProtoTag: m.tag, BitIndex: m.bit,
		}
		v.CSPresence = v.DeltaPresenceCS()
		f := EntityWireField{VarInfo: v, Role: m.role}
		result.State = append(result.State, f)
		f.Optional = m.role == "position"
		result.Delta = append(result.Delta, f)
	}
	for _, v := range ed.AllVars {
		v.ProtoTag = v.UserTag + dimensions + 1
		result.State = append(result.State, EntityWireField{VarInfo: v, Role: "variable"})
	}
	for _, v := range ed.TickVars {
		v.ProtoTag = v.UserTag + dimensions + 1
		result.Delta = append(result.Delta, EntityWireField{VarInfo: v, Role: "variable", Optional: true})
	}
	return result
}
