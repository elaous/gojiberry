package canvas

func (f *TextFlow) isCloser(r rune) bool {
	for _, c := range f.AutoClose {
		if c == r {
			return true
		}
	}
	return false
}

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

func (f *TextFlow) defaultComplete(prefix string) []string {
	return completeWords(string(textRunes(f)), f.Vocabulary, prefix)
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

func (f *TextFlow) acceptSuggestion(word string) {
	f.materialize()
	d := f.flatten()
	format := f.typeFormat(d, f.suggFrom)
	caret := f.sel.caret
	items := []Item{Chars{Text: word, Format: format}}
	if f.proposalsOn() {
		// while suggesting, completion is a proposal: typed letters struck, word follows
		repl, at := f.proposalRewrite(d, Cursor{Anchor: caret, Caret: caret}, f.suggFrom, caret, items)
		f.Replace([]Edit{{From: f.suggFrom, To: caret, Items: repl}})
		f.SetCursors([]Cursor{At(f.suggFrom + at)})
		return
	}
	f.Replace([]Edit{{From: f.suggFrom, To: caret, Items: items}})
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

// SetViewport keeps the popup inside the visible part.
func (f *TextFlow) SetViewport(x, y, w, h float64) { f.viewport = [4]float64{x, y, w, h} }

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
