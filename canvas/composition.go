package canvas

type Composer interface {
	// SetComposition shows preedit text at the caret; "" ends it.
	SetComposition(text string) bool
	// CompositionAnchor is the caret's x, y and height while composing.
	CompositionAnchor() (x, y, h float64, ok bool)
}

func (s *Stage) SetComposition(text string) bool {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if c, ok := s.Components[i].(Composer); ok && c.SetComposition(text) {
			s.composer = c
			return true
		}
	}
	s.composer = nil
	return false
}

// CompositionAnchor is the caret of the component showing the preedit.
func (s *Stage) CompositionAnchor() (x, y, h float64, ok bool) {
	if s.composer == nil {
		return 0, 0, 0, false
	}
	return s.composer.CompositionAnchor()
}

// SetComposition draws the preedit at the caret, underlined.
func (f *TextFlow) SetComposition(text string) bool {
	if text != "" && (!f.Editing() || len(f.Cursors()) != 1) {
		return false
	}
	f.preedit = text
	return true
}

// CompositionAnchor implements Composer.
func (f *TextFlow) CompositionAnchor() (x, y, h float64, ok bool) {
	if f.preedit == "" || len(f.Cursors()) != 1 {
		return 0, 0, 0, false
	}
	return f.caretRect(f.Cursors()[0])
}

func (f *TextFlow) drawPreedit(lay Layout) {
	if f.preedit == "" || f.Painter == nil || !f.Editing() || len(f.Cursors()) != 1 {
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
	font, colour, baseline := f.Style.Font(), f.Style.Color, y+h*0.78
	for i := range lay.Lines {
		ln := &lay.Lines[i]
		if ln.Y <= y+0.5 && y < ln.Y+ln.H && ln.Start <= c.Caret && c.Caret <= ln.End {
			baseline = ln.Baseline
			for _, r := range ln.Runs {
				if r.Start < c.Caret && c.Caret <= r.End || r.Start <= c.Caret && font == f.Style.Font() {
					font, colour = r.Font, r.Color
				}
			}
		}
	}
	if colour == "" {
		colour = "#222222"
	}
	p := f.Painter
	p.SetFont(font)
	w := p.MeasureTextWidth(f.preedit)
	p.SetFillStyle(f.ChromeStyle.PageBG)
	p.FillRect(x, y, w+2, h)
	p.SetTextAlign("left")
	p.SetTextBaseline("alphabetic")
	p.SetFillStyle(colour)
	p.FillText(f.preedit, x+1, baseline)
	p.FillRect(x+1, baseline+2, w, 1)
}

// SetComposition implements Composer: the content shows the preedit.
func (sv *ScrollView) SetComposition(text string) bool { return sv.content.SetComposition(text) }

func (sv *ScrollView) CompositionAnchor() (x, y, h float64, ok bool) {
	x, y, h, ok = sv.content.CompositionAnchor()
	return sv.X - sv.ScrollX + x, sv.Y - sv.ScrollY + y, h, ok
}

var (
	_ Composer = (*TextFlow)(nil)
	_ Composer = (*ScrollView)(nil)
)
