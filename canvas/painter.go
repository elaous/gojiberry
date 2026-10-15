package canvas

// Painter is the subset of Context2D the components here draw with.
// *html.Context2D (domrt/html) satisfies it as-is; it is declared here
// instead of reusing html.Canvas2D so this package's drawing code
// builds, and can be unit-tested, natively.
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

// DirectionPainter is a Painter that can draw text in a given direction ("ltr" or "rtl"): the direction of a
// string decides where its leading and trailing spaces and punctuation go. A painter without it draws right-to-left
// runs as the browser's default direction would.
type DirectionPainter interface {
	SetDirection(dir string)
}

// ImageSource is a decoded bitmap a component can draw. In a browser
// it is an HTMLImageElement (see LoadImage); tests use a fake.
type ImageSource interface {
	// Size is the bitmap's natural size in pixels.
	Size() (w, h float64)
}

// URLSource is an ImageSource that knows where it came from; only such
// images can be saved with a document.
type URLSource interface {
	ImageSource
	URL() string
}

// ImagePainter is a Painter that can also draw bitmaps and change the
// global alpha. NewPainter adapts an *html.Context2D to it.
type ImagePainter interface {
	Painter
	SetGlobalAlpha(alpha float64)

	// Save/Restore push and pop the painter's state (transform and
	// clip); Translate moves the origin; ClipRect restricts all later
	// drawing to a rectangle until the matching Restore.
	Save()
	Restore()
	Translate(x, y float64)
	// Rotate turns the origin's axes by angle radians (clockwise).
	Rotate(angle float64)
	ClipRect(x, y, w, h float64)

	// DrawImage draws the (sx, sy, sw, sh) region of src into the
	// (dx, dy, dw, dh) rectangle.
	DrawImage(src ImageSource, sx, sy, sw, sh, dx, dy, dw, dh float64)
}
