package lang

import (
	"sort"
	"strings"
	"unicode"
)

var Stopwords = map[string][]string{
	"en": {"the", "and", "is", "of", "to", "a", "in", "that", "it", "for", "with", "as", "was", "on", "are", "this", "be", "have", "not"},
	"es": {"el", "la", "de", "que", "y", "en", "los", "se", "del", "las", "un", "por", "con", "no", "una", "su", "para", "es", "al"},
	"fr": {"le", "de", "la", "et", "les", "des", "en", "un", "une", "du", "que", "est", "pour", "dans", "qui", "pas", "sur", "au", "ce"},
	"de": {"der", "die", "und", "das", "ist", "den", "von", "zu", "mit", "sich", "des", "auf", "für", "nicht", "eine", "ein", "im", "dem", "als"},
	"pt": {"o", "a", "de", "que", "e", "do", "da", "em", "um", "para", "com", "não", "uma", "os", "no", "se", "na", "por", "mais"},
	"it": {"il", "di", "che", "e", "la", "un", "per", "in", "una", "con", "non", "del", "le", "si", "da", "lo", "sono", "come", "della"},
	"nl": {"de", "het", "een", "en", "van", "ik", "te", "dat", "die", "in", "is", "niet", "op", "zijn", "met", "voor", "er", "maar", "aan"},
}

// Names are the languages' own names, for a picker.
var Names = map[string]string{
	"en": "English", "es": "Español", "fr": "Français", "de": "Deutsch", "pt": "Português", "it": "Italiano", "nl": "Nederlands",
}

const (
	MinWords = 12
	// MinMatches is the fewest stopword hits the winner must have.
	MinMatches = 3
)

// Codes lists the supported language codes, sorted.
func Codes() []string {
	out := make([]string, 0, len(Stopwords))
	for c := range Stopwords {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

var sets = func() map[string]map[string]bool {
	m := map[string]map[string]bool{}
	for code, ws := range Stopwords {
		s := map[string]bool{}
		for _, w := range ws {
			s[w] = true
		}
		m[code] = s
	}
	return m
}()

// Detect guesses the language of text.
func Detect(text string) (code string, ok bool) {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' })
	if len(words) < MinWords {
		return "", false
	}
	best, bestN, tie := "", 0, false
	for _, c := range Codes() { // sorted, so ties are decided the same way every time
		n := 0
		for _, w := range words {
			if sets[c][w] {
				n++
			}
		}
		switch {
		case n > bestN:
			best, bestN, tie = c, n, false
		case n == bestN && n > 0:
			tie = true
		}
	}
	if bestN < MinMatches || tie {
		return "", false
	}
	return best, true
}
