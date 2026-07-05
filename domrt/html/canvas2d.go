//go:build js && wasm

package html

import "syscall/js"

// Context2D wraps a <canvas> element's 2D rendering context
// (canvas.getContext("2d")). Get/Call cover anything without a typed
// method below, the same escape hatch as the rest of this package.
type Context2D struct {
	Context js.Value
}

// GetContext2D returns c's 2D rendering context, or nil if the browser
// has no 2D context to give.
func (c *CanvasElement) GetContext2D() *Context2D {
	ctx := c.Call("getContext", "2d")
	if ctx.IsUndefined() || ctx.IsNull() {
		return nil
	}
	return &Context2D{Context: ctx}
}

// Get returns a named property not covered by a typed getter below.
func (c *Context2D) Get(name string) js.Value { return c.Context.Get(name) }

// Call invokes a method not covered by a typed wrapper below.
func (c *Context2D) Call(method string, args ...any) js.Value {
	return c.Context.Call(method, args...)
}

// -- Style/state properties --

// FillStyle returns the color/gradient/pattern used by Fill/FillRect/FillText.
func (c *Context2D) FillStyle() string { return c.Context.Get("fillStyle").String() }

// SetFillStyle sets the style used by Fill/FillRect/FillText.
func (c *Context2D) SetFillStyle(style string) { c.Context.Set("fillStyle", style) }

// StrokeStyle returns the color/gradient/pattern used by Stroke/StrokeRect/StrokeText.
func (c *Context2D) StrokeStyle() string { return c.Context.Get("strokeStyle").String() }

// SetStrokeStyle sets the style used by Stroke/StrokeRect/StrokeText.
func (c *Context2D) SetStrokeStyle(style string) { c.Context.Set("strokeStyle", style) }

// LineWidth returns the current stroke width in canvas units.
func (c *Context2D) LineWidth() float64 { return c.Context.Get("lineWidth").Float() }

// SetLineWidth sets the stroke width in canvas units.
func (c *Context2D) SetLineWidth(width float64) { c.Context.Set("lineWidth", width) }

// LineCap returns the current line cap style ("butt", "round", or "square").
func (c *Context2D) LineCap() string { return c.Context.Get("lineCap").String() }

// SetLineCap sets the line cap style ("butt", "round", or "square").
func (c *Context2D) SetLineCap(cap string) { c.Context.Set("lineCap", cap) }

// LineJoin returns the current line join style ("round", "bevel", or "miter").
func (c *Context2D) LineJoin() string { return c.Context.Get("lineJoin").String() }

// SetLineJoin sets the line join style ("round", "bevel", or "miter").
func (c *Context2D) SetLineJoin(join string) { c.Context.Set("lineJoin", join) }

// Font returns the current CSS font shorthand used by FillText/StrokeText/MeasureTextWidth.
func (c *Context2D) Font() string { return c.Context.Get("font").String() }

// SetFont sets the CSS font shorthand (e.g. "16px sans-serif") used for text.
func (c *Context2D) SetFont(font string) { c.Context.Set("font", font) }

// TextAlign returns the current horizontal text alignment.
func (c *Context2D) TextAlign() string { return c.Context.Get("textAlign").String() }

// SetTextAlign sets the horizontal text alignment ("left", "right", "center", "start", "end").
func (c *Context2D) SetTextAlign(align string) { c.Context.Set("textAlign", align) }

// TextBaseline returns the current vertical text alignment.
func (c *Context2D) TextBaseline() string { return c.Context.Get("textBaseline").String() }

// SetTextBaseline sets the vertical text alignment (e.g. "top", "middle", "alphabetic").
func (c *Context2D) SetTextBaseline(baseline string) {
	c.Context.Set("textBaseline", baseline)
}

// GlobalAlpha returns the alpha (0-1) applied to all subsequent drawing.
func (c *Context2D) GlobalAlpha() float64 { return c.Context.Get("globalAlpha").Float() }

// SetGlobalAlpha sets the alpha (0-1) applied to all subsequent drawing.
func (c *Context2D) SetGlobalAlpha(alpha float64) { c.Context.Set("globalAlpha", alpha) }

// GlobalCompositeOperation returns how new drawing is composited over existing content.
func (c *Context2D) GlobalCompositeOperation() string {
	return c.Context.Get("globalCompositeOperation").String()
}

// SetGlobalCompositeOperation sets how new drawing is composited over existing content (e.g. "source-over", "multiply").
func (c *Context2D) SetGlobalCompositeOperation(op string) {
	c.Context.Set("globalCompositeOperation", op)
}

// -- Rectangles --

// ClearRect clears the given rectangle to transparent black.
func (c *Context2D) ClearRect(x, y, w, h float64) { c.Context.Call("clearRect", x, y, w, h) }

// FillRect fills the given rectangle with the current fill style.
func (c *Context2D) FillRect(x, y, w, h float64) { c.Context.Call("fillRect", x, y, w, h) }

// StrokeRect outlines the given rectangle with the current stroke style.
func (c *Context2D) StrokeRect(x, y, w, h float64) { c.Context.Call("strokeRect", x, y, w, h) }

// -- Paths --

// BeginPath starts a new path, discarding any current subpaths.
func (c *Context2D) BeginPath() { c.Context.Call("beginPath") }

// ClosePath closes the current subpath by drawing a line back to its start.
func (c *Context2D) ClosePath() { c.Context.Call("closePath") }

// MoveTo starts a new subpath at (x, y) without drawing.
func (c *Context2D) MoveTo(x, y float64) { c.Context.Call("moveTo", x, y) }

// LineTo adds a straight line from the current point to (x, y).
func (c *Context2D) LineTo(x, y float64) { c.Context.Call("lineTo", x, y) }

// Arc adds a circular arc centered at (x, y) with the given radius and angle range.
func (c *Context2D) Arc(x, y, radius, startAngle, endAngle float64, counterclockwise bool) {
	c.Context.Call("arc", x, y, radius, startAngle, endAngle, counterclockwise)
}

// ArcTo adds an arc defined by two tangent lines through (x1,y1) and (x2,y2) with the given radius.
func (c *Context2D) ArcTo(x1, y1, x2, y2, radius float64) {
	c.Context.Call("arcTo", x1, y1, x2, y2, radius)
}

// BezierCurveTo adds a cubic Bezier curve from the current point to (x, y) using the two control points.
func (c *Context2D) BezierCurveTo(cp1x, cp1y, cp2x, cp2y, x, y float64) {
	c.Context.Call("bezierCurveTo", cp1x, cp1y, cp2x, cp2y, x, y)
}

// QuadraticCurveTo adds a quadratic Bezier curve from the current point to (x, y) using the control point.
func (c *Context2D) QuadraticCurveTo(cpx, cpy, x, y float64) {
	c.Context.Call("quadraticCurveTo", cpx, cpy, x, y)
}

// Rect adds a rectangular subpath at (x, y) with the given size.
func (c *Context2D) Rect(x, y, w, h float64) { c.Context.Call("rect", x, y, w, h) }

// Fill fills the current path with the current fill style.
func (c *Context2D) Fill() { c.Context.Call("fill") }

// Stroke outlines the current path with the current stroke style.
func (c *Context2D) Stroke() { c.Context.Call("stroke") }

// Clip restricts subsequent drawing to the interior of the current path.
func (c *Context2D) Clip() { c.Context.Call("clip") }

// -- Text --

// FillText draws text filled with the current fill style, with its start point at (x, y).
func (c *Context2D) FillText(text string, x, y float64) { c.Context.Call("fillText", text, x, y) }

// StrokeText draws text outlined with the current stroke style, with its start point at (x, y).
func (c *Context2D) StrokeText(text string, x, y float64) { c.Context.Call("strokeText", text, x, y) }

// MeasureTextWidth returns the rendered width of text in the current font, in canvas units.
func (c *Context2D) MeasureTextWidth(text string) float64 {
	return c.Context.Call("measureText", text).Get("width").Float()
}

// -- Images --

// DrawImage draws image (a <img>/<canvas>/<video> element or ImageBitmap) at (dx, dy) in its natural size.
func (c *Context2D) DrawImage(image js.Value, dx, dy float64) {
	c.Context.Call("drawImage", image, dx, dy)
}

// DrawImageScaled draws image at (dx, dy) scaled to (dw, dh).
func (c *Context2D) DrawImageScaled(image js.Value, dx, dy, dw, dh float64) {
	c.Context.Call("drawImage", image, dx, dy, dw, dh)
}

// ImageData holds pixel data read back from the canvas: Pix is
// RGBA8-per-pixel, row-major, len(Pix) == Width*Height*4.
type ImageData struct {
	Width, Height int
	Pix           []byte
}

// GetImageData reads back the pixels in the given rectangle.
func (c *Context2D) GetImageData(x, y, w, h int) ImageData {
	data := c.Context.Call("getImageData", x, y, w, h)
	arr := data.Get("data")
	pix := make([]byte, arr.Length())
	js.CopyBytesToGo(pix, arr)
	return ImageData{Width: data.Get("width").Int(), Height: data.Get("height").Int(), Pix: pix}
}

// -- Transforms --

// Save pushes the current drawing state (styles, transform, clip) onto a stack.
func (c *Context2D) Save() { c.Context.Call("save") }

// Restore pops the most recently saved drawing state off the stack.
func (c *Context2D) Restore() { c.Context.Call("restore") }

// Translate moves the canvas origin by (x, y).
func (c *Context2D) Translate(x, y float64) { c.Context.Call("translate", x, y) }

// Rotate rotates the canvas coordinate system by angle radians (clockwise).
func (c *Context2D) Rotate(angle float64) { c.Context.Call("rotate", angle) }

// Scale scales the canvas coordinate system by (x, y).
func (c *Context2D) Scale(x, y float64) { c.Context.Call("scale", x, y) }

// Transform multiplies the current transform by the given matrix (a, b, cc, d, e, f).
func (c *Context2D) Transform(a, b, cc, d, e, f float64) {
	c.Context.Call("transform", a, b, cc, d, e, f)
}

// SetTransform replaces the current transform with the given matrix (a, b, cc, d, e, f).
func (c *Context2D) SetTransform(a, b, cc, d, e, f float64) {
	c.Context.Call("setTransform", a, b, cc, d, e, f)
}

// ResetTransform resets the current transform to the identity matrix.
func (c *Context2D) ResetTransform() { c.Context.Call("resetTransform") }
