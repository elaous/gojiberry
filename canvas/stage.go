package canvas

// Optional interfaces a Stage dispatches to.
type (
	// Drawer is anything a Stage can draw.
	Drawer interface{ Draw() }
	// ViewportSetter is told the visible part before each draw.
	ViewportSetter interface{ SetViewport(x, y, w, h float64) }
	// Overlayer draws on top of everything else in the Stage.
	Overlayer interface{ DrawOverlay() }
	// DoubleClicker handles a double click.
	DoubleClicker interface{ DoubleClick(x, y float64) bool }
	// MouseWheeler handles a wheel turn at (x, y) with deltas.
	MouseWheeler interface {
		MouseWheel(x, y, dx, dy float64) bool
	}
	// PainterSetter receives the Stage's painter when it is bound.
	PainterSetter interface{ SetPainter(ImagePainter) }
	// MouseDowner handles a primary-button press (canvas-local x, y).
	MouseDowner interface{ MouseDown(x, y float64) bool }
	// ModMouseDowner is MouseDowner that also receives modifier keys.
	ModMouseDowner interface {
		MouseDownMods(x, y float64, m Mods) bool
	}
	// MouseMover handles pointer movement.
	MouseMover interface{ MouseMove(x, y float64) bool }
	// MouseUpper handles a button release.
	MouseUpper interface{ MouseUp(x, y float64) bool }
	// MouseLeaver handles the pointer leaving the canvas.
	MouseLeaver interface{ MouseLeave() bool }
	// KeyHandler handles a key press; reports whether it consumed it.
	KeyHandler interface {
		HandleKey(key string, shift bool) bool
	}
	// TextInserter handles typed / pasted text.
	TextInserter interface{ InsertText(s string) bool }
	// Focuser reports whether the component needs keyboard focus.
	Focuser interface{ WantsFocus() bool }
	// CursorProvider returns a CSS cursor for the last pointer position.
	CursorProvider interface{ CSSCursor() string }
	// LinkHandlerSetter receives the Stage's handler for opening a link.
	LinkHandlerSetter interface{ SetLinkHandler(open func(url string)) }
	// Blinker toggles a caret's visibility; true means redraw.
	Blinker interface{ SetCaretVisible(on bool) bool }
)

// Mods are the modifier keys held during a pointer event.
type Mods struct{ Ctrl, Shift, Alt, Meta bool }

// Stage draws components onto one canvas and routes pointer and key events.
type Stage struct {
	composer      Composer // the component showing an input method's preedit, if any
	Painter       ImagePainter
	Width, Height float64 // canvas size, cleared on every Draw
	Components    []Drawer
	// Keymap binds key chords to Commands (nil means DefaultKeymap).
	Keymap Keymap
	// Formats is the format clipboard shared by the components.
	Formats *FormatClipboard
	// Features are the registered editing features.
	Features *Registry
	// Clipboard is the system clipboard for feature commands.
	Clipboard Clipboard
	// Redraw, if set, redraws the stage.
	Redraw func()
	// Prompt asks the user for a line of text.
	Prompt func(message, initial string) (string, bool)
	// EditNote asks for footnote text in a dialog with a length counter.
	EditNote func(title, initial string, max int, done func(text string, ok bool))
	// OpenLink opens a clicked link's URL.
	OpenLink func(url string)
	// OnDraw, if set, runs after every Draw.
	OnDraw func()
	// Role is what the user may do; the zero value is owner.
	Role Role
	// OnFontUse is told each font cut the first time content uses it.
	OnFontUse func(FontUse)
	fontsSeen map[FontUse]bool
}

// NewStage returns a stage for a width x height canvas.
func NewStage(p ImagePainter, width, height float64, components ...Drawer) *Stage {
	s := &Stage{Painter: p, Width: width, Height: height, Formats: &FormatClipboard{}, Features: NewRegistry(DefaultFeatures()...)}
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
		if fs, ok := c.(FormatClipboardSetter); ok && s.Formats != nil {
			fs.SetFormatClipboard(s.Formats)
		}
		if ls, ok := c.(LinkHandlerSetter); ok {
			ls.SetLinkHandler(func(url string) {
				if s.OpenLink != nil {
					s.OpenLink(url)
				}
			})
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
	m = s.Role.Mode(m)
	for _, c := range s.Components {
		if comp, ok := c.(Component); ok {
			comp.SetMode(m)
		} else if cm, ok := c.(interface{ SetContentMode(Mode) }); ok {
			cm.SetContentMode(m)
		}
	}
}

// Paint draws every component without clearing first.
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
	s.reportFonts()
	if s.OnDraw != nil {
		s.OnDraw()
	}
}

// each calls fn on every component, topmost first, until one returns true.
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
func (s *Stage) MouseDown(x, y float64) bool { return s.MouseDownMods(x, y, Mods{}) }

// MouseDownMods is MouseDown with the modifier keys held.
func (s *Stage) MouseDownMods(x, y float64, m Mods) bool {
	return s.each(func(c Drawer) bool {
		if h, ok := c.(ModMouseDowner); ok {
			return h.MouseDownMods(x, y, m)
		}
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

// MouseWheel offers a wheel turn to components, topmost first.
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

// HandleKey offers a key to components, topmost first, until one consumes it.
func (s *Stage) HandleKey(key string, shift bool) bool {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if h, ok := s.Components[i].(KeyHandler); ok && h.HandleKey(key, shift) {
			return true
		}
	}
	return false
}

// SetFormatClipboard replaces the format clipboard for every component.
func (s *Stage) SetFormatClipboard(c *FormatClipboard) {
	s.Formats = c
	for _, d := range s.Components {
		if fs, ok := d.(FormatClipboardSetter); ok {
			fs.SetFormatClipboard(c)
		}
	}
}

// HandleChord runs the command bound to a key chord.
func (s *Stage) HandleChord(chord string) bool {
	km := s.Keymap
	if km == nil {
		km = DefaultKeymap()
	}
	cmd, ok := km[chord]
	if !ok && s.Features != nil {
		cmd, ok = s.Features.keys[chord]
	}
	if !ok {
		return false
	}
	return s.RunCommand(cmd)
}

// RunCommand offers cmd to components, topmost first, until one applies it.
func (s *Stage) RunCommand(cmd Command) bool {
	if s.Features != nil && s.Features.Has(cmd) {
		t := s.EditTarget()
		if t == nil && cmd == CmdCommentAdd && s.Role.CanComment() {
			t = s.commentTarget() // commenting needs no edit mode
		}
		if t != nil || s.Features.global[cmd] {
			ctx := &CommandContext{Target: t, Clipboard: &s.Clipboard, Formats: s.Formats, Redraw: s.Redraw, Prompt: s.Prompt, EditNote: s.EditNote, OpenLink: s.OpenLink}
			if s.Features.Run(cmd, ctx) {
				return true
			}
		}
	}
	for i := len(s.Components) - 1; i >= 0; i-- {
		if h, ok := s.Components[i].(Commander); ok && h.HandleCommand(cmd) {
			return true
		}
	}
	return false
}

// Copy asks components, topmost first, for the text to copy.
func (s *Stage) Copy() (string, bool) {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if c, ok := s.Components[i].(Copier); ok {
			if text, ok := c.CopyText(); ok {
				return text, true
			}
		}
	}
	return "", false
}

// Cut asks components, topmost first, to cut their selection.
func (s *Stage) Cut() (string, bool) {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if c, ok := s.Components[i].(Cutter); ok {
			if text, ok := c.CutText(); ok {
				return text, true
			}
		}
	}
	return "", false
}

// Paste offers clipboard text to components, topmost first.
func (s *Stage) Paste(text string) bool {
	for i := len(s.Components) - 1; i >= 0; i-- {
		switch c := s.Components[i].(type) {
		case Paster:
			if c.PasteText(text) {
				return true
			}
		case TextInserter:
			if c.InsertText(text) {
				return true
			}
		}
	}
	return false
}

// InsertText offers typed text to components, topmost first.
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

// Cursor returns the CSS cursor the topmost component wants.
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

// EditTarget returns the topmost edited component's EditTarget, or nil.
func (s *Stage) EditTarget() EditTarget {
	for i := len(s.Components) - 1; i >= 0; i-- {
		if e, ok := s.Components[i].(Editable); ok {
			if t := e.EditTarget(); t != nil {
				return t
			}
		}
	}
	return nil
}
