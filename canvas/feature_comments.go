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

// BeginEdit makes the content editable and starts its undo history.
func (f *TextFlow) BeginEdit() { f.materialize() }

func (f *TextFlow) CommentSet() *CommentSet {
	if f.Comments == nil {
		f.Comments = NewCommentSet()
	}
	return f.Comments
}

func (f *TextFlow) CommentAuthor() string { return f.Author }

// Comments adds notes to text; Ctrl/Cmd+Alt+N comments the selection.
type Comments struct{}

const (
	CmdCommentAdd     Command = "comment.add"
	CmdCommentEdit    Command = "comment.edit"
	CmdCommentResolve Command = "comment.resolve"
	CmdCommentDelete  Command = "comment.delete"
)

func (Comments) Name() string { return "comments" }

const commentPrompt = "Comment (Markdown; @name or an e-mail address mentions someone)"

// askComment asks the person for a comment's text.
func askComment(c *CommandContext, set *CommentSet, initial string) (string, bool) {
	prompt := commentPrompt
	for range 5 {
		text, ok := c.Prompt(prompt, initial)
		if !ok || text == "" {
			return "", false
		}
		clean, problems := set.CheckMentions(text)
		p, blocked := BlockingMention(problems)
		if !blocked {
			return clean, true
		}
		prompt, initial = p.Message()+" "+commentPrompt, text
	}
	return "", false
}

func (Comments) Install(r *Registry) {
	r.AddCommand(CmdCommentAdd, "Add comment", "Mod+Alt+N", func(c *CommandContext) bool {
		h, ok := c.Target.(CommentHost)
		cur := c.Target.Cursors()[0]
		if !ok || c.Prompt == nil || cur.Empty() {
			return false
		}
		text, ok := askComment(c, h.CommentSet(), "")
		if !ok {
			return false
		}
		from, to := cur.Range()
		// Start history before the record is added, so undo removes it.
		if b, ok := c.Target.(interface{ BeginEdit() }); ok {
			b.BeginEdit()
		}
		cm := h.CommentSet().Add(Comment{Author: h.CommentAuthor(), Text: text, Created: time.Now()})
		tagComment(c.Target, from, to, cm.ID)
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
		text, ok := askComment(c, h.CommentSet(), cm.Text)
		if !ok {
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

// charComments lists the comment IDs on each character.
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

// commentAtCursor returns the target and comment ID at the primary cursor.
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

// untagComment removes comment id from the text, keeping other comments.
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
