package a11ycheck

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/docfile"
)

// WCAG 2 success criterion 1.4.3: text needs a contrast ratio of 4.5 to its background, or 3 when it
// is large (24px, or 18.66px and bold).
const (
	minRatio      = 4.5
	minRatioLarge = 3.0
	baseFontSize  = 16.0 // what a run with no size of its own is drawn at
)

func checkContrast(doc docfile.Document, opt Options) []Finding {
	page, ok := parseColor(opt.Page)
	if !ok {
		page = rgb{255, 255, 255}
	}
	var out []Finding
	offsets := itemOffsets(doc)
	for i, it := range doc.Content {
		c := it.Chars
		if c == nil || strings.TrimSpace(c.Text) == "" || c.Format.Color == "" {
			continue // the default colour is the page's own to get right
		}
		fg, ok := parseColor(c.Format.Color)
		if !ok {
			continue
		}
		bg := page
		if c.Format.Background != "" {
			if b, ok := parseColor(c.Format.Background); ok {
				bg = b
			}
		}
		size := baseFontSize
		if c.Format.FontSize > 0 {
			size = c.Format.FontSize
		}
		if c.Format.Scale > 0 {
			size *= c.Format.Scale
		}
		need := minRatio
		if size >= 24 || (size >= 18.66 && c.Format.FontWeight >= 700) {
			need = minRatioLarge
		}
		if ratio := contrast(fg, bg); ratio < need {
			out = append(out, Finding{Rule: RuleContrast, Severity: Warning, Offset: offsets[i], Item: i, Text: excerpt(c.Text),
				Message: fmt.Sprintf("The text %q has a contrast of %.1f:1 against its background; %.1f:1 is needed to read it comfortably.", excerpt(c.Text), ratio, need),
				Fix:     "Use a darker text colour or a lighter background.",
				Action:  ActionDefaultColour, Length: len([]rune(c.Text))})
		}
	}
	return out
}

type rgb struct{ r, g, b uint8 }

// luminance is the relative luminance of a colour (WCAG 2).
func (c rgb) luminance() float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.r) + 0.7152*lin(c.g) + 0.0722*lin(c.b)
}

// contrast is the contrast ratio of two colours, 1 to 21.
func contrast(a, b rgb) float64 {
	la, lb := a.luminance(), b.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

var namedColors = map[string]rgb{
	"black": {0, 0, 0}, "white": {255, 255, 255}, "red": {255, 0, 0}, "green": {0, 128, 0}, "blue": {0, 0, 255},
	"yellow": {255, 255, 0}, "gray": {128, 128, 128}, "grey": {128, 128, 128}, "silver": {192, 192, 192},
	"orange": {255, 165, 0}, "purple": {128, 0, 128}, "navy": {0, 0, 128}, "maroon": {128, 0, 0},
}

// parseColor reads "#rgb", "#rrggbb", "rgb(r, g, b)" and a few names; anything else (a transparent
// colour, a variable, a name not listed) is not understood, and the caller skips the check.
func parseColor(s string) (rgb, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if c, ok := namedColors[s]; ok {
		return c, true
	}
	if h, ok := strings.CutPrefix(s, "#"); ok {
		if len(h) == 3 {
			h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
		}
		if len(h) != 6 {
			return rgb{}, false
		}
		v, err := strconv.ParseUint(h, 16, 32)
		if err != nil {
			return rgb{}, false
		}
		return rgb{uint8(v >> 16), uint8(v >> 8), uint8(v)}, true
	}
	if in, ok := strings.CutPrefix(s, "rgb("); ok {
		in, ok = strings.CutSuffix(in, ")")
		parts := strings.Split(in, ",")
		if !ok || len(parts) != 3 {
			return rgb{}, false
		}
		var v [3]uint8
		for i, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || n < 0 || n > 255 {
				return rgb{}, false
			}
			v[i] = uint8(n)
		}
		return rgb{v[0], v[1], v[2]}, true
	}
	return rgb{}, false
}
