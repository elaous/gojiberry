// Package wordlist holds small word lists by language.
package wordlist

import (
	"sort"
	"strings"

	"github.com/elaous/gojiberry/lang"
)

// Codes lists the languages that have a list.
func Codes() []string { return []string{"ar", "de", "es", "fr", "he", "pt"} }

var raw = map[string]string{"de": de, "fr": fr, "es": es, "pt": pt, "ar": ar, "he": he}

// Base reduces a BCP 47 tag to its language.
func Base(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_"); i >= 0 {
		tag = tag[:i]
	}
	return tag
}

// For returns the words of the language of tag.
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
