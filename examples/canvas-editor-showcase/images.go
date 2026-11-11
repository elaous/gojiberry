//go:build js && wasm

package main

import (
	"encoding/base64"
	"fmt"
	"path"
	"strings"
	"syscall/js"
	"time"
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
)

type imageRef struct {
	offset int
	img    *canvas.Image
}

func documentImages(fl *canvas.TextFlow) []imageRef {
	if fl == nil {
		return nil
	}
	var out []imageRef
	offset := 0
	for _, it := range fl.Slice(0, utf8.RuneCountInString(fl.EditText())) {
		switch it := it.(type) {
		case canvas.Chars:
			offset += utf8.RuneCountInString(it.Text)
		case canvas.Inline:
			if im, ok := it.Object.(*canvas.Image); ok {
				out = append(out, imageRef{offset, im})
			}
			offset++
		}
	}
	return out
}

func imageMime(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".svg":
		return "image/svg+xml"
	}
	return ""
}

const maxInsertedSide = 280

func (u *ui) insertImage(fl func() *canvas.TextFlow, redraw, renderUI func()) {
	transfer.PickFile(".png,.jpg,.jpeg,.gif,.webp,.avif,.svg", func(name string, data []byte, err error) {
		f := fl()
		mime := imageMime(name)
		if err != nil || f == nil || mime == "" {
			u.imageMsg = T("img.badFile", "That is not a picture file (png, jpeg, gif, webp, avif or svg).")
			renderUI()
			return
		}
		u.insertBytes(f, mime, data, redraw, renderUI)
	})
}

func (u *ui) insertBytes(f *canvas.TextFlow, mime string, data []byte, redraw, renderUI func(), after ...func(*canvas.Image)) {
	url := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	if mime == "image/webp" || mime == "image/avif" || mime == "image/svg+xml" {
		minSide := 0
		if mime == "image/svg+xml" {
			minSide = 1024
		}
		convertToPNG(url, minSide, func(png string, ok bool) {
			if ok {
				u.placeImage(f, png, redraw, renderUI, after...)
				return
			}
			u.placeImage(f, url, redraw, renderUI, after...)
		})
		return
	}
	u.placeImage(f, url, redraw, renderUI, after...)
}

func convertToPNG(dataURL string, minSide int, done func(png string, ok bool)) {
	doc := js.Global().Get("document")
	img := doc.Call("createElement", "img")
	var onload, onerror js.Func
	release := func() { onload.Release(); onerror.Release() }
	onload = js.FuncOf(func(js.Value, []js.Value) any {
		defer release()
		w, h := img.Get("naturalWidth").Int(), img.Get("naturalHeight").Int()
		if w <= 0 || h <= 0 {
			if minSide <= 0 {
				done("", false)
				return nil
			}
			w, h = minSide, minSide
		}
		if minSide > 0 && max(w, h) < minSide { // scale a small vector picture up
			k := float64(minSide) / float64(max(w, h))
			w, h = int(float64(w)*k), int(float64(h)*k)
		}
		cv := doc.Call("createElement", "canvas")
		cv.Set("width", w)
		cv.Set("height", h)
		cv.Call("getContext", "2d").Call("drawImage", img, 0, 0, w, h)
		out := cv.Call("toDataURL", "image/png")
		if !out.Truthy() || !strings.HasPrefix(out.String(), "data:image/png") {
			done("", false)
			return nil
		}
		done(out.String(), true)
		return nil
	})
	onerror = js.FuncOf(func(js.Value, []js.Value) any {
		defer release()
		done("", false)
		return nil
	})
	img.Set("onload", onload)
	img.Set("onerror", onerror)
	img.Set("src", dataURL)
}

func (u *ui) placeImage(f *canvas.TextFlow, url string, redraw, renderUI func(), after ...func(*canvas.Image)) {
	canvas.LoadImage(url, func(src *canvas.HTMLImage) {
		im := canvas.NewImage(src, 0, 0)
		im.Placement = canvas.PlaceInline
		if w, h := im.W, im.H; w > 0 && h > 0 && max(w, h) > maxInsertedSide {
			k := maxInsertedSide / max(w, h)
			im.W, im.H = w*k, h*k
		}
		if u.imageLegend { // a numbered legend to finish: "Figure 3: "
			n := 1
			for _, r := range documentImages(f) {
				if strings.TrimSpace(r.img.Legend) != "" {
					n++
				}
			}
			im.Legend = fmt.Sprintf(T("img.legendStart", "Figure %d:"), n)
		}
		for _, a := range after {
			a(im)
		}
		pos := f.Cursors()[0].Caret
		f.Replace([]canvas.Edit{{From: pos, To: pos, Items: []canvas.Item{canvas.Inline{Object: im}}}})
		f.SetCursors([]canvas.Cursor{canvas.At(pos + 1)})
		u.imageMsg = T("img.inserted", "Picture inserted. Describe it for people who cannot see it, or say it is decorative.")
		u.imageFocus, u.imageFocusAt = true, time.Now()
		u.tab = "images"
		redraw()
		renderUI()
	}, func() {
		u.imageMsg = T("img.badFile", "That is not a picture file (png, jpeg, gif, webp, avif or svg).")
		renderUI()
	})
}

func imagesPanel(u *ui, state *State, flow func() *canvas.TextFlow, scroller func() *canvas.ScrollView, redraw, renderUI func()) xango.Node {
	fl := flow()
	canEdit := state.Role.CanEdit() && state.Mode == canvas.ModeEdit
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("img.title", "Pictures"))),
		btn("img-insert", T("img.insert", "Insert a picture…"), func() { u.insertImage(flow, redraw, renderUI) }, !canEdit),
		xango.El("div", []xango.Attr{xango.A("id", "img-legend-on")}, components.Checkbox(components.CheckboxProps{
			Label: T("img.legendOn", "Start a numbered legend (Figure N: ) when I insert a picture"), Checked: u.imageLegend, Disabled: !canEdit,
			OnChange: func(on bool) { u.imageLegend = on; renderUI() }})),
	}
	if !canEdit {
		nodes = append(nodes, para("img-readonly", T("img.editMode", "Switch to edit mode to insert or describe pictures."), false))
	}
	if u.imageMsg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "img-message"), xango.A("role", "status")}, xango.Text(u.imageMsg)))
	}
	imgs := documentImages(fl)
	if len(imgs) == 0 {
		nodes = append(nodes, para("img-none", T("img.none", "There are no pictures in the document."), false))
	}
	var items []xango.Node
	for i, r := range imgs {
		i, r := i, r
		id := fmt.Sprintf("img-alt-%d", i)
		kids := []xango.Node{
			xango.El("strong", nil, xango.Text(T("img.n", "Picture %d", i+1))),
			btn(fmt.Sprintf("img-go-%d", i), T("img.go", "Go to it"), func() {
				fl.GotoOffset(r.offset)
				scroller().RevealPending()
				redraw()
				renderUI()
			}, false),
			textarea(id, T("img.descLabel", "Description of picture %d", i+1), r.img.Alt, T("img.descHint", "What a person who cannot see it needs to know"), "2", func(v string) {
				if !u.claim(imageKey(i), T("lock.picture", "picture"), renderUI) {
					return
				}
				r.img.Alt = v
				if v != "" {
					r.img.Decorative = false
				}
				fl.Touch()
				renderUI()
			}),
			textarea(fmt.Sprintf("img-legend-%d", i), T("img.legendLabel", "Legend of picture %d (shown under it)", i+1), r.img.Legend, T("img.legendHint", "Everybody reads it, for example: Figure 1: the harbour at dawn"), "1", func(v string) {
				if !u.claim(imageKey(i), T("lock.picture", "picture"), renderUI) {
					return
				}
				r.img.Legend = v
				fl.Touch()
				redraw()
			}),
			diagramEditControl(i, r.img, canEdit, renderUI),
			hintControls(u, i, r.img, canEdit, fl, renderUI),
			ocrControls(i, r, canEdit, fl, redraw, renderUI),
			xango.El("div", []xango.Attr{xango.A("id", fmt.Sprintf("img-decorative-%d", i))}, components.Checkbox(components.CheckboxProps{
				Label: T("img.decorative", "Decorative: it carries nothing a description would add"), Checked: r.img.Decorative && r.img.Alt == "", Disabled: r.img.Alt != "" || !canEdit,
				OnChange: func(on bool) { r.img.Decorative = on; fl.Touch(); redraw(); renderUI() }})),
		}
		if r.img.Alt == "" && !r.img.Decorative {
			kids = append(kids, para(fmt.Sprintf("img-missing-%d", i), T("img.missing", "No description yet."), true))
		}
		if !r.img.Decorative {
			kids = append(kids, textarea(fmt.Sprintf("img-long-%d", i), T("img.longLabel", "Long description of picture %d (for a diagram or chart)", i+1), r.img.LongDesc,
				T("img.longHint", "Everything a person who cannot see it needs to follow the text around it"), "4", func(v string) {
					r.img.LongDesc = v
					fl.Touch()
					redraw()
				}))
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item")}, kids...))
	}
	if len(items) > 0 {
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, items...))
	}
	if u.imageFocus && time.Since(u.imageFocusAt) > 20*time.Second {
		u.imageFocus = false // nothing needed the focus in all that time
	}
	if u.imageFocus {
		for i, r := range imgs {
			if r.img.Alt == "" && !r.img.Decorative {
				u.imageFocus = false
				id := fmt.Sprintf("img-alt-%d", i)
				var try js.Func
				tries := 0
				try = js.FuncOf(func(js.Value, []js.Value) any {
					doc := js.Global().Get("document")
					if el := doc.Call("getElementById", id); el.Truthy() {
						el.Call("focus")
						if ae := doc.Get("activeElement"); ae.Truthy() && ae.Get("id").String() == id {
							try.Release()
							return nil
						}
					}
					if tries++; tries >= 300 {
						try.Release()
						return nil
					}
					js.Global().Call("setTimeout", try, 50)
					return nil
				})
				js.Global().Call("setTimeout", try, 50)
				break
			}
		}
	}
	nodes = append(nodes, imageSearchSection(u, state, flow, redraw, renderUI)...)
	nodes = append(nodes, mapSection(u, state, flow, redraw, renderUI)...)
	nodes = append(nodes, mediaSections(u, state, flow, scroller, redraw, renderUI)...)
	nodes = append(nodes, elementsSections(u, state, flow, redraw, renderUI)...)
	nodes = append(nodes, diagramSection(u, state, flow, redraw, renderUI)...)
	return xango.El("div", []xango.Attr{xango.A("id", "img-panel")}, nodes...)
}

func hintControls(u *ui, i int, im *canvas.Image, canEdit bool, fl *canvas.TextFlow, renderUI func()) xango.Node {
	if !hintOn() || !canEdit || im.Decorative {
		return xango.Frag()
	}
	nodes := []xango.Node{
		btn(fmt.Sprintf("img-hint-%d", i), T("img.hint.make", "Draft a description from the picture"), func() { u.draftFor(i, im, renderUI); renderUI() }, false),
	}
	if d := u.imageDraft[i]; d != "" {
		nodes = append(nodes,
			xango.El("p", []xango.Attr{xango.A("id", fmt.Sprintf("img-draft-%d", i)), xango.A("role", "status")},
				xango.Text(draftNote(u.imageDraftBy[i])+d)),
			btn(fmt.Sprintf("img-usedraft-%d", i), T("img.hint.use", "Use the draft as the description"), func() {
				im.Alt, im.Decorative = d, false
				delete(u.imageDraft, i)
				delete(u.imageDraftBy, i)
				fl.Touch()
				renderUI()
			}, false))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-img-hint")}, nodes...)
}
