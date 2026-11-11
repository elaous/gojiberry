package a11ycheck

import (
	"fmt"
	"golang.org/x/image/colornames"
	"math"
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/docfile"
)

const (
	minRatio      = 4.5
	minRatioLarge = 3.0
	baseFontSize  = 16.0
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
			continue
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

func contrast(a, b rgb) float64 {
	la, lb := a.luminance(), b.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func parseColor(s string) (rgb, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if c, ok := colornames.Map[s]; ok {
		return rgb{c.R, c.G, c.B}, true
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

func Contrast(fg, bg string) (ratio float64, ok bool) {
	a, ok1 := parseColor(fg)
	b, ok2 := parseColor(bg)
	if !ok1 || !ok2 {
		return 0, false
	}
	return contrast(a, b), true
}
