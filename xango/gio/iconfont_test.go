package gio

import (
	"bytes"
	"image"
	"testing"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget/material"

	"github.com/elaous/gojiberry/xango"
)

func TestIconFontTranslatesRecognizedSpanTextToASingleGlyph(t *testing.T) {
	w, err := headless.NewWindow(200, 40)
	if err != nil {
		t.Fatalf("headless.NewWindow: %v", err)
	}
	defer w.Release()

	th := material.NewTheme()
	node := xango.El("span", []xango.Attr{xango.A("class", "material-symbols-outlined")}, xango.Text("format_bold"))

	frame := func(iconFont *IconFont) (*image.RGBA, layout.Dimensions) {
		renderer := New()
		renderer.IconFont = iconFont
		var ops op.Ops
		var router input.Router
		gtx := layout.Context{
			Ops:         &ops,
			Source:      router.Source(),
			Constraints: layout.Constraints{Max: image.Pt(200, 40)},
		}
		dims := renderer.Layout(gtx, th, node)
		router.Frame(gtx.Ops)
		if err := w.Frame(&ops); err != nil {
			t.Fatalf("Frame: %v", err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 200, 40))
		if err := w.Screenshot(img); err != nil {
			t.Fatalf("Screenshot: %v", err)
		}
		return img, dims
	}

	plainImg, plainDims := frame(nil)

	translated := &IconFont{
		Class: "material-symbols-outlined",
		Translate: func(name string) (rune, bool) {
			if name == "format_bold" {
				return 'B', true
			}
			return 0, false
		},
	}
	iconImg, iconDims := frame(translated)

	if bytes.Equal(plainImg.Pix, iconImg.Pix) {
		t.Fatal("frame pixels identical with and without IconFont: translation had no visible effect")
	}
	if iconDims.Size.X >= plainDims.Size.X {
		t.Fatalf("translated single-glyph width %d >= literal 11-character width %d -- translation doesn't look like it actually replaced the text", iconDims.Size.X, plainDims.Size.X)
	}

	unrecognized := &IconFont{
		Class: "material-symbols-outlined",
		Translate: func(name string) (rune, bool) {
			return 0, false
		},
	}
	fallbackImg, fallbackDims := frame(unrecognized)
	if !bytes.Equal(plainImg.Pix, fallbackImg.Pix) {
		t.Fatal("unrecognized icon name did not fall back to identical plain-text pixels")
	}
	if fallbackDims.Size.X != plainDims.Size.X {
		t.Fatalf("unrecognized icon name fallback width %d != plain width %d", fallbackDims.Size.X, plainDims.Size.X)
	}

	otherClass := xango.El("span", []xango.Attr{xango.A("class", "something-else")}, xango.Text("format_bold"))
	renderer := New()
	renderer.IconFont = translated
	var ops op.Ops
	var router input.Router
	gtx := layout.Context{
		Ops:         &ops,
		Source:      router.Source(),
		Constraints: layout.Constraints{Max: image.Pt(200, 40)},
	}
	dims := renderer.Layout(gtx, th, otherClass)
	if dims.Size.X != plainDims.Size.X {
		t.Fatalf("span outside IconFont.Class got translated anyway: width %d != plain width %d", dims.Size.X, plainDims.Size.X)
	}
}
