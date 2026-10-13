package codegen

import (
	"fmt"
	"strings"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func writeGoEnvelope(b *strings.Builder, layout schema.EnvelopeLayout) {
	w := func(f string, args ...any) { fmt.Fprintf(b, f+"\n", args...) }
	name := layout.Name
	w("")
	w("type is%sPayload interface { is%sPayload() }", name, name)
	w("")
	w("// %s wraps one protocol payload.", name)
	w("type %s struct { Payload is%sPayload }", name, name)
	for _, v := range layout.Variants {
		w("")
		w("type %s_%s struct { %s *%s }", name, v.MessageType, v.MessageType, v.MessageType)
		w("func (*%s_%s) is%sPayload() {}", name, v.MessageType, name)
	}
	w("")
	w("// Marshal encodes the envelope to protobuf wire format.")
	w("func (m *%s) Marshal() []byte {", name)
	w("\tw := &pb.Writer{}")
	w("\tswitch p := m.Payload.(type) {")
	for _, v := range layout.Variants {
		w("\tcase *%s_%s:", name, v.MessageType)
		w("\t\tw.Tag(%d, 2).Bytes(p.%s.Marshal())", v.Tag, v.MessageType)
	}
	w("\t}")
	w("\treturn w.Finish()")
	w("}")
	w("")
	w("// Unmarshal decodes the envelope from protobuf wire format.")
	w("func (m *%s) Unmarshal(data []byte) (retErr error) {", name)
	w("\tdefer func() { if r := recover(); r != nil { retErr = fmt.Errorf(\"pb: decode %s: %%v\", r) } }()", name)
	w("\trd := pb.NewReader(data)")
	w("\tfor !rd.Done() {")
	w("\t\tfield, wire := rd.Tag()")
	w("\t\tswitch field {")
	for _, v := range layout.Variants {
		w("\t\tcase %d:", v.Tag)
		w("\t\t\tsub := &%s{}", v.MessageType)
		w("\t\t\tif err := sub.Unmarshal(rd.Bytes()); err != nil { return err }")
		w("\t\t\tm.Payload = &%s_%s{%s: sub}", name, v.MessageType, v.MessageType)
	}
	w("\t\tdefault: rd.Skip(wire)")
	w("\t\t}")
	w("\t}")
	w("\treturn nil")
	w("}")
}

func writeJSEnvelope(b *strings.Builder, layout schema.EnvelopeLayout, encode bool) {
	w := func(f string, args ...any) { fmt.Fprintf(b, f+"\n", args...) }
	w("export class %s {", layout.Name)
	for _, v := range layout.Variants {
		w("  %s?: %s;", schema.SnakeToCamel(v.SnakeName), v.MessageType)
	}
	w("")
	if encode {
		w("  static encode(m: %s): Uint8Array {", layout.Name)
		w("    const w = new PbWriter();")
		for _, v := range layout.Variants {
			field := schema.SnakeToCamel(v.SnakeName)
			w("    if (m.%s) w.tag(%d, 2).bytes(%s.encode(m.%s));", field, v.Tag, v.MessageType, field)
		}
		w("    return w.finish();")
	} else {
		w("  static decode(b: Uint8Array): %s {", layout.Name)
		w("    const r = new PbReader(b);")
		w("    const m = new %s();", layout.Name)
		w("    while (!r.done) {")
		w("      const t = r.tag();")
		w("      switch (t.field) {")
		for _, v := range layout.Variants {
			w("        case %d: m.%s = %s.decode(r.bytes()); break;", v.Tag, schema.SnakeToCamel(v.SnakeName), v.MessageType)
		}
		w("        default: r.skip(t.wire); break;")
		w("      }")
		w("    }")
		w("    return m;")
	}
	w("  }")
	w("}\n")
}

func writeCSEnvelope(b *strings.Builder, layout schema.EnvelopeLayout, encode bool) {
	w := func(f string, args ...any) { fmt.Fprintf(b, f+"\n", args...) }
	w("    public sealed class %s", layout.Name)
	w("    {")
	for _, v := range layout.Variants {
		w("        public %s %s { get; set; }", v.MessageType, schema.SnakeToPascal(v.SnakeName))
	}
	w("")
	if encode {
		w("        public static byte[] Encode(%s m)", layout.Name)
		w("        {")
		w("            var w = new PbWriter();")
		for _, v := range layout.Variants {
			field := schema.SnakeToPascal(v.SnakeName)
			w("            if (m.%s != null) w.Tag(%d, 2).Bytes(%s.Encode(m.%s));", field, v.Tag, v.MessageType, field)
		}
		w("            return w.Finish();")
	} else {
		w("        public static %s Decode(byte[] bytes)", layout.Name)
		w("        {")
		w("            var r = new PbReader(bytes);")
		w("            var m = new %s();", layout.Name)
		w("            while (!r.Done)")
		w("            {")
		w("                var tag = r.Tag();")
		w("                switch (tag.Field)")
		w("                {")
		for _, v := range layout.Variants {
			w("                    case %d: m.%s = %s.Decode(r.Bytes()); break;", v.Tag, schema.SnakeToPascal(v.SnakeName), v.MessageType)
		}
		w("                    default: r.Skip(tag.Wire); break;")
		w("                }")
		w("            }")
		w("            return m;")
	}
	w("        }")
	w("    }")
	w("")
}
