package wordlist

import (
	"slices"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/complete"
	"github.com/elaous/gojiberry/lang"
)

func TestEveryListIsUsable(t *testing.T) {
	for _, c := range Codes() {
		ws := For(c)
		if len(ws) < 200 {
			t.Errorf("%s: only %d words", c, len(ws))
		}
		if !slices.IsSorted(ws) {
			t.Errorf("%s: not sorted", c)
		}
		for i, w := range ws {
			if i > 0 && ws[i-1] == w {
				t.Errorf("%s: %q twice", c, w)
			}
			if w == "" || strings.ContainsAny(w, " \t\n0123456789") {
				t.Errorf("%s: %q is not a word", c, w)
			}
			if got := complete.Words(w); len(got) != 1 || got[0] != w {
				t.Errorf("%s: %q does not survive word splitting as one word", c, w)
			}
		}
		for _, s := range lang.Stopwords[c] {
			if !slices.Contains(ws, s) {
				t.Errorf("%s: stopword %q of package lang missing", c, s)
			}
		}
	}
}

func TestForReadsTagsAndKnowsOnlyItsLanguages(t *testing.T) {
	if len(For("de-AT")) == 0 || len(For("FR_ca")) == 0 || len(For("es")) == 0 {
		t.Error("a regional tag finds the list of its language")
	}
	for _, tag := range []string{"", "en", "en-US", "ja", "it"} {
		if For(tag) != nil {
			t.Errorf("%q has no list", tag)
		}
	}
	if Base(" De-AT ") != "de" {
		t.Error("Base")
	}
}

func TestListsFeedCompletion(t *testing.T) {
	prefix := map[string]string{"de": "Doku", "fr": "enreg", "es": "doc", "pt": "docum", "ar": "وثي", "he": "מסמ"}
	want := map[string]string{"de": "Dokument", "fr": "enregistrer", "es": "documento", "pt": "documento", "ar": "وثيقة", "he": "מסמך"}
	for c, p := range prefix {
		var ix complete.Index
		ix.AddVocabulary(For(c), 1)
		got := ix.Suggest(p, complete.Options{Max: 5})
		found := false
		for _, s := range got {
			found = found || s.Word == want[c]
		}
		if !found {
			t.Errorf("%s: %q gave %v, want %q among them", c, p, got, want[c])
		}
	}
}
