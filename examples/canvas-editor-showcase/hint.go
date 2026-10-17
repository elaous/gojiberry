//go:build js && wasm

package main

import (
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/imagehint"
)

// Drafting a description of a picture on the device (roadmap 61, step 3). It is off until the person turns it on
// (Preferences > Editor); the pixels are read in the page and never sent anywhere; and what it makes is a draft
// shown beside the field, which the author reads and chooses to use or not. It is a rough start from the shape
// and the colours (imagehint), not what the picture shows.

func hintOn() bool { return curPrefs.Hint == "on" }

// hintColour is the word for a colour in the page's language.
func hintColour(name string) string {
	switch name {
	case imagehint.Black:
		return T("img.hint.black", "black")
	case imagehint.White:
		return T("img.hint.white", "white")
	case imagehint.Grey:
		return T("img.hint.grey", "grey")
	case imagehint.Red:
		return T("img.hint.red", "red")
	case imagehint.Orange:
		return T("img.hint.orange", "orange")
	case imagehint.Yellow:
		return T("img.hint.yellow", "yellow")
	case imagehint.Green:
		return T("img.hint.green", "green")
	case imagehint.Cyan:
		return T("img.hint.cyan", "cyan")
	case imagehint.Blue:
		return T("img.hint.blue", "blue")
	case imagehint.Purple:
		return T("img.hint.purple", "purple")
	case imagehint.Pink:
		return T("img.hint.pink", "pink")
	}
	return T("img.hint.brown", "brown")
}

// hintPhrase is a hint as a sentence in the page's language.
func hintPhrase(h imagehint.Hint) string {
	shape := map[string]string{
		"wide":   T("img.hint.wide", "Wide picture"),
		"tall":   T("img.hint.tall", "Tall picture"),
		"square": T("img.hint.square", "Square picture"),
	}[h.Shape]
	out := shape
	if len(h.Colours) > 0 {
		var names []string
		for _, c := range h.Colours {
			names = append(names, hintColour(c))
		}
		out += T("img.hint.mostly", ", mostly ") + strings.Join(names, T("img.hint.and", " and "))
	}
	switch h.Tone {
	case "dark":
		out += T("img.hint.dark", ", dark overall")
	case "light":
		out += T("img.hint.light", ", light overall")
	}
	return out + "."
}

// describeAddon says the page has the describe add-on (window.addons.describe), which drafts a description from what
// the picture shows, on this device:
//
//	describe({image: Uint8Array /* PNG, longer side at most 768 */, mime: "image/png", language, model}) -> Promise<{text}>
func describeAddon() bool { return hasAddon("describe", "describe") }

// draftWithAddon asks the add-on for a draft of the picture at index i; the draft appears when it comes.
func (u *ui) draftWithAddon(i int, src *canvas.HTMLImage, renderUI func()) {
	png, ok := src.PNG(768)
	if !ok {
		u.imageMsg = T("img.hint.none", "No draft could be made for this picture.")
		return
	}
	u.imageMsg = T("img.hint.working", "Describing the picture on this device…")
	model := installedModel("describe", locale)
	go func() {
		v, err := domrt.Await(addon("describe").Call("describe", map[string]any{"image": u8(png), "mime": "image/png", "language": locale, "model": model}))
		text := ""
		if err == nil && v.Get("text").Type() == js.TypeString {
			text = strings.TrimSpace(v.Get("text").String())
		}
		if text == "" {
			u.imageMsg = T("img.hint.none", "No draft could be made for this picture.")
		} else {
			if u.imageDraft == nil {
				u.imageDraft = map[int]string{}
			}
			if u.imageDraftBy == nil {
				u.imageDraftBy = map[int]string{}
			}
			u.imageDraft[i], u.imageDraftBy[i] = text, "describe"
			u.imageMsg = ""
		}
		renderUI()
	}()
}

// draftFor drafts the description of the picture at index i of the document, in the page's language: from what the
// picture shows when the describe add-on is there, else from its shape and colours.
func (u *ui) draftFor(i int, im *canvas.Image, renderUI func()) {
	src, ok := im.Src.(*canvas.HTMLImage)
	if !ok {
		u.imageMsg = T("img.hint.none", "No draft could be made for this picture.")
		return
	}
	if describeAddon() {
		u.draftWithAddon(i, src, renderUI)
		return
	}
	delete(u.imageDraftBy, i)
	rgba, w, h, ok := src.Pixels(48)
	if !ok {
		u.imageMsg = T("img.hint.none", "No draft could be made for this picture.")
		return
	}
	sw, sh := src.Size()
	hint, ok := imagehint.Describe(imagehint.Pixels{RGBA: rgba, W: w, H: h, SrcW: sw, SrcH: sh})
	if !ok {
		u.imageMsg = T("img.hint.none", "No draft could be made for this picture.")
		return
	}
	if u.imageDraft == nil {
		u.imageDraft = map[int]string{}
	}
	u.imageDraft[i] = hintPhrase(hint)
	u.imageMsg = ""
}

// draftNote says where a draft came from, so that the author knows how far to trust it.
func draftNote(by string) string {
	if by == "describe" {
		return T("img.hint.draftAddon", "A draft from what the picture shows, made on this device by the describe add-on. It can be wrong: read it and change it: ")
	}
	return T("img.hint.draft", "A rough draft from the colours and shape, made on this device. Read it and change it: ")
}
