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
	// Regex reads Query as a regular expression (Go syntax).
	Regex bool
}

type Match struct{ From, To int }

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

// Find returns the matches of o in the flow's text.
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

func (f *TextFlow) ReplaceMatches(matches []Match, o FindOptions, repl string) (int, error) {
	re, err := o.compile()
	if err != nil {
		return 0, err
	}
	text := f.EditText()
	rs := []rune(text)
	d := f.flatten()
	// groups found in the whole text so lookarounds keep their meaning
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

func (f *TextFlow) ReplaceAll(o FindOptions, repl string) (int, error) {
	ms, err := f.Find(o)
	if err != nil {
		return 0, err
	}
	return f.ReplaceMatches(ms, o, repl)
}

type searchState struct {
	matches []Match
	cur     int
}

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
