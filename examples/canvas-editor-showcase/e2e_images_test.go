//go:build !(js && wasm)

package main

import (
	"archive/zip"
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

// A draft description is offered only when the person turned it on; it is made on the device from the picture's
// colours and shape; and it goes into the description only when the author uses it.
func TestDraftDescriptionIsOffOnRequestAndNeverAutomatic(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#img-insert")
	d.ChooseFile(func() { d.Click("#img-insert") }, "bar.png", pngBytes(t))
	d.WaitSelector("#img-alt-0")
	d.OK(!d.Has("#img-hint-0"), "no draft is offered until it is turned on")

	d.Eval(`localStorage.setItem('showcase:a11y', JSON.stringify({hint: 'on'}))`, nil)
	d.Reload()
	d.WaitSelector("canvas")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#img-insert")
	d.ChooseFile(func() { d.Click("#img-insert") }, "bar.png", pngBytes(t))
	d.WaitSelector("#img-hint-0")
	d.OK(d.Str(`document.getElementById('img-alt-0').value`) == "", "nothing is put in the description by itself")
	d.Click("#img-hint-0")
	d.WaitSelector("#img-draft-0")
	draft := d.Text("#img-draft-0")
	d.OK(strings.Contains(draft, "Wide picture") && strings.Contains(draft, "rough draft"), "the draft says what it is: "+draft)
	d.OK(d.Str(`document.getElementById('img-alt-0').value`) == "", "still nothing in the description")
	d.Click("#img-usedraft-0")
	d.WaitFor("the description has the draft", `document.getElementById('img-alt-0').value.startsWith('Wide picture')`)
	d.OK(!d.Has("#img-missing-0"), "the picture has a description now")
	d.NoErrors()
}

// A picture inserted with the legend option starts a numbered legend, the legend is typed under the picture, and the
// exports write it under the picture; a WebP picture is stored as PNG so that Word and PDF can carry it.
func TestAPictureWithALegendAndAWebPPicture(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "?lang=en")
	d.ToggleMode()
	d.CanvasClick(240, 164)
	d.Click(`button[data-tab="images"]`)
	d.WaitSelector("#img-insert")
	d.SetChecked("#img-legend-on input", true)
	d.ChooseFile(func() { d.Click("#img-insert") }, "bar.png", pngBytes(t))
	d.WaitSelector("#img-legend-0")
	d.OK(d.Value("#img-legend-0") == "Figure 1:", "the legend is started: "+d.Value("#img-legend-0"))
	d.Fill("#img-legend-0", "Figure 1: sales by quarter")
	d.Click(`button[data-tab="transfer"]`)
	_, html := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="html"]`) })
	d.OK(strings.Contains(string(html), "Figure 1: sales by quarter"), "the HTML export writes the legend")

	// a WebP picture: the browser converts it, so the saved picture is a PNG
	d.Click(`button[data-tab="images"]`)
	d.ChooseFile(func() { d.Click("#img-insert") }, "tiny.webp", webpBytes)
	d.WaitFor("two pictures", `document.querySelectorAll('[id^="img-alt-"]').length >= 2`)
	d.Click(`button[data-tab="transfer"]`)
	_, docx := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="docx"]`) })
	zr, err := zip.NewReader(bytes.NewReader(docx), int64(len(docx)))
	if err != nil {
		t.Fatal(err)
	}
	pngs := 0
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, "word/media/") && strings.HasSuffix(f.Name, ".png") {
			pngs++
		}
	}
	d.OK(pngs >= 2, "both pictures, the WebP one converted, are in the Word file as PNG: "+strings.Repeat("x", pngs))
	d.NoErrors()
}

// webpBytes is a 1x1 WebP picture.
var webpBytes = []byte{0x52, 0x49, 0x46, 0x46, 0x1a, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x4c, 0x0d, 0x00, 0x00, 0x00, 0x2f, 0x00, 0x00, 0x00, 0x10, 0x07, 0x10, 0x11, 0x11, 0x88, 0x88, 0xfe, 0x07, 0x00}
