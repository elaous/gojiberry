// Package complete suggests whole words for the part of a word being typed. It is a pure, in-memory
// index: no network, no telemetry. Words come from the text itself (the ones typed last count
// most), from a vocabulary (a dictionary, a user dictionary) and from providers a program adds
// (snippets, mentions).
package complete

import (
	"sort"
	"strings"
	"unicode"
)

// Suggestion is one candidate.
type Suggestion struct {
	// Word is the whole word to put in place of the prefix.
	Word string
	// Source says where it comes from: "text", "vocabulary" or a provider's name.
	Source string
}

// Provider adds candidates from a custom source (snippets, mentions). It is asked with the prefix as
// typed and returns whole words, best first.
type Provider interface {
	Name() string
	Complete(prefix string) []string
}

// Options configure Suggest.
type Options struct {
	// Max is the most suggestions returned; 5 when zero.
	Max int
	// MinPrefix is the fewest letters in a prefix that gets suggestions; 2 when zero.
	MinPrefix int
}

type entry struct {
	word  string
	lower string
	count int // times in the text
	last  int // position of the last use, for recency
	vocab int // weight from the vocabulary
}

// Index is the words known, searched by prefix. The zero value is ready to use.
type Index struct {
	words     map[string]*entry // by the word as written
	sorted    []*entry          // by lower-case form, rebuilt when dirty
	dirty     bool
	clock     int
	providers []Provider
}

// IsWordRune says what a word is made of: letters, digits, and the apostrophe, hyphen and underscore
// inside it.
func IsWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '_' || r == '\'' || r == '’' || r == '-'
}

// Words splits text into its words.
func Words(text string) []string {
	var out []string
	var cur []rune
	flush := func() {
		w := strings.Trim(string(cur), "'’-")
		if w != "" {
			out = append(out, w)
		}
		cur = cur[:0]
	}
	for _, r := range text {
		if IsWordRune(r) {
			cur = append(cur, r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// AddProvider adds a custom source.
func (ix *Index) AddProvider(p Provider) { ix.providers = append(ix.providers, p) }

func (ix *Index) get(w string) *entry {
	if ix.words == nil {
		ix.words = map[string]*entry{}
	}
	e := ix.words[w]
	if e == nil {
		e = &entry{word: w, lower: strings.ToLower(w)}
		ix.words[w] = e
		ix.dirty = true
	}
	return e
}

// AddText counts the words of text, in order: a word nearer the end of the text counts as used more
// recently. Call Reset first to index a text again after it changed.
func (ix *Index) AddText(text string) {
	for _, w := range Words(text) {
		ix.clock++
		e := ix.get(w)
		e.count++
		e.last = ix.clock
	}
}

// AddVocabulary adds words that are suggested even when the text does not have them; weight (1 or
// more) puts them above or below each other.
func (ix *Index) AddVocabulary(words []string, weight int) {
	if weight < 1 {
		weight = 1
	}
	for _, w := range words {
		if w = strings.TrimSpace(w); w != "" {
			e := ix.get(w)
			if weight > e.vocab {
				e.vocab = weight
			}
		}
	}
}

// Reset forgets the words of the text (the vocabulary and providers stay).
func (ix *Index) Reset() {
	for w, e := range ix.words {
		if e.vocab == 0 {
			delete(ix.words, w)
		} else {
			e.count, e.last = 0, 0
		}
	}
	ix.clock = 0
	ix.dirty = true
}

func (ix *Index) sort() {
	if !ix.dirty && ix.sorted != nil {
		return
	}
	ix.sorted = ix.sorted[:0]
	for _, e := range ix.words {
		ix.sorted = append(ix.sorted, e)
	}
	sort.Slice(ix.sorted, func(i, j int) bool { return ix.sorted[i].lower < ix.sorted[j].lower })
	ix.dirty = false
}

// score ranks candidates: a word used in the text beats a vocabulary word; more uses and later uses
// rank higher; a prefix match that keeps the typed case ranks above one that does not.
func (e *entry) score(prefix string, clock int) float64 {
	s := float64(e.vocab)
	if e.count > 0 {
		s += 10 + float64(e.count)
		if clock > 0 {
			s += 5 * float64(e.last) / float64(clock)
		}
	}
	if strings.HasPrefix(e.word, prefix) {
		s += 0.5
	}
	return s
}

// Suggest returns up to Max whole words that start with prefix (without regard to case) and are
// longer than it, best first. A prefix shorter than MinPrefix gets nothing. A word that is only the
// prefix again is not suggested. The case of the first letter follows what was typed.
func (ix *Index) Suggest(prefix string, o Options) []Suggestion {
	if o.Max <= 0 {
		o.Max = 5
	}
	if o.MinPrefix <= 0 {
		o.MinPrefix = 2
	}
	if len([]rune(prefix)) < o.MinPrefix {
		return nil
	}
	lower := strings.ToLower(prefix)
	ix.sort()
	i := sort.Search(len(ix.sorted), func(i int) bool { return ix.sorted[i].lower >= lower })
	type cand struct {
		e *entry
		s float64
	}
	var cands []cand
	for ; i < len(ix.sorted) && strings.HasPrefix(ix.sorted[i].lower, lower); i++ {
		e := ix.sorted[i]
		if len([]rune(e.lower)) <= len([]rune(lower)) {
			continue
		}
		// The text's own occurrence of the prefix being typed is not a candidate when it is the only use.
		cands = append(cands, cand{e, e.score(prefix, ix.clock)})
	}
	sort.SliceStable(cands, func(a, b int) bool {
		if cands[a].s != cands[b].s {
			return cands[a].s > cands[b].s
		}
		return cands[a].e.lower < cands[b].e.lower
	})
	var out []Suggestion
	seen := map[string]bool{}
	add := func(w, src string) {
		w = matchCase(w, prefix)
		k := strings.ToLower(w)
		if seen[k] || len([]rune(k)) <= len([]rune(lower)) || len(out) >= o.Max {
			return
		}
		seen[k] = true
		out = append(out, Suggestion{Word: w, Source: src})
	}
	for _, c := range cands {
		src := "vocabulary"
		if c.e.count > 0 {
			src = "text"
		}
		add(c.e.word, src)
	}
	for _, p := range ix.providers {
		for _, w := range p.Complete(prefix) {
			if strings.HasPrefix(strings.ToLower(w), lower) {
				add(w, p.Name())
			}
		}
	}
	return out
}

// matchCase gives word the capital of prefix's first letter when the prefix is capitalised and the
// word is not (typing "Hel" for "hello" gives "Hello"); otherwise the word is as it is.
func matchCase(word, prefix string) string {
	pr, wr := []rune(prefix), []rune(word)
	if len(pr) == 0 || len(wr) == 0 {
		return word
	}
	if unicode.IsUpper(pr[0]) && unicode.IsLower(wr[0]) && strings.EqualFold(string(wr[:len(pr)]), prefix) {
		wr[0] = unicode.ToUpper(wr[0])
		return string(wr)
	}
	return word
}

// PrefixAt finds the word being typed: the letters before offset in runes, and where they start. It
// is "" when the caret is not at the end of a word's start (for example after a space).
func PrefixAt(text []rune, offset int) (string, int) {
	if offset > len(text) {
		offset = len(text)
	}
	start := offset
	for start > 0 && IsWordRune(text[start-1]) {
		start--
	}
	return string(text[start:offset]), start
}

// WordEnd is where the word that contains offset ends, so a replacement covers the rest of the word.
func WordEnd(text []rune, offset int) int {
	end := offset
	for end < len(text) && IsWordRune(text[end]) {
		end++
	}
	return end
}
