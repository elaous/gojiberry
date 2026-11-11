package canvas

import (
	"slices"
	"strings"
)

// ContentItems returns the content stream as Items.
func (f *TextFlow) ContentItems() []Item { return slices.Clone(f.items()) }

// ResetContent discards edits, restoring content from Source.
func (f *TextFlow) ResetContent() {
	f.edited, f.derivedOK = false, false
	f.sel, f.extra = flowSel{}, nil
	f.Items = nil
	f.hist.reset()
}

// Editing reports whether the caret is in the text.
func (f *TextFlow) Editing() bool { return f.mode == ModeEdit && f.editing }

func (f *TextFlow) stopEditing() { f.editing, f.extra, f.pending = false, nil, Format{} }

func (f *TextFlow) materialize() {
	if f.edited {
		f.hist.start(f.histSnap())
		return
	}
	f.Items = slices.Clone(f.items())
	f.Text, f.edited = "", true
	f.hist.start(f.histSnap())
}

func (f *TextFlow) changed() {
	f.derivedOK = false
	f.hist.commit(f.histSnap(), f.histKind)
	f.histKind = ""
	f.notify()
}

func (f *TextFlow) notify() {
	if f.OnChange != nil {
		f.OnChange(f)
	}
	for _, fn := range f.listeners {
		fn(f)
	}
}

// Subscribe registers fn to run after every edit, like OnChange.
func (f *TextFlow) Subscribe(fn func(*TextFlow)) { f.listeners = append(f.listeners, fn) }

func (f *TextFlow) EditTarget() EditTarget {
	if f.Editing() {
		return f
	}
	return nil
}

func (f *TextFlow) HoldsObjects() bool { return true }

func (f *TextFlow) EditText() string { return string(f.flatten().runes) }

func (f *TextFlow) Cursors() []Cursor {
	return append([]Cursor{{Anchor: f.sel.anchor, Caret: f.sel.caret, After: f.sel.after}}, f.extra...)
}

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

// Slice returns runes [from, to) with their formatting.
func (f *TextFlow) Slice(from, to int) []Item {
	d := f.flatten()
	from, to = min(max(from, 0), len(d.runes)), min(max(to, 0), len(d.runes))
	var items []Item
	paraAt := func(i int) {
		for _, r := range d.paras {
			if r.at == i {
				items = append(items, r.pf)
			}
		}
	}
	for i := from; i < to; {
		paraAt(i)
		if o, ok := d.objs[i]; ok {
			items = append(items, o)
			i++
			continue
		}
		j := i
		var b strings.Builder
		for j < to && d.rs[j] == d.rs[i] && !d.isObj(j) && (j == i || !d.hasParaAt(j)) {
			b.WriteRune(d.runes[j])
			j++
		}
		items = append(items, Chars{Text: b.String(), Format: d.formats[d.rs[i]]})
		i = j
	}
	return items
}

func (f *TextFlow) Replace(edits []Edit) {
	f.materialize()
	es := slices.Clone(edits)
	slices.SortStableFunc(es, func(a, b Edit) int {
		// last first; at one offset the side behind the breaks is later
		if a.From == b.From && a.AfterOps != b.AfterOps {
			if a.AfterOps {
				return -1
			}
			return 1
		}
		return b.From - a.From
	})
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
		case ParaFormat: // goes with the paragraph it starts, if that is deleted
			if off < from || off >= to {
				out = append(out, it)
			}
		default: // break or column config at offset off
			if off <= from || off >= to {
				out = append(out, it)
			}
		}
	}
	insert()
	f.Items = mergeChars(out)
}

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

func (f *TextFlow) opAt(o int) int {
	off := 0
	for i, it := range f.Items {
		switch v := it.(type) {
		case Chars:
			off += len([]rune(v.Text))
		case Inline:
			off++
		case ParaFormat:
		default:
			if off == o {
				return i
			}
		}
	}
	return -1
}

func (f *TextFlow) typeFormat(d *flowData, o int) Format {
	fm := f.typeFormatRaw(d, o)
	fm.Proposal = "" // New text is a proposal only while suggesting.
	return fm
}

// CaretFormat is the format text typed at the caret takes.
func (f *TextFlow) CaretFormat() Format {
	f.Layout()
	return f.typeFormat(f.data, f.Cursors()[0].Caret)
}

// CaretFormatLast is CaretFormat from the latest draw, without recomputing.
func (f *TextFlow) CaretFormatLast() Format {
	if f.data == nil {
		return Format{}
	}
	return f.typeFormat(f.data, f.Cursors()[0].Caret)
}

func (f *TextFlow) typeFormatRaw(d *flowData, o int) Format {
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

// InsertText types s at every cursor, replacing selections.
func (f *TextFlow) InsertText(s string) bool {
	s = strings.ReplaceAll(s, "\r", "")
	if !f.Editing() || s == "" {
		return false
	}
	f.materialize()
	d := f.flatten()
	if rs := []rune(s); len(rs) == 1 && rs[0] != '\n' && !f.HasSelection() {
		f.histKind = "type"
	}
	if rs := []rune(s); len(rs) == 1 && rs[0] != '\n' && f.AutoClose != nil && !f.proposalsOn() {
		f.typePaired(d, rs[0]) // No bracket auto-closing while suggesting.
	} else {
		editCursors(f, func(c Cursor, _ []rune) (int, int, []Item) {
			from, to := c.Range()
			return from, to, []Item{Chars{Text: s, Format: f.typeFormat(d, from)}}
		})
	}
	f.histKind = ""
	f.pending, f.caretOff = Format{}, false
	if s == "$" && len(f.extra) == 0 && !f.proposalsOn() {
		f.dollarMathOnType()
	}
	if len([]rune(s)) == 1 && s != "\n" {
		f.updateSuggestions()
	} else {
		f.closeSuggestions()
	}
	return true
}

// PasteText inserts clipboard text as given, one line per cursor.
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

// CopyText returns the selected text, one line per cursor.
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
			from = min(from, len(rs))
			parts = append(parts, strings.ReplaceAll(string(rs[from:min(to, len(rs))]), string(objectRune), ""))
		}
	}
	return strings.Join(parts, "\n"), len(parts) > 0
}

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

func (f *TextFlow) HandleKey(key string, shift bool) bool {
	if f.Editing() && f.suggestKey(key) {
		return true
	}
	if f.Editing() && f.editKey(key, shift) {
		if key == "Backspace" || key == "Delete" {
			f.updateSuggestions()
		} else {
			f.closeSuggestions()
		}
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
	key = f.verticalKey(key)
	if endsRun(key) {
		f.hist.breakRun() // moving, Enter, Tab... end a run of typing or deleting
	}
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
			return f.visualStep(c.Caret, false)
		})
	case "ArrowRight":
		move(func(c Cursor) int {
			if _, to := c.Range(); !c.Empty() && !shift {
				return to
			}
			return f.visualStep(c.Caret, true)
		})
	case "ArrowUp", "ArrowDown":
		move(func(c Cursor) int {
			x, y, h, ok := f.CaretRect(c.Caret)
			if !ok {
				return c.Caret
			}
			if key == "ArrowUp" {
				o, _ := f.offsetAt(x, y-1)
				return o
			}
			o, _ := f.offsetAt(x, y+h+1)
			return o
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
		if len(f.extra) == 0 && !f.HasSelection() && (key == "Delete" || f.sel.after) {
			if i := f.opAt(f.sel.caret); i >= 0 {
				f.sel.after = false
				f.Items = mergeChars(slices.Delete(slices.Clone(f.Items), i, i+1))
				f.changed()
				break
			}
		}
		n := len(f.flatten().runes)
		if !f.HasSelection() {
			f.histKind = "delete"
		}
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
		f.histKind = ""
	case "Enter":
		return f.enter()
	case "Tab":
		if !f.indentList(map[bool]int{false: 1, true: -1}[shift]) {
			return false
		}
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

func (f *TextFlow) SetCaretVisible(on bool) bool {
	f.caretOff = !on
	return f.Editing()
}

func (f *TextFlow) WantsFocus() bool { return f.HasSelection() || f.Editing() }

// MouseDownMods: Ctrl/Cmd+click adds a cursor.
func (f *TextFlow) MouseDownMods(x, y float64, m Mods) bool {
	x, y = f.vpt(x, y)
	if (m.Ctrl || m.Meta) && f.mode == ModeEdit && f.openLinkAt(x, y) {
		return true
	}
	if (m.Ctrl || m.Meta) && f.Editing() && f.Painter != nil && !f.obstructed(x, y) && f.inText(x, y) {
		o, after := f.offsetAt(x, y)
		f.SetCursors(normalize(append(f.Cursors(), Cursor{Anchor: o, Caret: o, After: after})))
		return true
	}
	return f.mouseDown(x, y)
}

func (f *TextFlow) drawCarets() {
	if !f.Editing() || f.caretOff || f.Painter == nil {
		return
	}
	for _, c := range f.Cursors() {
		if f.skipCaretOnOtherPage(c.Caret) {
			continue
		}
		if x, y, h, ok := f.caretRect(c); ok {
			if f.StrongCaret {
				f.Painter.SetFillStyle(StrongCaretEdge)
				f.Painter.FillRect(x-1.5, y+1, 5, h-2)
				f.Painter.SetFillStyle(StrongCaretColor)
				f.Painter.FillRect(x-0.5, y+2, 3, h-4)
				continue
			}
			f.Painter.SetFillStyle(f.ChromeStyle.GripColor)
			f.Painter.FillRect(x, y+2, 1.5, h-4)
		}
	}
}

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

// SetTextColor colors the selection or next text; "" resets it.
func (f *TextFlow) SetTextColor(css string) bool {
	return f.ApplyFormat(func(fm *Format) { fm.Color = css })
}

// ValidLang reports whether s is a valid language tag.
func ValidLang(s string) bool {
	if s == "" {
		return true
	}
	if len(s) > 35 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	group := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '-':
			if group == 0 {
				return false
			}
			group = 0
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			if group++; group > 8 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (f *TextFlow) SetLanguage(tag string) bool {
	if !ValidLang(tag) {
		return false
	}
	return f.ApplyFormat(func(fm *Format) { fm.Lang = tag })
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

// ToggleUnderline, Strike, Superscript and Subscript toggle decorations.
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

// PasteFormat gives the selection format fm.
func (f *TextFlow) PasteFormat(fm Format) bool {
	return f.ApplyFormat(func(x *Format) {
		// the target keeps its own comment, bookmark and index marks
		comment, bookmark, index, proposal := x.Comment, x.Bookmark, x.Index, x.Proposal
		*x = fm
		x.Comment, x.Bookmark, x.Index, x.Proposal = comment, bookmark, index, proposal
	})
}

// HandleCommand runs editing and formatting commands.
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

func (f *TextFlow) commonOfSelection() Format {
	if f.HasSelection() {
		return f.CommonFormat()
	}
	return f.typeFormat(f.flatten(), f.sel.caret)
}

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

func (d *flowData) hasOpAt(o int) bool {
	for _, op := range d.ops {
		if op.at == o {
			return true
		}
	}
	return false
}

func (d *flowData) hasParaAt(o int) bool {
	for _, r := range d.paras {
		if r.at == o {
			return true
		}
	}
	return false
}
