//go:build js && wasm

package main

import "github.com/elaous/gojiberry/domrt/html"

const (
	fontSize   = 16.0
	leftMargin = 12.0
)

// fontFor returns the CSS font string Context2D.SetFont expects for f.
func fontFor(f charFormat) string {
	weight := ""
	if f.Bold {
		weight = "bold "
	}
	style := ""
	if f.Italic {
		style = "italic "
	}
	return style + weight + "16px sans-serif"
}

// layoutDocument measures d against ctx (so it uses the exact same
// font metrics rendering will) and returns where every character (and
// the position just past the last one) ends up.
func layoutDocument(ctx *html.Context2D, d *document, maxWidth float64) *layout {
	n := d.Len()
	lay := &layout{Chars: make([]charLayout, n+1)}
	x, y, line := leftMargin, topMargin, 0

	for i := 0; i < n; i++ {
		ch := d.runeAt(i)
		if ch == '\n' {
			lay.Chars[i] = charLayout{X: x, Y: y, Line: line}
			x, y, line = leftMargin, y+lineHeight, line+1
			continue
		}
		ctx.SetFont(fontFor(d.fmtAt(i)))
		w := ctx.MeasureTextWidth(string(ch))
		if x+w > maxWidth && x > leftMargin {
			x, y, line = leftMargin, y+lineHeight, line+1
		}
		lay.Chars[i] = charLayout{X: x, Y: y, W: w, Line: line}
		x += w
	}
	lay.Chars[n] = charLayout{X: x, Y: y, Line: line}
	lay.Lines = line + 1
	return lay
}
