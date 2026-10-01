package canvas

import (
	"math"
	"strconv"
)

// ImageTool is what dragging does to an Image in edit mode.
type ImageTool int

const (
	ToolMove  ImageTool = iota // drag the image left/right
	ToolClip                   // drag an edge to crop the image
	ToolAlpha                  // drag the slider to set opacity
)

// ImagePlacement says how an Image is positioned.
type ImagePlacement int

const (
	// PlaceFloating positions the image by its own X, Y (or its Anchor)
	// over the text, which can wrap around it.
	PlaceFloating ImagePlacement = iota
	// PlaceInline makes the image part of the text: put it in a
	// TextFlow's items as Inline{Object: img}. The flow sizes it to fit
	// the column and the page's text area (keeping its aspect ratio)
	// and places it on the baseline like a character.
	PlaceInline
)

// Insets are pixels cropped from each side of an image's frame.
type Insets struct{ Left, Top, Right, Bottom float64 }

// ImageStyle is the chrome Image draws in edit mode.
type ImageStyle struct {
	Font         string
	OutlineColor string
	GhostAlpha   float64 // opacity of the cropped-away part while clipping
	HandleSize   float64
	ButtonW      float64 // toolbar button width
	ButtonH      float64
	ButtonBG     string
	ButtonActive string
	ButtonText   string
	ButtonOnText string
	TrackColor   string
	SliderGap    float64 // distance from the image to the alpha slider
	SliderH      float64
}

// DefaultImageStyle is a light look matching DefaultTableStyle.
func DefaultImageStyle() ImageStyle {
	return ImageStyle{
		Font:         "12px sans-serif",
		OutlineColor: "#7c5cff",
		GhostAlpha:   0.3,
		HandleSize:   8,
		ButtonW:      52,
		ButtonH:      22,
		ButtonBG:     "#eceef1",
		ButtonActive: "#7c5cff",
		ButtonText:   "#2e3338",
		ButtonOnText: "#ffffff",
		TrackColor:   "#d4d6da",
		SliderGap:    10,
		SliderH:      8,
	}
}

// Image draws a bitmap with a crop (clip), an opacity (alpha channel)
// and, optionally, a text-wrap-around mode.
//
// (X, Y) is the top-left of the image's frame, W x H its displayed
// size (the bitmap is scaled to it). Clip crops the frame from each
// side; only the remaining "visible" rectangle is drawn. In edit mode
// a toolbar above the image picks the active tool:
//
//   - Move: drag horizontally; the position snaps to multiples of Snap.
//   - Clip: drag an edge handle; the crop snaps to multiples of Snap.
//   - Alpha: drag a slider below the image; opacity snaps to 5%.
//
// and a Wrap button toggles text wrapping. When Wrap is on and a Flow
// is set (SetFlow), the TextFlow lays text out around the visible
// rectangle -- the image registers itself with the flow as an Obstacle,
// so dragging, cropping or toggling it re-wraps the text live.
type Image struct {
	Src        ImageSource
	X, Y, W, H float64
	// Placement chooses floating (the default) or inline; see
	// ImagePlacement. Inline images ignore X, Y, Wrap and the Move tool.
	Placement ImagePlacement
	// Anchor, if set, pins a floating image to a page: X, Y are then
	// derived from it (relative to that page's sheet) using Pages or,
	// failing that, the Flow's pages. Dragging updates it.
	Anchor *Anchor
	// Pages is the PageSet Anchor refers to; if nil the flow's is used.
	Pages *PageSet
	Clip  Insets
	Alpha float64 // 0 (transparent) to 1 (opaque)
	// Snap is the drag granularity in px (NewImage sets 5).
	Snap float64
	// BoundsW, if non-zero, keeps the visible rectangle within x in
	// [0, BoundsW] while dragging.
	BoundsW float64
	// Wrap turns on text wrap-around; it has an effect once a Flow is set.
	Wrap bool
	// WrapMargin is the clear space kept between wrapped text and the
	// image (NewImage sets 8).
	WrapMargin float64

	ChromeStyle ImageStyle
	Painter     ImagePainter

	// OnChange is called after any change made by dragging or the toolbar.
	OnChange func(*Image)

	flow  *TextFlow
	mode  Mode
	scale float64 // displayed size / nominal size; 1 unless inline-fitted
	tool  ImageTool
	drag  *imageDrag
	hover string // cursor the pointer position wants
}

type imageDrag struct {
	what           string // "move", "left", "right", "top", "bottom", "alpha"
	startX, startY float64
	startImgX      float64
	startClip      Insets
}

// NewImage returns an image of src displayed at (x, y) at its natural
// size, fully opaque, in ModeView with the Move tool.
func NewImage(src ImageSource, x, y float64) *Image {
	w, h := src.Size()
	return &Image{
		Src: src, X: x, Y: y, W: w, H: h, Alpha: 1, Snap: 5, WrapMargin: 8, scale: 1,
		ChromeStyle: DefaultImageStyle(),
	}
}

// SetPainter implements PainterSetter.
func (im *Image) SetPainter(p ImagePainter) { im.Painter = p }

// SetFlow sets the text flow to wrap around and registers the image as
// one of its obstacles (removing it from any previous flow).
func (im *Image) SetFlow(f *TextFlow) {
	if im.flow != nil {
		im.flow.RemoveObstacle(im)
	}
	im.flow = f
	if f != nil {
		f.AddObstacle(im)
	}
}

// Flow returns the text flow set with SetFlow.
func (im *Image) Flow() *TextFlow { return im.flow }

// SetWrap turns text wrap-around on or off.
func (im *Image) SetWrap(on bool) {
	im.Wrap = on
}

// Dragging reports whether a drag is in progress.
func (im *Image) Dragging() bool { return im.drag != nil }

// Mode returns the current mode.
func (im *Image) Mode() Mode { return im.mode }

// SetMode switches mode; leaving edit mode cancels any drag. Returns
// true if a redraw is needed.
func (im *Image) SetMode(m Mode) bool {
	if m == im.mode {
		return false
	}
	im.mode = m
	if m == ModeView {
		im.drag, im.hover = nil, ""
	}
	return true
}

// Tool returns the active edit tool.
func (im *Image) Tool() ImageTool { return im.tool }

// SetTool picks the edit tool.
func (im *Image) SetTool(t ImageTool) { im.tool = t }

// Visible returns the visible (cropped) rectangle: x, y, w, h.
func (im *Image) Visible() (x, y, w, h float64) {
	im.resolve()
	sc := im.sc()
	return im.X + im.Clip.Left*sc, im.Y + im.Clip.Top*sc,
		(im.W - im.Clip.Left - im.Clip.Right) * sc, (im.H - im.Clip.Top - im.Clip.Bottom) * sc
}

// sc is the displayed-to-nominal scale: 1 unless the image was fitted
// into the text by the flow.
func (im *Image) sc() float64 {
	if im.Placement == PlaceInline && im.scale > 0 {
		return im.scale
	}
	return 1
}

func (im *Image) pageSet() *PageSet {
	if im.Pages != nil {
		return im.Pages
	}
	if im.flow != nil {
		return im.flow.Pages
	}
	return nil
}

// resolve updates X, Y from the Anchor.
func (im *Image) resolve() {
	if ps := im.pageSet(); im.Anchor != nil && ps != nil && im.Placement == PlaceFloating {
		im.X, im.Y = ps.Resolve(*im.Anchor)
	}
}

// syncAnchor re-derives the Anchor from X, Y after a drag.
func (im *Image) syncAnchor() {
	if ps := im.pageSet(); im.Anchor != nil && ps != nil {
		*im.Anchor = ps.AnchorAt(im.X, im.Y)
	}
}

// Measure implements InlineObject: the image's visible part, scaled
// down (never up) to fit maxW x maxH, keeping its aspect ratio.
func (im *Image) Measure(_ Painter, maxW, maxH float64) (float64, float64) {
	vw, vh := im.W-im.Clip.Left-im.Clip.Right, im.H-im.Clip.Top-im.Clip.Bottom
	if vw <= 0 || vh <= 0 {
		return 0, 0
	}
	im.scale = min(1, maxW/vw, maxH/vh)
	return vw * im.scale, vh * im.scale
}

// DrawAt implements InlineObject: it draws the visible part with its
// top-left at (x, y) and remembers where, for the edit chrome.
func (im *Image) DrawAt(p Painter, x, y float64) {
	sc := im.sc()
	im.X, im.Y = x-im.Clip.Left*sc, y-im.Clip.Top*sc
	ip, ok := p.(ImagePainter)
	if !ok {
		return
	}
	nw, nh := im.Src.Size()
	vw, vh := im.W-im.Clip.Left-im.Clip.Right, im.H-im.Clip.Top-im.Clip.Bottom
	sx, sy := nw/im.W, nh/im.H
	ip.SetGlobalAlpha(im.Alpha)
	ip.DrawImage(im.Src, im.Clip.Left*sx, im.Clip.Top*sy, vw*sx, vh*sy, x, y, vw*sc, vh*sc)
	ip.SetGlobalAlpha(1)
}

// Exclusion implements Obstacle: while Wrap is on, the visible
// rectangle plus WrapMargin is off limits to text.
func (im *Image) Exclusion(y0, y1 float64) (x0, x1 float64, ok bool) {
	if !im.Wrap || im.Placement == PlaceInline {
		return 0, 0, false
	}
	vx, vy, vw, vh := im.Visible()
	m := im.WrapMargin
	if vw <= 0 || vh <= 0 || y1 <= vy-m || y0 >= vy+vh+m {
		return 0, 0, false
	}
	return vx - m, vx + vw + m, true
}

func (im *Image) snap(v float64) float64 {
	if im.Snap <= 0 {
		return v
	}
	return math.Round(v/im.Snap) * im.Snap
}

func (im *Image) changed() {
	if im.OnChange != nil {
		im.OnChange(im)
	}
}

// --- toolbar -------------------------------------------------------

var toolbarLabels = [...]string{"Move", "Clip", "Alpha", "Wrap"}

// buttonRect returns toolbar button i (0-2 tools, 3 Wrap): x, y, w, h.
func (im *Image) buttonRect(i int) [4]float64 {
	s := im.ChromeStyle
	vx, vy, _, _ := im.Visible()
	return [4]float64{vx + float64(i)*(s.ButtonW+4), max(vy-s.ButtonH-6, 0), s.ButtonW, s.ButtonH}
}

// sliderRect returns the alpha slider's track: x, y, w, h.
func (im *Image) sliderRect() [4]float64 {
	s := im.ChromeStyle
	vx, vy, vw, vh := im.Visible()
	return [4]float64{vx, vy + vh + s.SliderGap, vw, s.SliderH}
}

// handleRect returns the crop handle on an edge ("left", "right",
// "top", "bottom"): x, y, w, h.
func (im *Image) handleRect(edge string) [4]float64 {
	hs := im.ChromeStyle.HandleSize
	vx, vy, vw, vh := im.Visible()
	switch edge {
	case "left":
		return [4]float64{vx - hs/2, vy + vh/2 - hs, hs, 2 * hs}
	case "right":
		return [4]float64{vx + vw - hs/2, vy + vh/2 - hs, hs, 2 * hs}
	case "top":
		return [4]float64{vx + vw/2 - hs, vy - hs/2, 2 * hs, hs}
	default:
		return [4]float64{vx + vw/2 - hs, vy + vh - hs/2, 2 * hs, hs}
	}
}

// --- pointer -------------------------------------------------------

func (im *Image) contains(x, y float64) bool {
	vx, vy, vw, vh := im.Visible()
	return inRect(x, y, [4]float64{vx, vy, vw, vh})
}

// whatAt returns what a press at (x, y) would grab: a toolbar button
// ("button:N"), a clip handle, the alpha slider, or the image body
// ("move"); "" for nothing.
func (im *Image) whatAt(x, y float64) string {
	for i := range toolbarLabels {
		if inRect(x, y, im.buttonRect(i)) {
			return "button:" + strconv.Itoa(i)
		}
	}
	switch im.tool {
	case ToolClip:
		for _, e := range [...]string{"left", "right", "top", "bottom"} {
			if inRect(x, y, im.handleRect(e)) {
				return e
			}
		}
	case ToolAlpha:
		t := im.sliderRect()
		if inRect(x, y, [4]float64{t[0] - 8, t[1] - 8, t[2] + 16, t[3] + 16}) {
			return "alpha"
		}
	case ToolMove:
		if im.Placement == PlaceFloating && im.contains(x, y) {
			return "move"
		}
	}
	return ""
}

// MouseDown handles toolbar clicks and starts a drag with the active
// tool (edit mode only).
func (im *Image) MouseDown(x, y float64) bool {
	if im.mode != ModeEdit {
		return false
	}
	what := im.whatAt(x, y)
	switch {
	case what == "":
		return false
	case len(what) > 7 && what[:7] == "button:":
		i, _ := strconv.Atoi(what[7:])
		if im.Placement == PlaceInline && (i == int(ToolMove) || i == 3) {
			return false // an inline image has no Move tool and doesn't wrap
		}
		if i == 3 {
			im.Wrap = !im.Wrap
		} else {
			im.tool = ImageTool(i)
		}
		im.changed()
		return true
	}
	im.drag = &imageDrag{what: what, startX: x, startY: y, startImgX: im.X, startClip: im.Clip}
	if what == "alpha" {
		im.setAlphaFrom(x)
	}
	return true
}

func (im *Image) setAlphaFrom(x float64) {
	t := im.sliderRect()
	a := (x - t[0]) / t[2]
	a = math.Round(min(max(a, 0), 1)*20) / 20 // 5% steps
	if a != im.Alpha {
		im.Alpha = a
		im.changed()
	}
}

// MouseMove continues a drag, or updates the hover cursor.
func (im *Image) MouseMove(x, y float64) bool {
	if im.mode != ModeEdit {
		return false
	}
	d := im.drag
	if d == nil {
		old := im.hover
		im.hover = ""
		switch w := im.whatAt(x, y); w {
		case "move":
			im.hover = "move"
		case "left", "right":
			im.hover = "ew-resize"
		case "top", "bottom":
			im.hover = "ns-resize"
		case "alpha", "":
		default:
			im.hover = "pointer"
		}
		return im.hover != old
	}
	dx, dy := x-d.startX, y-d.startY
	if d.what != "move" { // clip drags happen in displayed pixels; clips are nominal
		dx, dy = dx/im.sc(), dy/im.sc()
	}
	minSize := max(im.Snap, 10)
	before := *im
	switch d.what {
	case "move":
		nx := im.snap(d.startImgX + dx)
		if ps := im.pageSet(); im.Anchor != nil && ps != nil { // snap relative to the page
			px, _, _, _ := ps.PageRect(min(max(im.Anchor.Page, 0), len(ps.Pages)-1))
			nx = px + im.snap(d.startImgX+dx-px)
		}
		if im.BoundsW > 0 {
			_, _, vw, _ := im.Visible()
			nx = min(nx, im.BoundsW-vw-im.Clip.Left)
		}
		im.X = max(nx, -im.Clip.Left)
		im.syncAnchor()
	case "left":
		im.Clip.Left = min(max(im.snap(d.startClip.Left+dx), 0), im.W-im.Clip.Right-minSize)
	case "right":
		im.Clip.Right = min(max(im.snap(d.startClip.Right-dx), 0), im.W-im.Clip.Left-minSize)
	case "top":
		im.Clip.Top = min(max(im.snap(d.startClip.Top+dy), 0), im.H-im.Clip.Bottom-minSize)
	case "bottom":
		im.Clip.Bottom = min(max(im.snap(d.startClip.Bottom-dy), 0), im.H-im.Clip.Top-minSize)
	case "alpha":
		im.setAlphaFrom(x)
		return true
	}
	if im.X == before.X && im.Clip == before.Clip {
		return false
	}
	im.changed()
	return true
}

// MouseUp ends a drag.
func (im *Image) MouseUp(x, y float64) bool {
	if im.drag == nil {
		return false
	}
	im.drag = nil
	return true
}

// MouseLeave clears the hover cursor.
func (im *Image) MouseLeave() bool {
	changed := im.hover != ""
	im.hover = ""
	return changed
}

// CSSCursor implements CursorProvider.
func (im *Image) CSSCursor() string {
	if im.mode != ModeEdit {
		return ""
	}
	if im.drag != nil {
		switch im.drag.what {
		case "left", "right":
			return "ew-resize"
		case "top", "bottom":
			return "ns-resize"
		}
		return "grabbing"
	}
	return im.hover
}

// --- drawing -------------------------------------------------------

// Draw paints the image (cropped, at Alpha) and, in edit mode, the
// toolbar and the active tool's handles.
func (im *Image) Draw() {
	p, s := im.Painter, im.ChromeStyle
	nw, nh := im.Src.Size()
	sx, sy := nw/im.W, nh/im.H
	vx, vy, vw, vh := im.Visible()
	sc := im.sc()

	if im.mode == ModeEdit && im.tool == ToolClip {
		// Show what's cropped away, dimmed, behind the kept part.
		p.SetGlobalAlpha(im.Alpha * s.GhostAlpha)
		p.DrawImage(im.Src, 0, 0, nw, nh, im.X, im.Y, im.W*sc, im.H*sc)
	}
	if im.Placement == PlaceFloating { // an inline image is drawn by the flow, via DrawAt
		p.SetGlobalAlpha(im.Alpha)
		p.DrawImage(im.Src, im.Clip.Left*sx, im.Clip.Top*sy, vw/sc*sx, vh/sc*sy, vx, vy, vw, vh)
		p.SetGlobalAlpha(1)
	}
	p.SetGlobalAlpha(1)

	if im.mode != ModeEdit {
		return
	}
	p.SetStrokeStyle(s.OutlineColor)
	p.SetLineWidth(1)
	p.StrokeRect(vx+0.5, vy+0.5, vw-1, vh-1)

	p.SetFont(s.Font)
	p.SetTextBaseline("middle")
	p.SetTextAlign("center")
	for i, label := range toolbarLabels {
		r := im.buttonRect(i)
		on := (i < 3 && ImageTool(i) == im.tool) || (i == 3 && im.Wrap)
		if on {
			p.SetFillStyle(s.ButtonActive)
		} else {
			p.SetFillStyle(s.ButtonBG)
		}
		p.FillRect(r[0], r[1], r[2], r[3])
		if on {
			p.SetFillStyle(s.ButtonOnText)
		} else {
			p.SetFillStyle(s.ButtonText)
		}
		p.FillText(label, r[0]+r[2]/2, r[1]+r[3]/2)
	}
	p.SetTextAlign("left")

	switch im.tool {
	case ToolClip:
		p.SetFillStyle(s.OutlineColor)
		for _, e := range [...]string{"left", "right", "top", "bottom"} {
			h := im.handleRect(e)
			p.FillRect(h[0], h[1], h[2], h[3])
		}
	case ToolAlpha:
		t := im.sliderRect()
		p.SetFillStyle(s.TrackColor)
		p.FillRect(t[0], t[1], t[2], t[3])
		p.SetFillStyle(s.OutlineColor)
		p.FillRect(t[0], t[1], t[2]*im.Alpha, t[3])
		p.FillRect(t[0]+t[2]*im.Alpha-3, t[1]-4, 6, t[3]+8)
		p.SetFillStyle(s.ButtonText)
		p.FillText(strconv.Itoa(int(math.Round(im.Alpha*100)))+"%", t[0], t[1]+t[3]+12)
	}
}

var (
	_ Component = (*Image)(nil)
	_ Obstacle  = (*Image)(nil)
)
