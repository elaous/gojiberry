package lang

import "testing"

func TestValidTag(t *testing.T) {
	valid := []string{
		"", "en", "fr", "de-CH", "pt-BR", "zh-Hant-TW", "es-419", "sl-rozaj-biske", "en-US-x-twain", "x-private",
		"de-1996", "zh-yue", "en-a-bbb-x-a-ccc", "ast", "fil-PH",
	}
	for _, s := range valid {
		if !ValidTag(s) {
			t.Errorf("ValidTag(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"e", "12", "english please", "en_US", "en-", "-en", "en--US", "en-US-", "x", "x-", "en-x", "en-a",
		"en-US-US", "abcdefghi", "en-Latn-Latn", "not a tag!", "zh-Hant-TW-abcdefghi",
	}
	for _, s := range invalid {
		if ValidTag(s) {
			t.Errorf("ValidTag(%q) = true, want false", s)
		}
	}
}
