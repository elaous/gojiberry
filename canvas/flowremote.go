package canvas

import "slices"

// SetRemoteContent replaces the content with a copy of items.
func (f *TextFlow) SetRemoteContent(items []Item, cs []Cursor) {
	wasEditing := f.editing
	f.Transform, f.Source, f.Text = nil, "", ""
	f.Items = slices.Clone(items)
	f.edited, f.derivedOK = true, false
	f.hist.reset()
	f.hist.start(f.histSnap())
	f.pending, f.caretOff = Format{}, false
	if len(cs) > 0 {
		n := len(f.flatten().runes)
		clamp := func(o int) int { return min(max(o, 0), n) }
		f.sel.anchor, f.sel.caret, f.sel.after = clamp(cs[0].Anchor), clamp(cs[0].Caret), cs[0].After
		f.extra = nil
		for _, c := range cs[1:] {
			f.extra = append(f.extra, Cursor{Anchor: clamp(c.Anchor), Caret: clamp(c.Caret), After: c.After})
		}
	} else {
		f.clampCursors()
	}
	f.editing = wasEditing
}

func (f *TextFlow) Touch() { f.changed() }

// ReplaceAll makes the comment set hold exactly comments, in place.
func (s *CommentSet) ReplaceAll(comments []Comment) {
	if s == nil {
		return
	}
	s.list = append(s.list[:0:0], comments...)
}

// ReplaceAll makes the footnote set hold exactly notes, in place.
func (s *FootnoteSet) ReplaceAll(notes []Footnote) {
	if s == nil {
		return
	}
	s.list = append(s.list[:0:0], notes...)
}
