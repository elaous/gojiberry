// Package spell is a spell checker: a word list (a Hunspell dictionary, with
// its affix rules expanded, or a plain list), the words in a text it does not
// know, suggestions for them and word completion.
//
// English is embedded (SCOWL's en_US, see dict/NOTICE) and loads on first use;
// any other language is loaded from the app's own files with FromWordList or
// FromHunspell. Everything is pure Go and works in the browser's wasm build.
package spell

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"sort"
	"strings"
	"sync"
	"unicode"
)

//go:embed dict/en_US.aff.gz
var enAffGz []byte

//go:embed dict/en_US.dic.gz
var enDicGz []byte

// commonContractions are contractions that do not reduce to a word by
// stripping a suffix ("isn't" less "'t" is "isn", "won't" is not "will").
var commonContractions = []string{
	"don't", "won't", "can't", "isn't", "aren't", "wasn't", "weren't",
	"hasn't", "haven't", "hadn't", "doesn't", "didn't", "wouldn't",
	"shouldn't", "couldn't", "mustn't", "mightn't", "needn't",
	"i'm", "you're", "he's", "she's", "it's", "we're", "they're",
	"i've", "you've", "we've", "they've",
	"i'll", "you'll", "he'll", "she'll", "it'll", "we'll", "they'll",
	"i'd", "you'd", "he'd", "she'd", "it'd", "we'd", "they'd",
	"let's", "that's", "who's", "what's", "here's", "there's",
	"y'all", "o'clock",
}

// Dictionary is a set of known words (lower case). It does not change once
// built, so one can be shared.
type Dictionary struct{ words map[string]struct{} }

// New returns a dictionary of words (lower-cased).
func New(words ...string) *Dictionary {
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[strings.ToLower(w)] = struct{}{}
	}
	return &Dictionary{words: m}
}

// FromWordList reads a plain list, one word a line.
func FromWordList(text string) *Dictionary {
	m := make(map[string]struct{}, 1<<16)
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if w := strings.TrimSpace(sc.Text()); w != "" {
			m[strings.ToLower(w)] = struct{}{}
		}
	}
	return &Dictionary{words: m}
}

// FromHunspell reads a Hunspell dictionary: dic is the .dic file's text and
// aff the .aff's. Single-level prefix and suffix rules are expanded; compounding
// and cross-product prefix+suffix forms are not.
func FromHunspell(dic, aff string) *Dictionary {
	return &Dictionary{words: ExpandHunspellDictionary(dic, aff)}
}

// Gunzip returns the text of gzip-compressed data (a downloaded word list).
func Gunzip(gz []byte) (string, error) {
	r, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return "", err
	}
	defer func() { _ = r.Close() }()
	b, err := io.ReadAll(io.LimitReader(r, 256<<20))
	return string(b), err
}

var (
	englishOnce sync.Once
	english     *Dictionary
)

// English is the embedded English dictionary, built on first use.
func English() *Dictionary {
	englishOnce.Do(func() {
		aff, err1 := Gunzip(enAffGz)
		dic, err2 := Gunzip(enDicGz)
		if err1 != nil || err2 != nil {
			english = New()
			return
		}
		english = FromHunspell(dic, aff)
		for _, w := range commonContractions {
			english.words[w] = struct{}{}
		}
	})
	return english
}

// Len is how many words the dictionary holds.
func (d *Dictionary) Len() int { return len(d.words) }

// Has reports whether the lower-case word is in the dictionary.
func (d *Dictionary) Has(lower string) bool {
	_, ok := d.words[lower]
	return ok
}

// Checker checks text against a dictionary and the user's own words.
type Checker struct {
	Dict *Dictionary
	// Custom are words the user added (lower case), and Ignored words to leave
	// alone for now; both are consulted before the dictionary says no.
	Custom, Ignored map[string]struct{}
}

// NewChecker returns a checker for d with no added words.
func NewChecker(d *Dictionary) *Checker {
	return &Checker{Dict: d, Custom: map[string]struct{}{}, Ignored: map[string]struct{}{}}
}

// Add teaches the checker a word for good (the caller keeps the list and puts
// it back in Custom next time).
func (c *Checker) Add(word string) { c.Custom[strings.ToLower(word)] = struct{}{} }

// Ignore leaves a word alone until the checker is dropped.
func (c *Checker) Ignore(word string) { c.Ignored[strings.ToLower(word)] = struct{}{} }

// Known reports whether the (lower-case) word is a real word: in the
// dictionary, added or ignored, or a known word with a possessive 's.
func (c *Checker) Known(lower string) bool {
	has := func(w string) bool {
		if c.Dict != nil && c.Dict.Has(w) {
			return true
		}
		_, a := c.Custom[w]
		_, i := c.Ignored[w]
		return a || i
	}
	if has(lower) {
		return true
	}
	if base, ok := strings.CutSuffix(lower, "'s"); ok && base != "" {
		return has(base)
	}
	return false
}

// Span is a range [From, To) of a text, in runes.
type Span struct{ From, To int }

func isWordRune(r rune) bool { return unicode.IsLetter(r) }

// wordAt returns the end of the word that starts at i: letters, with an
// apostrophe between two letters ("don't") staying inside it.
func wordEnd(rs []rune, i int) int {
	for i < len(rs) && (isWordRune(rs[i]) || (rs[i] == '\'' && i+1 < len(rs) && isWordRune(rs[i+1]) && i > 0 && isWordRune(rs[i-1]))) {
		i++
	}
	return i
}

// Misspelled returns the words of text the checker does not know, in order.
// Digits and punctuation are never misspelled, and words that are all capital
// letters and short (acronyms) or contain digits are left alone.
func (c *Checker) Misspelled(text string) []Span {
	rs := []rune(text)
	var out []Span
	for i := 0; i < len(rs); {
		if !isWordRune(rs[i]) {
			i++
			continue
		}
		start := i
		i = wordEnd(rs, i)
		word := string(rs[start:i])
		if i-start <= 1 || (i < len(rs) && unicode.IsDigit(rs[i])) || (start > 0 && unicode.IsDigit(rs[start-1])) {
			continue // a lone letter, or part of something like "3rd" or "H2O"
		}
		if isAcronym(word) {
			continue
		}
		if !c.Known(strings.ToLower(word)) {
			out = append(out, Span{start, i})
		}
	}
	return out
}

func isAcronym(w string) bool {
	if len([]rune(w)) > 6 {
		return false
	}
	for _, r := range w {
		if !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

// Suggest returns up to max corrections for a misspelled word: words one edit
// away (a missing, extra or wrong letter, or two letters swapped), or two edits
// if there are none, with the case of the original word kept ("Teh" gives
// "The"). They are ranked by how likely the typo is (swapped letters, a wrong
// letter, a missing letter, an extra one), then by how much of the word they
// keep, then alphabetically.
func (c *Checker) Suggest(word string, max int) []string {
	lower := strings.ToLower(word)
	type cand struct {
		w     string
		score int
	}
	found := map[string]int{}
	score := func(e string, kind int) int { return kind*100 + commonPrefix(lower, e) + commonSuffix(lower, e) }
	for _, e := range edits1(lower) {
		if c.Known(e.s) {
			if sc := score(e.s, e.kind); sc > found[e.s] {
				found[e.s] = sc
			}
		}
	}
	if len(found) == 0 {
		for _, e1 := range edits1(lower) {
			for _, e2 := range edits1(e1.s) {
				if c.Known(e2.s) {
					found[e2.s] = score(e2.s, 0)
				}
			}
		}
	}
	cs := make([]cand, 0, len(found))
	for w, s := range found {
		cs = append(cs, cand{w, s})
	}
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].score != cs[j].score {
			return cs[i].score > cs[j].score
		}
		return cs[i].w < cs[j].w
	})
	out := make([]string, 0, min(len(cs), max))
	for _, x := range cs {
		if len(out) == max {
			break
		}
		out = append(out, matchCase(word, x.w))
	}
	return out
}

func commonPrefix(a, b string) int {
	ar, br := []rune(a), []rune(b)
	n := 0
	for n < len(ar) && n < len(br) && ar[n] == br[n] {
		n++
	}
	return n
}

func commonSuffix(a, b string) int {
	ar, br := []rune(a), []rune(b)
	n := 0
	for n < len(ar) && n < len(br) && ar[len(ar)-1-n] == br[len(br)-1-n] {
		n++
	}
	return n
}

// matchCase gives w the capitalisation of orig: ALL CAPS, Capitalised or lower.
func matchCase(orig, w string) string {
	rs := []rune(orig)
	if len(rs) == 0 {
		return w
	}
	allUpper := len(rs) > 1
	for _, r := range rs {
		if !unicode.IsUpper(r) {
			allUpper = false
		}
	}
	switch {
	case allUpper:
		return strings.ToUpper(w)
	case unicode.IsUpper(rs[0]):
		wr := []rune(w)
		wr[0] = unicode.ToUpper(wr[0])
		return string(wr)
	}
	return w
}

// edit is a string one edit from a word, and which kind of edit made it.
type edit struct {
	s    string
	kind int // editSwap, editSub, editInsert or editDelete: how likely a typo it is
}

const (
	editDelete = iota // the word has an extra letter
	editInsert        // the word lacks a letter
	editSub           // the word has a wrong letter
	editSwap          // two neighbouring letters are swapped
)

// edits1 is every string one deletion, swap of neighbours, substitution or
// insertion away from word (Peter Norvig's spelling corrector).
func edits1(word string) []edit {
	const letters = "abcdefghijklmnopqrstuvwxyz'"
	rs := []rune(word)
	n := len(rs)
	out := make([]edit, 0, 54*n+26)
	for i := 0; i < n; i++ {
		out = append(out, edit{string(rs[:i]) + string(rs[i+1:]), editDelete})
	}
	for i := 0; i < n-1; i++ {
		sw := append([]rune(nil), rs...)
		sw[i], sw[i+1] = sw[i+1], sw[i]
		out = append(out, edit{string(sw), editSwap})
	}
	for i := 0; i < n; i++ {
		for _, l := range letters {
			if l != rs[i] {
				r := append([]rune(nil), rs...)
				r[i] = l
				out = append(out, edit{string(r), editSub})
			}
		}
	}
	for i := 0; i <= n; i++ {
		for _, l := range letters {
			out = append(out, edit{string(rs[:i]) + string(l) + string(rs[i:]), editInsert})
		}
	}
	return out
}

// MinCompletePrefix is how many letters a completion needs.
const MinCompletePrefix = 3

// WordBefore returns the word that ends at pos in text and where it starts; ok
// is false when the character before pos is not part of a word.
func WordBefore(text []rune, pos int) (start int, word string, ok bool) {
	if pos <= 0 || pos > len(text) || !isWordRune(text[pos-1]) {
		return 0, "", false
	}
	i := pos
	for i > 0 && (isWordRune(text[i-1]) || (text[i-1] == '\'' && i > 1 && isWordRune(text[i-2]))) {
		i--
	}
	return i, string(text[i:pos]), true
}

// Complete returns the alphabetically first known word that starts with prefix
// and is longer than it.
func (c *Checker) Complete(prefix string) (string, bool) {
	lower := strings.ToLower(prefix)
	best := ""
	consider := func(w string) {
		if len(w) > len(lower) && strings.HasPrefix(w, lower) && (best == "" || w < best) {
			best = w
		}
	}
	if c.Dict != nil {
		for w := range c.Dict.words {
			consider(w)
		}
	}
	for w := range c.Custom {
		consider(w)
	}
	return best, best != ""
}
