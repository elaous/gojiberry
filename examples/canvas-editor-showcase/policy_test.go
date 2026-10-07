//go:build !(js && wasm)

package main

import "testing"

func TestCheckDirURL(t *testing.T) {
	ok := map[string][2]string{
		"https://dir.example":          {"https://dir.example", "dir.example"},
		" https://Dir.Example/ ":       {"https://dir.example", "dir.example"},
		"https://dir.example:8443/api": {"https://dir.example:8443/api", "dir.example:8443"},
		"http://localhost:8080":        {"http://localhost:8080", "localhost:8080"},
		"http://127.0.0.1:9000/":       {"http://127.0.0.1:9000", "127.0.0.1:9000"},
		"http://[::1]:9000":            {"http://[::1]:9000", "[::1]:9000"},
		"https://dir.example/?a=b#c":   {"https://dir.example", "dir.example"},
	}
	for in, want := range ok {
		clean, host, err := checkDirURL(in)
		if err != nil || clean != want[0] || host != want[1] {
			t.Errorf("%q: %q %q %v, want %v", in, clean, host, err, want)
		}
	}
	for _, in := range []string{
		"", "dir.example", "//dir.example", "http://dir.example", "http://localhost.evil.example",
		"http://127.0.0.1.evil.example", "ftp://dir.example", "javascript:alert(1)", "https://user:pw@dir.example",
		"https://", "https:dir.example", "file:///etc/passwd", "http://localhost@evil.example",
	} {
		if clean, _, err := checkDirURL(in); err == nil {
			t.Errorf("%q was accepted as %q", in, clean)
		}
	}
}

// The word that confirms erasing is exact: a lower-case "erase" is not it.
func TestEraseWordIsExact(t *testing.T) {
	for typed, want := range map[string]bool{"ERASE": true, " ERASE ": true, "erase": false, "Erase": false, "ERASE!": false, "": false, "ERAS": false} {
		if got := eraseWordMatches(typed, "ERASE"); got != want {
			t.Errorf("%q: %v", typed, got)
		}
	}
}
