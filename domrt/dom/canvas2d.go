//go:build js && wasm

package dom

import "syscall/js"

// Context2D wraps a <canvas> element's 2D rendering context
// (canvas.getContext("2d")). Get/Call cover anything without a typed
// method below, the same escape hatch as the rest of this package.
type Context2D struct {
	Context js.Value
}

// GetContext2D returns e's 2D rendering context, or nil if e isn't a
// <canvas> (or the browser has no 2D context to give).
func (e *Element) GetContext2D() *Context2D {
	ctx := e.Element.Call("getContext", "2d")
	if ctx.IsUndefined() || ctx.IsNull() {
		return nil
	}
	return &Context2D{Context: ctx}
}

func (c *Context2D) Get(name string) js.Value { return c.Context.Get(name) }
func (c *Context2D) Call(method string, args ...any) js.Value {
	return c.Context.Call(method, args...)
}

// -- Style/state properties --

func (c *Context2D) FillStyle() string           { return c.Context.Get("fillStyle").String() }
func (c *Context2D) SetFillStyle(style string)   { c.Context.Set("fillStyle", style) }
func (c *Context2D) StrokeStyle() string         { return c.Context.Get("strokeStyle").String() }
func (c *Context2D) SetStrokeStyle(style string) { c.Context.Set("strokeStyle", style) }
func (c *Context2D) LineWidth() float64          { return c.Context.Get("lineWidth").Float() }
func (c *Context2D) SetLineWidth(width float64)  { c.Context.Set("lineWidth", width) }
func (c *Context2D) LineCap() string             { return c.Context.Get("lineCap").String() }
func (c *Context2D) SetLineCap(cap string)       { c.Context.Set("lineCap", cap) }
func (c *Context2D) LineJoin() string            { return c.Context.Get("lineJoin").String() }
func (c *Context2D) SetLineJoin(join string)     { c.Context.Set("lineJoin", join) }
func (c *Context2D) Font() string                { return c.Context.Get("font").String() }
func (c *Context2D) SetFont(font string)         { c.Context.Set("font", font) }
func (c *Context2D) TextAlign() string           { return c.Context.Get("textAlign").String() }
func (c *Context2D) SetTextAlign(align string)   { c.Context.Set("textAlign", align) }
func (c *Context2D) TextBaseline() string        { return c.Context.Get("textBaseline").String() }
func (c *Context2D) SetTextBaseline(baseline string) {
	c.Context.Set("textBaseline", baseline)
}
func (c *Context2D) GlobalAlpha() float64         { return c.Context.Get("globalAlpha").Float() }
func (c *Context2D) SetGlobalAlpha(alpha float64) { c.Context.Set("globalAlpha", alpha) }
func (c *Context2D) GlobalCompositeOperation() string {
	return c.Context.Get("globalCompositeOperation").String()
}
func (c *Context2D) SetGlobalCompositeOperation(op string) {
	c.Context.Set("globalCompositeOperation", op)
}

// -- Rectangles --

func (c *Context2D) ClearRect(x, y, w, h float64)  { c.Context.Call("clearRect", x, y, w, h) }
func (c *Context2D) FillRect(x, y, w, h float64)   { c.Context.Call("fillRect", x, y, w, h) }
func (c *Context2D) StrokeRect(x, y, w, h float64) { c.Context.Call("strokeRect", x, y, w, h) }

// -- Paths --

func (c *Context2D) BeginPath()          { c.Context.Call("beginPath") }
func (c *Context2D) ClosePath()          { c.Context.Call("closePath") }
func (c *Context2D) MoveTo(x, y float64) { c.Context.Call("moveTo", x, y) }
func (c *Context2D) LineTo(x, y float64) { c.Context.Call("lineTo", x, y) }
func (c *Context2D) Arc(x, y, radius, startAngle, endAngle float64, counterclockwise bool) {
	c.Context.Call("arc", x, y, radius, startAngle, endAngle, counterclockwise)
}
func (c *Context2D) ArcTo(x1, y1, x2, y2, radius float64) {
	c.Context.Call("arcTo", x1, y1, x2, y2, radius)
}
func (c *Context2D) BezierCurveTo(cp1x, cp1y, cp2x, cp2y, x, y float64) {
	c.Context.Call("bezierCurveTo", cp1x, cp1y, cp2x, cp2y, x, y)
}
func (c *Context2D) QuadraticCurveTo(cpx, cpy, x, y float64) {
	c.Context.Call("quadraticCurveTo", cpx, cpy, x, y)
}
func (c *Context2D) Rect(x, y, w, h float64) { c.Context.Call("rect", x, y, w, h) }
func (c *Context2D) Fill()                   { c.Context.Call("fill") }
func (c *Context2D) Stroke()                 { c.Context.Call("stroke") }
func (c *Context2D) Clip()                   { c.Context.Call("clip") }

// -- Text --

func (c *Context2D) FillText(text string, x, y float64)   { c.Context.Call("fillText", text, x, y) }
func (c *Context2D) StrokeText(text string, x, y float64) { c.Context.Call("strokeText", text, x, y) }
func (c *Context2D) MeasureTextWidth(text string) float64 {
	return c.Context.Call("measureText", text).Get("width").Float()
}

// -- Images --

func (c *Context2D) DrawImage(image js.Value, dx, dy float64) {
	c.Context.Call("drawImage", image, dx, dy)
}
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

func (c *Context2D) Save()                  { c.Context.Call("save") }
func (c *Context2D) Restore()               { c.Context.Call("restore") }
func (c *Context2D) Translate(x, y float64) { c.Context.Call("translate", x, y) }
func (c *Context2D) Rotate(angle float64)   { c.Context.Call("rotate", angle) }
func (c *Context2D) Scale(x, y float64)     { c.Context.Call("scale", x, y) }
func (c *Context2D) Transform(a, b, cc, d, e, f float64) {
	c.Context.Call("transform", a, b, cc, d, e, f)
}
func (c *Context2D) SetTransform(a, b, cc, d, e, f float64) {
	c.Context.Call("setTransform", a, b, cc, d, e, f)
}
func (c *Context2D) ResetTransform() { c.Context.Call("resetTransform") }
