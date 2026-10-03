package canvas

import "strings"

// CommentHighlight is the fill behind text with an open comment.
const CommentHighlight = "rgba(255, 212, 0, 0.30)"

func (f *TextFlow) commentResolved(id string) bool {
	c, ok := f.Comments.Get(id)
	return ok && c.Resolved
}

// commentsOpen reports whether any of the comments in a Format.Comment value
// is not resolved (the text is highlighted while one is).
func (f *TextFlow) commentsOpen(value string) bool {
	for _, id := range commentList(value) {
		if !f.commentResolved(id) {
			return true
		}
	}
	return false
}

// commentAt returns the comment ID of the run at (x, y) and the run's
// rectangle, or "".
func (f *TextFlow) commentAt(x, y float64) (string, [4]float64) {
	if f.Comments.Len() == 0 {
		return "", [4]float64{}
	}
	for _, ln := range f.Layout().Lines {
		if y < ln.Y || y >= ln.Y+ln.H {
			continue
		}
		for _, r := range ln.Runs {
			if r.Comment != "" && x >= ln.X+r.X && x < ln.X+r.X+r.W {
				for _, id := range commentList(r.Comment) {
					if _, ok := f.Comments.Get(id); ok {
						return id, [4]float64{ln.X + r.X, ln.Y, r.W, ln.H}
					}
				}
			}
		}
	}
	return "", [4]float64{}
}

// HoveredComment returns the ID of the comment under the pointer, or "".
func (f *TextFlow) HoveredComment() string { return f.hoverComment }

// setHoverComment records the hovered comment and reports a change.
func (f *TextFlow) setHoverComment(id string, rect [4]float64) bool {
	if id == f.hoverComment && rect == f.hoverCRect {
		return false
	}
	changed := id != f.hoverComment
	f.hoverComment, f.hoverCRect = id, rect
	if changed && f.OnHoverComment != nil {
		f.OnHoverComment(id)
	}
	return true
}

// wrapWords breaks text into lines of at most n characters.
func wrapWords(text string, n int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(text) {
		switch {
		case cur == "":
			cur = w
		case len([]rune(cur))+1+len([]rune(w)) <= n:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// drawCommentTip draws the tip for the hovered comment: its author, the
// summary (Markdown stripped) wrapped to a few lines, and how many
// replies it has.
func (f *TextFlow) drawCommentTip() {
	if f.hoverComment == "" || f.Painter == nil {
		return
	}
	c, ok := f.Comments.Get(f.hoverComment)
	if !ok {
		return
	}
	const maxLines, lineH, pad = 4, 16.0, 8.0
	lines := wrapWords(CommentSummary(c.Text, 160), 38)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] += "…"
	}
	foot := ""
	switch n := len(c.Replies); {
	case n == 1:
		foot = "1 reply"
	case n > 1:
		foot = itoa(n) + " replies"
	}
	if c.Resolved {
		if foot != "" {
			foot += " · "
		}
		foot += "resolved"
	}
	p := f.Painter
	head := c.Author
	if head == "" {
		head = "Comment"
	}
	p.SetFont("bold 12px sans-serif")
	w := p.MeasureTextWidth(head)
	p.SetFont("12px sans-serif")
	for _, l := range append(lines[:len(lines):len(lines)], foot) {
		w = max(w, p.MeasureTextWidth(l))
	}
	w += 2 * pad
	h := 2*pad + lineH*float64(1+len(lines))
	if foot != "" {
		h += lineH
	}
	x, y := f.hoverCRect[0], f.hoverCRect[1]+f.hoverCRect[3]+4
	if f.hoverLink != "" { // a link tip sits there too: go above the text
		y = f.hoverCRect[1] - h - 4
	}
	p.SetFillStyle("#fffbe6")
	p.FillRect(x, y, w, h)
	p.SetStrokeStyle("#e6c200")
	p.SetLineWidth(1)
	p.StrokeRect(x+0.5, y+0.5, w-1, h-1)
	p.SetTextAlign("left")
	p.SetTextBaseline("alphabetic")
	ty := y + pad + 11
	p.SetFont("bold 12px sans-serif")
	p.SetFillStyle("#1f2328")
	p.FillText(head, x+pad, ty)
	p.SetFont("12px sans-serif")
	for _, l := range lines {
		ty += lineH
		p.FillText(l, x+pad, ty)
	}
	if foot != "" {
		ty += lineH
		p.SetFillStyle("#6b7280")
		p.FillText(foot, x+pad, ty)
	}
}

// CommentRange returns the stream range [from, to) of the text the
// comment is on (from its first character to its last).
func (f *TextFlow) CommentRange(id string) (from, to int, ok bool) {
	ids := charComments(f.ContentItems())
	from, to = -1, 0
	for i, c := range ids {
		if commentHas(c, id) {
			if from < 0 {
				from = i
			}
			to = i + 1
		}
	}
	return max(from, 0), to, from >= 0
}

// CommentExcerpts returns, for every comment that is on some text, that
// text (the characters carrying its ID, in order).
func (f *TextFlow) CommentExcerpts() map[string]string {
	out := map[string]string{}
	for _, it := range f.ContentItems() {
		if ch, ok := it.(Chars); ok {
			for _, id := range commentList(ch.Format.Comment) {
				out[id] += ch.Text
			}
		}
	}
	return out
}

// DeleteComment removes the comment and un-marks its text.
func (f *TextFlow) DeleteComment(id string) bool {
	if !f.Comments.Delete(id) {
		return false
	}
	untagComment(f, id)
	return true
}

// SelectComment selects the text the comment is on.
func (f *TextFlow) SelectComment(id string) bool {
	from, to, ok := f.CommentRange(id)
	if !ok {
		return false
	}
	f.SetCursors([]Cursor{{Anchor: from, Caret: to}})
	return true
}
