package canvas

import "reflect"

type CommentTargeter interface{ CommentTarget() EditTarget }

type commentTarget struct{ f *TextFlow }

var _ CommentHost = commentTarget{}

func (t commentTarget) EditText() string          { return t.f.EditText() }
func (t commentTarget) Slice(from, to int) []Item { return t.f.Slice(from, to) }

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

func (f *TextFlow) CommentTarget() EditTarget {
	if !f.HasSelection() {
		return nil
	}
	return commentTarget{f}
}

// CommentTarget implements CommentTargeter for the scrolled content.
func (sv *ScrollView) CommentTarget() EditTarget { return sv.content.commentTarget() }

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
