package spell

import (
	"reflect"
	"strings"
	"testing"
)

func TestEnglishKnowsCommonWordsAndInflections(t *testing.T) {
	c := NewChecker(English())
	if English().Len() < 100000 {
		t.Fatalf("only %d words", English().Len())
	}
	for _, w := range []string{"hello", "world", "walking", "walked", "editor's", "don't", "unhappiness", "dogs", "quickly"} {
		if !c.Known(w) {
			t.Errorf("%q should be known", w)
		}
	}
	for _, w := range []string{"helo", "wrold", "teh", "gojiberry", "asdfgh"} {
		if c.Known(w) {
			t.Errorf("%q should not be known", w)
		}
	}
}

func TestMisspelledFindsWordsByRuneOffset(t *testing.T) {
	c := NewChecker(English())
	text := "The quikc brown fox — café, H2O, NASA, 3rd and wrold's end."
	got := c.Misspelled(text)
	rs := []rune(text)
	var words []string
	for _, s := range got {
		words = append(words, string(rs[s.From:s.To]))
	}
	want := []string{"quikc", "café", "wrold's"}
	if !reflect.DeepEqual(words, want) {
		t.Errorf("misspelled %v, want %v", words, want)
	}
}

func TestAddAndIgnore(t *testing.T) {
	c := NewChecker(English())
	if len(c.Misspelled("Gojiberry editor")) != 1 {
		t.Fatal("gojiberry should be unknown at first")
	}
	c.Add("Gojiberry")
	if len(c.Misspelled("Gojiberry editor")) != 0 {
		t.Error("an added word is known")
	}
	c.Ignore("xyzzy")
	if len(c.Misspelled("xyzzy")) != 0 {
		t.Error("an ignored word is left alone")
	}
}

func TestSuggestRanksAndKeepsCase(t *testing.T) {
	c := NewChecker(English())
	cases := map[string]string{"teh": "the", "Teh": "The", "TEH": "THE", "wrold": "world", "recieve": "receive"}
	for in, want := range cases {
		got := c.Suggest(in, 5)
		if len(got) == 0 || !contains(got, want) {
			t.Errorf("Suggest(%q) = %v, want %q among them", in, got, want)
		}
	}
	if got := c.Suggest("qzxqzxqzx", 5); len(got) != 0 {
		t.Errorf("nonsense got %v", got)
	}
	if len(c.Suggest("wrold", 3)) > 3 {
		t.Error("max not respected")
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestCompleteAndWordBefore(t *testing.T) {
	c := NewChecker(New("apple", "application", "apply", "banana"))
	if w, ok := c.Complete("app"); !ok || w != "apple" {
		t.Errorf("complete %q %v", w, ok)
	}
	if _, ok := c.Complete("zzz"); ok {
		t.Error("no completion")
	}
	text := []rune("I like appl")
	if start, w, ok := WordBefore(text, len(text)); !ok || start != 7 || w != "appl" {
		t.Errorf("WordBefore %d %q %v", start, w, ok)
	}
	if _, _, ok := WordBefore([]rune("end. "), 5); ok {
		t.Error("after a space there is no word")
	}
}

func TestFromHunspellExpandsAffixes(t *testing.T) {
	aff := "SET UTF-8\nSFX S Y 1\nSFX S 0 s .\nPFX U Y 1\nPFX U 0 un .\n"
	dic := "2\ncat/S\nhappy/U\n"
	d := FromHunspell(dic, aff)
	for _, w := range []string{"cat", "cats", "happy", "unhappy"} {
		if !d.Has(w) {
			t.Errorf("%q missing from %d words", w, d.Len())
		}
	}
}

func TestWordListAndGunzip(t *testing.T) {
	d := FromWordList("Alpha\n\n beta \n")
	if !d.Has("alpha") || !d.Has("beta") || d.Len() != 2 {
		t.Errorf("%d words", d.Len())
	}
	if _, err := Gunzip([]byte("not gzip")); err == nil {
		t.Error("expected an error")
	}
	_ = strings.ToLower
}
