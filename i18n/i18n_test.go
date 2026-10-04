package i18n

import (
	"strings"
	"testing"
)

func TestTextFallsBackFromRegionToLanguageToEnglish(t *testing.T) {
	c := New()
	c.Add("pt", map[string]string{"hello": "olá", "bye": "adeus"})
	c.Add("pt-BR", map[string]string{"hello": "oi"})
	if got := c.Text("pt-BR", "hello", "hello"); got != "oi" {
		t.Errorf("region wins: %q", got)
	}
	if got := c.Text("pt-BR", "bye", "bye"); got != "adeus" {
		t.Errorf("language next: %q", got)
	}
	if got := c.Text("pt_br", "hello", "hello"); got != "oi" {
		t.Errorf("underscores and case: %q", got)
	}
	if got := c.Text("pt", "missing", "the English"); got != "the English" {
		t.Errorf("English default: %q", got)
	}
	if got := c.Text("xx", "hello", "hello"); got != "hello" {
		t.Errorf("unknown language: %q", got)
	}
	c.Fallback = "pt"
	if got := c.Text("fr", "bye", "bye"); got != "adeus" {
		t.Errorf("Fallback: %q", got)
	}
}

func TestFormattingArguments(t *testing.T) {
	c := New()
	c.Add("de", map[string]string{"n": "%[2]d von %[1]d"})
	if got := c.Text("de", "n", "%d of %d", 5, 2); got != "2 von 5" {
		t.Errorf("a language may reorder: %q", got)
	}
	if got := c.Text("en", "n", "%d of %d", 5, 2); got != "5 of 2" {
		t.Errorf("English default formats too: %q", got)
	}
}

func TestMatchAndDirection(t *testing.T) {
	avail := []string{"en", "es", "fr", "pt-BR"}
	cases := []struct {
		pref []string
		want string
	}{
		{[]string{"fr-CA", "en"}, "fr"},
		{[]string{"de", "es-MX"}, "es"},
		{[]string{"pt-PT"}, "pt-BR"},
		{[]string{"ja", "ko"}, "en"},
		{nil, "en"},
	}
	for _, c := range cases {
		if got := Match(c.pref, avail, "en"); got != c.want {
			t.Errorf("Match(%v) = %q, want %q", c.pref, got, c.want)
		}
	}
	if Direction("ar-EG") != "rtl" || Direction("he") != "rtl" || Direction("fr") != "ltr" || Direction("") != "ltr" {
		t.Error("direction")
	}
}

// The shipped packs hold the same ids, so no language is left half done, and
// every message keeps the fmt verbs of its English original's count.
func TestShippedPacksAreComplete(t *testing.T) {
	c := Default()
	langs := c.Languages()
	if len(langs) < 3 {
		t.Fatalf("languages %v", langs)
	}
	ref := c.msgs["es"]
	for _, l := range langs {
		for id := range ref {
			if _, ok := c.msgs[l][id]; !ok {
				t.Errorf("%s lacks %q", l, id)
			}
		}
		for id := range c.msgs[l] {
			if _, ok := ref[id]; !ok {
				t.Errorf("%s has %q, which es lacks", l, id)
			}
			if strings.Count(c.msgs[l][id], "%") != strings.Count(ref[id], "%") {
				t.Errorf("%s %q has a different number of verbs: %q vs %q", l, id, c.msgs[l][id], ref[id])
			}
		}
	}
	if got := c.Text("fr", "saver.save", "Save"); got != "Enregistrer" {
		t.Errorf("fr save: %q", got)
	}
	if got := c.Text("de", "find.nOfM", "%d of %d", 1, 4); got != "1 von 4" {
		t.Errorf("de: %q", got)
	}
}
