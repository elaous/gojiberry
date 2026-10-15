package canvas

import "math"

// Vertical text (TextFlow.Vertical): lines run from the top of the page to the bottom and follow one another
// from the right of the page to the left (vertical-rl, as in Chinese, Japanese and Korean books).
//
// The text is laid out as ordinary horizontal text in a virtual plane, and the plane is turned a quarter turn
// when it is drawn: a virtual point (u, v) of page p is the physical point
//
//	X = px + pw - (v - vy), Y = py + (u - vx)
//
// where (px, py, pw, ph) is the page's text area and (vx, vy) the origin of the page in the virtual plane (pages
// are stacked there so that no two overlap, whatever the shape of the page). A virtual line, left to right, is a
// column going down; the line that comes next, below it, is the one to its left. Everything that works in the
// plane (the layout, the carets, the selection, hit testing) is unchanged; the places where the plane meets the
// screen convert: drawing (drawVertical), the mouse (toVirtual), the keys (verticalKey) and the box the scroll
// view keeps in sight (physicalCaret). Latin text and digits are turned with the plane (they read sideways, as they
// do in vertical Japanese); ideographs, kana and hangul are turned back, so that they stand upright.

// verticalGap is the space between two pages in the virtual plane.
const verticalGap = 24

// physRect is the rectangle the text of a page flows into, on the screen.
func (f *TextFlow) physRect(page int) (x, y, w, h float64) {
	if f.Pages != nil {
		x, y, w, h = f.Pages.ContentRect(page)
	} else {
		x, y, w, h = f.X, f.Y, f.Width, f.Height
	}
	return x, y, w, max(h-f.footnoteBand(), 0)
}

// virtualOrigin is where page's text area starts in the virtual plane.
func (f *TextFlow) virtualOrigin(page int) (vx, vy float64) {
	px, _, pw, _ := f.physRect(page)
	return px, float64(page) * (pw + verticalGap)
}

// toVirtual turns a point on the screen into the point of the virtual plane it is, and the page it is on: the
// page whose text area holds it, or the nearest.
func (f *TextFlow) toVirtual(x, y float64) (u, v float64, page int) {
	best, bestD := 0, math.Inf(1)
	for i := range f.pageCount() {
		px, py, pw, ph := f.physRect(i)
		dx, dy := 0.0, 0.0
		switch {
		case x < px:
			dx = px - x
		case x > px+pw:
			dx = x - (px + pw)
		}
		switch {
		case y < py:
			dy = py - y
		case y > py+ph:
			dy = y - (py + ph)
		}
		if d := dx*dx + dy*dy; d < bestD {
			best, bestD = i, d
		}
	}
	px, py, pw, _ := f.physRect(best)
	vx, vy := f.virtualOrigin(best)
	return vx + (y - py), vy + (px + pw - x), best
}

// vpt is toVirtual for a flow that may not be vertical.
func (f *TextFlow) vpt(x, y float64) (float64, float64) {
	if !f.Vertical {
		return x, y
	}
	u, v, _ := f.toVirtual(x, y)
	return u, v
}

// toPhysical is the screen point of a point of the virtual plane on the page.
func (f *TextFlow) toPhysical(u, v float64, page int) (x, y float64) {
	px, py, pw, _ := f.physRect(page)
	vx, vy := f.virtualOrigin(page)
	return px + pw - (v - vy), py + (u - vx)
}

// lineOffsetPage is the page the line holding the offset is on.
func (f *TextFlow) lineOffsetPage(o int) int {
	lines := f.Layout().Lines
	for _, ln := range lines {
		if o <= ln.End {
			return ln.Page
		}
	}
	if n := len(lines); n > 0 {
		return lines[n-1].Page
	}
	return 0
}

// physicalCaret is the box of a caret on the screen, as CaretBox says it: the point (x, y) and a height. In
// vertical text a caret is a short horizontal stroke, so the box is a point on it and the height of the stroke's
// thickness, enough to keep it in view.
func (f *TextFlow) physicalCaret(x, y, h float64, ok bool, o int) (float64, float64, float64, bool) {
	if !ok || !f.Vertical {
		return x, y, h, ok
	}
	page := f.lineOffsetPage(o)
	px, py := f.toPhysical(x, y+h/2, page)
	return px, py, 2, true
}

// verticalKey turns an arrow key on the screen into the key for the virtual plane: down the column is forward
// along the line, and the column to the left is the next line.
func (f *TextFlow) verticalKey(key string) string {
	if !f.Vertical {
		return key
	}
	switch key {
	case "ArrowUp":
		return "ArrowLeft"
	case "ArrowDown":
		return "ArrowRight"
	case "ArrowLeft":
		return "ArrowDown"
	case "ArrowRight":
		return "ArrowUp"
	}
	return key
}

// SetVertical sets the writing mode of the flow: true for vertical text.
func (f *TextFlow) SetVertical(on bool) { f.Vertical = on }

// uprightRune reports whether a character stands upright in vertical text: the East Asian ideographs, kana,
// hangul and the full-width forms. The rest (Latin letters, digits, Arabic ...) is turned with the line.
func uprightRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x11FF, // hangul jamo
		r >= 0x2E80 && r <= 0x9FFF, // CJK radicals, punctuation, kana, ideographs
		r >= 0xA960 && r <= 0xA97F,
		r >= 0xAC00 && r <= 0xD7FF, // hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // compatibility ideographs
		r >= 0xFE30 && r <= 0xFE4F, // vertical forms
		r >= 0xFF00 && r <= 0xFFEF, // full-width forms
		r >= 0x20000 && r <= 0x3FFFF:
		return true
	}
	return false
}

// drawVertical draws a vertical flow: the sheet, then each page's lines in the plane turned onto the page.
func (f *TextFlow) drawVertical(lay Layout) {
	ip, ok := f.Painter.(ImagePainter)
	if !ok {
		f.drawBody(lay)
		return
	}
	defer func() { f.vdrawing = false }()
	for page := range f.pageCount() {
		sub := Layout{Pages: lay.Pages, Overflow: lay.Overflow, Breaks: lay.Breaks}
		for _, ln := range lay.Lines {
			if ln.Page == page {
				sub.Lines = append(sub.Lines, ln)
			}
		}
		px, py, pw, _ := f.physRect(page)
		vx, vy := f.virtualOrigin(page)
		ip.Save()
		ip.Translate(px+pw, py)
		ip.Rotate(math.Pi / 2)
		ip.Translate(0-vx, 0-vy)
		f.vpage, f.vdrawing = page, true
		f.drawBody(sub)
		ip.Restore()
	}
	f.drawFootnotes(lay)
}

// skipCaretOnOtherPage says whether the caret at offset o is not on the page being drawn (a vertical flow draws
// one page at a time).
func (f *TextFlow) skipCaretOnOtherPage(o int) bool {
	return f.Vertical && f.vdrawing && f.lineOffsetPage(o) != f.vpage
}

// fillRun draws a run's text at (x, y). In vertical text the characters that stand upright are turned back about
// their own centres and the rest is drawn along the line.
func (f *TextFlow) fillRun(r Run, ln Line, x, y float64) {
	p := f.Painter
	ip, ok := p.(ImagePainter)
	if !f.Vertical || !ok {
		p.FillText(r.Text, x, y)
		return
	}
	rs := []rune(r.Text)
	at := x
	for i := 0; i < len(rs); {
		j := i + 1
		up := uprightRune(rs[i])
		for j < len(rs) && uprightRune(rs[j]) == up {
			j++
		}
		seg := string(rs[i:j])
		if !up {
			p.FillText(seg, at, y)
			at += p.MeasureTextWidth(seg)
		} else {
			for _, c := range rs[i:j] {
				w := p.MeasureTextWidth(string(c))
				ip.Save()
				ip.Translate(at+w/2, ln.Y+ln.H/2)
				ip.Rotate(-math.Pi / 2)
				p.SetTextAlign("center")
				p.SetTextBaseline("middle")
				p.FillText(string(c), 0, 0)
				ip.Restore()
				at += w
			}
			p.SetTextAlign("left")
			p.SetTextBaseline("alphabetic")
		}
		i = j
	}
}

// drawObject draws an inline object. In vertical text the object stands upright: the plane reserved its size
// the other way round, and it is turned back about its centre.
func (f *TextFlow) drawObject(o PlacedObject) {
	ip, ok := f.Painter.(ImagePainter)
	if !f.Vertical || !ok {
		o.Object.DrawAt(f.Painter, o.X, o.Y)
		return
	}
	ip.Save()
	ip.Translate(o.X+o.W/2, o.Y+o.H/2)
	ip.Rotate(-math.Pi / 2)
	o.Object.DrawAt(f.Painter, -o.H/2, -o.W/2)
	ip.Restore()
}
