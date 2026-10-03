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
	// ModMouseDowner is MouseDowner that also wants the modifier keys
	// held (Ctrl/Cmd+click adds a cursor).
	ModMouseDowner interface {
		MouseDownMods(x, y float64, m Mods) bool
	}
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
	// LinkHandlerSetter receives the Stage's handler for opening a link.
	LinkHandlerSetter interface{ SetLinkHandler(open func(url string)) }
	// Blinker toggles a caret's visibility; true means redraw.
	Blinker interface{ SetCaretVisible(on bool) bool }
)

// Mods are the modifier keys held during a pointer event.
type Mods struct{ Ctrl, Shift, Alt, Meta bool }

// Stage draws several components onto one canvas, back to front, and
// routes pointer and keyboard events to them (topmost first). Only
// the browser glue -- Bind -- is js/wasm-only.
type Stage struct {
	Painter       ImagePainter
	Width, Height float64 // canvas size, cleared on every Draw
	Components    []Drawer
	// Keymap binds key chords to Commands (nil means DefaultKeymap).
	Keymap Keymap
	// Formats is the format clipboard shared by the components.
	Formats *FormatClipboard
	// Features are the registered editing features (multi-cursor, line
	// operations, Markdown clipboard...); NewStage installs
	// DefaultFeatures. Their chords are added to the keymap wherever the
	// Keymap itself has no binding for the chord.
	Features *Registry
	// Clipboard is the system clipboard for feature commands; Bind
	// fills it in.
	Clipboard Clipboard
	// Redraw, if set, redraws the stage (Bind sets it); feature
	// commands that finish later call it.
	Redraw func()
	// Prompt asks the user for a line of text; Bind sets it to the
	// browser's prompt. Commands that need it decline without it.
	Prompt func(message, initial string) (string, bool)
	// OpenLink opens a clicked link's URL; Bind sets it to open a new
	// browser tab.
	OpenLink func(url string)
	// OnDraw, if set, is called after every Draw (demos use it to
	// publish state).
	OnDraw func()
	// Role is what the user may do (the zero value is the owner); a
	// viewer's components stay in view mode. See SetRole.
	Role Role
	// OnFontUse is called, at a draw, the first time the content uses a
	// font cut (family, weight, style): the moment to download it. See
	// the fonts package's Library.
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
	s.reportFonts()
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

// SetFormatClipboard replaces the format clipboard and hands it to every
// component (ScrollView passes it on to its content).
func (s *Stage) SetFormatClipboard(c *FormatClipboard) {
	s.Formats = c
	for _, d := range s.Components {
		if fs, ok := d.(FormatClipboardSetter); ok {
			fs.SetFormatClipboard(c)
		}
	}
}

// HandleChord runs the command bound to a key chord (see Keymap and
// Chord), offering it to components topmost first until one applies it.
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
		if t != nil {
			ctx := &CommandContext{Target: t, Clipboard: &s.Clipboard, Formats: s.Formats, Redraw: s.Redraw, Prompt: s.Prompt, OpenLink: s.OpenLink}
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

// Paste offers clipboard text to components, topmost first: a Paster
// takes it as a paste, others as typed text.
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

// EditTarget returns the EditTarget of the topmost component being
// edited, or nil.
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
