package main

import "strings"

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

func norm(s string) string { return strings.Join(strings.Fields(s), " ") }
