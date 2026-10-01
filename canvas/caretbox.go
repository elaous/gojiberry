package canvas

// CaretBoxer is implemented by editable components that can say where
// their caret is, so a ScrollView can keep it in view while typing.
type CaretBoxer interface {
	// CaretBox returns the caret's x, top y and height in the component's
	// coordinates; ok is false when there is no caret to show.
	CaretBox() (x, y, h float64, ok bool)
}

// CaretBox implements CaretBoxer for the first (primary) cursor while the
// text is being edited.
func (f *TextFlow) CaretBox() (x, y, h float64, ok bool) {
	if f.mode != ModeEdit || !f.WantsFocus() {
		return 0, 0, 0, false
	}
	return f.caretRect(f.Cursors()[0])
}

// CaretBox implements CaretBoxer.
func (ft *FloatingText) CaretBox() (x, y, h float64, ok bool) {
	if !ft.WantsFocus() || !ft.editing {
		return 0, 0, 0, false
	}
	return ft.inner.CaretRect(ft.caret)
}

// CaretBox returns the caret of the topmost focused component that has
// one.
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

// scrollMargin is the room kept around a caret that is scrolled into view.
const scrollMargin = 16

// RevealCaret scrolls the minimum needed to bring the caret into view,
// and reports whether it moved.
func (sv *ScrollView) RevealCaret() bool {
	x, y, h, ok := sv.content.CaretBox()
	if !ok {
		return false
	}
	nx, ny := sv.ScrollX, sv.ScrollY
	switch {
	case y < ny+scrollMargin:
		ny = y - scrollMargin
	case y+h > ny+sv.Height-scrollMargin:
		ny = y + h + scrollMargin - sv.Height
	}
	switch {
	case x < nx+scrollMargin:
		nx = x - scrollMargin
	case x > nx+sv.Width-scrollMargin:
		nx = x + scrollMargin - sv.Width
	}
	return sv.ScrollTo(nx, ny)
}
