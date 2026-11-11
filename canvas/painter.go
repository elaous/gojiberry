package canvas

// Painter is the subset of Context2D the components draw with.
type Painter interface {
	SetFillStyle(style string)
	SetStrokeStyle(style string)
	SetLineWidth(width float64)
	SetFont(font string)
	SetTextAlign(align string)
	SetTextBaseline(baseline string)

	FillRect(x, y, w, h float64)
	StrokeRect(x, y, w, h float64)
	ClearRect(x, y, w, h float64)

	BeginPath()
	MoveTo(x, y float64)
	LineTo(x, y float64)
	Stroke()

	FillText(text string, x, y float64)
	MeasureTextWidth(text string) float64
}

// DirectionPainter is a Painter that draws text in a direction.
type DirectionPainter interface {
	SetDirection(dir string)
}

// ImageSource is a decoded bitmap a component can draw.
type ImageSource interface {
	// Size is the bitmap's natural size in pixels.
	Size() (w, h float64)
}

// URLSource is an ImageSource that knows its URL.
type URLSource interface {
	ImageSource
	URL() string
}

// ImagePainter is a Painter that also draws bitmaps.
type ImagePainter interface {
	Painter
	SetGlobalAlpha(alpha float64)

	// Save and Restore push and pop the painter state.
	Save()
	Restore()
	Translate(x, y float64)
	// Rotate turns the axes by angle radians, clockwise.
	Rotate(angle float64)
	ClipRect(x, y, w, h float64)

	// DrawImage draws a source region of src into a destination rectangle.
	DrawImage(src ImageSource, sx, sy, sw, sh, dx, dy, dw, dh float64)
}
