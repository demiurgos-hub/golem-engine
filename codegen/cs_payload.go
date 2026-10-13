package codegen

import (
	"fmt"
	"strings"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func writeCSPayload(b *strings.Builder, p payloadSpec) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format+"\n", args...) }
	w("    public sealed class %s", p.Name)
	w("    {")
	if p.EntityID {
		w("        public long EntityId { get; set; }")
	}
	for _, f := range p.Fields {
		w("        public %s %s { get; set; } = %s;", f.CSType, schema.SnakeToPascal(f.SnakeName), f.CSDefault)
	}
	w("")
	w("        public static byte[] Encode(%s m)", p.Name)
	w("        {")
	w("            var w = new PbWriter();")
	if p.EntityID {
		w("            if (m.EntityId != 0) w.Tag(1, 0).Int64(m.EntityId);")
	}
	for _, f := range p.Fields {
		name := schema.SnakeToPascal(f.SnakeName)
		if f.IsCustom {
			w("            { var bytes = %s.Encode(m.%s); if (bytes.Length > 0) w.Tag(%d, 2).Bytes(bytes); }", f.ProtoType, name, f.ProtoTag)
			continue
		}
		writeCSScalarEncode(b, name, f.ProtoType, f.ProtoTag, "")
	}
	w("            return w.Finish();")
	w("        }")
	if p.Decode {
		w("")
		w("        public static %s Decode(byte[] bytes)", p.Name)
		w("        {")
		w("            var r = new PbReader(bytes);")
		w("            var m = new %s();", p.Name)
		w("            while (!r.Done)")
		w("            {")
		w("                var tag = r.Tag();")
		w("                switch (tag.Field)")
		w("                {")
		if p.EntityID {
			w("                    case 1: m.EntityId = r.Int64(); break;")
		}
		for _, f := range p.Fields {
			name := schema.SnakeToPascal(f.SnakeName)
			if f.IsCustom {
				w("                    case %d: m.%s = %s.Decode(r.Bytes()); break;", f.ProtoTag, name, f.ProtoType)
			} else {
				w("                    case %d: m.%s = r.%s(); break;", f.ProtoTag, name, protoCSReaderMethod[f.ProtoType])
			}
		}
		w("                    default: r.Skip(tag.Wire); break;")
		w("                }")
		w("            }")
		w("            return m;")
		w("        }")
	}
	w("    }")
	w("")
}
