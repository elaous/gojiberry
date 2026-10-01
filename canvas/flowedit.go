package canvas

import (
	"slices"
	"strings"
)

// A TextFlow is editable in edit mode: click in the text to put the
// caret there, then type. The arrow keys, Home, End, Backspace, Delete
// and Enter work as in an editor (Shift extends the selection), text can
// be cut, copied and pasted, and the formatting commands (bold, italic,
// ... and the color palette's SetTextColor) apply to the selection or to
// the text typed next. Page and column breaks and column configs are
// part of the content: Delete in front of one removes it.
//
// The first edit turns the content into Items (a flow whose content came
// from Source and a Transform, or from Text, stops using them: the
// Markdown tables, images and so on become items the flow owns). Read the
// result with ContentItems; OnChange is called after every edit. The
// registered Features (multi-cursor, line operations, Markdown clipboard)
// work on a flow through its EditTarget.
//
// Known limits: typing at the offset where a break sits goes in front of
// the break; auto-closing brackets and completion are FloatingText's.

// ContentItems returns the content stream now: the Items (or what the
// Transform made of Source).
func (f *TextFlow) ContentItems() []Item { return slices.Clone(f.items()) }

// ResetContent forgets edits, so Source, Text and Items are the content
// again; call it after setting them.
func (f *TextFlow) ResetContent() {
	f.edited, f.derivedOK = false, false
	f.sel, f.extra = flowSel{}, nil
	f.Items = nil
}

// Editing reports whether the caret is in the text.
func (f *TextFlow) Editing() bool { return f.mode == ModeEdit && f.editing }

func (f *TextFlow) stopEditing() { f.editing, f.extra, f.pending = false, nil, Format{} }

// materialize makes Items the content, so it can be edited.
func (f *TextFlow) materialize() {
	if f.edited {
		return
	}
	f.Items = slices.Clone(f.items())
	f.Text, f.edited = "", true
}

func (f *TextFlow) changed() {
	f.derivedOK = false
	if f.OnChange != nil {
		f.OnChange(f)
	}
}

// --- EditTarget ---------------------------------------------------------

// EditTarget implements Editable: the flow itself while the caret is in
// it, else nil.
func (f *TextFlow) EditTarget() EditTarget {
	if f.Editing() {
		return f
	}
	return nil
}

// HoldsObjects implements ObjectHolder.
func (f *TextFlow) HoldsObjects() bool { return true }

// EditText implements EditTarget: the text stream, each inline object a
// placeholder rune.
func (f *TextFlow) EditText() string { return string(f.flatten().runes) }

// Cursors implements EditTarget: the primary cursor first.
func (f *TextFlow) Cursors() []Cursor {
	return append([]Cursor{{Anchor: f.sel.anchor, Caret: f.sel.caret, After: f.sel.after}}, f.extra...)
}

// SetCursors implements EditTarget.
func (f *TextFlow) SetCursors(cs []Cursor) {
	if len(cs) == 0 {
		return
	}
	n := len(f.flatten().runes)
	clamp := func(o int) int { return min(max(o, 0), n) }
	f.sel.anchor, f.sel.caret, f.sel.after = clamp(cs[0].Anchor), clamp(cs[0].Caret), cs[0].After
	f.extra = nil
	for _, c := range cs[1:] {
		f.extra = append(f.extra, Cursor{Anchor: clamp(c.Anchor), Caret: clamp(c.Caret), After: c.After})
	}
	f.pending, f.caretOff = Format{}, false
}

// Slice implements EditTarget: the runes [from, to) with their
// formatting, inline objects as Inline items.
func (f *TextFlow) Slice(from, to int) []Item {
	d := f.flatten()
	from, to = min(max(from, 0), len(d.runes)), min(max(to, 0), len(d.runes))
	var items []Item
	for i := from; i < to; {
		if o, ok := d.objs[i]; ok {
			items = append(items, o)
			i++
			continue
		}
		j := i
		var b strings.Builder
		for j < to && d.rs[j] == d.rs[i] && !d.isObj(j) {
			b.WriteRune(d.runes[j])
			j++
		}
		items = append(items, Chars{Text: b.String(), Format: d.formats[d.rs[i]]})
		i = j
	}
	return items
}

// Replace implements EditTarget.
func (f *TextFlow) Replace(edits []Edit) {
	f.materialize()
	es := slices.Clone(edits)
	slices.SortFunc(es, func(a, b Edit) int { return b.From - a.From }) // last first
	for _, e := range es {
		f.replaceRange(e.From, e.To, e.Items, e.AfterOps)
	}
	f.clampCursors()
	f.changed()
}

func (f *TextFlow) clampCursors() {
	n := len(f.flatten().runes)
	f.sel.anchor, f.sel.caret = min(f.sel.anchor, n), min(f.sel.caret, n)
	for i, c := range f.extra {
		f.extra[i] = Cursor{Anchor: min(c.Anchor, n), Caret: min(c.Caret, n), After: c.After}
	}
}

// replaceRange swaps the stream range [from, to) of f.Items for ins.
// Breaks and column configs strictly inside the range go with it; ones
// at its ends stay. ins goes in front of any break at offset from, or
// behind them with after.
func (f *TextFlow) replaceRange(from, to int, ins []Item, after bool) {
	n := len(f.flatten().runes)
	from, to = min(max(from, 0), n), min(max(to, 0), n)
	to = max(to, from)

	var out []Item
	inserted := false
	insert := func() {
		if !inserted {
			out = append(out, ins...)
			inserted = true
		}
	}
	chars := func(rs []rune, fm Format) {
		if len(rs) > 0 {
			out = append(out, Chars{Text: string(rs), Format: fm})
		}
	}
	off := 0
	for _, it := range f.Items {
		_, isChars := it.(Chars)
		_, isInline := it.(Inline)
		if !inserted && (off > from || (off == from && (!after || isChars || isInline))) {
			insert()
		}
		switch v := it.(type) {
		case Chars:
			rs := []rune(v.Text)
			a, b := off, off+len(rs)
			off = b
			if from >= b { // wholly in front of the range
				chars(rs, v.Format)
				continue
			}
			if from > a { // the insertion point is inside this run
				chars(rs[:from-a], v.Format)
			}
			insert()
			if to < b { // what is left after the range
				chars(rs[max(to, a)-a:], v.Format)
			}
		case Inline:
			if off < from || off >= to {
				out = append(out, v)
			}
			off++
		default: // break or column config at offset off
			if off <= from || off >= to {
				out = append(out, it)
			}
		}
	}
	insert()
	f.Items = mergeChars(out)
}

// mergeChars joins neighbouring Chars with the same Format and drops
// empty ones.
func mergeChars(items []Item) []Item {
	var out []Item
	for _, it := range items {
		c, ok := it.(Chars)
		if ok && c.Text == "" {
			continue
		}
		if ok && len(out) > 0 {
			if p, pok := out[len(out)-1].(Chars); pok && p.Format == c.Format {
				out[len(out)-1] = Chars{Text: p.Text + c.Text, Format: c.Format}
				continue
			}
		}
		out = append(out, it)
	}
	return out
}

// opAt returns the index in f.Items of a break or column config sitting
// at stream offset o, or -1.
func (f *TextFlow) opAt(o int) int {
	off := 0
	for i, it := range f.Items {
		switch v := it.(type) {
		case Chars:
			off += len([]rune(v.Text))
		case Inline:
			off++
		default:
			if off == o {
				return i
			}
		}
	}
	return -1
}

// --- typing -----------------------------------------------------------

// typeFormat is the format text typed at offset o takes: the pending one
// if set, else that of the character before (or after, at the start).
func (f *TextFlow) typeFormat(d *flowData, o int) Format {
	if !f.pending.IsZero() {
		return f.pending
	}
	if i := o - 1; i >= 0 && i < len(d.runes) && !d.isObj(i) {
		return d.formats[d.rs[i]]
	}
	if i := o; i >= 0 && i < len(d.runes) && !d.isObj(i) {
		return d.formats[d.rs[i]]
	}
	return Format{}
}

// InsertText types s at every cursor, replacing selections (while the
// caret is in the text).
func (f *TextFlow) InsertText(s string) bool {
	s = strings.ReplaceAll(s, "\r", "")
	if !f.Editing() || s == "" {
		return false
	}
	f.materialize()
	d := f.flatten()
	editCursors(f, func(c Cursor, _ []rune) (int, int, []Item) {
		from, to := c.Range()
		return from, to, []Item{Chars{Text: s, Format: f.typeFormat(d, from)}}
	})
	f.pending, f.caretOff = Format{}, false
	return true
}

// PasteText inserts clipboard text as it is: one line per cursor when the
// counts match, else all of it at each.
func (f *TextFlow) PasteText(text string) bool {
	text = strings.ReplaceAll(text, "\r", "")
	if !f.Editing() || text == "" {
		return false
	}
	cs := normalize(f.Cursors())
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(cs) == 1 || len(lines) != len(cs) {
		return f.InsertText(text)
	}
	f.materialize()
	d := f.flatten()
	i := 0
	editCursors(f, func(c Cursor, _ []rune) (int, int, []Item) {
		from, to := c.Range()
		line := lines[i]
		i++
		return from, to, []Item{Chars{Text: line, Format: f.typeFormat(d, from)}}
	})
	return true
}

// CopyText implements Copier: the selected text (every cursor's, one per
// line).
func (f *TextFlow) CopyText() (string, bool) {
	if len(f.extra) == 0 {
		if !f.HasSelection() {
			return "", false
		}
		return f.SelectedText(), true
	}
	rs := f.flatten().runes
	var parts []string
	for _, c := range normalize(f.Cursors()) {
		from, to := c.Range()
		if from < to {
			parts = append(parts, strings.ReplaceAll(string(rs[from:min(to, len(rs))]), string(objectRune), ""))
		}
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}

// CutText implements Cutter.
func (f *TextFlow) CutText() (string, bool) {
	if !f.Editing() {
		return "", false
	}
	text, ok := f.CopyText()
	if !ok {
		return "", false
	}
	editCursors(f, func(c Cursor, _ []rune) (int, int, []Item) {
		from, to := c.Range()
		return from, to, nil
	})
	return text, true
}

// --- keys -------------------------------------------------------------

// HandleKey implements KeyHandler: the editing keys while the caret is in
// the text; Escape otherwise clears the selection.
func (f *TextFlow) HandleKey(key string, shift bool) bool {
	if f.Editing() && f.editKey(key, shift) {
		return true
	}
	if key == "Escape" && f.HasSelection() {
		f.ClearSelection()
		return true
	}
	return false
}

func (f *TextFlow) lineAt(o int) (Line, bool) {
	lines := f.Layout().Lines
	for _, ln := range lines {
		if o <= ln.End {
			return ln, true
		}
	}
	if len(lines) > 0 {
		return lines[len(lines)-1], true
	}
	return Line{}, false
}

func (f *TextFlow) editKey(key string, shift bool) bool {
	move := func(to func(c Cursor) int) {
		n := len(f.flatten().runes)
		cs := f.Cursors()
		for i, c := range cs {
			p := min(max(to(c), 0), n)
			cs[i].Caret, cs[i].After = p, false
			if !shift {
				cs[i].Anchor = p
			}
		}
		f.SetCursors(normalize(cs))
	}
	switch key {
	case "ArrowLeft":
		move(func(c Cursor) int {
			if from, _ := c.Range(); !c.Empty() && !shift {
				return from
			}
			return c.Caret - 1
		})
	case "ArrowRight":
		move(func(c Cursor) int {
			if _, to := c.Range(); !c.Empty() && !shift {
				return to
			}
			return c.Caret + 1
		})
	case "ArrowUp", "ArrowDown":
		move(func(c Cursor) int {
			x, y, h, ok := f.CaretRect(c.Caret)
			if !ok {
				return c.Caret
			}
			if key == "ArrowUp" {
				return f.OffsetAt(x, y-1)
			}
			return f.OffsetAt(x, y+h+1)
		})
	case "Home":
		move(func(c Cursor) int {
			if ln, ok := f.lineAt(c.Caret); ok {
				return ln.Start
			}
			return c.Caret
		})
	case "End":
		move(func(c Cursor) int {
			if ln, ok := f.lineAt(c.Caret); ok {
				return ln.End
			}
			return c.Caret
		})
	case "Backspace", "Delete":
		f.materialize()
		// Delete removes a break in front of the caret; Backspace one
		// behind it (a caret placed after a break).
		if len(f.extra) == 0 && !f.HasSelection() && (key == "Delete" || f.sel.after) {
			if i := f.opAt(f.sel.caret); i >= 0 {
				f.sel.after = false
				f.Items = mergeChars(slices.Delete(slices.Clone(f.Items), i, i+1))
				f.changed()
				break
			}
		}
		n := len(f.flatten().runes)
		editCursors(f, func(c Cursor, _ []rune) (int, int, []Item) {
			from, to := c.Range()
			switch {
			case from != to:
			case key == "Backspace" && from > 0:
				from--
			case key == "Delete" && to < n:
				to++
			}
			return from, to, nil
		})
	case "Enter":
		return f.InsertText("\n")
	case "Escape":
		switch {
		case len(f.extra) > 0:
			f.SetCursors(f.Cursors()[:1])
		case f.HasSelection():
			f.sel.anchor = f.sel.caret
		default:
			f.stopEditing()
		}
	default:
		return false
	}
	f.pending, f.caretOff = Format{}, false
	return true
}

// SetCaretVisible implements Blinker.
func (f *TextFlow) SetCaretVisible(on bool) bool {
	f.caretOff = !on
	return f.Editing()
}

// WantsFocus implements Focuser: a selection needs the keyboard so Copy
// (and Select all) reach it, and so does the caret.
func (f *TextFlow) WantsFocus() bool { return f.HasSelection() || f.Editing() }

// MouseDownMods implements ModMouseDowner: Ctrl/Cmd+click in the text
// adds a cursor.
func (f *TextFlow) MouseDownMods(x, y float64, m Mods) bool {
	if (m.Ctrl || m.Meta) && f.Editing() && f.Painter != nil && !f.obstructed(x, y) && f.inText(x, y) {
		o, after := f.offsetAt(x, y)
		f.SetCursors(normalize(append(f.Cursors(), Cursor{Anchor: o, Caret: o, After: after})))
		return true
	}
	return f.MouseDown(x, y)
}

// drawCarets draws a caret at every cursor (while editing).
func (f *TextFlow) drawCarets() {
	if !f.Editing() || f.caretOff || f.Painter == nil {
		return
	}
	for _, c := range f.Cursors() {
		if x, y, h, ok := f.caretRect(c); ok {
			f.Painter.SetFillStyle(f.ChromeStyle.GripColor)
			f.Painter.FillRect(x, y+2, 1.5, h-4)
		}
	}
}

// --- formatting -------------------------------------------------------

// current returns the styles the selections (or the carets) cover.
func (f *TextFlow) current() []TextStyle {
	d := f.flatten()
	var out []TextStyle
	for _, c := range f.Cursors() {
		from, to := c.Range()
		if from == to {
			out = append(out, f.typeFormat(d, from).resolve(f.Style))
			continue
		}
		for i := from; i < to && i < len(d.runes); i++ {
			if !d.isObj(i) {
				out = append(out, d.styles[d.rs[i]])
			}
		}
	}
	return out
}

// ApplyFormat applies fn to the format of the selected characters; with
// no selection it changes the format of the text typed next. It reports
// whether anything changed.
func (f *TextFlow) ApplyFormat(fn func(*Format)) bool {
	cs := normalize(f.Cursors())
	any := false
	for _, c := range cs {
		if !c.Empty() {
			any = true
		}
	}
	if !any {
		before := f.pending
		if before.IsZero() {
			before = f.typeFormat(f.flatten(), f.sel.caret)
		}
		after := before
		fn(&after)
		f.pending = after
		return after != before
	}
	f.materialize()
	var edits []Edit
	changed := false
	for _, c := range cs {
		from, to := c.Range()
		if from == to {
			continue
		}
		items := f.Slice(from, to)
		for i, it := range items {
			if ch, ok := it.(Chars); ok {
				fm := ch.Format
				fn(&fm)
				if fm != ch.Format {
					changed = true
				}
				items[i] = Chars{Text: ch.Text, Format: fm}
			}
		}
		edits = append(edits, Edit{From: from, To: to, Items: items})
	}
	if changed {
		keep := f.Cursors()
		f.Replace(edits)
		f.SetCursors(keep)
	}
	return changed
}

// SetTextColor colors the selected text (or the text typed next); ""
// returns it to the default color.
func (f *TextFlow) SetTextColor(css string) bool {
	return f.ApplyFormat(func(fm *Format) { fm.Color = css })
}

// SetHighlight highlights the selected text; "" removes the highlight.
func (f *TextFlow) SetHighlight(css string) bool {
	return f.ApplyFormat(func(fm *Format) { fm.Background = css })
}

func (f *TextFlow) toggle(has func(TextStyle) bool, set func(*Format, bool)) bool {
	all := true
	for _, s := range f.current() {
		all = all && has(s)
	}
	return f.ApplyFormat(func(fm *Format) { set(fm, !all) })
}

// ToggleBold makes the selection bold, or regular if it all is bold.
func (f *TextFlow) ToggleBold() bool {
	return f.toggle(func(s TextStyle) bool { return s.FontWeight >= 600 }, func(fm *Format, on bool) {
		if on {
			fm.FontWeight = 700
		} else {
			fm.FontWeight = 400
		}
	})
}

// ToggleItalic is ToggleBold for italics.
func (f *TextFlow) ToggleItalic() bool {
	return f.toggle(func(s TextStyle) bool { return s.FontStyle == StyleItalic }, func(fm *Format, on bool) {
		if on {
			fm.FontStyle = StyleItalic
		} else {
			fm.FontStyle = StyleNormal
		}
	})
}

// ToggleUnderline, ToggleStrike, ToggleSuperscript and ToggleSubscript
// switch a decoration: on unless it is on everywhere already.
func (f *TextFlow) ToggleUnderline() bool {
	return f.toggle(func(s TextStyle) bool { return s.Underline }, func(fm *Format, on bool) { fm.Underline = on })
}

func (f *TextFlow) ToggleStrike() bool {
	return f.toggle(func(s TextStyle) bool { return s.Strike }, func(fm *Format, on bool) { fm.Strike = on })
}

func (f *TextFlow) ToggleSuperscript() bool {
	return f.toggle(func(s TextStyle) bool { return s.Superscript }, func(fm *Format, on bool) {
		fm.Superscript = on
		if on {
			fm.Subscript = false
		}
	})
}

func (f *TextFlow) ToggleSubscript() bool {
	return f.toggle(func(s TextStyle) bool { return s.Subscript }, func(fm *Format, on bool) {
		fm.Subscript = on
		if on {
			fm.Superscript = false
		}
	})
}

// PasteFormat gives the selected text exactly format fm; with no
// selection it applies to the text typed next.
func (f *TextFlow) PasteFormat(fm Format) bool {
	return f.ApplyFormat(func(x *Format) { *x = fm })
}

// HandleCommand implements Commander. While the caret is in the text the
// formatting commands, select-all and the format painter work; with just
// a selection (view mode too) select-all and copy-format do.
func (f *TextFlow) HandleCommand(cmd Command) bool {
	if f.Editing() {
		switch cmd {
		case CmdBold:
			f.ToggleBold()
		case CmdItalic:
			f.ToggleItalic()
		case CmdUnderline:
			f.ToggleUnderline()
		case CmdStrike:
			f.ToggleStrike()
		case CmdSuperscript:
			f.ToggleSuperscript()
		case CmdSubscript:
			f.ToggleSubscript()
		case CmdPasteFormat:
			fm, ok := Format{}, false
			if f.formats != nil {
				fm, ok = f.formats.Get()
			}
			if !ok {
				return false
			}
			f.PasteFormat(fm)
		case CmdSelectAll:
			f.SelectAll()
		case CmdCopyFormat:
			if f.formats == nil {
				return false
			}
			f.formats.Set(f.commonOfSelection())
		default:
			return false
		}
		f.caretOff = false
		return true
	}
	if !f.HasSelection() {
		return false
	}
	switch cmd {
	case CmdSelectAll:
		f.SelectAll()
	case CmdCopyFormat:
		if f.formats == nil {
			return false
		}
		f.formats.Set(f.CommonFormat())
	default:
		return false
	}
	return true
}

// commonOfSelection is CommonFormat, or the typing format at the caret.
func (f *TextFlow) commonOfSelection() Format {
	if f.HasSelection() {
		return f.CommonFormat()
	}
	return f.typeFormat(f.flatten(), f.sel.caret)
}

// caretRect is CaretRect for a cursor: with After it is on the line that
// starts at its offset (after a break) when there is one.
func (f *TextFlow) caretRect(c Cursor) (x, y, h float64, ok bool) {
	if c.After {
		for _, ln := range f.Layout().Lines {
			if ln.Start == c.Caret {
				return ln.X, ln.Y, ln.H, true
			}
		}
	}
	return f.CaretRect(c.Caret)
}

// hasOpAt reports whether a break or column config sits at offset o.
func (d *flowData) hasOpAt(o int) bool {
	for _, op := range d.ops {
		if op.at == o {
			return true
		}
	}
	return false
}
