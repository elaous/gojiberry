package clientkey

import (
	"strings"
	"testing"
)

func TestOf(t *testing.T) {
	for in, want := range map[string]string{
		"198.51.100.7":         "198.51.100.7",
		"::ffff:198.51.100.7":  "198.51.100.7",
		"2001:db8:1:2:3:4:5:6": "2001:db8:1:2::/64",
		"2001:DB8:1:2::1%eth0": "2001:db8:1:2::/64",
		" Not-An-Address ":     "not-an-address",
	} {
		if got := Of(in); got != want {
			t.Errorf("Of(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Of(strings.Repeat("x", 100)); len(got) != 64 {
		t.Errorf("a long name is %d bytes, want 64", len(got))
	}
}
