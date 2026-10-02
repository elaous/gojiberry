package canvas

import "slices"

// Support for keeping a TextFlow in step with a remote copy of the
// document (see the collab package): replace the whole content without it
// counting as an edit, keep the cursors where the caller says, and let
// side data changes tell the flow they happened.

// SetRemoteContent replaces the flow's content with items (a copy is
// taken) and puts the cursors at cs, as when another person's edit
// arrives. It does not call OnChange (it is not a local edit), keeps the
// caret in the text if it was there, and clears the undo history: undo
// would otherwise rewind other people's changes along with yours.
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

// Touch tells the flow that its side data (comments, footnotes) changed
// without a text edit, so OnChange listeners (the collab bridge) hear of
// it. Commands that change a comment or note call it.
func (f *TextFlow) Touch() { f.changed() }

// ReplaceAll makes the set hold exactly comments, in place (so a set
// shared with other code stays the same set).
func (s *CommentSet) ReplaceAll(comments []Comment) {
	if s == nil {
		return
	}
	s.list = append(s.list[:0:0], comments...)
}

// ReplaceAll makes the set hold exactly notes, in place.
func (s *FootnoteSet) ReplaceAll(notes []Footnote) {
	if s == nil {
		return
	}
	s.list = append(s.list[:0:0], notes...)
}
