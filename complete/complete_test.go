package complete

import (
	"strings"
	"testing"
)

func words(ss []Suggestion) string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Word)
	}
	return strings.Join(out, ",")
}

func TestTextWordsRankByUseAndRecency(t *testing.T) {
	var ix Index
	ix.AddText("planet plan plan planning planet planet plant")
	got := words(ix.Suggest("pla", Options{}))
	if got != "planet,plant,plan,planning" { // used most; then the later use; then by count
		t.Fatalf("got %s", got)
	}
	if first := words(ix.Suggest("pla", Options{Max: 1})); first != "planet" {
		t.Fatalf("most used first: %s", first)
	}
	// a word used last beats an equally used earlier one
	var jx Index
	jx.AddText("alpha alps alpine")
	if got := words(jx.Suggest("al", Options{Max: 1})); got != "alpine" {
		t.Fatalf("recency: %s", got)
	}
}

func TestVocabularyAndCaseAndLimits(t *testing.T) {
	var ix Index
	ix.AddVocabulary([]string{"hello", "help", "helmet"}, 1)
	ix.AddVocabulary([]string{"helicopter"}, 3)
	ix.AddText("help help")
	if got := words(ix.Suggest("Hel", Options{Max: 3})); got != "Help,Helicopter,Hello" {
		t.Fatalf("got %s", got)
	}
	if ix.Suggest("h", Options{}) != nil {
		t.Fatal("a one-letter prefix gets nothing")
	}
	if got := ix.Suggest("hello", Options{}); len(got) != 0 {
		t.Fatalf("the prefix itself is no suggestion: %v", got)
	}
}

type snippets struct{}

func (snippets) Name() string               { return "snippet" }
func (snippets) Complete(p string) []string { return []string{"signature block", "sig-off", "other"} }

func TestProviderAndReset(t *testing.T) {
	var ix Index
	ix.AddText("signal")
	ix.AddProvider(snippets{})
	got := ix.Suggest("sig", Options{})
	if len(got) != 3 || got[0].Word != "signal" || got[1].Source != "snippet" {
		t.Fatalf("got %+v", got)
	}
	ix.Reset()
	if got := words(ix.Suggest("sig", Options{})); strings.Contains(got, "signal") {
		t.Fatalf("reset keeps text words: %s", got)
	}
}

func TestPrefixAt(t *testing.T) {
	text := []rune("see the plan-b pla")
	if p, s := PrefixAt(text, len(text)); p != "pla" || s != 15 {
		t.Fatalf("%q %d", p, s)
	}
	if p, _ := PrefixAt([]rune("a "), 2); p != "" {
		t.Fatalf("after a space: %q", p)
	}
	if e := WordEnd([]rune("plan b"), 2); e != 4 {
		t.Fatalf("end %d", e)
	}
	if got := Words("It's a well-known 'fact'."); strings.Join(got, "|") != "It's|a|well-known|fact" {
		t.Fatalf("%q", got)
	}
}
