package canvas

import (
	"slices"
	"time"
)

// CommentHost is implemented by targets that hold comments (a TextFlow).
type CommentHost interface {
	// CommentSet returns the comments, making the set if there is none.
	CommentSet() *CommentSet
	// CommentAuthor is who new comments are by.
	CommentAuthor() string
}

var _ CommentHost = (*TextFlow)(nil)

// BeginEdit makes the content editable and starts its undo history, before a
// change that is made in two steps (a comment record, then its tag).
func (f *TextFlow) BeginEdit() { f.materialize() }

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
		// Start the target's history before the record is added, so that undo
		// takes the record away together with its tag.
		if b, ok := c.Target.(interface{ BeginEdit() }); ok {
			b.BeginEdit()
		}
		cm := h.CommentSet().Add(Comment{Author: h.CommentAuthor(), Text: text, Created: time.Now()})
		tagComment(c.Target, from, to, cm.ID)
		// If nothing took the tag (only an object was selected, or the target
		// refused the edit) the comment would be anchored to nothing.
		if !slices.ContainsFunc(charComments(c.Target.Slice(0, len([]rune(c.Target.EditText())))), func(v string) bool { return commentHas(v, cm.ID) }) {
			h.CommentSet().Delete(cm.ID)
			return false
		}
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
		ok = h.CommentSet().Update(id, func(cm *Comment) { cm.Text = text })
		touch(c.Target)
		return ok
	})
	r.AddCommand(CmdCommentResolve, "Resolve comment", "", func(c *CommandContext) bool {
		h, id := commentAtCursor(c.Target)
		ok := h != nil && h.CommentSet().Update(id, func(cm *Comment) { cm.Resolved = !cm.Resolved })
		if ok {
			touch(c.Target)
		}
		return ok
	})
	r.AddCommand(CmdCommentDelete, "Delete comment", "", func(c *CommandContext) bool {
		h, id := commentAtCursor(c.Target)
		if h == nil {
			return false
		}
		h.CommentSet().Delete(id)
		untagComment(c.Target, id)
		touch(c.Target)
		return true
	})
}

// charComments lists the Format.Comment value (the comment IDs, comma-
// separated) of every character of items.
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
		if i >= 0 && i < len(ids) {
			for _, id := range commentList(ids[i]) {
				if _, ok := h.CommentSet().Get(id); ok {
					return h, id
				}
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
	retag(t, from, to, func(old string) string { return commentAdd(old, id) })
}

// untagComment takes comment id off all the text that has it (other
// comments on the same text stay).
func untagComment(t EditTarget, id string) {
	ids := charComments(t.Slice(0, len([]rune(t.EditText()))))
	for i := 0; i < len(ids); {
		if !commentHas(ids[i], id) {
			i++
			continue
		}
		j := i
		for j < len(ids) && commentHas(ids[j], id) {
			j++
		}
		retag(t, i, j, func(old string) string { return commentRemove(old, id) })
		i = j
	}
}

// touch tells a target with side data that it changed (see TextFlow.Touch).
func touch(t EditTarget) {
	if tt, ok := t.(interface{ Touch() }); ok {
		tt.Touch()
	}
}
