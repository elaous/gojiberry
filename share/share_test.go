package share

import (
	"net/url"
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	text := "The quick brown fox jumps over the lazy dog."
	s := Of(text, 4, 19)
	if s.Quote != "quick brown fox" {
		t.Fatalf("quote = %q", s.Quote)
	}
	link := Link("https://x.test/app?doc=d1&hub=h", url.Values{"transport": {"ws"}}, s)
	u, _ := url.Parse(link)
	if u.Query().Get("doc") != "d1" || u.Query().Get("transport") != "ws" {
		t.Errorf("link %q lost its parameters", link)
	}
	got, ok := Parse(u.Query())
	if !ok || got != s {
		t.Fatalf("parsed %+v, %v", got, ok)
	}
	if from, to, ok := got.Resolve(text); !ok || from != 4 || to != 19 {
		t.Errorf("resolved %d-%d %v", from, to, ok)
	}
}

func TestResolveAfterTheTextMoved(t *testing.T) {
	s := Of("one two three two", 14, 17) // the second "two"
	for _, c := range []struct {
		text     string
		from, to int
		wantOK   bool
		desc     string
	}{
		{"zero. one two three two", 10, 13, true, "shifted: the nearest occurrence (ambiguous quotes pick the closest)"},
		{"one two three", 4, 7, true, "the other occurrence is the only one left"},
		{"nothing here", 0, 0, false, "gone"},
	} {
		from, to, ok := s.Resolve(c.text)
		if ok != c.wantOK || ok && (from != c.from || to != c.to) {
			t.Errorf("%s: %d-%d %v", c.desc, from, to, ok)
		}
	}
}

func TestLongQuoteAndEdges(t *testing.T) {
	long := strings.Repeat("ab ", 100)
	s := Of(long, 0, len([]rune(long)))
	if n := len([]rune(s.Quote)); n != QuoteRunes {
		t.Errorf("quote has %d runes", n)
	}
	if !Of("abc", 2, 2).Empty() || !Of("abc", 5, 9).Empty() {
		t.Error("empty ranges must give an empty selection")
	}
	if _, ok := Parse(url.Values{"sel": {"9-3"}}); ok {
		t.Error("inverted range accepted")
	}
	if _, ok := Parse(url.Values{"sel": {"x"}}); ok {
		t.Error("garbage accepted")
	}
	if from, to, ok := (Selection{From: 2, To: 40}).Resolve("héllo wörld"); !ok || from != 2 || to != 11 {
		t.Errorf("no quote: %d-%d %v", from, to, ok)
	}
}
