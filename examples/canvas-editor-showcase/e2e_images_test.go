//go:build !(js && wasm)

package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for x := range 40 {
		for y := range 20 {
			img.Set(x, y, color.RGBA{R: uint8(x * 6), G: 80, B: 200, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// A picture is inserted from a file with no description, the panel asks for one (or for "decorative"),
// the Check tab flags it until one is given, the reading copy says what it is, and nothing else changes.
func TestInsertAPictureAndDescribeIt(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#img-insert")
	d.ChooseFile(func() { d.Click("#img-insert") }, "bar.png", pngBytes(t))
	d.WaitSelector("#img-alt-0")
	d.OK(strings.Contains(d.Text("#img-message"), "Describe it for people who cannot see it"), "the panel asks: "+d.Text("#img-message"))
	d.OK(d.Has("#img-missing-0"), "the picture is marked as lacking a description")
	d.WaitFor("the description field has the focus", `document.activeElement && document.activeElement.id === 'img-alt-0'`)

	// the Check tab flags it
	d.Click(`button[data-tab="check"]`)
	d.Click("#gj-check-run")
	d.WaitFor("a finding", `document.querySelectorAll('.gj-check-image-alt').length === 1`)
	// and the reading copy names it as lacking one
	d.OK(strings.Contains(d.Text("#a11y-text"), "Image without a description"), "the reading copy: "+d.Text("#a11y-text"))

	// describe it
	d.Click(`button[data-tab="images"]`)
	d.Fill("#img-alt-0", "A blue bar chart of sales")
	d.WaitFor("the reading copy has it", `document.getElementById('a11y-text').textContent.includes('Image: A blue bar chart of sales')`)
	d.OK(!d.Has("#img-missing-0"), "no longer marked")
	d.OK(d.Bool(`document.querySelector('#img-decorative-0 input').disabled`), "decorative is off the table while a description exists")
	d.Click(`button[data-tab="check"]`)
	d.Click("#gj-check-run")
	d.WaitFor("clean", `document.getElementById('gj-check-summary').textContent.includes('Nothing found')`)

	// or say it is decorative: the reading copy leaves it out and the check passes
	d.Click(`button[data-tab="images"]`)
	d.Fill("#img-alt-0", "")
	d.WaitFor("missing again", `!!document.querySelector('#img-missing-0')`)
	d.SetChecked("#img-decorative-0 input", true)
	d.WaitFor("decorative", `!document.querySelector('#img-missing-0')`)
	d.OK(strings.Count(d.Text("#a11y-text"), "Image") == 1 && !strings.Contains(d.Text("#a11y-text"), "without a description"), "a decorative picture is left out of the reading copy (only the sample picture is read)")
	d.Click(`button[data-tab="check"]`)
	d.Click("#gj-check-run")
	d.WaitFor("clean again", `document.getElementById('gj-check-summary').textContent.includes('Nothing found')`)

	// a file that is not a picture is refused in words
	d.Click(`button[data-tab="images"]`)
	d.ChooseFile(func() { d.Click("#img-insert") }, "notes.txt", []byte("hello"))
	d.WaitFor("refused", `document.getElementById('img-message').textContent.includes('not a picture file')`)
	for _, v := range d.Axe("", axeDisabled...) {
		t.Errorf("the Pictures tab: %s", v)
	}
	d.NoErrors()
}
