package transpile

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// CapWords turns a snake_case or kebab-case name into UpperCamelCase.
func CapWords(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' })
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		r, n := utf8.DecodeRuneInString(p) // a rune, not a byte: "élan" stays valid UTF-8
		b.WriteRune(unicode.ToUpper(r))
		b.WriteString(p[n:])
	}
	out := b.String()
	// a Go identifier cannot start with a digit or be empty
	if r, _ := utf8.DecodeRuneInString(out); out == "" || (!unicode.IsLetter(r) && r != '_') {
		out = "X" + out
	}
	return out
}
