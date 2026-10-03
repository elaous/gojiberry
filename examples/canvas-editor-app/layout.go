//go:build js && wasm

package main

import (
	"math"

	"github.com/elaous/gojiberry/domrt/html"
)

const (
	fontSize   = 16.0
	lineHeight = 22.0
	leftMargin = 12.0
	topMargin  = 24.0
	// ascent is how far above its baseline a line's text reaches: line k is
	// drawn with its baseline at topMargin+k*lineHeight, so the clickable band
	// of line k starts ascent above that.
	ascent = 16.0
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

// charLayout is where character i is drawn: X is its left edge, Y its
// baseline, and Line the index into layout's Lines.
type charLayout struct {
	X, Y float64
	Line int
}

// layout is the computed position of every character in a document,
// word-wrapped to maxWidth. Line breaks happen on '\n' (an explicit
// Enter) or, failing to fit a whole word, mid-word -- this is a
// character-based wrap, not word-based, so very long unbroken words
// can still overflow a line; a real editor would look ahead to the
// next space before committing to a break.
type layout struct {
	Chars []charLayout // parallel to document's characters, plus one extra trailing entry for the end-of-document caret position
	Lines int
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
		lay.Chars[i] = charLayout{X: x, Y: y, Line: line}
		x += w
	}
	lay.Chars[n] = charLayout{X: x, Y: y, Line: line}
	lay.Lines = line + 1
	return lay
}

// hitTest returns the index of the character position closest to
// (clickX, clickY) -- the same index space as document.Cursor, so its
// result can be assigned directly to Cursor/Anchor on a click.
func hitTest(lay *layout, clickX, clickY float64) int {
	targetLine := int(math.Floor((clickY - topMargin + ascent) / lineHeight))
	if targetLine < 0 {
		targetLine = 0
	}
	best, bestDist := 0, 1e18
	for i, cl := range lay.Chars {
		if cl.Line != targetLine {
			// Distance in line-space dominates: prefer any position on
			// the clicked line over the nearest x on the wrong line.
			lineDist := float64(cl.Line-targetLine) * 1e6
			if lineDist < 0 {
				lineDist = -lineDist
			}
			if lineDist < bestDist {
				best, bestDist = i, lineDist
			}
			continue
		}
		dist := cl.X - clickX
		if dist < 0 {
			dist = -dist
		}
		if dist < bestDist {
			best, bestDist = i, dist
		}
	}
	return best
}
