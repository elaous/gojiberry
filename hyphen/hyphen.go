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

func FromPatterns(r io.Reader) (*Hyphenator, error) {
	l, err := hyphenation.New(bufio.NewReader(r))
	if err != nil {
		return nil, err
	}
	l.Leftmin, l.Rightmin = 2, 2
	return &Hyphenator{lang: l}, nil
}

func (h *Hyphenator) Positions(word string) []int {
	if h == nil || h.lang == nil {
		return nil
	}
	return h.lang.Hyphenate(word)
}
