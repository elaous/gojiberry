// Package wordlist holds small word lists of common words for completion in German, French,
// Spanish, Portuguese, Arabic and Hebrew (English has the full spell dictionary in package spell). They are not dictionaries: each
// is a few hundred frequent words (the function words of package lang, and everyday verbs, nouns
// and adjectives) that were written for this repository, so they carry no third-party licence. A
// list only feeds suggestions; it never marks a word as wrong.
package wordlist

import (
	"sort"
	"strings"

	"github.com/elaous/gojiberry/lang"
)

// Codes lists the languages that have a list, sorted.
func Codes() []string { return []string{"ar", "de", "es", "fr", "he", "pt"} }

var raw = map[string]string{"de": de, "fr": fr, "es": es, "pt": pt, "ar": ar, "he": he}

// Base reduces a BCP 47 tag to its language ("de-AT" and "DE_ch" give "de"); "" for no tag.
func Base(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		tag = tag[:i]
	}
	return tag
}

// For returns the words of the language of tag (a BCP 47 tag), sorted and without duplicates, or
// nil when there is no list for it. The function words of package lang are included.
func For(tag string) []string {
	code := Base(tag)
	text, ok := raw[code]
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(w string) {
		if w != "" && !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	for _, w := range strings.Fields(text) {
		add(w)
	}
	for _, w := range lang.Stopwords[code] {
		add(w)
	}
	sort.Strings(out)
	return out
}
