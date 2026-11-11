package canvas

func (f *TextFlow) LangAt(o int) string { return f.typeFormat(f.flatten(), o).Lang }

const ghostAlpha = 0.5

func (f *TextFlow) drawGhost(lay Layout) {
	if f.Ghost == "" || !f.Editing() || f.Painter == nil || len(f.Cursors()) != 1 {
		return
	}
	c := f.Cursors()[0]
	if f.skipCaretOnOtherPage(c.Caret) {
		return
	}
	x, y, h, ok := f.caretRect(c)
	if !ok {
		return
	}
	var ln *Line
	for i := range lay.Lines {
		if l := &lay.Lines[i]; l.Y <= y+0.5 && y < l.Y+l.H && l.Start <= c.Caret && c.Caret <= l.End {
			ln = l
		}
	}
	font, colour, baseline := f.Style.Font(), f.Style.Color, y+h*0.78
	var run *Run
	if ln != nil {
		baseline = ln.Baseline
		for i := range ln.Runs {
			if r := &ln.Runs[i]; r.Start < c.Caret && c.Caret <= r.End || run == nil && r.Start <= c.Caret {
				run = r
			}
		}
	}
	if run != nil {
		font, colour = run.Font, run.Color
	}
	if colour == "" {
		colour = "#222222"
	}
	p := f.Painter
	ip, fade := p.(ImagePainter)
	if fade {
		ip.SetGlobalAlpha(ghostAlpha)
		defer ip.SetGlobalAlpha(1)
	}
	p.SetTextAlign("left")
	p.SetTextBaseline("alphabetic")
	p.SetFont(font)
	atEnd := ln == nil || c.Caret >= ln.End || ln.End <= ln.Start
	if atEnd {
		p.SetFillStyle(colour)
		p.FillText(f.Ghost, x+1, baseline)
		return
	}
	tag := f.Ghost + "  Tab"
	w := p.MeasureTextWidth(tag)
	p.SetFillStyle("#ffffff")
	p.FillRect(x, y+h, w+8, h*0.9)
	p.SetStrokeStyle(colour)
	p.SetLineWidth(1)
	p.StrokeRect(x+0.5, y+h+0.5, w+7, h*0.9-1)
	p.SetFillStyle(colour)
	p.FillText(tag, x+4, y+h+h*0.7)
}
