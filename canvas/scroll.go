package canvas

// ScrollStyle controls how a ScrollView's scrollbars and frame look.
type ScrollStyle struct {
	BorderColor     string
	TrackColor      string
	ThumbColor      string
	ThumbHoverColor string
	BarSize         float64 // scrollbar thickness
	MinThumb        float64 // shortest a thumb can get
}

// DefaultScrollStyle is a light look matching DefaultTableStyle.
func DefaultScrollStyle() ScrollStyle {
	return ScrollStyle{
		BorderColor:     "#c4c7cd",
		TrackColor:      "rgba(0, 0, 0, 0.06)",
		ThumbColor:      "rgba(0, 0, 0, 0.35)",
		ThumbHoverColor: "rgba(0, 0, 0, 0.55)",
		BarSize:         10,
		MinThumb:        24,
	}
}

// ScrollView is a scroll container over larger content.
type ScrollView struct {
	X, Y, Width, Height float64
	ScrollX, ScrollY    float64
	LockH, LockV        bool
	Style               ScrollStyle
	// OnScroll is called after the scroll position changes.
	OnScroll func(*ScrollView)
	// Typewriter keeps the typed line at a fixed height in the view.
	Typewriter   bool
	TypewriterAt float64
	// TypewriterX makes the typed letter stay put horizontally too.
	TypewriterX bool

	content *Stage
	cw, ch  float64
	painter ImagePainter

	drag        *scrollDrag
	hoverBar    byte // 'v', 'h' or 0
	contentDown bool // a press started in the content, so moves keep going to it
}

type scrollDrag struct {
	vertical    bool
	startMouse  float64
	startScroll float64
}

// NewScrollView returns an empty scroll view at (x, y).
func NewScrollView(x, y, w, h float64) *ScrollView {
	return &ScrollView{X: x, Y: y, Width: w, Height: h, Style: DefaultScrollStyle(), content: &Stage{Formats: &FormatClipboard{}}}
}

// Add puts a canvas object in the content.
func (sv *ScrollView) Add(obj Drawer, w, h float64) {
	sv.content.Add(obj)
	sv.cw, sv.ch = max(sv.cw, w), max(sv.ch, h)
	if sv.painter != nil {
		if ps, ok := obj.(PainterSetter); ok {
			ps.SetPainter(sv.painter)
		}
	}
}

// SetContent replaces the content components, sized w x h.
func (sv *ScrollView) SetContent(ds []Drawer, w, h float64) {
	sv.content.Components = nil
	for _, d := range ds {
		sv.Add(d, 0, 0)
	}
	sv.SetContentSize(w, h)
}

// SetContentSize overrides the content size.
func (sv *ScrollView) SetContentSize(w, h float64) {
	sv.cw, sv.ch = w, h
	sv.ScrollTo(sv.ScrollX, sv.ScrollY)
}

// ContentSize returns the content's size.
func (sv *ScrollView) ContentSize() (w, h float64) { return sv.cw, sv.ch }

// SetContentMode puts every content component in mode m.
func (sv *ScrollView) SetContentMode(m Mode) { sv.content.SetMode(m) }

func (sv *ScrollView) SetPainter(p ImagePainter) {
	sv.painter = p
	sv.content.SetPainter(p)
}

func (sv *ScrollView) maxScroll() (mx, my float64) {
	return max(sv.cw-sv.Width, 0), max(sv.ch-sv.Height, 0)
}

// ScrollTo scrolls to (x, y), clamped to the content.
func (sv *ScrollView) ScrollTo(x, y float64) bool {
	mx, my := sv.maxScroll()
	nx, ny := sv.ScrollX, sv.ScrollY
	loX, hiX, loY, hiY := 0.0, mx, 0.0, my
	lockH, lockV := sv.LockH, sv.LockV
	if sv.carriage() { // Typewriter lets the page run past its edges.
		loX, hiX = -sv.Width*sv.typewriterAt(), mx+sv.Width*(1-sv.typewriterAt())
		loY, hiY = -sv.Height*sv.typewriterAt(), my+sv.Height*(1-sv.typewriterAt())
		lockH, lockV = false, false
	}
	if !lockH {
		nx = min(max(x, loX), hiX)
	}
	if !lockV {
		ny = min(max(y, loY), hiY)
	}
	if nx == sv.ScrollX && ny == sv.ScrollY {
		return false
	}
	sv.ScrollX, sv.ScrollY = nx, ny
	if sv.OnScroll != nil {
		sv.OnScroll(sv)
	}
	return true
}

// ScrollBy scrolls by (dx, dy); see ScrollTo.
func (sv *ScrollView) ScrollBy(dx, dy float64) bool {
	return sv.ScrollTo(sv.ScrollX+dx, sv.ScrollY+dy)
}

func (sv *ScrollView) showV() bool { return !sv.LockV && sv.ch > sv.Height }
func (sv *ScrollView) showH() bool { return !sv.LockH && sv.cw > sv.Width }

// bar returns a scrollbar's track and thumb rectangles.
func (sv *ScrollView) bar(vertical bool) (track, thumb [4]float64) {
	s := sv.Style
	mx, my := sv.maxScroll()
	if vertical {
		length := sv.Height
		if sv.showH() {
			length -= s.BarSize
		}
		track = [4]float64{sv.X + sv.Width - s.BarSize, sv.Y, s.BarSize, length}
		tl := min(max(length*sv.Height/sv.ch, s.MinThumb), length)
		pos := 0.0
		if my > 0 {
			pos = (length - tl) * sv.ScrollY / my
		}
		return track, [4]float64{track[0], track[1] + pos, s.BarSize, tl}
	}
	length := sv.Width
	if sv.showV() {
		length -= s.BarSize
	}
	track = [4]float64{sv.X, sv.Y + sv.Height - s.BarSize, length, s.BarSize}
	tl := min(max(length*sv.Width/sv.cw, s.MinThumb), length)
	pos := 0.0
	if mx > 0 {
		pos = (length - tl) * sv.ScrollX / mx
	}
	return track, [4]float64{track[0] + pos, track[1], tl, s.BarSize}
}

// barAt returns 'v' or 'h' over a visible scrollbar.
func (sv *ScrollView) barAt(x, y float64) byte {
	if sv.showV() {
		if t, _ := sv.bar(true); inRect(x, y, t) {
			return 'v'
		}
	}
	if sv.showH() {
		if t, _ := sv.bar(false); inRect(x, y, t) {
			return 'h'
		}
	}
	return 0
}

// scrollForThumbPos converts a thumb position to a scroll offset.
func (sv *ScrollView) scrollForThumbPos(vertical bool, pos float64) float64 {
	track, thumb := sv.bar(vertical)
	mx, my := sv.maxScroll()
	if vertical {
		room := track[3] - thumb[3]
		if room <= 0 {
			return 0
		}
		return min(max(pos/room, 0), 1) * my
	}
	room := track[2] - thumb[2]
	if room <= 0 {
		return 0
	}
	return min(max(pos/room, 0), 1) * mx
}

func (sv *ScrollView) inside(x, y float64) bool {
	return inRect(x, y, [4]float64{sv.X, sv.Y, sv.Width, sv.Height})
}

// toContent maps viewport-space (canvas) coordinates into content space.
func (sv *ScrollView) toContent(x, y float64) (float64, float64) {
	return x - sv.X + sv.ScrollX, y - sv.Y + sv.ScrollY
}

// MouseDown grabs a thumb, jumps along a track, or passes the press on.
func (sv *ScrollView) MouseDown(x, y float64) bool { return sv.MouseDownMods(x, y, Mods{}) }

// MouseDownMods is MouseDown with modifier keys passed to the content.
func (sv *ScrollView) MouseDownMods(x, y float64, m Mods) bool {
	switch sv.barAt(x, y) {
	case 'v':
		track, thumb := sv.bar(true)
		if !inRect(x, y, thumb) { // click on the track: centre the thumb there
			sv.ScrollTo(sv.ScrollX, sv.scrollForThumbPos(true, y-track[1]-thumb[3]/2))
		}
		sv.drag = &scrollDrag{vertical: true, startMouse: y, startScroll: sv.ScrollY}
		return true
	case 'h':
		track, thumb := sv.bar(false)
		if !inRect(x, y, thumb) {
			sv.ScrollTo(sv.scrollForThumbPos(false, x-track[0]-thumb[2]/2), sv.ScrollY)
		}
		sv.drag = &scrollDrag{startMouse: x, startScroll: sv.ScrollX}
		return true
	}
	if !sv.inside(x, y) {
		return false
	}
	sv.contentDown = true
	cx, cy := sv.toContent(x, y)
	changed := sv.content.MouseDownMods(cx, cy, m)
	if sv.RevealPending() { // a link to a bookmark was followed
		changed = true
	}
	return changed
}

// EditTarget returns the content's edit target.
func (sv *ScrollView) EditTarget() EditTarget { return sv.content.EditTarget() }

// DoubleClick hands a double click to the content (inside the viewport).
func (sv *ScrollView) DoubleClick(x, y float64) bool {
	if !sv.inside(x, y) || sv.barAt(x, y) != 0 {
		return false
	}
	cx, cy := sv.toContent(x, y)
	return sv.content.DoubleClick(cx, cy)
}

// MouseMove drags a thumb, tracks hover, and passes movement on.
func (sv *ScrollView) MouseMove(x, y float64) bool {
	if d := sv.drag; d != nil {
		track, thumb := sv.bar(d.vertical)
		mx, my := sv.maxScroll()
		room, span, delta := track[2]-thumb[2], mx, x-d.startMouse
		if d.vertical {
			room, span, delta = track[3]-thumb[3], my, y-d.startMouse
		}
		target := d.startScroll
		if room > 0 {
			target += delta / room * span
		}
		if d.vertical {
			return sv.ScrollTo(sv.ScrollX, target)
		}
		return sv.ScrollTo(target, sv.ScrollY)
	}
	changed := false
	bar := sv.barAt(x, y)
	if bar != sv.hoverBar {
		sv.hoverBar, changed = bar, true
	}
	if bar == 0 && (sv.inside(x, y) || sv.contentDown) {
		cx, cy := sv.toContent(x, y)
		if sv.content.MouseMove(cx, cy) {
			changed = true
		}
	}
	return changed
}

// MouseUp ends a thumb drag and passes the release on.
func (sv *ScrollView) MouseUp(x, y float64) bool {
	changed := sv.drag != nil
	sv.drag, sv.contentDown = nil, false
	cx, cy := sv.toContent(x, y)
	if sv.content.MouseUp(cx, cy) {
		changed = true
	}
	return changed
}

// MouseLeave clears hover state.
func (sv *ScrollView) MouseLeave() bool {
	changed := sv.hoverBar != 0
	sv.hoverBar = 0
	if sv.content.MouseLeave() {
		changed = true
	}
	return changed
}

// MouseWheel scrolls when the pointer is over the viewport.
func (sv *ScrollView) MouseWheel(x, y, dx, dy float64) bool {
	if !sv.inside(x, y) {
		return false
	}
	cx, cy := sv.toContent(x, y)
	if sv.content.MouseWheel(cx, cy, dx, dy) {
		return true
	}
	return sv.ScrollBy(dx, dy)
}

// SetFormatClipboard hands the format clipboard to the content.
func (sv *ScrollView) SetFormatClipboard(c *FormatClipboard) { sv.content.SetFormatClipboard(c) }

// HandleCommand forwards to the content.
func (sv *ScrollView) HandleCommand(cmd Command) bool {
	ok := sv.content.RunCommand(cmd)
	if ok {
		sv.afterEvent()
	}
	return ok
}

// CopyText, CutText and PasteText forward to the content.
func (sv *ScrollView) CopyText() (string, bool) { return sv.content.Copy() }
func (sv *ScrollView) CutText() (string, bool) {
	t, ok := sv.content.Cut()
	if ok {
		sv.afterEvent()
	}
	return t, ok
}
func (sv *ScrollView) PasteText(text string) bool {
	ok := sv.content.Paste(text)
	if ok {
		sv.afterEvent()
	}
	return ok
}

// HandleKey forwards to the content.
func (sv *ScrollView) HandleKey(key string, shift bool) bool {
	ok := sv.content.HandleKey(key, shift)
	if ok {
		sv.afterEvent()
	}
	return ok
}

// InsertText forwards to the content.
func (sv *ScrollView) InsertText(s string) bool {
	ok := sv.content.InsertText(s)
	if ok {
		sv.afterEvent()
	}
	return ok
}

// WantsFocus reports whether the content needs keyboard focus.
func (sv *ScrollView) WantsFocus() bool { return sv.content.WantsFocus() }

// SetCaretVisible forwards the blink phase to the content.
func (sv *ScrollView) SetCaretVisible(on bool) bool { return sv.content.SetCaretVisible(on) }

func (sv *ScrollView) CSSCursor() string {
	if sv.hoverBar != 0 || sv.drag != nil {
		return "default"
	}
	if c := sv.content.Cursor(); c != "default" {
		return c
	}
	return ""
}

// --- drawing -------------------------------------------------------

// Draw paints the content clipped to the viewport, then frame and scrollbars.
func (sv *ScrollView) Draw() {
	p := sv.painter
	for _, c := range sv.content.Components {
		if vs, ok := c.(ViewportSetter); ok {
			vs.SetViewport(sv.ScrollX, sv.ScrollY, sv.Width, sv.Height)
		}
	}
	p.Save()
	p.ClipRect(sv.X, sv.Y, sv.Width, sv.Height)
	p.Translate(sv.X-sv.ScrollX, sv.Y-sv.ScrollY)
	sv.content.Paint()
	p.Restore()

	p.SetStrokeStyle(sv.Style.BorderColor)
	p.SetLineWidth(1)
	p.StrokeRect(sv.X+0.5, sv.Y+0.5, sv.Width-1, sv.Height-1)
	for _, vertical := range [...]bool{true, false} {
		if (vertical && !sv.showV()) || (!vertical && !sv.showH()) {
			continue
		}
		track, thumb := sv.bar(vertical)
		p.SetFillStyle(sv.Style.TrackColor)
		p.FillRect(track[0], track[1], track[2], track[3])
		hovered := (vertical && sv.hoverBar == 'v') || (!vertical && sv.hoverBar == 'h') ||
			(sv.drag != nil && sv.drag.vertical == vertical)
		if hovered {
			p.SetFillStyle(sv.Style.ThumbHoverColor)
		} else {
			p.SetFillStyle(sv.Style.ThumbColor)
		}
		p.FillRect(thumb[0]+2, thumb[1]+2, thumb[2]-4, thumb[3]-4)
	}
}

// carriage reports whether the view works as a typewriter carriage.
func (sv *ScrollView) carriage() bool { return sv.Typewriter && sv.TypewriterX }

// typewriterAt is the typed line's height, from 0 (top) to 1 (bottom).
func (sv *ScrollView) typewriterAt() float64 {
	if sv.TypewriterAt <= 0 || sv.TypewriterAt >= 1 {
		return 0.5
	}
	return sv.TypewriterAt
}
