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

// GetContext3D returns c's WebGL rendering context (preferring WebGL2,
// falling back to WebGL1), or nil if the browser supports neither.
func (c *CanvasElement) GetContext3D() *Context3D {
	ctx := c.Call("getContext", "webgl2")
	if ctx.IsUndefined() || ctx.IsNull() {
		ctx = c.Call("getContext", "webgl")
	}
	if ctx.IsUndefined() || ctx.IsNull() {
		return nil
	}
	return &Context3D{Context: ctx}
}

// Get returns a named property not covered by a typed method below.
func (c *Context3D) Get(name string) js.Value {
	return c.Context.Get(name)
}

// Call invokes a method not covered by a typed wrapper below.
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

// ARRAY_BUFFER is the buffer target for vertex attribute data.
func (c *Context3D) ARRAY_BUFFER() int { return c.constant("ARRAY_BUFFER") }

// ELEMENT_ARRAY_BUFFER is the buffer target for element/index data.
func (c *Context3D) ELEMENT_ARRAY_BUFFER() int { return c.constant("ELEMENT_ARRAY_BUFFER") }

// STATIC_DRAW hints that a buffer's contents are set once and used many times.
func (c *Context3D) STATIC_DRAW() int { return c.constant("STATIC_DRAW") }

// DYNAMIC_DRAW hints that a buffer's contents are changed often and used many times.
func (c *Context3D) DYNAMIC_DRAW() int { return c.constant("DYNAMIC_DRAW") }

// STREAM_DRAW hints that a buffer's contents are set once and used at most a few times.
func (c *Context3D) STREAM_DRAW() int { return c.constant("STREAM_DRAW") }

// VERTEX_SHADER identifies a shader as a vertex shader for CreateShader.
func (c *Context3D) VERTEX_SHADER() int { return c.constant("VERTEX_SHADER") }

// FRAGMENT_SHADER identifies a shader as a fragment shader for CreateShader.
func (c *Context3D) FRAGMENT_SHADER() int { return c.constant("FRAGMENT_SHADER") }

// COMPILE_STATUS is the GetShaderParameter pname reporting shader compile success.
func (c *Context3D) COMPILE_STATUS() int { return c.constant("COMPILE_STATUS") }

// LINK_STATUS is the GetProgramParameter pname reporting program link success.
func (c *Context3D) LINK_STATUS() int { return c.constant("LINK_STATUS") }

// FLOAT is the 32-bit floating point component type.
func (c *Context3D) FLOAT() int { return c.constant("FLOAT") }

// UNSIGNED_BYTE is the 8-bit unsigned integer component type.
func (c *Context3D) UNSIGNED_BYTE() int { return c.constant("UNSIGNED_BYTE") }

// UNSIGNED_SHORT is the 16-bit unsigned integer component type, commonly used for element indices.
func (c *Context3D) UNSIGNED_SHORT() int { return c.constant("UNSIGNED_SHORT") }

// UNSIGNED_INT is the 32-bit unsigned integer component type.
func (c *Context3D) UNSIGNED_INT() int { return c.constant("UNSIGNED_INT") }

// TRIANGLES is the draw mode where every 3 vertices form a triangle.
func (c *Context3D) TRIANGLES() int { return c.constant("TRIANGLES") }

// TRIANGLE_STRIP is the draw mode where each vertex after the first two forms a triangle with the previous two.
func (c *Context3D) TRIANGLE_STRIP() int { return c.constant("TRIANGLE_STRIP") }

// TRIANGLE_FAN is the draw mode where each vertex after the first two forms a triangle with the previous vertex and the first vertex.
func (c *Context3D) TRIANGLE_FAN() int { return c.constant("TRIANGLE_FAN") }

// LINES is the draw mode where every 2 vertices form a line segment.
func (c *Context3D) LINES() int { return c.constant("LINES") }

// LINE_STRIP is the draw mode where each vertex connects to the previous one.
func (c *Context3D) LINE_STRIP() int { return c.constant("LINE_STRIP") }

// POINTS is the draw mode where each vertex is drawn as a single point.
func (c *Context3D) POINTS() int { return c.constant("POINTS") }

// COLOR_BUFFER_BIT selects the color buffer for Clear.
func (c *Context3D) COLOR_BUFFER_BIT() int { return c.constant("COLOR_BUFFER_BIT") }

// DEPTH_BUFFER_BIT selects the depth buffer for Clear.
func (c *Context3D) DEPTH_BUFFER_BIT() int { return c.constant("DEPTH_BUFFER_BIT") }

// DEPTH_TEST is the Enable/Disable capability for depth testing.
func (c *Context3D) DEPTH_TEST() int { return c.constant("DEPTH_TEST") }

// BLEND is the Enable/Disable capability for blending.
func (c *Context3D) BLEND() int { return c.constant("BLEND") }

// CULL_FACE is the Enable/Disable capability for face culling.
func (c *Context3D) CULL_FACE() int { return c.constant("CULL_FACE") }

// TEXTURE_2D is the bind/image target for 2D textures.
func (c *Context3D) TEXTURE_2D() int { return c.constant("TEXTURE_2D") }

// TEXTURE0 is the first texture unit; add 1, 2, ... to address the others.
func (c *Context3D) TEXTURE0() int { return c.constant("TEXTURE0") }

// RGBA is the pixel format with an alpha channel.
func (c *Context3D) RGBA() int { return c.constant("RGBA") }

// RGB is the pixel format without an alpha channel.
func (c *Context3D) RGB() int { return c.constant("RGB") }

// TEXTURE_MIN_FILTER is the TexParameteri pname for the minification filter.
func (c *Context3D) TEXTURE_MIN_FILTER() int { return c.constant("TEXTURE_MIN_FILTER") }

// TEXTURE_MAG_FILTER is the TexParameteri pname for the magnification filter.
func (c *Context3D) TEXTURE_MAG_FILTER() int { return c.constant("TEXTURE_MAG_FILTER") }

// LINEAR is the filter mode that linearly interpolates between texels.
func (c *Context3D) LINEAR() int { return c.constant("LINEAR") }

// NEAREST is the filter mode that samples the closest texel with no interpolation.
func (c *Context3D) NEAREST() int { return c.constant("NEAREST") }

// CLAMP_TO_EDGE is the wrap mode that clamps texture coordinates to the edge texel.
func (c *Context3D) CLAMP_TO_EDGE() int { return c.constant("CLAMP_TO_EDGE") }

// TEXTURE_WRAP_S is the TexParameteri pname for the horizontal (S) wrap mode.
func (c *Context3D) TEXTURE_WRAP_S() int { return c.constant("TEXTURE_WRAP_S") }

// TEXTURE_WRAP_T is the TexParameteri pname for the vertical (T) wrap mode.
func (c *Context3D) TEXTURE_WRAP_T() int { return c.constant("TEXTURE_WRAP_T") }

// -- Viewport / clearing --

// Viewport sets the affine transform from clip space to the given pixel rectangle.
func (c *Context3D) Viewport(x, y, width, height int) {
	c.Context.Call("viewport", x, y, width, height)
}

// ClearColor sets the RGBA color Clear uses when clearing the color buffer.
func (c *Context3D) ClearColor(r, g, b, a float64) {
	c.Context.Call("clearColor", r, g, b, a)
}

// Clear clears the buffers selected by mask (an OR of *_BUFFER_BIT constants).
func (c *Context3D) Clear(mask int) { c.Context.Call("clear", mask) }

// Enable turns on a WebGL capability (e.g. DEPTH_TEST, BLEND, CULL_FACE).
func (c *Context3D) Enable(cap int) { c.Context.Call("enable", cap) }

// Disable turns off a WebGL capability.
func (c *Context3D) Disable(cap int) { c.Context.Call("disable", cap) }

// -- Shaders / programs --

// CreateShader allocates a new shader object of the given type (VERTEX_SHADER/FRAGMENT_SHADER).
func (c *Context3D) CreateShader(shaderType int) js.Value {
	return c.Context.Call("createShader", shaderType)
}

// ShaderSource sets shader's GLSL source code.
func (c *Context3D) ShaderSource(shader js.Value, source string) {
	c.Context.Call("shaderSource", shader, source)
}

// CompileShader compiles shader's currently set source.
func (c *Context3D) CompileShader(shader js.Value) {
	c.Context.Call("compileShader", shader)
}

// GetShaderParameter returns a shader parameter (e.g. pass COMPILE_STATUS to check compile success).
func (c *Context3D) GetShaderParameter(shader js.Value, pname int) js.Value {
	return c.Context.Call("getShaderParameter", shader, pname)
}

// GetShaderInfoLog returns the compile log for shader, useful when COMPILE_STATUS is false.
func (c *Context3D) GetShaderInfoLog(shader js.Value) string {
	return c.Context.Call("getShaderInfoLog", shader).String()
}

// CreateProgram allocates a new, empty program object.
func (c *Context3D) CreateProgram() js.Value { return c.Context.Call("createProgram") }

// AttachShader attaches a compiled shader to program.
func (c *Context3D) AttachShader(program, shader js.Value) {
	c.Context.Call("attachShader", program, shader)
}

// LinkProgram links program's attached shaders into a usable program.
func (c *Context3D) LinkProgram(program js.Value) { c.Context.Call("linkProgram", program) }

// GetProgramParameter returns a program parameter (e.g. pass LINK_STATUS to check link success).
func (c *Context3D) GetProgramParameter(program js.Value, pname int) js.Value {
	return c.Context.Call("getProgramParameter", program, pname)
}

// GetProgramInfoLog returns the link log for program, useful when LINK_STATUS is false.
func (c *Context3D) GetProgramInfoLog(program js.Value) string {
	return c.Context.Call("getProgramInfoLog", program).String()
}

// UseProgram makes program the current program for subsequent draw calls.
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

// CreateBuffer allocates a new buffer object.
func (c *Context3D) CreateBuffer() js.Value { return c.Context.Call("createBuffer") }

// BindBuffer binds buffer to target (ARRAY_BUFFER/ELEMENT_ARRAY_BUFFER) for subsequent buffer calls.
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

// BufferDataUint16 uploads data (typically element/index data) to the
// buffer currently bound to target.
func (c *Context3D) BufferDataUint16(target int, data []uint16, usage int) {
	jsArr := js.Global().Get("Uint16Array").New(len(data))
	for i, v := range data {
		jsArr.SetIndex(i, int(v))
	}
	c.Context.Call("bufferData", target, jsArr, usage)
}

// -- Attributes / uniforms --

// GetAttribLocation returns the location of an attribute variable in program, or -1 if not found.
func (c *Context3D) GetAttribLocation(program js.Value, name string) int {
	return c.Context.Call("getAttribLocation", program, name).Int()
}

// EnableVertexAttribArray enables the vertex attribute at index for use by DrawArrays/DrawElements.
func (c *Context3D) EnableVertexAttribArray(index int) {
	c.Context.Call("enableVertexAttribArray", index)
}

// VertexAttribPointer describes the layout of the vertex attribute at
// index within the buffer currently bound to ARRAY_BUFFER.
func (c *Context3D) VertexAttribPointer(index, size, valueType int, normalized bool, stride, offset int) {
	c.Context.Call("vertexAttribPointer", index, size, valueType, normalized, stride, offset)
}

// GetUniformLocation returns the location of a uniform variable in program, or a null value if not found.
func (c *Context3D) GetUniformLocation(program js.Value, name string) js.Value {
	return c.Context.Call("getUniformLocation", program, name)
}

// Uniform1f sets a float uniform.
func (c *Context3D) Uniform1f(loc js.Value, x float64) { c.Context.Call("uniform1f", loc, x) }

// Uniform2f sets a vec2 uniform.
func (c *Context3D) Uniform2f(loc js.Value, x, y float64) { c.Context.Call("uniform2f", loc, x, y) }

// Uniform3f sets a vec3 uniform.
func (c *Context3D) Uniform3f(loc js.Value, x, y, z float64) {
	c.Context.Call("uniform3f", loc, x, y, z)
}

// Uniform4f sets a vec4 uniform.
func (c *Context3D) Uniform4f(loc js.Value, x, y, z, w float64) {
	c.Context.Call("uniform4f", loc, x, y, z, w)
}

// Uniform1i sets an int (or sampler) uniform.
func (c *Context3D) Uniform1i(loc js.Value, x int) { c.Context.Call("uniform1i", loc, x) }

// UniformMatrix4fv sets a mat4 uniform from a column-major 4x4 matrix.
func (c *Context3D) UniformMatrix4fv(loc js.Value, transpose bool, m [16]float32) {
	jsArr := js.Global().Get("Float32Array").New(16)
	for i, v := range m {
		jsArr.SetIndex(i, v)
	}
	c.Context.Call("uniformMatrix4fv", loc, transpose, jsArr)
}

// -- Textures --

// CreateTexture allocates a new texture object.
func (c *Context3D) CreateTexture() js.Value { return c.Context.Call("createTexture") }

// BindTexture binds texture to target (e.g. TEXTURE_2D) for subsequent texture calls.
func (c *Context3D) BindTexture(target int, texture js.Value) {
	c.Context.Call("bindTexture", target, texture)
}

// ActiveTexture selects which texture unit (TEXTURE0, TEXTURE0+1, ...) subsequent BindTexture calls affect.
func (c *Context3D) ActiveTexture(unit int) { c.Context.Call("activeTexture", unit) }

// TexParameteri sets an integer texture parameter (e.g. TEXTURE_MIN_FILTER) on the texture bound to target.
func (c *Context3D) TexParameteri(target, pname, param int) {
	c.Context.Call("texParameteri", target, pname, param)
}

// TexImage2DElement uploads an <img>/<canvas>/<video> source (already
// loaded) as a texture's image data.
func (c *Context3D) TexImage2DElement(target, level, internalFormat int, format, texType int, source js.Value) {
	c.Context.Call("texImage2D", target, level, internalFormat, format, texType, source)
}

// -- Drawing --

// DrawArrays draws primitives using the currently bound buffers/program, starting at vertex first for count vertices.
func (c *Context3D) DrawArrays(mode, first, count int) {
	c.Context.Call("drawArrays", mode, first, count)
}

// DrawElements draws primitives using the index buffer bound to ELEMENT_ARRAY_BUFFER.
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
