package canvas

import "time"

// CommentHost is implemented by targets that hold comments (a TextFlow).
type CommentHost interface {
	// CommentSet returns the comments, making the set if there is none.
	CommentSet() *CommentSet
	// CommentAuthor is who new comments are by.
	CommentAuthor() string
}

var _ CommentHost = (*TextFlow)(nil)

// CommentSet implements CommentHost.
func (f *TextFlow) CommentSet() *CommentSet {
	if f.Comments == nil {
		f.Comments = NewCommentSet()
	}
	return f.Comments
}

// CommentAuthor implements CommentHost.
func (f *TextFlow) CommentAuthor() string { return f.Author }

// Comments adds notes to text (see Comment):
//
//   - Ctrl/Cmd+Alt+N comments the selection, asking for the text
//     (Markdown; @username and e-mail addresses mention people);
//   - comment.edit changes the comment at the caret, comment.resolve
//     toggles it resolved, comment.delete removes it and un-marks its text.
//
// They need a Prompt (the Stage's host provides one) and a target with
// comments (a TextFlow); a viewer cannot run them, as a viewer cannot edit.
type Comments struct{}

// Command names.
const (
	CmdCommentAdd     Command = "comment.add"
	CmdCommentEdit    Command = "comment.edit"
	CmdCommentResolve Command = "comment.resolve"
	CmdCommentDelete  Command = "comment.delete"
)

// Name implements Feature.
func (Comments) Name() string { return "comments" }

const commentPrompt = "Comment (Markdown; @name or an e-mail address mentions someone)"

// Install implements Feature.
func (Comments) Install(r *Registry) {
	r.AddCommand(CmdCommentAdd, "Add comment", "Mod+Alt+N", func(c *CommandContext) bool {
		h, ok := c.Target.(CommentHost)
		cur := c.Target.Cursors()[0]
		if !ok || c.Prompt == nil || cur.Empty() {
			return false
		}
		text, ok := c.Prompt(commentPrompt, "")
		if !ok || text == "" {
			return false
		}
		from, to := cur.Range()
		cm := h.CommentSet().Add(Comment{Author: h.CommentAuthor(), Text: text, Created: time.Now()})
		tagComment(c.Target, from, to, cm.ID)
		return true
	})
	r.AddCommand(CmdCommentEdit, "Edit comment", "Mod+Alt+Shift+N", func(c *CommandContext) bool {
		h, id := commentAtCursor(c.Target)
		if h == nil || c.Prompt == nil {
			return false
		}
		cm, _ := h.CommentSet().Get(id)
		text, ok := c.Prompt(commentPrompt, cm.Text)
		if !ok || text == "" {
			return false
		}
		return h.CommentSet().Update(id, func(cm *Comment) { cm.Text = text })
	})
	r.AddCommand(CmdCommentResolve, "Resolve comment", "", func(c *CommandContext) bool {
		h, id := commentAtCursor(c.Target)
		return h != nil && h.CommentSet().Update(id, func(cm *Comment) { cm.Resolved = !cm.Resolved })
	})
	r.AddCommand(CmdCommentDelete, "Delete comment", "", func(c *CommandContext) bool {
		h, id := commentAtCursor(c.Target)
		if h == nil {
			return false
		}
		h.CommentSet().Delete(id)
		untagComment(c.Target, id)
		return true
	})
}

// charComments lists the comment ID of every character of items.
func charComments(items []Item) []string {
	var out []string
	for _, it := range items {
		switch v := it.(type) {
		case Chars:
			for range v.Text {
				out = append(out, v.Format.Comment)
			}
		case Inline:
			out = append(out, "")
		}
	}
	return out
}

// commentAtCursor returns the target as a CommentHost and the ID of the
// comment at the primary cursor (the first selected character's, or the
// one at or before the caret); h is nil if there is none.
func commentAtCursor(t EditTarget) (CommentHost, string) {
	h, ok := t.(CommentHost)
	if !ok {
		return nil, ""
	}
	cur := t.Cursors()[0]
	pos := cur.Caret
	if !cur.Empty() {
		pos, _ = cur.Range()
	}
	ids := charComments(t.Slice(0, len([]rune(t.EditText()))))
	for _, i := range []int{pos, pos - 1} {
		if i >= 0 && i < len(ids) && ids[i] != "" {
			if _, ok := h.CommentSet().Get(ids[i]); ok {
				return h, ids[i]
			}
		}
	}
	return nil, ""
}

// retag rewrites the comment of the characters in [from, to) with fn.
func retag(t EditTarget, from, to int, fn func(old string) string) {
	items := t.Slice(from, to)
	out := make([]Item, len(items))
	for i, it := range items {
		if ch, ok := it.(Chars); ok {
			ch.Format.Comment = fn(ch.Format.Comment)
			it = ch
		}
		out[i] = it
	}
	cs := t.Cursors()
	t.Replace([]Edit{{From: from, To: to, Items: out}})
	t.SetCursors(cs)
}

// tagComment puts comment id on [from, to).
func tagComment(t EditTarget, from, to int, id string) {
	retag(t, from, to, func(string) string { return id })
}

// untagComment takes comment id off all the text that has it.
func untagComment(t EditTarget, id string) {
	ids := charComments(t.Slice(0, len([]rune(t.EditText()))))
	for i := 0; i < len(ids); {
		if ids[i] != id {
			i++
			continue
		}
		j := i
		for j < len(ids) && ids[j] == id {
			j++
		}
		retag(t, i, j, func(string) string { return "" })
		i = j
	}
}
