package canvas

import "testing"

func TestValidLang(t *testing.T) {
	for _, ok := range []string{"", "fr", "pt-BR", "zh-Hant-TW", "en-x-private1"} {
		if !ValidLang(ok) {
			t.Errorf("%q is a language tag", ok)
		}
	}
	for _, bad := range []string{"-", "fr-", "-fr", "fr--CA", "fr CA", "fr\"><script>", "waytoolonggroup", "a-b-c-d-e-f-g-h-i-j-k-l-m-n-o-p-q-r-s-t"} {
		if ValidLang(bad) {
			t.Errorf("%q is not a language tag", bad)
		}
	}
}

func TestSetLanguageTagsTheSelection(t *testing.T) {
	f, _ := newFlow("hello monde", 400, 100)
	f.SetSelection(6, 11)
	if !f.SetLanguage("fr") {
		t.Fatal("SetLanguage reported no change")
	}
	var tagged, plain string
	for _, it := range f.Slice(0, 11) {
		if c, ok := it.(Chars); ok {
			switch c.Format.Lang {
			case "fr":
				tagged += c.Text
			case "":
				plain += c.Text
			}
		}
	}
	if tagged != "monde" || plain != "hello " {
		t.Errorf("tagged %q, untagged %q", tagged, plain)
	}
	if f.SetLanguage("not a tag") {
		t.Error("an invalid tag was accepted")
	}
	if !f.SetLanguage("") {
		t.Fatal("SetLanguage(\"\") reported no change")
	}
	for _, it := range f.Slice(0, 11) {
		if c, ok := it.(Chars); ok && c.Format.Lang != "" {
			t.Errorf("%q still tagged %q", c.Text, c.Format.Lang)
		}
	}
}
