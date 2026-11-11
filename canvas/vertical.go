package canvas

import "math"

const verticalGap = 24

func (f *TextFlow) physRect(page int) (x, y, w, h float64) {
	x, y, w, h = f.pageContent(page)
	return x, y, w, max(h-f.footnoteBandOf(page), 0)
}

func (f *TextFlow) virtualOrigin(page int) (vx, vy float64) {
	px, _, pw, _ := f.physRect(page)
	return px, float64(page) * (pw + verticalGap)
}

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

func (f *TextFlow) vpt(x, y float64) (float64, float64) {
	if !f.Vertical {
		return x, y
	}
	u, v, _ := f.toVirtual(x, y)
	return u, v
}

func (f *TextFlow) toPhysical(u, v float64, page int) (x, y float64) {
	px, py, pw, _ := f.physRect(page)
	vx, vy := f.virtualOrigin(page)
	return px + pw - (v - vy), py + (u - vx)
}

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

func (f *TextFlow) physicalCaret(x, y, h float64, ok bool, o int) (float64, float64, float64, bool) {
	if !ok || !f.Vertical {
		return x, y, h, ok
	}
	page := f.lineOffsetPage(o)
	px, py := f.toPhysical(x, y+h/2, page)
	return px, py, 2, true
}

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

func (f *TextFlow) SetVertical(on bool) { f.Vertical = on }

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

func (f *TextFlow) skipCaretOnOtherPage(o int) bool {
	return f.Vertical && f.vdrawing && f.lineOffsetPage(o) != f.vpage
}

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
		colour, tinted := f.typedColour(r, rs, i)
		same := func(k int) bool {
			c, in := f.typedColour(r, rs, k)
			return uprightRune(rs[k]) == uprightRune(rs[i]) && in == tinted && c == colour
		}
		j := i + 1
		for j < len(rs) && same(j) {
			j++
		}
		up := uprightRune(rs[i])
		if tinted {
			p.SetFillStyle(colour)
		} else {
			p.SetFillStyle(r.Color)
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
	p.SetFillStyle(r.Color)
}

func (f *TextFlow) drawObject(o PlacedObject) {
	ip, ok := f.Painter.(ImagePainter)
	if !f.Vertical || !ok {
		o.Object.DrawAt(f.Painter, o.X, o.Y)
		f.drawBusy(o)
		return
	}
	ip.Save()
	ip.Translate(o.X+o.W/2, o.Y+o.H/2)
	ip.Rotate(-math.Pi / 2)
	o.Object.DrawAt(f.Painter, -o.H/2, -o.W/2)
	ip.Restore()
}
