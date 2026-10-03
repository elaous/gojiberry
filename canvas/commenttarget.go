package canvas

import "reflect"

// A person who may comment but not edit (a viewer, or anyone while the
// document is in view mode) can still select text and add a comment. The
// comment command then gets a target that only lets the comment mark change:
// it passes a Replace through only if the text, its formatting and every
// other item stay exactly as they were.

// CommentTargeter is implemented by components that can offer a
// comment-only target for the text selected in them.
type CommentTargeter interface{ CommentTarget() EditTarget }

type commentTarget struct{ f *TextFlow }

var _ CommentHost = commentTarget{}

func (t commentTarget) EditText() string          { return t.f.EditText() }
func (t commentTarget) Slice(from, to int) []Item { return t.f.Slice(from, to) }

// Cursors is the selected text: in view mode the selection is the only
// thing the person has made, and the edit cursor is somewhere else.
func (t commentTarget) Cursors() []Cursor {
	if from, to := t.f.Selection(); from != to {
		return []Cursor{{Anchor: from, Caret: to}}
	}
	return t.f.Cursors()
}
func (t commentTarget) SetCursors(cs []Cursor)  { t.f.SetCursors(cs) }
func (t commentTarget) CommentSet() *CommentSet { return t.f.CommentSet() }
func (t commentTarget) CommentAuthor() string   { return t.f.CommentAuthor() }
func (t commentTarget) Touch()                  { t.f.Touch() }

// Replace applies the edits if they change nothing but Format.Comment.
func (t commentTarget) Replace(edits []Edit) {
	for _, e := range edits {
		if !reflect.DeepEqual(skeleton(t.f.Slice(e.From, e.To)), skeleton(e.Items)) {
			return
		}
	}
	t.f.Replace(edits)
}

// skeleton lists items rune by rune with the comment cleared, so two item
// lists that differ only in comments (or in how runs are split) are equal.
func skeleton(items []Item) []any {
	var out []any
	type char struct {
		r rune
		f Format
	}
	for _, it := range items {
		if ch, ok := it.(Chars); ok {
			f := ch.Format
			f.Comment = ""
			for _, r := range ch.Text {
				out = append(out, char{r, f})
			}
			continue
		}
		out = append(out, it)
	}
	return out
}

// CommentTarget implements CommentTargeter: the selected text, for
// commenting only; nil when nothing is selected.
func (f *TextFlow) CommentTarget() EditTarget {
	if !f.HasSelection() {
		return nil
	}
	return commentTarget{f}
}

// CommentTarget implements CommentTargeter for the scrolled content.
func (sv *ScrollView) CommentTarget() EditTarget { return sv.content.commentTarget() }

// commentTarget returns the comment-only target of the topmost component
// that has one.
func (s *Stage) commentTarget() EditTarget {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if c, ok := s.Components[i].(CommentTargeter); ok {
			if t := c.CommentTarget(); t != nil {
				return t
			}
		}
	}
	return nil
}
