package canvas

// CaretBoxer reports where an editable component's caret is.
type CaretBoxer interface {
	CaretBox() (x, y, h float64, ok bool)
}

// CaretBox returns the primary cursor while text is edited.
func (f *TextFlow) CaretBox() (x, y, h float64, ok bool) {
	if f.mode != ModeEdit || !f.WantsFocus() {
		return 0, 0, 0, false
	}
	c := f.Cursors()[0]
	x, y, h, ok = f.caretRect(c)
	return f.physicalCaret(x, y, h, ok, c.Caret)
}

// CaretBox implements CaretBoxer.
func (ft *FloatingText) CaretBox() (x, y, h float64, ok bool) {
	if !ft.WantsFocus() || !ft.editing {
		return 0, 0, 0, false
	}
	return ft.inner.CaretRect(ft.caret)
}

// CaretBox returns the topmost focused caret.
func (s *Stage) CaretBox() (x, y, h float64, ok bool) {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if c, isC := s.Components[i].(CaretBoxer); isC {
			if x, y, h, ok = c.CaretBox(); ok {
				return x, y, h, true
			}
		}
	}
	return 0, 0, 0, false
}

// CaretBox implements CaretBoxer, in the ScrollView's own coordinates.
func (sv *ScrollView) CaretBox() (x, y, h float64, ok bool) {
	x, y, h, ok = sv.content.CaretBox()
	if !ok {
		return 0, 0, 0, false
	}
	return x - sv.ScrollX + sv.X, y - sv.ScrollY + sv.Y, h, true
}

const scrollMargin = 16

func (sv *ScrollView) RevealCaret() bool {
	x, y, h, ok := sv.content.CaretBox()
	if !ok {
		return false
	}
	nx, ny := sv.ScrollX, sv.ScrollY
	switch {
	case sv.Typewriter:
		at := sv.typewriterAt()
		ny = y + h/2 - sv.Height*at
	case y < ny+scrollMargin:
		ny = y - scrollMargin
	case y+h > ny+sv.Height-scrollMargin:
		ny = y + h + scrollMargin - sv.Height
	}
	switch {
	case sv.carriage():
		nx = x - sv.Width*sv.typewriterAt() // the place of the letter being typed
	case x < nx+scrollMargin:
		nx = x - scrollMargin
	case x > nx+sv.Width-scrollMargin:
		nx = x + scrollMargin - sv.Width
	}
	return sv.ScrollTo(nx, ny)
}

// TakeReveal returns the first pending GotoOffset position.
func (s *Stage) TakeReveal() (x, y, h float64, ok bool) {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if r, isR := s.Components[i].(Revealer); isR {
			if x, y, h, ok = r.TakeReveal(); ok {
				return x, y, h, true
			}
		}
	}
	return 0, 0, 0, false
}

// RevealPending scrolls a GotoOffset position to the top; reports movement.
func (sv *ScrollView) RevealPending() bool {
	_, y, _, ok := sv.content.TakeReveal()
	if !ok {
		return false
	}
	return sv.ScrollTo(sv.ScrollX, y-scrollMargin)
}

func (sv *ScrollView) afterEvent() {
	if !sv.RevealPending() {
		sv.RevealCaret()
	}
}
