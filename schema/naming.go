package schema

import (
	"strings"
	"unicode"
)

// SnakeToPascal converts snake_case to PascalCase (e.g. "dir_x" -> "DirX").
func SnakeToPascal(s string) string {
	parts := strings.Split(s, "_")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

// SnakeToCamel converts snake_case to camelCase (e.g. "dir_x" -> "dirX").
func SnakeToCamel(s string) string {
	p := SnakeToPascal(s)
	if p == "" {
		return ""
	}
	return strings.ToLower(p[:1]) + p[1:]
}

// LcFirst lowercases only the first character.
func LcFirst(s string) string {
	if s == "" {
		return ""
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// ToSnakeCase converts PascalCase to snake_case (e.g. "Player" -> "player").
func ToSnakeCase(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) && i > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// SortedKeys returns map keys in deterministic alphabetical order.
func SortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
