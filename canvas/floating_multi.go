package canvas

import (
	"slices"
	"strings"
)

// FloatingText is an EditTarget that edits at several cursors.

var _ Editable = (*FloatingText)(nil)

// EditTarget returns the box while it is being edited, else nil.
func (ft *FloatingText) EditTarget() EditTarget {
	if ft.mode != ModeEdit || !ft.editing {
		return nil
	}
	return ft
}

func (ft *FloatingText) EditText() string { return ft.Text() }

func (ft *FloatingText) Cursors() []Cursor {
	return append([]Cursor{{Anchor: ft.anchor, Caret: ft.caret}}, ft.extra...)
}

func (ft *FloatingText) SetCursors(cs []Cursor) {
	if len(cs) == 0 {
		return
	}
	clamp := func(o int) int { return min(max(o, 0), len(ft.runes)) }
	ft.anchor, ft.caret = clamp(cs[0].Anchor), clamp(cs[0].Caret)
	ft.extra = nil
	for _, c := range cs[1:] {
		ft.extra = append(ft.extra, Cursor{Anchor: clamp(c.Anchor), Caret: clamp(c.Caret)})
	}
	ft.pending = Format{}
	ft.caretOn = true
}

func (ft *FloatingText) Slice(from, to int) []Item {
	from, to = min(max(from, 0), len(ft.runes)), min(max(to, 0), len(ft.runes))
	var items []Item
	for i := from; i < to; {
		j := i
		var b strings.Builder
		for j < to && ft.runes[j].f == ft.runes[i].f {
			b.WriteRune(ft.runes[j].r)
			j++
		}
		items = append(items, Chars{Text: b.String(), Format: ft.runes[i].f})
		i = j
	}
	return items
}

func itemRunes(items []Item) []fRune {
	var out []fRune
	for _, it := range items {
		if c, ok := it.(Chars); ok {
			for _, r := range strings.ReplaceAll(c.Text, "\r", "") {
				out = append(out, fRune{r, c.Format})
			}
		}
	}
	return out
}

func (ft *FloatingText) Replace(edits []Edit) {
	es := slices.Clone(edits)
	slices.SortFunc(es, func(a, b Edit) int { return b.From - a.From }) // last first
	for _, e := range es {
		from, to := min(max(e.From, 0), len(ft.runes)), min(max(e.To, 0), len(ft.runes))
		ft.runes = slices.Replace(ft.runes, from, max(to, from), itemRunes(e.Items)...)
	}
	n := len(ft.runes)
	ft.anchor, ft.caret = min(ft.anchor, n), min(ft.caret, n)
	for i := range ft.extra {
		ft.extra[i] = Cursor{Anchor: min(ft.extra[i].Anchor, n), Caret: min(ft.extra[i].Caret, n)}
	}
	ft.changed()
}

// formatBefore is the format new text typed at o takes.
func (ft *FloatingText) formatBefore(o int) Format {
	switch {
	case o > 0 && o <= len(ft.runes):
		return ft.runes[o-1].f
	case len(ft.runes) > 0:
		return ft.runes[0].f
	}
	return Format{}
}

// editAll applies gen at every cursor, in document order.
func (ft *FloatingText) editAll(gen func(c Cursor) (from, to int, ins []fRune)) {
	cs := normalize(ft.Cursors())
	type ed struct {
		from, to int
		ins      []fRune
	}
	eds := make([]ed, len(cs))
	for i, c := range cs {
		f, t, ins := gen(c)
		eds[i] = ed{f, t, ins}
	}
	for i := len(eds) - 1; i >= 0; i-- {
		e := eds[i]
		ft.runes = slices.Replace(ft.runes, e.from, e.to, e.ins...)
	}
	out := make([]Cursor, len(eds))
	shift := 0
	for i, e := range eds {
		out[i] = At(e.from + shift + len(e.ins))
		shift += len(e.ins) - (e.to - e.from)
	}
	ft.SetCursors(out)
	ft.changed()
}

// multiInsert types s at every cursor, replacing selections.
func (ft *FloatingText) multiInsert(s string) {
	s = strings.ReplaceAll(s, "\r", "")
	ft.editAll(func(c Cursor) (int, int, []fRune) {
		from, to := c.Range()
		f := ft.formatBefore(from)
		var ins []fRune
		for _, r := range s {
			ins = append(ins, fRune{r, f})
		}
		return from, to, ins
	})
	ft.closeSuggestions()
}

// multiPaste pastes one line per cursor when counts match.
func (ft *FloatingText) multiPaste(text string) {
	text = strings.ReplaceAll(text, "\r", "")
	cs := normalize(ft.Cursors())
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != len(cs) {
		ft.multiInsert(text)
		return
	}
	i := 0
	ft.editAll(func(c Cursor) (int, int, []fRune) {
		from, to := c.Range()
		f := ft.formatBefore(from)
		var ins []fRune
		for _, r := range lines[i] {
			ins = append(ins, fRune{r, f})
		}
		i++
		return from, to, ins
	})
	ft.closeSuggestions()
}

// multiSelectedText joins every cursor's selection with newlines.
func (ft *FloatingText) multiSelectedText() (string, bool) {
	var parts []string
	for _, c := range normalize(ft.Cursors()) {
		from, to := c.Range()
		if from == to {
			continue
		}
		var b strings.Builder
		for _, fr := range ft.runes[from:to] {
			b.WriteRune(fr.r)
		}
		parts = append(parts, b.String())
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}

// multiKey is handleEditKey with several cursors.
func (ft *FloatingText) multiKey(key string, shift bool) bool {
	move := func(to func(c Cursor) int) {
		cs := ft.Cursors()
		for i, c := range cs {
			p := min(max(to(c), 0), len(ft.runes))
			cs[i].Caret = p
			if !shift {
				cs[i].Anchor = p
			}
		}
		ft.SetCursors(normalize(cs))
	}
	switch key {
	case "ArrowLeft":
		move(func(c Cursor) int {
			if f, _ := c.Range(); !c.Empty() && !shift {
				return f
			}
			return c.Caret - 1
		})
	case "ArrowRight":
		move(func(c Cursor) int {
			if _, t := c.Range(); !c.Empty() && !shift {
				return t
			}
			return c.Caret + 1
		})
	case "ArrowUp", "ArrowDown":
		move(func(c Cursor) int {
			x, y, h, ok := ft.inner.CaretRect(c.Caret)
			if !ok {
				return c.Caret
			}
			if key == "ArrowUp" {
				return ft.inner.OffsetAt(x, y-1)
			}
			return ft.inner.OffsetAt(x, y+h+1)
		})
	case "Home":
		move(func(c Cursor) int {
			if ln, ok := ft.lineOf(c.Caret); ok {
				return ln.Start
			}
			return c.Caret
		})
	case "End":
		move(func(c Cursor) int {
			if ln, ok := ft.lineOf(c.Caret); ok {
				return ln.End
			}
			return c.Caret
		})
	case "Backspace", "Delete":
		ft.editAll(func(c Cursor) (int, int, []fRune) {
			from, to := c.Range()
			switch {
			case from != to:
			case key == "Backspace" && from > 0:
				from--
			case key == "Delete" && to < len(ft.runes):
				to++
			}
			return from, to, nil
		})
	case "Enter":
		ft.multiInsert("\n")
	case "Escape":
		ft.SetCursors(ft.Cursors()[:1])
	default:
		return false
	}
	ft.caretOn = true
	return true
}

// MouseDownMods: Ctrl/Cmd+click adds a cursor.
func (ft *FloatingText) MouseDownMods(x, y float64, m Mods) bool {
	if (m.Ctrl || m.Meta) && ft.mode == ModeEdit && ft.editing && ft.whatAt(x, y) == "text" {
		ft.sync()
		o := ft.inner.OffsetAt(x, y)
		ft.SetCursors(normalize(append(ft.Cursors(), At(o))))
		return true
	}
	return ft.MouseDown(x, y)
}
