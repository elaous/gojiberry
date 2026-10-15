package canvas

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

// FindOptions says what to look for.
type FindOptions struct {
	Query string
	// MatchCase makes the search case sensitive.
	MatchCase bool
	// WholeWord only matches whole words.
	WholeWord bool
	// Regex reads Query as a regular expression (Go syntax). In a
	// replacement, $1 and ${name} stand for what a group matched.
	Regex bool
}

// Match is a range [From, To) of the text stream, in runes.
type Match struct{ From, To int }

// ErrEmptyQuery is returned by Find and Replace for an empty query.
var ErrEmptyQuery = errors.New("canvas: nothing to look for")

func (o FindOptions) compile() (*regexp.Regexp, error) {
	if o.Query == "" {
		return nil, ErrEmptyQuery
	}
	expr := o.Query
	if !o.Regex {
		expr = regexp.QuoteMeta(expr)
	}
	if !o.MatchCase {
		expr = "(?i)" + expr
	}
	return regexp.Compile(expr)
}

// FindAll returns the matches of o in text, in order, as rune offsets. Empty
// matches (a regex like "x*") are skipped. With WholeWord a match must not be
// glued to a word character on a side where it starts or ends with one.
func FindAll(text string, o FindOptions) ([]Match, error) {
	re, err := o.compile()
	if err != nil {
		return nil, err
	}
	return findAll(re, text, o.WholeWord), nil
}

func findAll(re *regexp.Regexp, text string, whole bool) []Match {
	var out []Match
	toRune := runeIndex(text)
	for _, loc := range re.FindAllStringIndex(text, -1) {
		s, e := loc[0], loc[1]
		if e == s {
			continue
		}
		if whole {
			first, _ := utf8.DecodeRuneInString(text[s:e])
			last, _ := utf8.DecodeLastRuneInString(text[s:e])
			if before, _ := utf8.DecodeLastRuneInString(text[:s]); isWordRune(first) && s > 0 && isWordRune(before) {
				continue
			}
			if after, _ := utf8.DecodeRuneInString(text[e:]); isWordRune(last) && e < len(text) && isWordRune(after) {
				continue
			}
		}
		out = append(out, Match{From: toRune(s), To: toRune(e)})
	}
	return out
}

// runeIndex returns a function mapping byte offsets in s (at the start of a
// rune) to rune offsets.
func runeIndex(s string) func(int) int {
	if len(s) == utf8.RuneCountInString(s) {
		return func(b int) int { return b }
	}
	idx := make([]int, len(s)+1)
	n := 0
	for i := range s {
		idx[i] = n
		n++
	}
	idx[len(s)] = n
	return func(b int) int { return idx[b] }
}

// Find returns the matches of o in the flow's text. A match that crosses an
// inline object (an image, a table of contents) is left out.
func (f *TextFlow) Find(o FindOptions) ([]Match, error) {
	text := f.EditText()
	ms, err := FindAll(text, o)
	if err != nil {
		return nil, err
	}
	rs := []rune(text)
	out := ms[:0]
	for _, m := range ms {
		if m.To <= len(rs) && !strings.ContainsRune(string(rs[m.From:m.To]), objectRune) {
			out = append(out, m)
		}
	}
	return out, nil
}

// ReplaceMatches changes the matches (as Find returned them, still valid for the
// current text) to repl, giving each the formatting of its first character,
// and returns how many were changed. With o.Regex, repl may use $1 and
// ${name}. It is one undoable change.
func (f *TextFlow) ReplaceMatches(matches []Match, o FindOptions, repl string) (int, error) {
	re, err := o.compile()
	if err != nil {
		return 0, err
	}
	text := f.EditText()
	rs := []rune(text)
	d := f.flatten()
	// The matches' groups are found in the whole text, not in the matched text
	// alone, so a pattern that looks around (\b, \B, ^) means what it did in Find.
	var all map[[2]int][]int // the groups of the match at bytes [from, to)
	var byteAt []int         // rune offset -> byte offset
	if o.Regex {
		all = map[[2]int][]int{}
		for _, loc := range re.FindAllStringSubmatchIndex(text, -1) {
			all[[2]int{loc[0], loc[1]}] = loc
		}
		byteAt = make([]int, 0, len(rs)+1)
		for i := range text {
			byteAt = append(byteAt, i)
		}
		byteAt = append(byteAt, len(text))
	}
	var edits []Edit
	for _, m := range matches {
		if m.From < 0 || m.To > len(rs) || m.To <= m.From {
			continue
		}
		with := repl
		if o.Regex {
			with = expandReplacement(re, text, all, byteAt[m.From], byteAt[m.To], repl)
		}
		var fm Format
		if m.From < len(d.rs) {
			fm = d.formats[d.rs[m.From]]
		}
		var items []Item
		if with != "" {
			items = []Item{Chars{Text: with, Format: fm}}
		}
		edits = append(edits, Edit{From: m.From, To: m.To, Items: items})
	}
	if len(edits) == 0 {
		return 0, nil
	}
	f.Replace(edits)
	return len(edits), nil
}

// expandReplacement applies a regex replacement template to the match of the
// bytes [from, to) of text, whose groups are in all. A match that is not there
// (the text changed since Find) is expanded from its own text.
func expandReplacement(re *regexp.Regexp, text string, all map[[2]int][]int, from, to int, repl string) string {
	if loc, ok := all[[2]int{from, to}]; ok {
		return string(re.ExpandString(nil, repl, text, loc))
	}
	matched := text[from:to]
	if loc := re.FindStringSubmatchIndex(matched); loc != nil {
		return string(re.ExpandString(nil, repl, matched, loc))
	}
	return repl
}

// ReplaceRange puts text in place of the range [from, to) of the text stream,
// in the formatting of its first character (an empty range inserts).
func (f *TextFlow) ReplaceRange(from, to int, text string) {
	d := f.flatten()
	var fm Format
	if from < len(d.rs) {
		fm = d.formats[d.rs[from]]
	}
	var items []Item
	if text != "" {
		items = []Item{Chars{Text: text, Format: fm}}
	}
	f.Replace([]Edit{{From: from, To: to, Items: items}})
}

// ReplaceAll finds every match of o and replaces it with repl; it returns how
// many it changed.
func (f *TextFlow) ReplaceAll(o FindOptions, repl string) (int, error) {
	ms, err := f.Find(o)
	if err != nil {
		return 0, err
	}
	return f.ReplaceMatches(ms, o, repl)
}

// --- highlighting ---------------------------------------------------------

type searchState struct {
	matches []Match
	cur     int
}

// SetSearch highlights matches on the canvas, the one at index current in the
// stronger colour (pass -1 for none). It draws on the next Draw.
func (f *TextFlow) SetSearch(matches []Match, current int) {
	f.search = searchState{matches: append([]Match(nil), matches...), cur: current}
}

// ClearSearch removes the highlights.
func (f *TextFlow) ClearSearch() { f.search = searchState{} }

// Search returns the matches being highlighted and the current one.
func (f *TextFlow) Search() ([]Match, int) { return f.search.matches, f.search.cur }

// GotoMatch selects match m and scrolls it into view.
func (f *TextFlow) GotoMatch(m Match) {
	f.GotoOffset(m.From)
	f.SetSelection(m.From, m.To)
}

func (f *TextFlow) drawSearch(lay Layout) {
	if len(f.search.matches) == 0 {
		return
	}
	p := f.Painter
	for i, m := range f.search.matches {
		color := f.ChromeStyle.FindColor
		if i == f.search.cur {
			color = f.ChromeStyle.FindCurrentColor
		}
		if color == "" {
			continue
		}
		p.SetFillStyle(color)
		for _, ln := range lay.Lines {
			s, e := max(m.From, ln.Start), min(m.To, ln.End)
			for _, r := range f.lineRects(ln, s, e) {
				p.FillRect(r[0], ln.Y, r[1]-r[0], ln.H)
			}
		}
	}
}
