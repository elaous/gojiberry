package canvas

import "strings"

// Links in a TextFlow: text with Format.Link set is drawn as a link and
// shows its URL in a small tip on hover. A click opens it (OnLink) in
// view mode; in edit mode a plain click puts the caret there and
// Ctrl/Cmd+click opens it.

// linkAt returns the URL of the link at (x, y) and the run's rectangle,
// or "".
func (f *TextFlow) linkAt(x, y float64) (url string, rect [4]float64) {
	for _, ln := range f.Layout().Lines {
		if y < ln.Y || y >= ln.Y+ln.H {
			continue
		}
		for _, r := range ln.Runs {
			if r.Link != "" && x >= ln.X+r.X && x < ln.X+r.X+r.W {
				return r.Link, [4]float64{ln.X + r.X, ln.Y, r.W, ln.H}
			}
		}
	}
	return "", [4]float64{}
}

// HoveredLink returns the URL under the pointer, or "".
func (f *TextFlow) HoveredLink() string { return f.hoverLink }

// updateLinkHover tracks the link under the pointer, and says whether
// anything to redraw changed.
func (f *TextFlow) updateLinkHover(x, y float64) bool {
	cid, crect := f.commentAt(x, y)
	commentChanged := f.setHoverComment(cid, crect)
	url, rect := f.linkAt(x, y)
	f.hoverX, f.hoverY = x, y
	if commentChanged && url == f.hoverLink {
		return true
	}
	if url == f.hoverLink && rect == f.hoverRect {
		return url != "" // the tip follows the pointer
	}
	changed := url != f.hoverLink
	f.hoverLink, f.hoverRect = url, rect
	if changed && f.OnHoverLink != nil {
		f.OnHoverLink(url)
	}
	return true
}

// openLinkAt opens the link at (x, y), if there is one and something is
// listening.
func (f *TextFlow) openLinkAt(x, y float64) bool {
	url, _ := f.linkAt(x, y)
	if name, ok := strings.CutPrefix(url, "#"); ok && f.GotoBookmark(name) {
		return true
	}
	open := f.OnLink
	if open == nil {
		open = f.stageLink
	}
	if url == "" || open == nil || !SafeLinkURL(url) {
		return false
	}
	open(url)
	return true
}

// DrawOverlay implements Overlayer: the tip showing the hovered link's URL.
func (f *TextFlow) DrawOverlay() {
	if f.sugg != nil && f.mode == ModeEdit {
		f.sugg.Draw()
	}
	f.drawCommentTip()
	if f.hoverLink == "" || f.Painter == nil {
		return
	}
	p := f.Painter
	text := f.hoverLink
	hint := "Click to open"
	if f.mode == ModeEdit {
		hint = "Ctrl+click to open"
	}
	p.SetFont("12px sans-serif")
	w := max(p.MeasureTextWidth(text), p.MeasureTextWidth(hint)) + 16
	x, y := f.hoverRect[0], f.hoverRect[1]+f.hoverRect[3]+4
	if f.hoverX+w > x+w { // keep it near the pointer, not past the run
		x = max(x, f.hoverX-8)
	}
	p.SetFillStyle("#1f2328")
	p.FillRect(x, y, w, 38)
	p.SetFillStyle("#ffffff")
	p.SetTextAlign("left")
	p.SetTextBaseline("alphabetic")
	p.FillText(text, x+8, y+16)
	p.SetFillStyle("#9aa3ad")
	p.FillText(hint, x+8, y+31)
}

var _ Overlayer = (*TextFlow)(nil)

// SetLinkHandler implements LinkHandlerSetter: the Stage opens links
// unless OnLink is set.
func (f *TextFlow) SetLinkHandler(open func(url string)) { f.stageLink = open }

// SafeLinkURL reports whether a link may be opened or shown as an href: a
// relative reference, #bookmark, or an http, https, mailto or tel URL. Other
// schemes (javascript:, data:, ...) in a document's links are not followed.
func SafeLinkURL(u string) bool {
	u = strings.TrimSpace(u)
	i := strings.IndexAny(u, ":/?#")
	if i < 0 || u[i] != ':' {
		return true // no scheme
	}
	switch strings.ToLower(u[:i]) {
	case "http", "https", "mailto", "tel":
		return true
	}
	return false
}
