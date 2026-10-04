// Package share builds links to a piece of selected text in a document, and
// finds the text again when the link is opened.
//
// A link carries the selection as rune offsets and the start of the quoted
// text (?sel=120-184&q=The+quick+brown...). The offsets alone would point at
// the wrong words once the document has changed; the quote lets the opening
// side check them and, if they moved, look for the quote near where it was.
package share

import (
	"net/url"
	"strconv"
	"strings"
)

// QuoteRunes is how much of the selected text a link quotes.
const QuoteRunes = 80

// Selection is a range of a document's text, [From, To) in runes, with the
// text it held.
type Selection struct {
	From, To int
	// Quote is the start of the selected text (at most QuoteRunes).
	Quote string
}

// Of describes the range [from, to) of text. An empty or inverted range
// gives the zero Selection.
func Of(text string, from, to int) Selection {
	rs := []rune(text)
	from, to = max(from, 0), min(to, len(rs))
	if to <= from {
		return Selection{}
	}
	q := rs[from:to]
	if len(q) > QuoteRunes {
		q = q[:QuoteRunes]
	}
	return Selection{From: from, To: to, Quote: string(q)}
}

// Empty reports whether nothing is selected.
func (s Selection) Empty() bool { return s.To <= s.From }

// Query is the selection as URL parameters: sel and q.
func (s Selection) Query() url.Values {
	v := url.Values{}
	if !s.Empty() {
		v.Set("sel", strconv.Itoa(s.From)+"-"+strconv.Itoa(s.To))
		v.Set("q", s.Quote)
	}
	return v
}

// Parse reads a selection from URL parameters; ok is false when there is none
// or it is malformed.
func Parse(v url.Values) (Selection, bool) {
	a, b, found := strings.Cut(v.Get("sel"), "-")
	if !found {
		return Selection{}, false
	}
	from, err1 := strconv.Atoi(a)
	to, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || from < 0 || to <= from {
		return Selection{}, false
	}
	return Selection{From: from, To: to, Quote: v.Get("q")}, true
}

// Link is base with params and the selection added.
func Link(base string, params url.Values, s Selection) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, vs := range params {
		q[k] = vs
	}
	for k, vs := range s.Query() {
		q[k] = vs
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Resolve finds the selection in text, which may have changed since the link
// was made: at the same offsets if the quote is still there, otherwise at the
// occurrence of the quote nearest to them (keeping the length). ok is false
// when the quote cannot be found; a link without a quote trusts its offsets
// as far as the text reaches.
func (s Selection) Resolve(text string) (from, to int, ok bool) {
	rs := []rune(text)
	if s.Empty() || s.From >= len(rs) && s.Quote == "" {
		return 0, 0, false
	}
	n := s.To - s.From
	clamp := func(f int) (int, int, bool) { return f, min(f+n, len(rs)), true }
	q := []rune(s.Quote)
	if len(q) == 0 {
		return clamp(s.From)
	}
	at := func(i int) bool {
		return i >= 0 && i+len(q) <= len(rs) && string(rs[i:i+len(q)]) == s.Quote
	}
	if at(s.From) {
		return clamp(s.From)
	}
	best := -1
	for i := 0; i+len(q) <= len(rs); i++ {
		if at(i) && (best < 0 || abs(i-s.From) < abs(best-s.From)) {
			best = i
		}
	}
	if best < 0 {
		return 0, 0, false
	}
	return clamp(best)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
