package i18n

import "testing"

func TestPluralCategories(t *testing.T) {
	cases := []struct {
		lang string
		n    int
		want string
	}{
		{"en", 1, "one"}, {"en", 0, "other"}, {"en", 2, "other"}, {"de", 1, "one"}, {"de-AT", 5, "other"},
		{"fr", 0, "one"}, {"fr", 1, "one"}, {"fr", 2, "other"}, {"fr", 1000000, "many"},
		{"es", 0, "other"}, {"es", 1, "one"}, {"es", 2000000, "many"},
		{"ru", 1, "one"}, {"ru", 21, "one"}, {"ru", 11, "many"}, {"ru", 3, "few"}, {"ru", 13, "many"}, {"ru", 25, "many"}, {"ru", 0, "many"},
		{"pl", 1, "one"}, {"pl", 22, "few"}, {"pl", 12, "many"}, {"pl", 5, "many"},
		{"cs", 3, "few"}, {"cs", 5, "other"}, {"ja", 1, "other"},
		{"ar", 0, "zero"}, {"ar", 2, "two"}, {"ar", 7, "few"}, {"ar", 11, "many"}, {"ar", 100, "other"},
		{"en", -1, "one"},
	}
	for _, c := range cases {
		if got := PluralCategory(c.lang, c.n); got != c.want {
			t.Errorf("%s %d: %s, want %s", c.lang, c.n, got, c.want)
		}
	}
}

func TestCatalogPlural(t *testing.T) {
	c := New()
	c.Add("ru", map[string]string{"f#one": "%d файл", "f#few": "%d файла", "f#many": "%d файлов"})
	c.Add("de", map[string]string{"f#one": "%d Datei", "f#other": "%d Dateien"})
	for _, tc := range []struct {
		lang string
		n    int
		want string
	}{{"ru", 1, "1 файл"}, {"ru", 3, "3 файла"}, {"ru", 5, "5 файлов"}, {"de", 1, "1 Datei"}, {"de", 4, "4 Dateien"}, {"en", 1, "1 file"}, {"en", 4, "4 files"}, {"de-CH", 2, "2 Dateien"}} {
		if got := c.Plural(tc.lang, "f", "%d file", "%d files", tc.n); got != tc.want {
			t.Errorf("%s %d: %q, want %q", tc.lang, tc.n, got, tc.want)
		}
	}
	// a language that lacks the category falls back to "other", then to the English
	c.Add("fr", map[string]string{"g#other": "%d fichiers"})
	if got := c.Plural("fr", "g", "%d fichier", "%d fichiers", 1); got != "1 fichiers" {
		t.Errorf("fallback to other: %q", got)
	}
	if got := c.Plural("en", "none", "One thing", "Things", 1); got != "One thing" {
		t.Errorf("a text with no verb: %q", got)
	}
}
