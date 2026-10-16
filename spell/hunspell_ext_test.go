package spell

import (
	"slices"
	"testing"
)

func has(d *Dictionary, words ...string) (missing []string) {
	for _, w := range words {
		if !d.Has(w) {
			missing = append(missing, w)
		}
	}
	return
}

func TestLoadHunspellDecodesTheCharacterSetTheAffixFileNames(t *testing.T) {
	aff := []byte("SET ISO8859-1\nTRY e\n")
	dic := []byte("2\ngr\xf6\xdfe\nf\xfcr\n") // größe, für in Latin-1
	d := LoadHunspell(dic, aff, HunspellOptions{})
	if m := has(d, "größe", "für"); len(m) != 0 {
		t.Errorf("not decoded: %v", m)
	}
	// UTF-8 bytes under the same SET line are left as they are.
	d = LoadHunspell([]byte("1\ngröße\n"), aff, HunspellOptions{})
	if m := has(d, "größe"); len(m) != 0 {
		t.Errorf("UTF-8 was decoded twice: %v", m)
	}
}

func TestCrossProductsMakeTheFormsWithAPrefixAndASuffix(t *testing.T) {
	aff := []byte("PFX U Y 1\nPFX U 0 un .\nSFX A Y 1\nSFX A 0 able .\nPFX N N 1\nPFX N 0 non .\n")
	dic := []byte("3\nlove/UA\ntrain/N\nread/UA\n")
	d := LoadHunspell(dic, aff, HunspellOptions{})
	if m := has(d, "love", "unlove", "loveable", "unloveable", "unreadable", "nontrain"); len(m) != 0 {
		t.Errorf("missing %v", m)
	}
	if d.Has("nontrainable") {
		t.Error("a class that does not cross is not crossed")
	}
	plain := LoadHunspell(dic, aff, HunspellOptions{NoCrossProducts: true})
	if plain.Has("unloveable") || !plain.Has("loveable") || !plain.Has("unlove") {
		t.Error("without cross products only single affixes")
	}
	// The embedded English reading is the single-level one.
	if FromHunspell(string(dic), string(aff)).Has("unloveable") {
		t.Error("FromHunspell stays single level")
	}
}

func TestCompoundFlagMakesWordsOfSeveralStems(t *testing.T) {
	aff := []byte("COMPOUNDFLAG Z\nCOMPOUNDMIN 3\n")
	dic := []byte("5\nhaus/Z\ntuer/Z\nschloss\nab/Z\nfenster/Z\n")
	d := LoadHunspell(dic, aff, HunspellOptions{})
	if m := has(d, "haus", "schloss", "haustuer", "hausfenster", "haustuerfenster"); len(m) != 0 {
		t.Errorf("missing %v", m)
	}
	for _, w := range []string{"schlosshaus", "hausschloss", "abhaus", "hausab", "haus1", "tuerhau", "hausx"} {
		if d.Has(w) {
			t.Errorf("%q is not a compound of flagged stems of 3 letters or more", w)
		}
	}
	c := NewChecker(d)
	if got := c.Misspelled("haustuer schlosshaus"); len(got) != 1 {
		t.Errorf("%v", got)
	}
}

func TestCompoundBeginMiddleEndKeepTheirPlaces(t *testing.T) {
	aff := []byte("COMPOUNDBEGIN B\nCOMPOUNDMIDDLE M\nCOMPOUNDEND E\nCOMPOUNDMIN 2\n")
	dic := []byte("4\nmega/B\ncyber/M\nbyte/E\nfoo/BE\n")
	d := LoadHunspell(dic, aff, HunspellOptions{})
	if m := has(d, "megabyte", "megacyberbyte", "foobyte", "megafoo"); len(m) != 0 {
		t.Errorf("missing %v", m)
	}
	for _, w := range []string{"bytemega", "cybermega", "megacyber", "bytebyte", "cyberbyte"} {
		if d.Has(w) {
			t.Errorf("%q has a part in the wrong place", w)
		}
	}
	if !d.Has("mega") {
		t.Error("a stem with a compound flag is still a word by itself")
	}
}

func TestCompoundRulePatternsWithStarAndQuestionMark(t *testing.T) {
	aff := []byte("COMPOUNDMIN 1\nCOMPOUNDRULE 2\nCOMPOUNDRULE XY?Z\nCOMPOUNDRULE AB*\n")
	dic := []byte("5\nre/X\nmid/Y\ning/Z\nta/A\nb/B\n")
	d := LoadHunspell(dic, aff, HunspellOptions{})
	if m := has(d, "reing", "remiding", "tab", "tabb", "tabbbb"); len(m) != 0 {
		t.Errorf("missing %v", m)
	}
	for _, w := range []string{"midre", "remidmiding", "tata", "bta", "reingre"} {
		if d.Has(w) {
			t.Errorf("%q does not fit a rule", w)
		}
	}
}

func TestNeedAffixForbiddenWordAndNumericFlags(t *testing.T) {
	aff := []byte("FLAG num\nNEEDAFFIX 9\nFORBIDDENWORD 8\nSFX 1 Y 1\nSFX 1 0 en .\n")
	dic := []byte("4\nfrag/1,9\nkind/1\nbose/8\nbosen\n")
	d := LoadHunspell(dic, aff, HunspellOptions{})
	if d.Has("frag") || !d.Has("fragen") {
		t.Error("a stem that needs an affix is not a word alone")
	}
	if !d.Has("kind") || !d.Has("kinden") {
		t.Error("an ordinary stem and its affixed form")
	}
	if d.Has("bose") {
		t.Error("a forbidden word is never right")
	}
	if !d.Has("bosen") {
		t.Error("a word next to it is")
	}
}

func TestSuggestionsUseTheLanguagesLettersCommonMistakesAndKeyboard(t *testing.T) {
	// TRY: the language's own letters, so a missing ü is found.
	d := LoadHunspell([]byte("2\nfür\nkind\n"), []byte("TRY ürn\n"), HunspellOptions{})
	c := NewChecker(d)
	if got := c.Suggest("fur", 3); !slices.Contains(got, "für") {
		t.Errorf("without ü in the alphabet nothing would be found: %v", got)
	}
	if got := NewChecker(New("für")).Suggest("fur", 3); slices.Contains(got, "für") {
		t.Error("the Latin alphabet has no ü: the plain dictionary should not find it")
	}
	// REP: a common mistake, anchored or not.
	d = LoadHunspell([]byte("3\nfoto\ntelefon\nafon\n"), []byte("REP 3\nREP ph f\nREP ^ph f\nREP on$ ong\n"), HunspellOptions{})
	c = NewChecker(d)
	if got := c.Suggest("photo", 3); !slices.Contains(got, "foto") {
		t.Errorf("REP ph->f: %v", got)
	}
	if got := c.Suggest("telephon", 3); !slices.Contains(got, "telefon") {
		t.Errorf("REP ph->f inside the word: %v", got)
	}
	// KEY: a letter next to the right one beats an unrelated edit of the same size.
	d = LoadHunspell([]byte("2\nkind\nkimb\n"), []byte("KEY qwertyuiop|asdfghjkl|zxcvbnm\n"), HunspellOptions{})
	got := NewChecker(d).Suggest("kimd", 2)
	if len(got) < 1 || got[0] != "kind" {
		t.Errorf("the neighbour of m on the keyboard is n: %v", got)
	}
	got = NewChecker(New("kind", "kimb")).Suggest("kimd", 2)
	if len(got) < 1 || got[0] != "kimb" {
		t.Errorf("without KEY the order is alphabetical: %v", got)
	}
}

func TestLoadHunspellStopsAtTheWordLimit(t *testing.T) {
	aff := []byte("SFX A Y 3\nSFX A 0 s .\nSFX A 0 ed .\nSFX A 0 ing .\n")
	dic := []byte("3\nwalk/A\ntalk/A\nlook/A\n")
	d := LoadHunspell(dic, aff, HunspellOptions{MaxWords: 5})
	if d.Len() > 5 {
		t.Errorf("%d words", d.Len())
	}
}
