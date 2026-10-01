package canvas

// The optional interfaces below are what a Stage dispatches to. A
// component implements only the ones it needs. For the pointer methods
// the result means "state changed, redraw".
type (
	// Drawer is anything a Stage can draw. Components (which also have
	// view/edit modes) are Drawers; so are modeless ones like Menu and
	// ScrollView.
	Drawer interface{ Draw() }
	// ViewportSetter is told, before each draw, which part of its
	// coordinate space is visible (a ScrollView's viewport, in content
	// coordinates) so popups can stay inside it.
	ViewportSetter interface{ SetViewport(x, y, w, h float64) }
	// Overlayer draws on top of everything else in the Stage (a menu,
	// editing chrome), after every component's Draw.
	Overlayer interface{ DrawOverlay() }
	// DoubleClicker handles a double click (the second mousedown of a
	// pair), after MouseDown has seen it.
	DoubleClicker interface{ DoubleClick(x, y float64) bool }
	// MouseWheeler handles a wheel turn at (x, y) with the given deltas
	// (pixels, positive = down/right). It reports whether it handled
	// the wheel, in which case the page itself should not scroll and
	// the stage is redrawn.
	MouseWheeler interface {
		MouseWheel(x, y, dx, dy float64) bool
	}
	// PainterSetter receives the Stage's painter when it is bound.
	PainterSetter interface{ SetPainter(ImagePainter) }
	// MouseDowner handles a primary-button press (canvas-local x, y).
	MouseDowner interface{ MouseDown(x, y float64) bool }
	// MouseMover handles pointer movement.
	MouseMover interface{ MouseMove(x, y float64) bool }
	// MouseUpper handles a button release.
	MouseUpper interface{ MouseUp(x, y float64) bool }
	// MouseLeaver handles the pointer leaving the canvas.
	MouseLeaver interface{ MouseLeave() bool }
	// KeyHandler handles a key press (DOM KeyboardEvent.key names) and
	// reports whether it was consumed.
	KeyHandler interface {
		HandleKey(key string, shift bool) bool
	}
	// TextInserter handles typed / pasted text.
	TextInserter interface{ InsertText(s string) bool }
	// Focuser says whether the component currently needs keyboard
	// focus (an active cell, an open menu), so the page's hidden input
	// can be focused or blurred to match.
	Focuser interface{ WantsFocus() bool }
	// CursorProvider returns a CSS cursor for the last pointer
	// position, or "" for no opinion.
	CursorProvider interface{ CSSCursor() string }
	// Blinker toggles a caret's visibility; true means redraw.
	Blinker interface{ SetCaretVisible(on bool) bool }
)

// Stage draws several components onto one canvas, back to front, and
// routes pointer and keyboard events to them (topmost first). Only
// the browser glue -- Bind -- is js/wasm-only.
type Stage struct {
	Painter       ImagePainter
	Width, Height float64 // canvas size, cleared on every Draw
	Components    []Drawer
	// OnDraw, if set, is called after every Draw (demos use it to
	// publish state).
	OnDraw func()
}

// NewStage returns a stage for a width x height canvas.
func NewStage(p ImagePainter, width, height float64, components ...Drawer) *Stage {
	s := &Stage{Painter: p, Width: width, Height: height}
	s.Add(components...)
	return s
}

// Add appends components (drawn above the existing ones).
func (s *Stage) Add(cs ...Drawer) {
	for _, c := range cs {
		s.Components = append(s.Components, c)
		if ps, ok := c.(PainterSetter); ok && s.Painter != nil {
			ps.SetPainter(s.Painter)
		}
	}
}

// SetPainter replaces the painter and hands it to every component.
func (s *Stage) SetPainter(p ImagePainter) {
	s.Painter = p
	for _, c := range s.Components {
		if ps, ok := c.(PainterSetter); ok {
			ps.SetPainter(p)
		}
	}
}

// SetMode puts every component in mode m.
func (s *Stage) SetMode(m Mode) {
	for _, c := range s.Components {
		if comp, ok := c.(Component); ok {
			comp.SetMode(m)
		} else if cm, ok := c.(interface{ SetContentMode(Mode) }); ok {
			cm.SetContentMode(m)
		}
	}
}

// Paint draws every component, back to front, without clearing first
// (ScrollView uses it to draw its content).
func (s *Stage) Paint() {
	for _, c := range s.Components {
		c.Draw()
	}
	for _, c := range s.Components {
		if o, ok := c.(Overlayer); ok {
			o.DrawOverlay()
		}
	}
}

// Draw clears the canvas and draws every component, back to front.
func (s *Stage) Draw() {
	s.Painter.ClearRect(0, 0, s.Width, s.Height)
	s.Paint()
	if s.OnDraw != nil {
		s.OnDraw()
	}
}

// each calls fn on every component, topmost first, and reports whether
// any call returned true. It does not stop early: every component sees
// every pointer event.
func (s *Stage) each(fn func(Drawer) bool) bool {
	changed := false
	for i := len(s.Components) - 1; i >= 0; i-- {
		if fn(s.Components[i]) {
			changed = true
		}
	}
	return changed
}

// MouseDown dispatches a press; true means redraw.
func (s *Stage) MouseDown(x, y float64) bool {
	return s.each(func(c Drawer) bool {
		h, ok := c.(MouseDowner)
		return ok && h.MouseDown(x, y)
	})
}

// MouseMove dispatches pointer movement; true means redraw.
func (s *Stage) MouseMove(x, y float64) bool {
	return s.each(func(c Drawer) bool {
		h, ok := c.(MouseMover)
		return ok && h.MouseMove(x, y)
	})
}

// DoubleClick dispatches a double click; true means redraw.
func (s *Stage) DoubleClick(x, y float64) bool {
	return s.each(func(c Drawer) bool {
		h, ok := c.(DoubleClicker)
		return ok && h.DoubleClick(x, y)
	})
}

// MouseUp dispatches a release; true means redraw.
func (s *Stage) MouseUp(x, y float64) bool {
	return s.each(func(c Drawer) bool {
		h, ok := c.(MouseUpper)
		return ok && h.MouseUp(x, y)
	})
}

// MouseWheel offers a wheel turn to components, topmost first, until
// one handles it.
func (s *Stage) MouseWheel(x, y, dx, dy float64) bool {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if h, ok := s.Components[i].(MouseWheeler); ok && h.MouseWheel(x, y, dx, dy) {
			return true
		}
	}
	return false
}

// MouseLeave dispatches the pointer leaving the canvas.
func (s *Stage) MouseLeave() bool {
	return s.each(func(c Drawer) bool {
		h, ok := c.(MouseLeaver)
		return ok && h.MouseLeave()
	})
}

// HandleKey offers a key to components, topmost first, until one
// consumes it.
func (s *Stage) HandleKey(key string, shift bool) bool {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if h, ok := s.Components[i].(KeyHandler); ok && h.HandleKey(key, shift) {
			return true
		}
	}
	return false
}

// InsertText offers typed text to components, topmost first, until one
// takes it.
func (s *Stage) InsertText(text string) bool {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if h, ok := s.Components[i].(TextInserter); ok && h.InsertText(text) {
			return true
		}
	}
	return false
}

// WantsFocus reports whether any component needs keyboard focus.
func (s *Stage) WantsFocus() bool {
	for _, c := range s.Components {
		if f, ok := c.(Focuser); ok && f.WantsFocus() {
			return true
		}
	}
	return false
}

// Cursor returns the CSS cursor the topmost opinionated component
// wants, or "default".
func (s *Stage) Cursor() string {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if cp, ok := s.Components[i].(CursorProvider); ok {
			if c := cp.CSSCursor(); c != "" {
				return c
			}
		}
	}
	return "default"
}

// SetCaretVisible forwards a blink phase; true means redraw.
func (s *Stage) SetCaretVisible(on bool) bool {
	return s.each(func(c Drawer) bool {
		b, ok := c.(Blinker)
		return ok && b.SetCaretVisible(on)
	})
}
