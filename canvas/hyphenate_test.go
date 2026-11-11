package canvas

import (
	"strings"
	"testing"
)

// breaks a word after every second letter: "internationalization" -> in-te-rn-...
type everyOther struct{}

func (everyOther) Positions(w string) []int {
	var out []int
	for i := 2; i <= len([]rune(w))-2; i += 2 {
		out = append(out, i)
	}
	return out
}

func layoutLines(t *testing.T, h Hyphenator, text string, width float64) []Line {
	t.Helper()
	f := NewTextFlow(text, 0, 0, width, 400)
	f.SetPainter(&fakePainter{})
	f.Hyphenator = h
	return f.Layout().Lines
}

func TestWordsAreBrokenAtHyphenationPointsWhenTheyDoNotFit(t *testing.T) {
	text := "a international words"
	plain := layoutLines(t, nil, text, 100)
	hyph := layoutLines(t, everyOther{}, text, 100)
	for _, ln := range plain {
		if ln.Hyphen {
			t.Fatal("no hyphenator, no hyphens")
		}
	}
	var sawHyphen bool
	var rebuilt strings.Builder
	for _, ln := range hyph {
		rebuilt.WriteString(ln.Text)
		if ln.Hyphen {
			sawHyphen = true
			if ln.HyphenW <= 0 {
				t.Error("a hyphenated line needs the hyphen's width")
			}
			if got := ln.W; got <= 0 {
				t.Errorf("line width %v", got)
			}
		} else {
			rebuilt.WriteString(" ")
		}
	}
	if !sawHyphen {
		t.Fatalf("the long word was not hyphenated: %+v", hyph)
	}
	if got := strings.ReplaceAll(strings.TrimSpace(rebuilt.String()), "  ", " "); got != text {
		t.Errorf("the text changed: %q", got)
	}
}

func TestAHyphenFillsTheLineAndFits(t *testing.T) {
	// Plain wrapping puts the long word on its own line.
	hyph := layoutLines(t, everyOther{}, "short internationalization", 160)
	if len(hyph) < 2 || !hyph[0].Hyphen || !strings.HasPrefix(hyph[0].Text, "short ") {
		t.Fatalf("lines %+v", hyph)
	}
	if hyph[0].W > 160+1e-9 {
		t.Errorf("the hyphenated line is %v wide, more than the 160 allowed", hyph[0].W)
	}
}

func TestNothingIsHyphenatedThatFitsOrIsNotAWord(t *testing.T) {
	for _, text := range []string{"short words only here", "http://example.com/a/very/long/path/that/is/not/a/word", "12345678901234567890"} {
		for _, ln := range layoutLines(t, everyOther{}, text, 100) {
			if ln.Hyphen && !strings.ContainsAny(ln.Text, "abcdefghijklmnopqrstuvwxyz") {
				t.Errorf("%q: hyphenated %q", text, ln.Text)
			}
		}
	}
	// A symbol inside the token keeps it whole.
	for _, ln := range layoutLines(t, everyOther{}, "see re-implementation notes", 60) {
		if ln.Hyphen && strings.Contains(ln.Text, "-") {
			t.Errorf("hyphenated across an existing hyphen: %q", ln.Text)
		}
	}
}
