//go:build js && wasm

package main

import (
	"encoding/base64"
	"fmt"
	"path"
	"strings"
	"syscall/js"
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
)

// The Images tab: insert a picture from a file, and describe each picture in the document for a
// person who cannot see it, or say it is decorative. A picture without either is a finding of the Check
// tab, and the reading copy names it as lacking a description.

// imageRef is a picture in the text: where it is (a caret position) and the object to describe.
type imageRef struct {
	offset int
	img    *canvas.Image
}

// documentImages lists the inline pictures of the flow in order.
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

// imageMime is the type of a picture file by its name, or "" if it is not one the page takes.
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
	}
	return ""
}

// maxInsertedSide is the longest side, in pixels, a picture is shown at when it is inserted.
const maxInsertedSide = 280

// insertImage picks a picture file and puts it at the caret as an inline picture with no description yet.
func (u *ui) insertImage(fl func() *canvas.TextFlow, redraw, renderUI func()) {
	transfer.PickFile(".png,.jpg,.jpeg,.gif,.webp", func(name string, data []byte, err error) {
		f := fl()
		mime := imageMime(name)
		if err != nil || f == nil || mime == "" {
			u.imageMsg = T("img.badFile", "That is not a picture file (png, jpeg, gif or webp).")
			renderUI()
			return
		}
		url := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
		canvas.LoadImage(url, func(src *canvas.HTMLImage) {
			im := canvas.NewImage(src, 0, 0)
			im.Placement = canvas.PlaceInline
			if w, h := im.W, im.H; w > 0 && h > 0 && max(w, h) > maxInsertedSide {
				k := maxInsertedSide / max(w, h)
				im.W, im.H = w*k, h*k
			}
			pos := f.Cursors()[0].Caret
			f.Replace([]canvas.Edit{{From: pos, To: pos, Items: []canvas.Item{canvas.Inline{Object: im}}}})
			f.SetCursors([]canvas.Cursor{canvas.At(pos + 1)})
			u.imageMsg = T("img.inserted", "Picture inserted. Describe it for people who cannot see it, or say it is decorative.")
			u.imageFocus = true
			u.tab = "images"
			redraw()
			renderUI()
		}, func() {
			u.imageMsg = T("img.badFile", "That is not a picture file (png, jpeg, gif or webp).")
			renderUI()
		})
	})
}

// imagesPanel is the Images tab.
func imagesPanel(u *ui, state *State, flow func() *canvas.TextFlow, scroller func() *canvas.ScrollView, redraw, renderUI func()) xango.Node {
	fl := flow()
	canEdit := state.Role.CanEdit() && state.Mode == canvas.ModeEdit
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("img.title", "Pictures"))),
		btn("img-insert", T("img.insert", "Insert a picture…"), func() { u.insertImage(flow, redraw, renderUI) }, !canEdit),
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
				r.img.Alt = v
				if v != "" {
					r.img.Decorative = false
				}
				fl.Touch()
				renderUI()
			}),
			xango.El("div", []xango.Attr{xango.A("id", fmt.Sprintf("img-decorative-%d", i))}, components.Checkbox(components.CheckboxProps{
				Label: T("img.decorative", "Decorative: it carries nothing a description would add"), Checked: r.img.Decorative && r.img.Alt == "", Disabled: r.img.Alt != "" || !canEdit,
				OnChange: func(on bool) { r.img.Decorative = on; fl.Touch(); redraw(); renderUI() }})),
		}
		if r.img.Alt == "" && !r.img.Decorative {
			kids = append(kids, para(fmt.Sprintf("img-missing-%d", i), T("img.missing", "No description yet."), true))
		}
		if !r.img.Decorative {
			// a diagram or chart needs more than a sentence: the long description is written as text after it
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
	if u.imageFocus {
		u.imageFocus = false
		// the first picture without a description takes the focus, once the panel is drawn
		for i, r := range imgs {
			if r.img.Alt == "" && !r.img.Decorative {
				id := fmt.Sprintf("img-alt-%d", i)
				js.Global().Call("setTimeout", js.FuncOf(func(js.Value, []js.Value) any {
					if el := js.Global().Get("document").Call("getElementById", id); el.Truthy() {
						el.Call("focus")
					}
					return nil
				}), 50)
				break
			}
		}
	}
	nodes = append(nodes, elementsSections(u, state, flow, redraw, renderUI)...)
	return xango.El("div", []xango.Attr{xango.A("id", "img-panel")}, nodes...)
}
