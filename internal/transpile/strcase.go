package transpile

import "strings"

// CapWords turns a snake_case or kebab-case name into UpperCamelCase,
// e.g. "created_at" -> "CreatedAt". Shared by the JSON struct-inference
// loader here and the experimental HCL/sqlc loaders' field naming
// (exported so internal/transpile/experimental can use the same
// convention without duplicating it).
func CapWords(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' })
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}
