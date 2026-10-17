package canvas

import (
	"slices"
)

type grip struct{ band, gap int }

var noGrip = grip{-1, -1}

type flowDrag struct {
	grip           grip
	cfg            int
	widths         []float64
	startX         float64
	startL, startR float64
}

// --- caret queries (used by editable components) --------------------

// CaretRect returns where a caret at text-stream offset goes: its x, the
// top y and the height of its line. ok is false if there is no layout
// to place it in.
func (f *TextFlow) CaretRect(offset int) (x, y, h float64, ok bool) {
	lay := f.Layout()
	if len(lay.Lines) == 0 {
		return 0, 0, 0, false
	}
	idx := len(lay.Lines) - 1
	for i, ln := range lay.Lines {
		if offset <= ln.End {
			idx = i
			break
		}
	}
	ln := lay.Lines[idx]
	offset = min(max(offset, ln.Start), ln.End)
	return f.lineX(ln, offset), ln.Y, ln.H, true
}

// CaretPlace is where the caret would be drawn at an offset (see CaretRects).
type CaretPlace struct {
	X, Y, H float64
	OK      bool
}

// CaretRects is CaretRect for many offsets from one layout. CaretRect works the whole layout out each time it is
// called, so a caller that wants the place of every paragraph of a long text asks here: the cost is one layout,
// not one per offset.
func (f *TextFlow) CaretRects(offsets []int) []CaretPlace {
	out := make([]CaretPlace, len(offsets))
	lay := f.Layout()
	if len(lay.Lines) == 0 {
		return out
	}
	for k, offset := range offsets {
		idx := len(lay.Lines) - 1
		for i, ln := range lay.Lines {
			if offset <= ln.End {
				idx = i
				break
			}
		}
		ln := lay.Lines[idx]
		offset = min(max(offset, ln.Start), ln.End)
		out[k] = CaretPlace{X: f.lineX(ln, offset), Y: ln.Y, H: ln.H, OK: true}
	}
	return out
}

// OffsetAt returns the text-stream offset nearest (x, y), for placing a
// caret from a click. With columns it picks the line whose column the
// point is in, so a click in the second column doesn't land in the first.
func (f *TextFlow) OffsetAt(x, y float64) int {
	x, y = f.vpt(x, y)
	o, _ := f.offsetAt(x, y)
	return o
}

// offsetAt is OffsetAt that also says whether the point is on a line that
// starts at the offset right after a break (see Cursor.After).
func (f *TextFlow) offsetAt(x, y float64) (offset int, afterBreak bool) {
	lay := f.Layout()
	if len(lay.Lines) == 0 {
		return 0, false
	}
	best, bestScore := 0, 1e18
	for i, c := range lay.Lines {
		dy := 0.0
		switch {
		case y < c.Y:
			dy = c.Y - y
		case y >= c.Y+c.H:
			dy = y - (c.Y + c.H)
		}
		dx := 0.0
		colW := c.ColW
		if colW == 0 {
			colW = c.W
		}
		switch {
		case x < c.ColX:
			dx = c.ColX - x
		case x >= c.ColX+colW:
			dx = x - (c.ColX + colW)
		}
		if score := dy*1000 + dx; score < bestScore {
			best, bestScore = i, score
		}
	}
	ln := lay.Lines[best]
	if ln.Bidi {
		return f.lineOffsetAt(ln, x), false
	}
	if x <= ln.X {
		return ln.Start, ln.Start > 0 && f.data.hasOpAt(ln.Start)
	}
	prev := 0.0
	for o := ln.Start + 1; o <= ln.End; o++ {
		w := f.data.measure(ln.Start, o)
		if x < ln.X+(prev+w)/2 {
			return o - 1, o-1 == ln.Start && ln.Start > 0 && f.data.hasOpAt(ln.Start)
		}
		prev = w
	}
	return ln.End, false
}

// --- column grips ----------------------------------------------------

// gripRect returns the draggable grip above gap g of band b: x, y, w, h.
func (f *TextFlow) gripRect(b Band, g int) [4]float64 {
	cx := b.Xs[g] + b.Widths[g] + b.Gap/2
	s := f.ChromeStyle
	return [4]float64{cx - s.GripW/2, b.Top - 2 - s.GripH, s.GripW, s.GripH}
}

// GripAt returns the band and gap index of the grip at (x, y), or
// (-1, -1).
func (f *TextFlow) GripAt(x, y float64) (band, gap int) {
	g := f.gripAt(f.Layout().Bands, x, y)
	return g.band, g.gap
}

func (f *TextFlow) gripAt(bands []Band, x, y float64) grip {
	for bi, b := range bands {
		for g := range len(b.Xs) - 1 {
			if inRect(x, y, f.gripRect(b, g)) {
				return grip{bi, g}
			}
		}
	}
	return noGrip
}

// Dragging reports whether a gap is being dragged.
func (f *TextFlow) Dragging() bool { return f.drag != nil }

// MouseDown starts dragging a gap if (x, y) is on its grip (edit mode).
func (f *TextFlow) MouseDown(x, y float64) bool {
	x, y = f.vpt(x, y)
	return f.mouseDown(x, y)
}

// mouseDown is MouseDown for a point in the plane the text is laid out in.
func (f *TextFlow) mouseDown(x, y float64) bool {
	if id, ok := f.footnoteAt(x, y); ok && f.OnFootnote != nil && f.mode == ModeEdit {
		f.OnFootnote(id)
		return true
	}
	// A click on a row of a table of contents goes to the heading it lists.
	if off, ok := f.tocTargetAt(x, y); ok && !f.suggestionsOpen() {
		f.GotoOffset(off)
		return true
	}
	if f.suggestionsOpen() {
		if f.sugg.MouseDown(x, y) {
			return true
		}
		f.closeSuggestions()
	}
	if f.objectClickAt(x, y) {
		return true // a form field's icon or box
	}
	if f.mode != ModeEdit {
		if f.openLinkAt(x, y) {
			return true
		}
		return f.beginSelect(x, y)
	}
	if f.toggleCheckAt(x, y) {
		return true
	}
	bands := f.Layout().Bands
	g := f.gripAt(bands, x, y)
	if g == noGrip || f.Vertical {
		return f.beginSelect(x, y)
	}
	f.ClearSelection()
	b := bands[g.band]
	f.drag = &flowDrag{
		grip: g, cfg: b.Cfg, widths: slices.Clone(b.Widths), startX: x,
		startL: b.Widths[g.gap], startR: b.Widths[g.gap+1],
	}
	return true
}

// MouseMove drags the gap (keeping the two neighbours' combined width)
// or updates which grip is hovered.
func (f *TextFlow) MouseMove(x, y float64) bool {
	x, y = f.vpt(x, y)
	hover := false
	if f.sel.dragging {
		f.hoverLink = ""
		f.setHoverComment("", [4]float64{})
	} else {
		hover = f.updateLinkHover(x, y)
	}
	return f.mouseMove(x, y) || hover
}

func (f *TextFlow) mouseMove(x, y float64) bool {
	if f.sel.dragging {
		o, _ := f.offsetAt(x, y)
		changed := o != f.sel.caret
		f.sel.caret, f.sel.after = o, false
		return changed
	}
	if f.mode != ModeEdit {
		return false
	}
	if d := f.drag; d != nil {
		total := d.startL + d.startR
		left := min(max(d.startL+x-d.startX, f.minColumn()), total-f.minColumn())
		if left < f.minColumn() || d.widths[d.grip.gap] == left { // pair narrower than two minimums, or no change
			return false
		}
		d.widths[d.grip.gap], d.widths[d.grip.gap+1] = left, total-left
		f.setConfigWidths(d.cfg, slices.Clone(d.widths))
		f.notify()
		return true
	}
	h := f.gripAt(f.Layout().Bands, x, y)
	changed := h != f.hover
	f.hover = h
	return changed
}

// MouseUp ends a drag.
func (f *TextFlow) MouseUp(x, y float64) bool {
	if f.sel.dragging {
		f.sel.dragging = false
		return true
	}
	if f.drag == nil {
		return false
	}
	f.drag = nil
	return true
}

// MouseLeave clears the hovered grip (a drag in progress continues).
func (f *TextFlow) MouseLeave() bool {
	changed := f.hover != noGrip
	if f.hoverComment != "" {
		f.setHoverComment("", [4]float64{})
		changed = true
	}
	if f.hoverLink != "" {
		f.hoverLink, changed = "", true
		if f.OnHoverLink != nil {
			f.OnHoverLink("")
		}
	}
	f.hover = noGrip
	return changed
}

// CSSCursor implements CursorProvider: col-resize over a grip or while
// dragging.
func (f *TextFlow) CSSCursor() string {
	if f.mode == ModeEdit && (f.drag != nil || f.hover != noGrip) {
		return "col-resize"
	}
	if f.hoverLink != "" && f.mode != ModeEdit {
		return "pointer"
	}
	return ""
}

// --- drawing ---------------------------------------------------------

// Draw paints the text (and, for a single page, the page) and, in edit
// mode, the column guides and grips.
func (f *TextFlow) Draw() {
	p, cs := f.Painter, f.ChromeStyle
	if f.Pages == nil && !f.Transparent {
		p.SetFillStyle(cs.PageBG)
		p.FillRect(f.X, f.Y, f.Width, f.Height)
		p.SetStrokeStyle(cs.PageBorder)
		p.SetLineWidth(1)
		p.StrokeRect(f.X+0.5, f.Y+0.5, f.Width-1, f.Height-1)
	}

	lay := f.Layout()
	f.lastLay = lay
	if f.Vertical {
		f.drawVertical(lay)
		return
	}
	f.drawBody(lay)
}

// drawBody draws the text of a layout, its marks, and in edit mode the column grips.
func (f *TextFlow) drawBody(lay Layout) {
	p, cs := f.Painter, f.ChromeStyle
	f.drawSearch(lay)
	f.drawReadMarks(lay)
	f.drawSelection(lay)
	f.drawPeerSelections(lay)
	defer f.drawSquiggles(lay)
	defer f.drawBookmarks()
	defer f.drawCarets()
	defer f.drawGhost(lay)
	defer f.drawPreedit(lay)
	defer f.drawPeerCarets()
	if !f.Vertical {
		defer f.drawFootnotes(lay)
	}
	p.SetTextAlign("left")
	p.SetTextBaseline("alphabetic")
	for _, ln := range lay.Lines {
		if ln.QuoteBar != "" {
			p.SetFillStyle(ln.QuoteBar)
			p.FillRect(ln.BarX, ln.Y, 3, ln.H)
		}
		if c := ln.DropCap; c != nil {
			p.SetFont(c.Font)
			p.SetFillStyle(orStr(c.Color, "#222222"))
			p.FillText(c.Text, c.X, c.Baseline)
		}
		if m := ln.Marker; m != nil {
			p.SetFont(m.Font)
			p.SetFillStyle(m.Color)
			p.FillText(m.Text, m.X, ln.Baseline)
		}
		for _, r := range ln.Runs {
			x, y := ln.X+r.X, ln.Baseline+r.DY
			if r.Background != "" {
				p.SetFillStyle(r.Background)
				p.FillRect(x, ln.Y, r.W, ln.H)
			} else if r.Comment != "" && f.commentsOpen(r.Comment) {
				p.SetFillStyle(CommentHighlight)
				p.FillRect(x, ln.Y, r.W, ln.H)
			}
			p.SetFont(r.Font)
			p.SetFillStyle(r.Color)
			dp, _ := p.(DirectionPainter)
			if dp != nil && r.Level%2 == 1 {
				dp.SetDirection("rtl")
			}
			f.fillRun(r, ln, x, y)
			if dp != nil && r.Level%2 == 1 {
				dp.SetDirection("ltr")
			}
			if r.Underline {
				p.FillRect(x, y+1.5, r.W, 1)
			}
			if r.Strike {
				p.FillRect(x, y-r.Size*0.3, r.W, 1)
			}
		}
		if ln.Hyphen && len(ln.Runs) > 0 {
			r := ln.Runs[len(ln.Runs)-1]
			p.SetFont(r.Font)
			p.SetFillStyle(r.Color)
			p.FillText("-", ln.X+r.X+r.W, ln.Baseline+r.DY)
		}
		for _, o := range ln.Objects {
			f.drawObject(o)
		}
	}

	if f.mode != ModeEdit {
		return
	}
	for bi, b := range lay.Bands {
		_, py, _, ph := f.pageRect(b.Page)
		for g := range len(b.Xs) - 1 {
			active := f.hover == grip{bi, g} || (f.drag != nil && f.drag.grip == grip{bi, g})
			cx := b.Xs[g] + b.Widths[g] + b.Gap/2
			p.SetFillStyle(cs.GuideColor)
			p.FillRect(cx-0.5, b.Top, 1, py+ph-b.Top)

			r := f.gripRect(b, g)
			if active {
				p.SetFillStyle(cs.GripActiveColor)
			} else {
				p.SetFillStyle(cs.GripColor)
			}
			p.FillRect(r[0], r[1], r[2], r[3])
			p.SetFillStyle("#ffffff")
			for k := range 3 { // three vertical lines: a column grip
				p.FillRect(r[0]+r[2]/2-5+float64(k)*4, r[1]+4, 2, r[3]-8)
			}
		}
	}
}

var _ Component = (*TextFlow)(nil)
