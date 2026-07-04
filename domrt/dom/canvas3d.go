//go:build js && wasm

package dom

import "syscall/js"

// Context3D wraps a <canvas> element's WebGL rendering context
// (canvas.getContext("webgl2"), falling back to "webgl"). Get/Call
// cover anything without a typed method below -- WebGL's real surface
// is hundreds of methods and constants; this wraps the core pipeline
// (shaders, programs, buffers, textures, drawing) rather than
// reimplementing the whole spec speculatively.
//
// Constants (TRIANGLES, ARRAY_BUFFER, COLOR_BUFFER_BIT, ...) are
// exposed as methods that read the real value off the live context
// object (gl.Get("TRIANGLES")) instead of hardcoded Go integers, so
// they can't silently drift from whatever the browser actually
// defines.
type Context3D struct {
	Context js.Value
}

// GetContext3D returns e's WebGL rendering context (preferring WebGL2,
// falling back to WebGL1), or nil if e isn't a <canvas> or the browser
// supports neither.
func (e *Element) GetContext3D() *Context3D {
	ctx := e.Element.Call("getContext", "webgl2")
	if ctx.IsUndefined() || ctx.IsNull() {
		ctx = e.Element.Call("getContext", "webgl")
	}
	if ctx.IsUndefined() || ctx.IsNull() {
		return nil
	}
	return &Context3D{Context: ctx}
}

func (c *Context3D) Get(name string) js.Value {
	return c.Context.Get(name)
}

func (c *Context3D) Call(method string, args ...any) js.Value {
	return c.Context.Call(method, args...)
}

// constant reads a named constant (e.g. "TRIANGLES", "ARRAY_BUFFER")
// off the live context.
func (c *Context3D) constant(name string) int {
	return c.Context.Get(name).Int()
}

// -- Constants used by the methods below (see the package doc comment
// on why these read from the live context instead of being hardcoded) --

func (c *Context3D) ARRAY_BUFFER() int         { return c.constant("ARRAY_BUFFER") }
func (c *Context3D) ELEMENT_ARRAY_BUFFER() int { return c.constant("ELEMENT_ARRAY_BUFFER") }
func (c *Context3D) STATIC_DRAW() int          { return c.constant("STATIC_DRAW") }
func (c *Context3D) DYNAMIC_DRAW() int         { return c.constant("DYNAMIC_DRAW") }
func (c *Context3D) STREAM_DRAW() int          { return c.constant("STREAM_DRAW") }

func (c *Context3D) VERTEX_SHADER() int   { return c.constant("VERTEX_SHADER") }
func (c *Context3D) FRAGMENT_SHADER() int { return c.constant("FRAGMENT_SHADER") }
func (c *Context3D) COMPILE_STATUS() int  { return c.constant("COMPILE_STATUS") }
func (c *Context3D) LINK_STATUS() int     { return c.constant("LINK_STATUS") }

func (c *Context3D) FLOAT() int          { return c.constant("FLOAT") }
func (c *Context3D) UNSIGNED_BYTE() int  { return c.constant("UNSIGNED_BYTE") }
func (c *Context3D) UNSIGNED_SHORT() int { return c.constant("UNSIGNED_SHORT") }
func (c *Context3D) UNSIGNED_INT() int   { return c.constant("UNSIGNED_INT") }

func (c *Context3D) TRIANGLES() int      { return c.constant("TRIANGLES") }
func (c *Context3D) TRIANGLE_STRIP() int { return c.constant("TRIANGLE_STRIP") }
func (c *Context3D) TRIANGLE_FAN() int   { return c.constant("TRIANGLE_FAN") }
func (c *Context3D) LINES() int          { return c.constant("LINES") }
func (c *Context3D) LINE_STRIP() int     { return c.constant("LINE_STRIP") }
func (c *Context3D) POINTS() int         { return c.constant("POINTS") }

func (c *Context3D) COLOR_BUFFER_BIT() int { return c.constant("COLOR_BUFFER_BIT") }
func (c *Context3D) DEPTH_BUFFER_BIT() int { return c.constant("DEPTH_BUFFER_BIT") }
func (c *Context3D) DEPTH_TEST() int       { return c.constant("DEPTH_TEST") }
func (c *Context3D) BLEND() int            { return c.constant("BLEND") }
func (c *Context3D) CULL_FACE() int        { return c.constant("CULL_FACE") }

func (c *Context3D) TEXTURE_2D() int { return c.constant("TEXTURE_2D") }
func (c *Context3D) TEXTURE0() int   { return c.constant("TEXTURE0") }
func (c *Context3D) RGBA() int       { return c.constant("RGBA") }
func (c *Context3D) RGB() int        { return c.constant("RGB") }

func (c *Context3D) TEXTURE_MIN_FILTER() int { return c.constant("TEXTURE_MIN_FILTER") }
func (c *Context3D) TEXTURE_MAG_FILTER() int { return c.constant("TEXTURE_MAG_FILTER") }
func (c *Context3D) LINEAR() int             { return c.constant("LINEAR") }
func (c *Context3D) NEAREST() int            { return c.constant("NEAREST") }
func (c *Context3D) CLAMP_TO_EDGE() int      { return c.constant("CLAMP_TO_EDGE") }
func (c *Context3D) TEXTURE_WRAP_S() int     { return c.constant("TEXTURE_WRAP_S") }
func (c *Context3D) TEXTURE_WRAP_T() int     { return c.constant("TEXTURE_WRAP_T") }

// -- Viewport / clearing --

func (c *Context3D) Viewport(x, y, width, height int) {
	c.Context.Call("viewport", x, y, width, height)
}

func (c *Context3D) ClearColor(r, g, b, a float64) {
	c.Context.Call("clearColor", r, g, b, a)
}

func (c *Context3D) Clear(mask int) { c.Context.Call("clear", mask) }

func (c *Context3D) Enable(cap int)  { c.Context.Call("enable", cap) }
func (c *Context3D) Disable(cap int) { c.Context.Call("disable", cap) }

// -- Shaders / programs --

func (c *Context3D) CreateShader(shaderType int) js.Value {
	return c.Context.Call("createShader", shaderType)
}

func (c *Context3D) ShaderSource(shader js.Value, source string) {
	c.Context.Call("shaderSource", shader, source)
}

func (c *Context3D) CompileShader(shader js.Value) {
	c.Context.Call("compileShader", shader)
}

func (c *Context3D) GetShaderParameter(shader js.Value, pname int) js.Value {
	return c.Context.Call("getShaderParameter", shader, pname)
}

func (c *Context3D) GetShaderInfoLog(shader js.Value) string {
	return c.Context.Call("getShaderInfoLog", shader).String()
}

func (c *Context3D) CreateProgram() js.Value { return c.Context.Call("createProgram") }

func (c *Context3D) AttachShader(program, shader js.Value) {
	c.Context.Call("attachShader", program, shader)
}

func (c *Context3D) LinkProgram(program js.Value) { c.Context.Call("linkProgram", program) }

func (c *Context3D) GetProgramParameter(program js.Value, pname int) js.Value {
	return c.Context.Call("getProgramParameter", program, pname)
}

func (c *Context3D) GetProgramInfoLog(program js.Value) string {
	return c.Context.Call("getProgramInfoLog", program).String()
}

func (c *Context3D) UseProgram(program js.Value) { c.Context.Call("useProgram", program) }

// CompileProgram is a convenience wrapping the usual
// create-shader/compile/attach/link sequence for a vertex+fragment
// shader pair, returning the linked program or an error naming
// whichever shader (or the link step) failed and its info log.
func (c *Context3D) CompileProgram(vertexSrc, fragmentSrc string) (js.Value, error) {
	compile := func(shaderType int, src string) (js.Value, error) {
		shader := c.CreateShader(shaderType)
		c.ShaderSource(shader, src)
		c.CompileShader(shader)
		if !c.GetShaderParameter(shader, c.COMPILE_STATUS()).Bool() {
			return js.Undefined(), &webglError{"compiling shader: " + c.GetShaderInfoLog(shader)}
		}
		return shader, nil
	}
	vs, err := compile(c.VERTEX_SHADER(), vertexSrc)
	if err != nil {
		return js.Undefined(), err
	}
	fs, err := compile(c.FRAGMENT_SHADER(), fragmentSrc)
	if err != nil {
		return js.Undefined(), err
	}
	program := c.CreateProgram()
	c.AttachShader(program, vs)
	c.AttachShader(program, fs)
	c.LinkProgram(program)
	if !c.GetProgramParameter(program, c.LINK_STATUS()).Bool() {
		return js.Undefined(), &webglError{"linking program: " + c.GetProgramInfoLog(program)}
	}
	return program, nil
}

type webglError struct{ msg string }

func (e *webglError) Error() string { return e.msg }

// -- Buffers --

func (c *Context3D) CreateBuffer() js.Value { return c.Context.Call("createBuffer") }

func (c *Context3D) BindBuffer(target int, buffer js.Value) {
	c.Context.Call("bindBuffer", target, buffer)
}

// BufferDataFloat32 uploads data to the buffer currently bound to
// target (ARRAY_BUFFER/ELEMENT_ARRAY_BUFFER).
func (c *Context3D) BufferDataFloat32(target int, data []float32, usage int) {
	jsArr := js.Global().Get("Float32Array").New(len(data))
	for i, v := range data {
		jsArr.SetIndex(i, v)
	}
	c.Context.Call("bufferData", target, jsArr, usage)
}

func (c *Context3D) BufferDataUint16(target int, data []uint16, usage int) {
	jsArr := js.Global().Get("Uint16Array").New(len(data))
	for i, v := range data {
		jsArr.SetIndex(i, int(v))
	}
	c.Context.Call("bufferData", target, jsArr, usage)
}

// -- Attributes / uniforms --

func (c *Context3D) GetAttribLocation(program js.Value, name string) int {
	return c.Context.Call("getAttribLocation", program, name).Int()
}

func (c *Context3D) EnableVertexAttribArray(index int) {
	c.Context.Call("enableVertexAttribArray", index)
}

func (c *Context3D) VertexAttribPointer(index, size, valueType int, normalized bool, stride, offset int) {
	c.Context.Call("vertexAttribPointer", index, size, valueType, normalized, stride, offset)
}

func (c *Context3D) GetUniformLocation(program js.Value, name string) js.Value {
	return c.Context.Call("getUniformLocation", program, name)
}

func (c *Context3D) Uniform1f(loc js.Value, x float64)    { c.Context.Call("uniform1f", loc, x) }
func (c *Context3D) Uniform2f(loc js.Value, x, y float64) { c.Context.Call("uniform2f", loc, x, y) }
func (c *Context3D) Uniform3f(loc js.Value, x, y, z float64) {
	c.Context.Call("uniform3f", loc, x, y, z)
}
func (c *Context3D) Uniform4f(loc js.Value, x, y, z, w float64) {
	c.Context.Call("uniform4f", loc, x, y, z, w)
}
func (c *Context3D) Uniform1i(loc js.Value, x int) { c.Context.Call("uniform1i", loc, x) }

func (c *Context3D) UniformMatrix4fv(loc js.Value, transpose bool, m [16]float32) {
	jsArr := js.Global().Get("Float32Array").New(16)
	for i, v := range m {
		jsArr.SetIndex(i, v)
	}
	c.Context.Call("uniformMatrix4fv", loc, transpose, jsArr)
}

// -- Textures --

func (c *Context3D) CreateTexture() js.Value { return c.Context.Call("createTexture") }

func (c *Context3D) BindTexture(target int, texture js.Value) {
	c.Context.Call("bindTexture", target, texture)
}

func (c *Context3D) ActiveTexture(unit int) { c.Context.Call("activeTexture", unit) }

func (c *Context3D) TexParameteri(target, pname, param int) {
	c.Context.Call("texParameteri", target, pname, param)
}

// TexImage2DElement uploads an <img>/<canvas>/<video> source (already
// loaded) as a texture's image data.
func (c *Context3D) TexImage2DElement(target, level, internalFormat int, format, texType int, source js.Value) {
	c.Context.Call("texImage2D", target, level, internalFormat, format, texType, source)
}

// -- Drawing --

func (c *Context3D) DrawArrays(mode, first, count int) {
	c.Context.Call("drawArrays", mode, first, count)
}

func (c *Context3D) DrawElements(mode, count, valueType, offset int) {
	c.Context.Call("drawElements", mode, count, valueType, offset)
}

// ReadPixels reads back a rectangle of the current framebuffer as
// RGBA8, the one format+type combination every WebGL implementation
// is guaranteed to support for the default framebuffer -- pass
// RGBA()/UNSIGNED_BYTE() for format/pixelType.
func (c *Context3D) ReadPixels(x, y, width, height, format, pixelType int) []byte {
	jsArr := js.Global().Get("Uint8Array").New(width * height * 4)
	c.Context.Call("readPixels", x, y, width, height, format, pixelType, jsArr)
	out := make([]byte, width*height*4)
	js.CopyBytesToGo(out, jsArr)
	return out
}
