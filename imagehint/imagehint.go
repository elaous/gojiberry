// Package imagehint drafts a description of a picture on the device, from its pixels, for the author to correct:
// the shape (wide, tall, square) and the main colours. It is a rough starting point, not a description of what the
// picture shows, and it says so; a person must read and change it. Nothing leaves the device: the pixels are read in
// the page and this package only counts them.
//
// A better describer (an on-device captioning model) plugs into the same place: a Describer takes the pixels and
// answers a draft. The showcase offers a draft only when the person has turned the setting on, and puts it in
// front of the author as a draft to accept, never into the document by itself.
package imagehint

import (
	"image/color"
	"sort"
)

// Pixels is a picture as the page hands it over: a small RGBA copy (4 bytes a pixel, row by row) of W by H, and the
// size the picture has on the page.
type Pixels struct {
	RGBA         []byte
	W, H         int
	SrcW, SrcH   float64
	HasTransp    bool // some pixels are transparent
	BrightnessOK bool
}

// Hint is what could be told from the pixels.
type Hint struct {
	// Shape is "wide", "tall" or "square".
	Shape string
	// Colours are the one to three main colours, as names from Names, the most of the picture first.
	Colours []string
	// Tone is "dark", "light" or "" for a picture that is neither.
	Tone string
}

// A Describer drafts a description from the pixels; ok is false when it has nothing to say.
type Describer func(Pixels) (draft string, ok bool)

// The colour names Describe uses, in the order of their entries in palette.
const (
	Black  = "black"
	White  = "white"
	Grey   = "grey"
	Red    = "red"
	Orange = "orange"
	Yellow = "yellow"
	Green  = "green"
	Cyan   = "cyan"
	Blue   = "blue"
	Purple = "purple"
	Pink   = "pink"
	Brown  = "brown"
)

// Names is every colour name Describe can answer.
var Names = []string{Black, White, Grey, Red, Orange, Yellow, Green, Cyan, Blue, Purple, Pink, Brown}

// Describe tells the shape, tone and main colours of the picture.
func Describe(p Pixels) (Hint, bool) {
	if p.W <= 0 || p.H <= 0 || len(p.RGBA) < p.W*p.H*4 {
		return Hint{}, false
	}
	var h Hint
	sw, sh := p.SrcW, p.SrcH
	if sw <= 0 || sh <= 0 {
		sw, sh = float64(p.W), float64(p.H)
	}
	switch {
	case sw >= sh*1.25:
		h.Shape = "wide"
	case sh >= sw*1.25:
		h.Shape = "tall"
	default:
		h.Shape = "square"
	}
	counts := map[string]int{}
	total, lum := 0, 0.0
	for i := 0; i+3 < p.W*p.H*4; i += 4 {
		a := p.RGBA[i+3]
		if a < 128 {
			continue // transparent: the page shows through
		}
		r, g, b := p.RGBA[i], p.RGBA[i+1], p.RGBA[i+2]
		counts[nameOf(r, g, b)]++
		total++
		lum += 0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)
	}
	if total == 0 {
		return Hint{}, false
	}
	type kv struct {
		name string
		n    int
	}
	var all []kv
	for n, c := range counts {
		all = append(all, kv{n, c})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].name < all[j].name
	})
	for _, c := range all {
		if len(h.Colours) == 3 || (len(h.Colours) > 0 && float64(c.n) < 0.12*float64(total)) {
			break
		}
		h.Colours = append(h.Colours, c.name)
	}
	switch mean := lum / float64(total); {
	case mean < 70:
		h.Tone = "dark"
	case mean > 190:
		h.Tone = "light"
	}
	return h, true
}

// nameOf is the name of the palette colour a pixel is nearest to, with the grey, black and white of a pixel that has
// little colour told apart by its lightness.
func nameOf(r, g, b uint8) string {
	c := color.RGBA{r, g, b, 255}
	h, s, v := hsv(c)
	switch {
	case v < 0.2:
		return Black
	case s < 0.12 && v > 0.88:
		return White
	case s < 0.18:
		return Grey
	}
	switch {
	case h < 15 || h >= 345:
		if v < 0.5 {
			return Brown
		}
		return Red
	case h < 40:
		if v < 0.65 {
			return Brown
		}
		return Orange
	case h < 70:
		return Yellow
	case h < 160:
		return Green
	case h < 200:
		return Cyan
	case h < 260:
		return Blue
	case h < 300:
		return Purple
	}
	return Pink
}

// hsv is the hue (0 to 360), saturation and value (0 to 1) of a colour.
func hsv(c color.RGBA) (h, s, v float64) {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	mx, mn := max(r, g, b), min(r, g, b)
	v = mx
	d := mx - mn
	if mx > 0 {
		s = d / mx
	}
	if d == 0 {
		return 0, s, v
	}
	switch mx {
	case r:
		h = 60 * ((g - b) / d)
		if h < 0 {
			h += 360
		}
	case g:
		h = 60 * ((b-r)/d + 2)
	default:
		h = 60 * ((r-g)/d + 4)
	}
	return h, s, v
}
