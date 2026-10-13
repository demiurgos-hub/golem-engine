package codegen

import (
	"fmt"
	"strings"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func writeGoEntityMessage(b *strings.Builder, name string, fields []schema.EntityWireField) {
	w := func(f string, args ...any) { fmt.Fprintf(b, f+"\n", args...) }
	w("")
	w("type %s struct {", name)
	for _, f := range fields {
		typ := f.GoType
		if f.Optional && !f.IsRepeated && !f.IsMap {
			typ = "*" + typ
		}
		w("\t%s %s", f.GoName, typ)
	}
	w("}")
	w("")
	w("// Marshal encodes the message to protobuf wire format.")
	w("func (m *%s) Marshal() []byte {", name)
	w("\tw := &pb.Writer{}")
	for _, f := range fields {
		if f.IsRepeated || f.IsMap {
			writeGoCollectionMarshal(b, goVarCollection(f.VarInfo), f.ProtoTag, f.Optional)
		} else {
			writeGoFieldEncode(b, f.GoName, f.ProtoType, f.ProtoTag, f.Optional)
		}
	}
	w("\treturn w.Finish()")
	w("}")
	w("")
	w("// Unmarshal decodes the message from protobuf wire format.")
	w("func (m *%s) Unmarshal(data []byte) (retErr error) {", name)
	w("\tdefer func() { if r := recover(); r != nil { retErr = fmt.Errorf(\"pb: decode %s: %%v\", r) } }()", name)
	w("\trd := pb.NewReader(data)")
	w("\tfor !rd.Done() {")
	w("\t\tfield, wire := rd.Tag()")
	w("\t\tswitch field {")
	for _, f := range fields {
		if f.IsRepeated || f.IsMap {
			writeGoCollectionUnmarshal(b, goVarCollection(f.VarInfo), f.ProtoTag, f.Optional)
		} else if f.Optional {
			w("\t\tcase %d:", f.ProtoTag)
			w("\t\t\tv := rd.%s()", goWriterMethod[f.ProtoType])
			w("\t\t\tm.%s = &v", f.GoName)
		} else {
			w("\t\tcase %d: m.%s = rd.%s()", f.ProtoTag, f.GoName, goWriterMethod[f.ProtoType])
		}
	}
	w("\t\tdefault: rd.Skip(wire)")
	w("\t\t}")
	w("\t}")
	w("\treturn nil")
	w("}")
}

type goCollectionField struct {
	schema.CollectionInfo
	GoName       string
	ElemIsCustom bool
}

func goVarCollection(v schema.VarInfo) goCollectionField {
	c := schema.CollectionInfo{IsRepeated: v.IsRepeated, IsMap: v.IsMap, GoType: v.GoType, ElemProtoType: v.ElemProtoType, MapKeyProtoType: v.MapKeyProtoType, ElemGoType: v.ElemGoType, MapKeyGoType: v.MapKeyGoType}
	custom := v.ElemIsCustom
	if v.Collection != nil {
		c = *v.Collection
		custom = c.CustomType != nil
	}
	return goCollectionField{c, v.GoName, custom}
}

func goWorldCollection(v schema.WorldFieldInfo) goCollectionField {
	return goVarCollection(schema.VarInfo{Collection: v.Collection, IsRepeated: v.IsRepeated, IsMap: v.IsMap, GoName: v.GoName, GoType: v.GoType, ElemProtoType: v.ElemProtoType, MapKeyProtoType: v.MapKeyProtoType, ElemGoType: v.ElemGoType, MapKeyGoType: v.MapKeyGoType, ElemIsCustom: v.ElemIsCustom})
}
