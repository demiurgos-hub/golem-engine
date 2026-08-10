package codegen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/demiurgos-hub/golem-engine/schema"
)

func protocolConstantsFixture() schema.ProtoTemplateData {
	return schema.ProtoTemplateData{
		ProtocolConstantSets: []schema.ProtocolConstantSetData{
			{
				Name: "ItemErrorCode",
				Members: []schema.ProtocolConstantData{
					{Name: "Busy", Value: "item_busy"},
					{Name: "NoEffect", Value: "item_no_effect"},
				},
			},
		},
	}
}

func TestGenerateProtocolConstantsAcrossLanguages(t *testing.T) {
	outDir := t.TempDir()
	proto := protocolConstantsFixture()
	if err := generateGoProto(outDir, "github.com/demiurgos-hub/golem-engine/golem", "generated", nil, nil, nil, nil, proto); err != nil {
		t.Fatalf("generateGoProto: %v", err)
	}
	if err := generateJSProto(outDir, "golem-engine", nil, nil, nil, nil, proto); err != nil {
		t.Fatalf("generateJSProto: %v", err)
	}
	if err := generateCSharpProto(outDir, "GolemEngine.Unity", "Game.Generated", nil, nil, nil, nil, proto); err != nil {
		t.Fatalf("generateCSharpProto: %v", err)
	}

	tests := []struct {
		file string
		want []string
	}{
		{
			file: "entities_pb.go",
			want: []string{
				`ItemErrorCodeBusy     = "item_busy"`,
				`ItemErrorCodeNoEffect = "item_no_effect"`,
			},
		},
		{
			file: "entities_pb.ts",
			want: []string{
				"export const ItemErrorCode = {",
				`Busy: "item_busy"`,
				`NoEffect: "item_no_effect"`,
				"export type ItemErrorCode = (typeof ItemErrorCode)[keyof typeof ItemErrorCode];",
			},
		},
		{
			file: "EntitiesPb.cs",
			want: []string{
				"public static class ItemErrorCode",
				`public const string Busy = "item_busy";`,
				`public const string NoEffect = "item_no_effect";`,
			},
		},
	}

	for _, tt := range tests {
		data, err := os.ReadFile(filepath.Join(outDir, tt.file))
		if err != nil {
			t.Fatalf("read %s: %v", tt.file, err)
		}
		content := string(data)
		for _, want := range tt.want {
			if !strings.Contains(content, want) {
				t.Errorf("%s missing %q\n%s", tt.file, want, content)
			}
		}
	}
}
