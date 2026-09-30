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
