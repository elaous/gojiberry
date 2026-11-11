package main

import "testing"

func TestPseudoAt(t *testing.T) {
	for _, c := range []struct {
		in, kind, arg string
		n             int
	}{
		{`:has-text("Save")`, "has-text", "Save", 17},
		{`:text-is("a b")x`, "text-is", "a b", 15},
		{`:has-text("say \"hi\"")`, "has-text", `say "hi"`, 23},
		{`:has-text("a")[x]`, "has-text", "a", 14},
		{`:has-text("open`, "", "", 0},
		{`:has-text("a" )`, "", "", 0},
		{`:other("a")`, "", "", 0},
		{`div`, "", "", 0},
		{``, "", "", 0},
	} {
		kind, arg, n := pseudoAt(c.in)
		if kind != c.kind || arg != c.arg || n != c.n {
			t.Errorf("pseudoAt(%q) = %q, %q, %d; want %q, %q, %d", c.in, kind, arg, n, c.kind, c.arg, c.n)
		}
	}
}

func TestNorm(t *testing.T) {
	for in, want := range map[string]string{
		"":            "",
		"  a  ":       "a",
		"a\n\t b   c": "a b c",
		" x y":        "x y",
	} {
		if got := norm(in); got != want {
			t.Errorf("norm(%q) = %q; want %q", in, got, want)
		}
	}
}
