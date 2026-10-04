// Package hyphen finds the points where a word may be broken across two lines,
// with Liang's algorithm (the one TeX uses) over TeX hyphenation patterns.
// English (hyph-en-us, embedded; see NOTICE) is built in; other languages load
// their patterns with FromPatterns. A *Hyphenator is a canvas.Hyphenator:
//
//	flow.Hyphenator = hyphen.English()
package hyphen

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"io"
	"sync"

	"github.com/speedata/hyphenation"
)

//go:embed hyph-en-us.txt.gz
var enPatternsGz []byte

// Hyphenator breaks words by a set of patterns.
type Hyphenator struct{ lang *hyphenation.Lang }

var (
	englishOnce sync.Once
	english     *Hyphenator
)

// English returns the hyphenator for American English, built on first use.
func English() *Hyphenator {
	englishOnce.Do(func() {
		zr, err := gzip.NewReader(bytes.NewReader(enPatternsGz))
		if err == nil {
			defer func() { _ = zr.Close() }()
			if h, err := FromPatterns(zr); err == nil {
				english = h
				return
			}
		}
		english = &Hyphenator{} // no patterns: nothing is ever broken
	})
	return english
}

// FromPatterns reads TeX hyphenation patterns (the .pat.txt form: one pattern a
// line, with a line for exceptions after "\hyphenation" optional). Two letters
// are always kept together at each end of a word.
func FromPatterns(r io.Reader) (*Hyphenator, error) {
	l, err := hyphenation.New(bufio.NewReader(r))
	if err != nil {
		return nil, err
	}
	l.Leftmin, l.Rightmin = 2, 2
	return &Hyphenator{lang: l}, nil
}

// Positions returns the rune offsets in word where a hyphen may go, ascending:
// an offset k means word[:k] may end a line and word[k:] continue on the next.
func (h *Hyphenator) Positions(word string) []int {
	if h == nil || h.lang == nil {
		return nil
	}
	return h.lang.Hyphenate(word)
}
