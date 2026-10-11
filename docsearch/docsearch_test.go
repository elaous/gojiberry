package docsearch

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func sample() *Index {
	var ix Index
	ix.Add("a", "Budget plan", "The yearly budget is set in spring. Spring is when the plan starts.")
	ix.Add("b", "Notes", "Remember the budget meeting; bring the plan and the numbers.")
	ix.Add("c", "Recipes", "Flour, butter and sugar. Mix well.")
	return &ix
}

func ids(h []Hit) string {
	var s []string
	for _, x := range h {
		s = append(s, x.ID)
	}
	return strings.Join(s, ",")
}

func TestSearchAllWordsTitleBoostAndPrefix(t *testing.T) {
	ix := sample()
	if got := ids(ix.Search("budget plan", 0)); got != "a,b" {
		t.Fatalf("both words, the one with the title match first: %q", got)
	}
	if got := ids(ix.Search("budget spr", 0)); got != "a" {
		t.Fatalf("the last word may be a start: %q", got)
	}
	if got := ids(ix.Search("bud spring", 0)); got != "" {
		t.Fatalf("only the last word is a start: %q", got)
	}
	if ix.Search("", 0) != nil || ix.Search("  ,. ", 0) != nil || ix.Search("zebra", 0) != nil {
		t.Fatal("nothing to find")
	}
	if got := ix.Search("the", 1); len(got) != 1 {
		t.Fatalf("max: %d", len(got))
	}
}

func TestAddReplacesAndRemoveForgets(t *testing.T) {
	ix := sample()
	ix.Add("c", "Recipes", "Now about budget too")
	if got := ids(ix.Search("budget", 0)); !strings.Contains(got, "c") {
		t.Fatalf("replaced text is searched: %q", got)
	}
	ix.Remove("c")
	if got := ids(ix.Search("budget", 0)); strings.Contains(got, "c") || ix.Len() != 2 {
		t.Fatalf("removed: %q %d", got, ix.Len())
	}
}

func TestSnippetShowsTheMatch(t *testing.T) {
	ix := sample()
	h := ix.Search("numbers", 0)
	if len(h) != 1 || !strings.Contains(h[0].Snippet, "numbers") || !strings.HasPrefix(h[0].Snippet, "…") {
		t.Fatalf("%+v", h)
	}
	long := strings.Repeat("filler ", 40) + "needle" + strings.Repeat(" more", 40)
	ix.Add("d", "", long)
	h = ix.Search("needle", 0)
	if len([]rune(h[0].Snippet)) > 70 || !strings.Contains(h[0].Snippet, "needle") {
		t.Fatalf("%q", h[0].Snippet)
	}
	ix.Add("e", "", "ÄÖ straße")
	if h = ix.Search("straße", 0); len(h) != 1 || h[0].Snippet == "" {
		t.Fatalf("%+v", h)
	}
}

func TestSealOpenRoundTripAndTamper(t *testing.T) {
	key, err := DeriveKey([]byte("owner secret"), "store-1")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sample().Seal(key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("budget")) || bytes.Contains(sealed, []byte("Flour")) {
		t.Fatal("the sealed index shows words")
	}
	again, _ := sample().Seal(key)
	if bytes.Equal(sealed, again) {
		t.Fatal("the nonce must differ each time")
	}
	ix, err := Open(key, sealed)
	if err != nil || ids(ix.Search("budget plan", 0)) != "a,b" {
		t.Fatalf("round trip: %v", err)
	}
	other, _ := DeriveKey([]byte("owner secret"), "store-2")
	if k2, _ := DeriveKey([]byte("owner secret"), "store-1"); !bytes.Equal(key, k2) || bytes.Equal(key, other) {
		t.Fatal("keys follow secret and label")
	}
	if _, err := Open(other, sealed); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("another key: %v", err)
	}
	bad := bytes.Clone(sealed)
	bad[len(bad)-1] ^= 1
	for _, b := range [][]byte{bad, sealed[:10], nil, []byte("not an index at all, but long enough to try")} {
		if _, err := Open(key, b); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("%q: %v", b, err)
		}
	}
	if _, err := Open([]byte("short"), sealed); err == nil {
		t.Fatal("a short key is refused")
	}
	if _, err := sample().Seal([]byte("short")); err == nil {
		t.Fatal("a short key is refused when sealing")
	}
}
