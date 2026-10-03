package canvas

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// fakePainter records draw calls; every rune measures 8px wide.
type fakePainter struct{ calls []string }

func (f *fakePainter) rec(format string, a ...any) {
	f.calls = append(f.calls, fmt.Sprintf(format, a...))
}

func (f *fakePainter) SetFillStyle(s string)           { f.rec("fillStyle %s", s) }
func (f *fakePainter) SetStrokeStyle(s string)         { f.rec("strokeStyle %s", s) }
func (f *fakePainter) SetLineWidth(w float64)          { f.rec("lineWidth %g", w) }
func (f *fakePainter) SetFont(s string)                { f.rec("font %s", s) }
func (f *fakePainter) SetTextAlign(s string)           { f.rec("textAlign %s", s) }
func (f *fakePainter) SetTextBaseline(s string)        { f.rec("textBaseline %s", s) }
func (f *fakePainter) FillRect(x, y, w, h float64)     { f.rec("fillRect %g %g %g %g", x, y, w, h) }
func (f *fakePainter) StrokeRect(x, y, w, h float64)   { f.rec("strokeRect %g %g %g %g", x, y, w, h) }
func (f *fakePainter) ClearRect(x, y, w, h float64)    { f.rec("clearRect %g %g %g %g", x, y, w, h) }
func (f *fakePainter) BeginPath()                      { f.rec("beginPath") }
func (f *fakePainter) MoveTo(x, y float64)             { f.rec("moveTo %g %g", x, y) }
func (f *fakePainter) LineTo(x, y float64)             { f.rec("lineTo %g %g", x, y) }
func (f *fakePainter) Stroke()                         { f.rec("stroke") }
func (f *fakePainter) FillText(t string, x, y float64) { f.rec("fillText %q %g %g", t, x, y) }
func (f *fakePainter) MeasureTextWidth(t string) float64 {
	return 8 * float64(utf8.RuneCountInString(t))
}

func (f *fakePainter) has(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fakePainter) SetGlobalAlpha(a float64) { f.rec("alpha %g", a) }
func (f *fakePainter) DrawImage(_ ImageSource, sx, sy, sw, sh, dx, dy, dw, dh float64) {
	f.rec("drawImage %g %g %g %g -> %g %g %g %g", sx, sy, sw, sh, dx, dy, dw, dh)
}

var _ ImagePainter = (*fakePainter)(nil)

// fakeImage is an ImageSource of a fixed size.
type fakeImage struct{ w, h float64 }

func (i fakeImage) Size() (float64, float64) { return i.w, i.h }

func (f *fakePainter) Save()                       { f.rec("save") }
func (f *fakePainter) Restore()                    { f.rec("restore") }
func (f *fakePainter) Translate(x, y float64)      { f.rec("translate %g %g", x, y) }
func (f *fakePainter) ClipRect(x, y, w, h float64) { f.rec("clipRect %g %g %g %g", x, y, w, h) }

func (f *fakePainter) Rotate(a float64) { f.rec("rotate %.3f", a) }
