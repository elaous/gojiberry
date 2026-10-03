package canvas

import (
	"sort"
	"strings"
)

// Auto-closing pairs and word completion for the TextFlow, like the
// FloatingText's, but working at every cursor (pairs) and at the primary
// one (the completion popup).
//
// Both are off until asked for: set AutoClose (DefaultAutoClose is the
// code-editor set) and Complete, Completer or Vocabulary.

func (f *TextFlow) isCloser(r rune) bool {
	for _, c := range f.AutoClose {
		if c == r {
			return true
		}
	}
	return false
}

// typePaired types one character at every cursor with auto-closing: it
// wraps a selection in a pair, overtypes a closer already ahead of the
// caret, or inserts a pair -- except where that would be annoying: a
// quote after a letter (an apostrophe) or an opener in front of a word.
func (f *TextFlow) typePaired(d *flowData, r rune) {
	closer, isOpener := f.AutoClose[r]
	rs := textRunes(f)
	var edits []Edit
	var out []Cursor
	shift := 0
	for _, c := range normalize(f.Cursors()) {
		from, to := c.Range()
		format := f.typeFormat(d, from)
		chars := func(s string) Item { return Chars{Text: s, Format: format} }
		var prev, next rune
		if from > 0 {
			prev = rs[from-1]
		}
		if to < len(rs) {
			next = rs[to]
		}
		var ins []Item
		cur := At(0)
		switch {
		case isOpener && from != to:
			ins = append([]Item{chars(string(r))}, f.Slice(from, to)...)
			ins = append(ins, chars(string(closer)))
			cur = Cursor{Anchor: from + shift + 1, Caret: to + shift + 1} // the wrapped text stays selected
		case from == to && f.isCloser(r) && next == r:
			out = append(out, At(from+shift+1)) // step over the closer
			continue
		case from == to && isOpener && (closer != r || (!isWordRune(prev) && prev != r)) && !isWordRune(next):
			ins = []Item{chars(string(r) + string(closer))}
			cur = At(from + shift + 1)
		default:
			ins = []Item{chars(string(r))}
			cur = At(from + shift + 1)
		}
		edits = append(edits, Edit{From: from, To: to, Items: ins, AfterOps: c.After && from == to})
		if from != to && isOpener {
			cur.Anchor, cur.Caret = from+shift+1, from+shift+1+(to-from)
		}
		out = append(out, cur)
		shift += itemsLen(ins) - (to - from)
	}
	if len(edits) > 0 {
		f.Replace(edits)
	}
	f.SetCursors(out)
}

// --- completion ---------------------------------------------------------

func (f *TextFlow) completionOn() bool {
	return f.Complete || f.Completer != nil || len(f.Vocabulary) > 0
}

func (f *TextFlow) wordBeforeCaret() (string, int) {
	rs := textRunes(f)
	caret := min(f.sel.caret, len(rs))
	start := caret
	for start > 0 && isIdentRune(rs[start-1]) {
		start--
	}
	return string(rs[start:caret]), start
}

// defaultComplete suggests the longer words in the text and Vocabulary
// that start with prefix (case-insensitively), most frequent first.
func (f *TextFlow) defaultComplete(prefix string) []string {
	lower := strings.ToLower(prefix)
	counts := map[string]int{}
	var word []rune
	flush := func() {
		if len(word) > len([]rune(prefix)) {
			if w := string(word); strings.HasPrefix(strings.ToLower(w), lower) {
				counts[w]++
			}
		}
		word = word[:0]
	}
	for _, r := range textRunes(f) {
		if isIdentRune(r) {
			word = append(word, r)
		} else {
			flush()
		}
	}
	flush()
	for _, w := range f.Vocabulary {
		if len([]rune(w)) > len([]rune(prefix)) && strings.HasPrefix(strings.ToLower(w), lower) {
			counts[w]++
		}
	}
	out := make([]string, 0, len(counts))
	for w := range counts {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool {
		if counts[out[i]] != counts[out[j]] {
			return counts[out[i]] > counts[out[j]]
		}
		return out[i] < out[j]
	})
	return out[:min(len(out), maxSuggestions)]
}

func (f *TextFlow) suggestMenu() *Menu {
	if f.sugg == nil {
		f.sugg = NewMenu(f.Painter)
		f.sugg.Style.MinWidth = 120
	}
	f.sugg.Painter = f.Painter
	return f.sugg
}

func (f *TextFlow) suggestionsOpen() bool { return f.sugg != nil && f.sugg.IsOpen() }

func (f *TextFlow) closeSuggestions() {
	if f.sugg != nil {
		f.sugg.Close()
	}
}

// updateSuggestions recomputes the completion popup for the word before
// the caret (one cursor, nothing selected).
func (f *TextFlow) updateSuggestions() {
	f.closeSuggestions()
	if !f.completionOn() || !f.Editing() || len(f.extra) > 0 || f.sel.caret != f.sel.anchor || f.Painter == nil {
		return
	}
	prefix, start := f.wordBeforeCaret()
	if len([]rune(prefix)) < minCompletePrefix {
		return
	}
	var words []string
	if f.Completer != nil {
		words = f.Completer.Complete(prefix)
	} else {
		words = f.defaultComplete(prefix)
	}
	if len(words) == 0 {
		return
	}
	x, y, h, ok := f.CaretRect(f.sel.caret)
	if !ok {
		return
	}
	items := make([]MenuItem, len(words))
	for i, w := range words {
		w := w
		items[i] = MenuItem{Label: w, Action: func() { f.acceptSuggestion(w) }}
	}
	f.suggFrom = start
	m := f.suggestMenu()
	m.OpenWithin(items, x, y+h+2, f.viewport)
	m.SetHover(0)
}

// acceptSuggestion replaces the word being typed with the completion.
func (f *TextFlow) acceptSuggestion(word string) {
	f.materialize()
	d := f.flatten()
	format := f.typeFormat(d, f.suggFrom)
	caret := f.sel.caret
	f.Replace([]Edit{{From: f.suggFrom, To: caret, Items: []Item{Chars{Text: word, Format: format}}}})
	f.SetCursors([]Cursor{At(f.suggFrom + len([]rune(word)))})
}

// Suggestions returns the completions currently offered.
func (f *TextFlow) Suggestions() []string {
	if !f.suggestionsOpen() {
		return nil
	}
	return f.sugg.Labels()
}

// SuggestionMenu returns the completion popup's menu.
func (f *TextFlow) SuggestionMenu() *Menu { return f.suggestMenu() }

// SetViewport implements ViewportSetter: the popup stays inside the
// visible part.
func (f *TextFlow) SetViewport(x, y, w, h float64) { f.viewport = [4]float64{x, y, w, h} }

// suggestKey drives an open completion popup: Up/Down choose, Tab or
// Enter accept, Escape dismisses. It reports whether it used the key.
func (f *TextFlow) suggestKey(key string) bool {
	if !f.suggestionsOpen() {
		return false
	}
	m := f.sugg
	n := len(m.items)
	switch key {
	case "ArrowDown":
		m.SetHover((m.Hover() + 1) % n)
	case "ArrowUp":
		m.SetHover((m.Hover() - 1 + n) % n)
	case "Tab", "Enter":
		m.Activate(m.Hover())
	case "Escape":
		f.closeSuggestions()
	default:
		return false
	}
	return true
}

var _ ViewportSetter = (*TextFlow)(nil)
