package imagehint

import (
	"image/color"
	"sort"
)

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
	Shape   string
	Colours []string
	Tone    string
}

type Describer func(Pixels) (draft string, ok bool)

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
