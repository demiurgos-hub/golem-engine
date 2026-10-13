package codegen

import (
	"fmt"
	"strings"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func writeJSScalarEncode(b *strings.Builder, name, protoType string, tag int, optional bool, indent string) {
	wt, wm := scalarWireType(protoType), protoWriterMethod[protoType]
	guard := "m." + name + " !== " + protoTSZero[protoType]
	if optional {
		guard = "m." + name + " !== undefined"
	} else if protoType == "bytes" {
		guard = "m." + name + ".length > 0"
	}
	fmt.Fprintf(b, "%sif (%s) w.tag(%d, %d).%s(m.%s);\n", indent, guard, tag, wt, wm, name)
}

// Empty presence selects ordinary omit-zero encoding.
func writeCSScalarEncode(b *strings.Builder, name, protoType string, tag int, presence schema.CSDeltaPresence) {
	wt, wm := scalarWireType(protoType), protoCSWriterMethod[protoType]
	value := "m." + name
	guard := value + " != " + protoCSZero[protoType]
	if presence != "" {
		if presence == schema.Reference {
			guard = value + " != null"
		} else {
			guard = value + ".HasValue"
			value += ".Value"
		}
	} else if protoType == "bytes" {
		guard = value + ".Length > 0"
	}
	fmt.Fprintf(b, "            if (%s) w.Tag(%d, %d).%s(%s);\n", guard, tag, wt, wm, value)
}
