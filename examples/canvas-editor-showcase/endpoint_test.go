package main

import "testing"

func TestAllowedEndpoint(t *testing.T) {
	const page = "https://app.opendocs.io"
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"/collab", "https://app.opendocs.io/collab", true},
		{"https://app.opendocs.io/api", "https://app.opendocs.io/api", true},
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"http://localhost:9000/x", "http://localhost:9000/x", true},
		{"http://[::1]:9000", "http://[::1]:9000", true},
		{"https://evil.example", "", false},
		{"https://app.opendocs.io.evil.example", "", false},
		{"https://app.opendocs.io@evil.example", "", false},
		{"//evil.example/x", "", false},
		{"javascript:alert(1)", "", false},
		{"ftp://127.0.0.1", "", false},
		{"http://app.opendocs.io", "", false}, // another scheme is another origin
		{"", "", false},
		{"  ", "", false},
	} {
		got, ok := allowedEndpoint(c.in, page)
		if ok != c.ok || got != c.want {
			t.Errorf("allowedEndpoint(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}
