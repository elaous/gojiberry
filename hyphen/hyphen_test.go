package hyphen

import (
	"strings"
	"testing"
)

func TestEnglishFindsPoints(t *testing.T) {
	h := English()
	got := h.Positions("hyphenation")
	if len(got) == 0 {
		t.Fatal("no points in hyphenation")
	}
	for _, p := range got {
		if p < 2 || p > len("hyphenation")-2 {
			t.Errorf("point %d too near an end", p)
		}
	}
	if len(h.Positions("cat")) != 0 || len(h.Positions("")) != 0 {
		t.Error("short words are not broken")
	}
	if (*Hyphenator)(nil).Positions("hyphenation") != nil {
		t.Error("a nil hyphenator breaks nothing")
	}
}

func TestFromPatterns(t *testing.T) {
	h, err := FromPatterns(strings.NewReader("1ba\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := h.Positions("abbaba"); len(got) == 0 {
		t.Error("the pattern 1ba should allow a break before 'ba'")
	}
}
