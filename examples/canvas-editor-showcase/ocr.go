//go:build js && wasm

package main

import (
	"fmt"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/xango"
)

// Text from a picture (the ocr add-on, window.addons.ocr), for scans and photographed pages:
//
//	recognize({image: Uint8Array /* PNG, longer side at most 2048 */, mime: "image/png", language, model}) -> Promise<{text}>
//
// It runs on this device. The text is shown for the author to read and correct, and goes into the document only when
// they press a button: after the picture, or as its long description.

// ocrState is the text read from each picture (by its index in the document), and which are being read.
var ocrState struct {
	text    map[int]string
	working map[int]bool
	msg     map[int]string
}

func ocrAddon() bool { return hasAddon("ocr", "recognize") }

// readPicture asks the add-on for the text in the picture at index i.
func readPicture(i int, im *canvas.Image, renderUI func()) {
	if ocrState.text == nil {
		ocrState.text, ocrState.working, ocrState.msg = map[int]string{}, map[int]bool{}, map[int]string{}
	}
	src, ok := im.Src.(*canvas.HTMLImage)
	if !ok {
		ocrState.msg[i] = T("ocr.none", "No text could be read from this picture.")
		return
	}
	png, ok := src.PNG(2048)
	if !ok {
		ocrState.msg[i] = T("ocr.none", "No text could be read from this picture.")
		return
	}
	ocrState.working[i], ocrState.msg[i] = true, ""
	model := installedModel("ocr", locale)
	go func() {
		v, err := domrt.Await(addon("ocr").Call("recognize", map[string]any{"image": u8(png), "mime": "image/png", "language": locale, "model": model}))
		ocrState.working[i] = false
		text := ""
		if err == nil && v.Get("text").Type() == js.TypeString {
			text = strings.TrimSpace(v.Get("text").String())
		}
		if text == "" {
			ocrState.msg[i] = T("ocr.none", "No text could be read from this picture.")
		} else {
			ocrState.text[i] = text
		}
		renderUI()
	}()
}

// ocrControls is the offer under a picture, when the page has the ocr add-on: a button that reads the text in it, the
// text (to correct), and what to do with it.
func ocrControls(i int, r imageRef, canEdit bool, fl *canvas.TextFlow, redraw, renderUI func()) xango.Node {
	if !ocrAddon() {
		return xango.Frag()
	}
	nodes := []xango.Node{
		btn(fmt.Sprintf("img-ocr-%d", i), T("ocr.read", "Read the text in the picture"), func() { readPicture(i, r.img, renderUI); renderUI() }, ocrState.working[i]),
	}
	if ocrState.working[i] {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", fmt.Sprintf("img-ocr-working-%d", i)), xango.A("role", "status")}, xango.Text(T("ocr.working", "Reading the picture on this device…"))))
	}
	if m := ocrState.msg[i]; m != "" {
		nodes = append(nodes, para(fmt.Sprintf("img-ocr-msg-%d", i), m, false))
	}
	if t := ocrState.text[i]; t != "" {
		nodes = append(nodes,
			textarea(fmt.Sprintf("img-ocr-text-%d", i), T("ocr.textLabel", "Text read from picture %d (check it against the picture)", i+1), t, "", "5", func(v string) { ocrState.text[i] = v }),
			btn(fmt.Sprintf("img-ocr-insert-%d", i), T("ocr.insert", "Put the text after the picture"), func() {
				pos := r.offset + 1
				fl.Replace([]canvas.Edit{{From: pos, To: pos, Items: []canvas.Item{canvas.Chars{Text: "\n" + ocrState.text[i] + "\n"}}}})
				delete(ocrState.text, i)
				ocrState.msg[i] = T("ocr.inserted", "The text is in the document, after the picture.")
				redraw()
				renderUI()
			}, !canEdit),
			btn(fmt.Sprintf("img-ocr-long-%d", i), T("ocr.asLong", "Use the text as the long description"), func() {
				r.img.LongDesc = ocrState.text[i]
				delete(ocrState.text, i)
				fl.Touch()
				renderUI()
			}, !canEdit))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-img-ocr"), xango.A("id", fmt.Sprintf("img-ocr-box-%d", i))}, nodes...)
}
