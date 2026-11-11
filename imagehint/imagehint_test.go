package imagehint

import (
	"slices"
	"testing"
)

func solid(w, h int, base, patch [3]byte, patchLeftFrac float64) Pixels {
	px := make([]byte, w*h*4)
	for y := range h {
		for x := range w {
			c := base
			if float64(x) < patchLeftFrac*float64(w) {
				c = patch
			}
			i := (y*w + x) * 4
			px[i], px[i+1], px[i+2], px[i+3] = c[0], c[1], c[2], 255
		}
	}
	return Pixels{RGBA: px, W: w, H: h, SrcW: float64(w * 20), SrcH: float64(h * 20)}
}

func TestDescribeNamesTheMainColoursAndTheShape(t *testing.T) {
	blue, white := [3]byte{20, 60, 200}, [3]byte{250, 250, 250}
	h, ok := Describe(solid(32, 16, blue, white, 0.3))
	if !ok || h.Shape != "wide" {
		t.Fatalf("%+v %v", h, ok)
	}
	if !slices.Equal(h.Colours, []string{Blue, White}) {
		t.Errorf("colours %v", h.Colours)
	}
	if tall, _ := Describe(Pixels{RGBA: solid(8, 16, blue, blue, 0).RGBA, W: 8, H: 16, SrcW: 100, SrcH: 300}); tall.Shape != "tall" {
		t.Errorf("shape %q", tall.Shape)
	}
	if sq, _ := Describe(solid(16, 16, blue, blue, 0)); sq.Shape != "wide" && sq.Shape != "square" {
		t.Errorf("shape %q", sq.Shape)
	}
}

func TestColourNames(t *testing.T) {
	for want, c := range map[string][3]byte{
		Black: {5, 5, 5}, White: {255, 255, 255}, Grey: {128, 128, 128}, Red: {220, 30, 30}, Orange: {240, 140, 20},
		Yellow: {240, 230, 20}, Green: {30, 180, 60}, Cyan: {20, 200, 210}, Blue: {20, 60, 200}, Purple: {140, 40, 190},
		Pink: {240, 80, 170}, Brown: {110, 70, 30},
	} {
		if got := nameOf(c[0], c[1], c[2]); got != want {
			t.Errorf("%v is %s, want %s", c, got, want)
		}
	}
	if len(Names) != 12 {
		t.Errorf("%d names", len(Names))
	}
}

func TestToneAndNothingToSay(t *testing.T) {
	if h, _ := Describe(solid(8, 8, [3]byte{10, 10, 30}, [3]byte{10, 10, 30}, 0)); h.Tone != "dark" {
		t.Errorf("dark: %+v", h)
	}
	if h, _ := Describe(solid(8, 8, [3]byte{250, 250, 240}, [3]byte{250, 250, 240}, 0)); h.Tone != "light" {
		t.Errorf("light: %+v", h)
	}
	if _, ok := Describe(Pixels{}); ok {
		t.Error("no pixels, no hint")
	}
	clear := Pixels{RGBA: make([]byte, 8*8*4), W: 8, H: 8}
	if _, ok := Describe(clear); ok {
		t.Error("a picture of nothing has nothing to say")
	}
}
