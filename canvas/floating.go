package canvas

import (
	"math"
	"slices"
	"strings"
)

type FloatingStyle struct {
	Background   string // box fill; empty for transparent
	BorderColor  string // drawn in view mode too; empty for none
	OutlineColor string // edit-mode outline, caret and resize handle
	SelectColor  string
	Font         string // toolbar font
	ButtonH      float64
	ButtonBG     string
	ButtonActive string
	ButtonText   string
	ButtonOnText string
	HandleSize   float64
}

// DefaultFloatingStyle is a light look matching DefaultTableStyle.
func DefaultFloatingStyle() FloatingStyle {
	return FloatingStyle{
		Background: "#fff8dc", BorderColor: "#e2d9a8", OutlineColor: "#7c5cff",
		SelectColor: "rgba(124, 92, 255, 0.28)", Font: "12px sans-serif", ButtonH: 22,
		ButtonBG: "#eceef1", ButtonActive: "#7c5cff", ButtonText: "#2e3338", ButtonOnText: "#ffffff",
		HandleSize: 10,
	}
}

type fRune struct {
	r rune
	f Format
}

// FloatingText is a text box that text can wrap around.
type FloatingText struct {
	X, Y, Width, Height float64
	Padding             float64
	Style               TextStyle // base style; per-character Formats override it
	Background          string    // overrides ChromeStyle.Background when non-empty
	Snap                float64   // drag granularity (NewFloatingText sets 5)
	BoundsW             float64   // if non-zero, keeps the box within x in [0, BoundsW]
	Wrap                bool
	WrapMargin          float64
	Opacity             float64
	Anchor              *Anchor
	Pages               *PageSet
	// AutoClose maps each opening character to its closer.
	AutoClose map[rune]rune
	// Completer suggests completions for the word before the caret.
	Completer Completer
	// Vocabulary adds words the default completer may suggest.
	Vocabulary []string

	ChromeStyle FloatingStyle
	Painter     ImagePainter
	// OnChange runs after any change to text, format or box.
	OnChange func(*FloatingText)

	runes    []fRune
	hist     snapshotHistory[boxSnap]
	histKind string
	caret    int
	anchor   int // the other end of the selection
	extra    []Cursor
	pending  Format // format for the next typed text when nothing is selected
	editing  bool
	caretOn  bool

	inner *TextFlow
	flow  *TextFlow // the flow this box wraps around

	formats  *FormatClipboard
	pal      *Menu  // color palette
	palKind  string // "text" or "highlight"
	sugg     *Menu  // completion popup
	suggFrom int    // offset where the word being completed starts
	viewport [4]float64
	mode     Mode
	drag     *floatDrag
	hover    string
}

type floatDrag struct {
	what           string // "move", "resize" or "select"
	startX, startY float64
	boxX, boxY     float64
	boxW, boxH     float64
}

// NewFloatingText returns a box holding text, in ModeView.
func NewFloatingText(text string, x, y, w, h float64) *FloatingText {
	ft := &FloatingText{
		X: x, Y: y, Width: w, Height: h, Padding: 8, Style: DefaultTextStyle(),
		Snap: 5, WrapMargin: 8, ChromeStyle: DefaultFloatingStyle(), caretOn: true, AutoClose: DefaultAutoClose(),
		inner: NewTextFlow("", 0, 0, 0, 0),
	}
	ft.inner.Transparent = true
	ft.SetText(text)
	return ft
}

func (ft *FloatingText) SetPainter(p ImagePainter) { ft.Painter = p; ft.inner.Painter = p }

func (ft *FloatingText) pageSet() *PageSet {
	if ft.Pages != nil {
		return ft.Pages
	}
	if ft.flow != nil {
		return ft.flow.Pages
	}
	return nil
}

func (ft *FloatingText) resolve() {
	if ps := ft.pageSet(); ft.Anchor != nil && ps != nil {
		ft.X, ft.Y = ps.Resolve(*ft.Anchor)
	}
}

func (ft *FloatingText) SetFlow(f *TextFlow) {
	if ft.flow != nil {
		ft.flow.RemoveObstacle(ft)
	}
	ft.flow = f
	if f != nil {
		f.AddObstacle(ft)
	}
}

// Flow returns the flow set with SetFlow.
func (ft *FloatingText) Flow() *TextFlow { return ft.flow }

// Text returns the plain text.
func (ft *FloatingText) Text() string {
	var b strings.Builder
	for _, fr := range ft.runes {
		b.WriteRune(fr.r)
	}
	return b.String()
}

func (ft *FloatingText) SetText(s string) {
	ft.runes = ft.runes[:0]
	for _, r := range s {
		ft.runes = append(ft.runes, fRune{r: r})
	}
	ft.caret, ft.anchor, ft.pending = len(ft.runes), len(ft.runes), Format{}
	ft.hist.reset()
}

func (ft *FloatingText) Items() []Item {
	var items []Item
	for i := 0; i < len(ft.runes); {
		j := i
		var b strings.Builder
		for j < len(ft.runes) && ft.runes[j].f == ft.runes[i].f {
			b.WriteRune(ft.runes[j].r)
			j++
		}
		items = append(items, Chars{Text: b.String(), Format: ft.runes[i].f})
		i = j
	}
	return items
}

// FormatAt returns the format of the character at offset i.
func (ft *FloatingText) FormatAt(i int) Format {
	if i < 0 || i >= len(ft.runes) {
		return Format{}
	}
	return ft.runes[i].f
}

// Caret returns the caret's offset in the text (in runes).
func (ft *FloatingText) Caret() int { return ft.caret }

// Selection returns the selected range [from, to); equal means none.
func (ft *FloatingText) Selection() (from, to int) {
	return min(ft.caret, ft.anchor), max(ft.caret, ft.anchor)
}

func (ft *FloatingText) Editing() bool { return ft.editing }

// Box returns the box rectangle: x, y, w, h.
func (ft *FloatingText) Box() (x, y, w, h float64) {
	ft.resolve()
	return ft.X, ft.Y, ft.Width, ft.Height
}

func (ft *FloatingText) Exclusion(y0, y1 float64) (x0, x1 float64, ok bool) {
	ft.resolve()
	if !ft.Wrap || y1 <= ft.Y-ft.WrapMargin || y0 >= ft.Y+ft.Height+ft.WrapMargin {
		return 0, 0, false
	}
	return ft.X - ft.WrapMargin, ft.X + ft.Width + ft.WrapMargin, true
}

func (ft *FloatingText) ContainsPoint(x, y float64) bool {
	ft.resolve()
	if ft.inBox(x, y) {
		return true
	}
	if ft.mode != ModeEdit {
		return false
	}
	return ft.whatAt(x, y) != ""
}

// Mode returns the current mode.
func (ft *FloatingText) Mode() Mode { return ft.mode }

// SetMode switches mode; leaving edit mode stops editing and drag.
func (ft *FloatingText) SetMode(m Mode) bool {
	if m == ft.mode {
		return false
	}
	ft.mode = m
	if m == ModeView {
		ft.editing, ft.drag, ft.hover = false, nil, ""
		ft.anchor = ft.caret
		ft.closePopups()
	}
	return true
}

func (ft *FloatingText) changed() {
	ft.hist.commit(ft.histSnap(), ft.histKind)
	ft.histKind = ""
	if ft.OnChange != nil {
		ft.OnChange(ft)
	}
}

func (ft *FloatingText) sync() {
	ft.resolve()
	f := ft.inner
	f.Items, f.Style = ft.Items(), ft.Style
	f.X, f.Y = ft.X+ft.Padding, ft.Y+ft.Padding
	f.Width, f.Height = ft.Width-2*ft.Padding, ft.Height-2*ft.Padding
}

func (ft *FloatingText) snap(v float64) float64 {
	if ft.Snap <= 0 {
		return v
	}
	return math.Round(v/ft.Snap) * ft.Snap
}

func (ft *FloatingText) replace(from, to int, ins []fRune) {
	ft.runes = slices.Replace(ft.runes, from, to, ins...)
	ft.caret = from + len(ins)
	ft.anchor = ft.caret
}

func (ft *FloatingText) typingFormat() Format {
	if !ft.pending.IsZero() {
		return ft.pending
	}
	switch {
	case ft.caret > 0:
		return ft.runes[ft.caret-1].f
	case len(ft.runes) > 0:
		return ft.runes[0].f
	}
	return Format{}
}

func (ft *FloatingText) insertPlain(s string) bool {
	s = strings.ReplaceAll(s, "\r", "")
	if ft.mode != ModeEdit || !ft.editing || s == "" {
		return false
	}
	f := ft.typingFormat()
	var ins []fRune
	for _, r := range s {
		ins = append(ins, fRune{r, f})
	}
	from, to := ft.Selection()
	ft.replace(from, to, ins)
	ft.pending = Format{}
	ft.caretOn = true
	ft.changed()
	return true
}

func (ft *FloatingText) lineOf(o int) (Line, bool) {
	lines := ft.inner.Layout().Lines
	for _, ln := range lines {
		if o <= ln.End {
			return ln, true
		}
	}
	if len(lines) > 0 {
		return lines[len(lines)-1], true
	}
	return Line{}, false
}

func (ft *FloatingText) handleEditKey(key string, shift bool) bool {
	if ft.mode != ModeEdit || !ft.editing {
		return false
	}
	if endsRun(key) {
		ft.hist.breakRun()
	}
	ft.sync()
	if len(ft.extra) > 0 {
		return ft.multiKey(key, shift)
	}
	move := func(to int) {
		ft.caret = min(max(to, 0), len(ft.runes))
		if !shift {
			ft.anchor = ft.caret
		}
		ft.pending = Format{}
	}
	switch key {
	case "ArrowLeft":
		if from, to := ft.Selection(); from != to && !shift {
			move(from)
		} else {
			move(ft.caret - 1)
		}
	case "ArrowRight":
		if from, to := ft.Selection(); from != to && !shift {
			move(to)
		} else {
			move(ft.caret + 1)
		}
	case "ArrowUp", "ArrowDown":
		x, y, h, ok := ft.inner.CaretRect(ft.caret)
		if !ok {
			return true
		}
		if key == "ArrowUp" {
			move(ft.inner.OffsetAt(x, y-1))
		} else {
			move(ft.inner.OffsetAt(x, y+h+1))
		}
	case "Home":
		if ln, ok := ft.lineOf(ft.caret); ok {
			move(ln.Start)
		}
	case "End":
		if ln, ok := ft.lineOf(ft.caret); ok {
			move(ln.End)
		}
	case "Backspace", "Delete":
		from, to := ft.Selection()
		hadSelection := from != to
		switch {
		case from != to:
		case key == "Backspace" && ft.betweenPair():
			from--
			to++
		case key == "Backspace" && from > 0:
			from--
		case key == "Delete" && to < len(ft.runes):
			to++
		default:
			return true
		}
		if !hadSelection {
			ft.histKind = "delete"
		}
		ft.replace(from, to, nil)
		ft.changed()
	case "Enter":
		return ft.insertPlain("\n")
	case "Escape":
		ft.editing = false
		ft.anchor = ft.caret
	default:
		return false
	}
	ft.caretOn = true
	return true
}

func (ft *FloatingText) ApplyFormat(fn func(*Format)) bool {
	from, to := ft.Selection()
	if from == to {
		before := ft.pending
		if before.IsZero() {
			before = ft.typingFormat()
		}
		after := before
		fn(&after)
		ft.pending = after
		return after != before
	}
	changed := false
	for i := from; i < to; i++ {
		f := ft.runes[i].f
		fn(&f)
		if f != ft.runes[i].f {
			ft.runes[i].f, changed = f, true
		}
	}
	if changed {
		ft.changed()
	}
	return changed
}

func (ft *FloatingText) current() []TextStyle {
	from, to := ft.Selection()
	if from == to {
		return []TextStyle{ft.typingFormat().resolve(ft.Style)}
	}
	out := make([]TextStyle, 0, to-from)
	for _, fr := range ft.runes[from:to] {
		out = append(out, fr.f.resolve(ft.Style))
	}
	return out
}

func (ft *FloatingText) ToggleBold() bool {
	all := true
	for _, s := range ft.current() {
		all = all && s.FontWeight >= 600
	}
	return ft.ApplyFormat(func(f *Format) {
		if all {
			f.FontWeight = 400
		} else {
			f.FontWeight = 700
		}
	})
}

// ToggleItalic is ToggleBold for italics.
func (ft *FloatingText) ToggleItalic() bool {
	all := true
	for _, s := range ft.current() {
		all = all && s.FontStyle == StyleItalic
	}
	return ft.ApplyFormat(func(f *Format) {
		if all {
			f.FontStyle = StyleNormal
		} else {
			f.FontStyle = StyleItalic
		}
	})
}

// ChangeSize adds delta px to the font size, minimum 6.
func (ft *FloatingText) ChangeSize(delta float64) bool {
	base := ft.Style.FontSize
	if base <= 0 {
		base = 16
	}
	return ft.ApplyFormat(func(f *Format) {
		size := f.FontSize
		if size == 0 {
			size = base
		}
		f.FontSize = max(size+delta, 6)
	})
}

var floatButtons = [...]struct {
	label string
	w     float64
}{{"Move", 44}, {"B", 28}, {"I", 28}, {"A-", 32}, {"A+", 32}, {"Wrap", 48}, {"Color", 46}, {"Mark", 42}, {"Fade", 42}}

func (ft *FloatingText) buttonRect(i int) [4]float64 {
	x := ft.X
	for j := range i {
		x += floatButtons[j].w + 4
	}
	return [4]float64{x, max(ft.Y-ft.ChromeStyle.ButtonH-6, 0), floatButtons[i].w, ft.ChromeStyle.ButtonH}
}

func (ft *FloatingText) handleRect() [4]float64 {
	hs := ft.ChromeStyle.HandleSize
	return [4]float64{ft.X + ft.Width - hs/2, ft.Y + ft.Height - hs/2, hs, hs}
}

func (ft *FloatingText) inBox(x, y float64) bool {
	return inRect(x, y, [4]float64{ft.X, ft.Y, ft.Width, ft.Height})
}

func (ft *FloatingText) whatAt(x, y float64) string {
	for i := range floatButtons {
		if inRect(x, y, ft.buttonRect(i)) {
			return "button:" + string(rune('0'+i))
		}
	}
	switch {
	case inRect(x, y, ft.handleRect()):
		return "resize"
	case ft.inBox(x, y):
		return "text"
	}
	return ""
}

// MouseDown handles toolbar, resize handle and caret placement.
func (ft *FloatingText) MouseDown(x, y float64) bool {
	if ft.mode != ModeEdit {
		return false
	}
	ft.resolve()
	if ft.popupsMouseDown(x, y) {
		return true
	}
	what := ft.whatAt(x, y)
	d := &floatDrag{startX: x, startY: y, boxX: ft.X, boxY: ft.Y, boxW: ft.Width, boxH: ft.Height}
	switch {
	case what == "":
		was := ft.editing
		ft.editing = false
		ft.anchor = ft.caret
		return was
	case strings.HasPrefix(what, "button:"):
		switch what[len("button:"):] {
		case "0":
			d.what = "move"
			ft.drag = d
		case "1":
			ft.ToggleBold()
		case "2":
			ft.ToggleItalic()
		case "3":
			ft.ChangeSize(-2)
		case "4":
			ft.ChangeSize(2)
		case "5":
			ft.Wrap = !ft.Wrap
			ft.changed()
		case "6":
			ft.openPalette(6, "text")
		case "7":
			ft.openPalette(7, "highlight")
		case "8":
			ft.CycleOpacity()
		}
		return true
	case what == "resize":
		d.what = "resize"
		ft.drag = d
	default: // inside the text
		ft.sync()
		ft.extra = nil
		if !ft.editing {
			ft.hist.start(ft.histSnap())
		}
		ft.hist.breakRun()
		ft.editing = true
		ft.caret = ft.inner.OffsetAt(x, y)
		ft.anchor = ft.caret
		ft.pending = Format{}
		ft.caretOn = true
		d.what = "select"
		ft.drag = d
	}
	return true
}

// MouseMove continues a drag, or updates the hover cursor.
func (ft *FloatingText) MouseMove(x, y float64) bool {
	if ft.mode != ModeEdit {
		return false
	}
	ft.resolve()
	d := ft.drag
	if d == nil {
		old := ft.hover
		ft.hover = ""
		switch w := ft.whatAt(x, y); {
		case w == "text":
			ft.hover = "text"
		case w == "resize":
			ft.hover = "nwse-resize"
		case w == "button:0":
			ft.hover = "move"
		case w != "":
			ft.hover = "pointer"
		}
		return ft.hover != old
	}
	dx, dy := x-d.startX, y-d.startY
	switch d.what {
	case "select":
		ft.sync()
		if o := ft.inner.OffsetAt(x, y); o != ft.caret {
			ft.caret = o
			return true
		}
		return false
	case "move":
		nx, ny := max(ft.snap(d.boxX+dx), 0), max(ft.snap(d.boxY+dy), 0)
		if ps := ft.pageSet(); ft.Anchor != nil && ps != nil {
			a := ps.AnchorAt(d.boxX+dx, d.boxY+dy)
			a.X, a.Y = ft.snap(a.X), ft.snap(a.Y)
			nx, ny = ps.Resolve(a)
		}
		if ft.BoundsW > 0 {
			nx = min(nx, max(ft.BoundsW-ft.Width, 0))
		}
		if nx == ft.X && ny == ft.Y {
			return false
		}
		ft.X, ft.Y = nx, ny
		if ps := ft.pageSet(); ft.Anchor != nil && ps != nil {
			*ft.Anchor = ps.AnchorAt(nx, ny)
		}
	case "resize":
		nw, nh := max(ft.snap(d.boxW+dx), 20), max(ft.snap(d.boxH+dy), 20)
		if nw == ft.Width && nh == ft.Height {
			return false
		}
		ft.Width, ft.Height = nw, nh
	}
	ft.changed()
	return true
}

// MouseUp ends a drag.
func (ft *FloatingText) MouseUp(x, y float64) bool {
	if ft.drag == nil {
		return false
	}
	ft.drag = nil
	return true
}

// MouseLeave clears the hover cursor.
func (ft *FloatingText) MouseLeave() bool {
	changed := ft.hover != ""
	ft.hover = ""
	return changed
}

// Dragging reports whether a move, resize or selection drag runs.
func (ft *FloatingText) Dragging() bool { return ft.drag != nil }

func (ft *FloatingText) WantsFocus() bool {
	return ft.mode == ModeEdit && (ft.editing || ft.palOpen())
}

func (ft *FloatingText) SetCaretVisible(on bool) bool {
	changed := ft.caretOn != on && ft.editing
	ft.caretOn = on
	return changed
}

func (ft *FloatingText) CSSCursor() string {
	if ft.mode != ModeEdit {
		return ""
	}
	if ft.drag != nil {
		switch ft.drag.what {
		case "move":
			return "grabbing"
		case "resize":
			return "nwse-resize"
		}
		return "text"
	}
	return ft.hover
}

func (ft *FloatingText) Draw() {
	p, cs := ft.Painter, ft.ChromeStyle
	ft.sync()
	if ft.Opacity > 0 && ft.Opacity < 1 {
		p.SetGlobalAlpha(ft.Opacity)
		defer p.SetGlobalAlpha(1)
	}
	bg := cs.Background
	if ft.Background != "" {
		bg = ft.Background
	}
	if bg != "" {
		p.SetFillStyle(bg)
		p.FillRect(ft.X, ft.Y, ft.Width, ft.Height)
	}
	if cs.BorderColor != "" {
		p.SetStrokeStyle(cs.BorderColor)
		p.SetLineWidth(1)
		p.StrokeRect(ft.X+0.5, ft.Y+0.5, ft.Width-1, ft.Height-1)
	}
	edit := ft.mode == ModeEdit
	if edit {
		ft.drawSelection()
	}
	ft.inner.Draw()
	if !edit {
		return
	}

	p.SetStrokeStyle(cs.OutlineColor)
	p.SetLineWidth(1)
	p.StrokeRect(ft.X+0.5, ft.Y+0.5, ft.Width-1, ft.Height-1)
	if ft.editing && ft.caretOn {
		for _, c := range ft.Cursors() {
			if x, y, h, ok := ft.inner.CaretRect(c.Caret); ok {
				p.SetFillStyle(cs.OutlineColor)
				p.FillRect(x, y+2, 1.5, h-4)
			}
		}
	}
	p.SetFont(cs.Font)
	p.SetTextBaseline("middle")
	p.SetTextAlign("center")
	for i, b := range floatButtons {
		r := ft.buttonRect(i)
		on := (b.label == "Wrap" && ft.Wrap) || (b.label == "Move" && ft.drag != nil && ft.drag.what == "move") ||
			(b.label == "Color" && ft.palOpen() && ft.palKind == "text") || (b.label == "Mark" && ft.palOpen() && ft.palKind == "highlight")
		if on {
			p.SetFillStyle(cs.ButtonActive)
		} else {
			p.SetFillStyle(cs.ButtonBG)
		}
		p.FillRect(r[0], r[1], r[2], r[3])
		if on {
			p.SetFillStyle(cs.ButtonOnText)
		} else {
			p.SetFillStyle(cs.ButtonText)
		}
		p.FillText(b.label, r[0]+r[2]/2, r[1]+r[3]/2)
	}
	p.SetTextAlign("left")
	h := ft.handleRect()
	p.SetFillStyle(cs.OutlineColor)
	p.FillRect(h[0], h[1], h[2], h[3])
}

func (ft *FloatingText) drawSelection() {
	p := ft.Painter
	p.SetFillStyle(ft.ChromeStyle.SelectColor)
	lay := ft.inner.Layout()
	for _, c := range ft.Cursors() {
		from, to := c.Range()
		if from == to {
			continue
		}
		for _, ln := range lay.Lines {
			s, e := max(from, ln.Start), min(to, ln.End)
			for _, r := range ft.inner.lineRects(ln, s, e) {
				p.FillRect(r[0], ln.Y, r[1]-r[0], ln.H)
			}
		}
	}
}

var (
	_ Component = (*FloatingText)(nil)
	_ Obstacle  = (*FloatingText)(nil)
)

// CycleOpacity steps opacity through 1, 0.75, 0.5, 0.25.
func (ft *FloatingText) CycleOpacity() {
	switch cur := ft.Opacity; {
	case cur == 0 || cur >= 1:
		ft.Opacity = 0.75
	case cur > 0.5:
		ft.Opacity = 0.5
	case cur > 0.25:
		ft.Opacity = 0.25
	default:
		ft.Opacity = 0
	}
	ft.changed()
}
