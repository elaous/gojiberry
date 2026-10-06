// Command selector is the page-side half of the e2e driver (see main.go). This file holds
// its pure functions, which build on every platform so that plain `go test` can run them.
package main

import "strings"

// pseudoAt reads a `:has-text("...")` or `:text-is("...")` at the start of s: its kind,
// its (unescaped) argument and its length, or length 0 if s does not start with one.
func pseudoAt(s string) (kind, arg string, n int) {
	for _, k := range []string{"has-text", "text-is"} {
		head := ":" + k + `("`
		if !strings.HasPrefix(s, head) {
			continue
		}
		var b strings.Builder
		for i := len(head); i < len(s); i++ {
			switch c := s[i]; {
			case c == '\\' && i+1 < len(s):
				i++
				b.WriteByte(s[i])
			case c == '"':
				if i+1 < len(s) && s[i+1] == ')' {
					return k, b.String(), i + 2
				}
				return "", "", 0
			default:
				b.WriteByte(c)
			}
		}
	}
	return "", "", 0
}

// norm collapses runs of white space and trims, as the text of an element is compared.
func norm(s string) string { return strings.Join(strings.Fields(s), " ") }
