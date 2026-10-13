package codegen

import (
	"fmt"
	"strings"
)

func writeJSPayload(b *strings.Builder, p payloadSpec) {
	w := func(format string, args ...any) { fmt.Fprintf(b, format+"\n", args...) }

	w("export class %s {", p.Name)
	if p.EntityID {
		w("  entityId: number = 0;")
	}
	for _, f := range p.Fields {
		if f.IsCustom {
			w("  %s: %s = %s;", f.FieldName, f.TSType, f.TSDefault)
		} else {
			w("  %s: %s = %s;", f.FieldName, tsType(f.ProtoType), scalarTSDefault(f.ProtoType))
		}
	}
	w("")

	w("  static encode(m: %s): Uint8Array {", p.Name)
	w("    const w = new PbWriter();")
	if p.EntityID {
		w("    if (m.entityId !== 0) w.tag(1, 0).int64(m.entityId);")
	}
	for _, f := range p.Fields {
		if f.IsCustom {
			w("    { const _b = encode%s(m.%s); if (_b.byteLength > 0) w.tag(%d, 2).bytes(_b); }", f.ProtoType, f.FieldName, f.ProtoTag)
		} else {
			writeJSScalarEncode(b, f.FieldName, f.ProtoType, f.ProtoTag, false, "    ")
		}
	}
	w("    return w.finish();")
	w("  }")
	if p.Decode {
		w("")

		w("  static decode(b: Uint8Array): %s {", p.Name)
		w("    const r = new PbReader(b);")
		w("    const m = new %s();", p.Name)
		w("    while (!r.done) {")
		w("      const t = r.tag();")
		w("      switch (t.field) {")
		if p.EntityID {
			w("        case 1: m.entityId = r.int64(); break;")
		}
		for _, f := range p.Fields {
			if f.IsCustom {
				w("        case %d: m.%s = decode%s(r.bytes()); break;", f.ProtoTag, f.FieldName, f.ProtoType)
			} else {
				rm := protoReaderMethod[f.ProtoType]
				w("        case %d: m.%s = r.%s(); break;", f.ProtoTag, f.FieldName, rm)
			}
		}
		w("        default: r.skip(t.wire); break;")
		w("      }")
		w("    }")
		w("    return m;")
		w("  }")
	}
	w("}\n")
}
