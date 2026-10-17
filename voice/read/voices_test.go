package read

import "testing"

func TestDescribeSaysAccentAndGender(t *testing.T) {
	for _, c := range []struct {
		v                        Voice
		language, accent, gender string
	}{
		{Voice{Name: "Samantha", Lang: "en-US"}, "English", "American", Female},
		{Voice{Name: "Daniel", Lang: "en-GB"}, "English", "British", Male},
		{Voice{Name: "Google UK English Female", Lang: "en_GB"}, "English", "British", Female},
		{Voice{Name: "Microsoft Guy Online (Natural)", Lang: "en-US"}, "English", "American", Male},
		{Voice{Name: "Microsoft Zira Desktop", Lang: "en-US"}, "English", "American", Female},
		{Voice{Name: "Karen", Lang: "en-AU"}, "English", "Australian", Female},
		{Voice{Name: "Luciana", Lang: "pt-BR"}, "Portuguese", "Brazilian", Female},
		{Voice{Name: "Lessac", Lang: "en-US"}, "English", "American", Female},
		{Voice{Name: "Alan", Lang: "en-GB"}, "English", "British", Male},
		{Voice{Name: "Thorsten", Lang: "de-DE"}, "German", "German", Male},
		{Voice{Name: "Southern_english_female", Lang: "en-GB"}, "English", "British", Female},
		{Voice{Name: "Default", Lang: "en"}, "English", "", ""},
		{Voice{Name: "Something", Lang: "xx-YY"}, "", "YY", ""},
	} {
		got := Describe(c.v)
		if got.Language != c.language || got.Accent != c.accent || got.Gender != c.gender {
			t.Errorf("%+v: got %+v, want %s/%s/%s", c.v, got, c.language, c.accent, c.gender)
		}
	}
	if l := Label(Voice{Name: "Samantha", Lang: "en-US", Local: true}); l != "Samantha (American, female)" {
		t.Errorf("label %q", l)
	}
	if l := Label(Voice{Name: "Remote", Lang: "en-GB"}); l != "Remote (British, online)" {
		t.Errorf("label %q", l)
	}
}

func TestForLanguageOffersOnlyWhatMatchesAndIsAllowed(t *testing.T) {
	all := []Voice{
		{Name: "Samantha", Lang: "en-US", Local: true}, {Name: "Daniel", Lang: "en-GB", Local: true}, {Name: "Net English", Lang: "en-US", Local: false}, {Name: "Anna", Lang: "de-DE", Local: true},
		{Name: "Karen", Lang: "en-AU", Local: true}, {Name: "Amelie", Lang: "fr-CA", Local: true},
	}
	got := ForLanguage(all, "en-GB", false)
	if len(got) != 3 || got[0].Name != "Daniel" {
		t.Fatalf("the exact tag comes first and online voices are left out: %+v", got)
	}
	if got := ForLanguage(all, "en", true); len(got) != 4 {
		t.Fatalf("with online voices allowed: %+v", got)
	}
	if got := ForLanguage(all, "pt-BR", true); len(got) != 0 {
		t.Fatalf("no Portuguese voice: %+v", got)
	}
	if got := ForLanguage(all, "", true); len(got) != 0 {
		t.Fatal("no language, no list")
	}
	if v, ok := Find(all, "en-US", "Daniel", false); !ok || v.Lang != "en-GB" {
		t.Fatalf("find: %+v %v", v, ok)
	}
	if _, ok := Find(all, "en-US", "Net English", false); ok {
		t.Fatal("an online voice is not found unless allowed")
	}
	if _, ok := Find(all, "en-US", "", true); ok {
		t.Fatal("no name, no voice")
	}
}
