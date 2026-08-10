package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProtocolConstantsTestFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "protocol_constants.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write protocol constants: %v", err)
	}
	return path
}

func TestLoadProtocolConstantsResolvesStableNamesAndOrder(t *testing.T) {
	path := writeProtocolConstantsTestFile(t, `
sets:
  shop_error_code:
    failed: shop_failed
    busy: shop_busy
  close_reason:
    session_replaced: session_replaced
`)

	sets, err := LoadProtocolConstants(path)
	if err != nil {
		t.Fatalf("LoadProtocolConstants: %v", err)
	}
	if len(sets) != 2 || sets[0].Name != "CloseReason" || sets[1].Name != "ShopErrorCode" {
		t.Fatalf("sets = %#v", sets)
	}
	shop := sets[1]
	if len(shop.Members) != 2 {
		t.Fatalf("shop members = %#v", shop.Members)
	}
	if shop.Members[0].Name != "Busy" || shop.Members[0].Value != "shop_busy" {
		t.Fatalf("first shop member = %#v", shop.Members[0])
	}
	if shop.Members[1].Name != "Failed" || shop.Members[1].Value != "shop_failed" {
		t.Fatalf("second shop member = %#v", shop.Members[1])
	}
}

func TestLoadProtocolConstantsRejectsInvalidCatalogs(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "set name",
			body: "sets:\n  BadName:\n    ok: value\n",
			want: "canonical snake_case",
		},
		{
			name: "member name",
			body: "sets:\n  errors:\n    bad-name: value\n",
			want: "canonical snake_case",
		},
		{
			name: "empty set",
			body: "sets:\n  errors: {}\n",
			want: "at least one member",
		},
		{
			name: "empty value",
			body: "sets:\n  errors:\n    failed: '  '\n",
			want: "non-empty value",
		},
		{
			name: "member generated collision",
			body: "sets:\n  errors:\n    a1: first\n    a_1: second\n",
			want: "both generate",
		},
		{
			name: "duplicate value",
			body: "sets:\n  first:\n    one: duplicate\n    two: duplicate\n",
			want: "duplicate value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadProtocolConstants(writeProtocolConstantsTestFile(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want containing %q", err, tt.want)
			}
		})
	}
}
