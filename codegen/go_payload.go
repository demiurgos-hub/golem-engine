package codegen

import (
	"fmt"
	"strings"
)

func writeGoPayload(b *strings.Builder, p payloadSpec) {
	w := func(f string, a ...any) { fmt.Fprintf(b, f+"\n", a...) }
	structName := p.Name

	w("")
	w("type %s struct {", structName)
	if p.EntityID {
		w("\tEntityId int64")
	}
	for _, f := range p.Fields {
		w("\t%s %s", f.GoName, f.GoType)
	}
	w("}")

	// Marshal
	w("")
	w("// Marshal encodes the payload to protobuf wire format.")
	w("func (m *%s) Marshal() []byte {", structName)
	w("\tw := &pb.Writer{}")
	if p.EntityID {
		writeGoFieldEncode(b, "EntityId", "int64", 1, false)
	}
	for _, f := range p.Fields {
		if f.IsCustom {
			w("\tif _b := m.%s.Marshal(); len(_b) > 0 { w.Tag(%d, 2).Bytes(_b) }", f.GoName, f.ProtoTag)
		} else {
			writeGoFieldEncode(b, f.GoName, f.ProtoType, f.ProtoTag, false)
		}
	}
	w("\treturn w.Finish()")
	w("}")

	// Unmarshal
	w("")
	w("// Unmarshal decodes the payload from protobuf wire format.")
	w("func (m *%s) Unmarshal(data []byte) (retErr error) {", structName)
	w("\tdefer func() { if r := recover(); r != nil { retErr = fmt.Errorf(\"pb: decode %s: %%v\", r) } }()", structName)
	w("\trd := pb.NewReader(data)")
	w("\tfor !rd.Done() {")
	w("\t\tfield, wire := rd.Tag()")
	w("\t\tswitch field {")
	if p.EntityID {
		w("\t\tcase 1: m.EntityId = rd.Int64()")
	}
	for _, f := range p.Fields {
		if f.IsCustom {
			w("\t\tcase %d:", f.ProtoTag)
			w("\t\t\tvar _sub %s", f.GoType)
			w("\t\t\tif _err := _sub.Unmarshal(rd.Bytes()); _err != nil { return _err }")
			w("\t\t\tm.%s = _sub", f.GoName)
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
