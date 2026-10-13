package codegen

import "github.com/demiurgos-hub/golem-engine/schema"

func scalarWireType(name string) int     { s, _ := schema.LookupScalar(name); return s.WireType }
func scalarTSDefault(name string) string { s, _ := schema.LookupScalar(name); return s.TSDefault }
func scalarCSDefault(name string) string { s, _ := schema.LookupScalar(name); return s.CSDefault }

// msgField is a backend field view, not a schema resolution or allocation layer.
type msgField struct {
	Name, ProtoType                string
	Tag                            int
	IsOptional                     bool
	IsRepeated, IsMap              bool
	MapKeyProtoType, ElemProtoType string
	ElemIsCustom                   bool
	ElemType, MapKeyType           string
	CSPresence                     schema.CSDeltaPresence
}

func (f msgField) csPresence() schema.CSDeltaPresence {
	return (schema.VarInfo{CSPresence: f.CSPresence, ProtoType: f.ProtoType, IsRepeated: f.IsRepeated, IsMap: f.IsMap}).DeltaPresenceCS()
}

// useCollection projects resolved schema metadata into one backend's field view.
func (f *msgField) useCollection(c *schema.CollectionInfo, elementType, keyType string) {
	f.IsRepeated, f.IsMap = c.IsRepeated, c.IsMap
	f.ElemProtoType, f.MapKeyProtoType = c.ElemProtoType, c.MapKeyProtoType
	f.ElemIsCustom = c.CustomType != nil
	f.ElemType, f.MapKeyType = elementType, keyType
}

type payloadSpec struct {
	Name     string
	EntityID bool
	Fields   []schema.PayloadFieldInfo
	Decode   bool
}

func commandPayload(cmd schema.CommandData) payloadSpec {
	p := payloadSpec{Name: cmd.Name + "Command", EntityID: cmd.Target == "entity"}
	for _, field := range cmd.Fields {
		p.Fields = append(p.Fields, schema.PayloadFieldInfo(field))
	}
	return p
}

func eventPayload(event schema.EventData) payloadSpec {
	p := payloadSpec{Name: event.Name + "Event", EntityID: event.EntityType != "", Decode: true}
	for _, field := range event.Fields {
		p.Fields = append(p.Fields, schema.PayloadFieldInfo(field))
	}
	return p
}

type goFullStateView struct {
	Name   string
	Is3D   bool
	Fields []schema.VarInfo
}

func goFullStateProjection(entity schema.EntityData, includeOwner bool) goFullStateView {
	v := goFullStateView{Name: entity.Name, Is3D: entity.Is3D}
	for _, field := range entity.AllVars {
		if includeOwner || !field.OwnerOnly {
			v.Fields = append(v.Fields, field)
		}
	}
	return v
}
