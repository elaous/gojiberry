//go:build js && wasm

package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/xango"
)

// Diagrams (the diagram add-on, window.addons.diagram): a flowchart or a map drawn from a description in the add-on's own
// notation:
//
//	render({kind: "flowchart" | "map", source, language}) -> Promise<{svg: string}>
//
// The page shows the drawing, asks for a description in words (a drawing without one is refused, as for an equation) and
// puts it in the document as a picture; the source is kept as the picture's long description, so the diagram can be
// written again from it and is also read by whoever cannot see the drawing.

// maxSVG is the largest drawing taken from the add-on, in bytes.
const maxSVG = 2 << 20

var diagramState struct {
	kind    string
	source  string
	alt     string
	svg     string
	busy    bool
	msg     string
	focus   bool
	drawnBy string // the source and kind the preview was made from: a changed source needs drawing again
	// editing is the picture in the document being drawn again; the new drawing then takes its place.
	editing *canvas.Image
}

// editDiagram loads the description of a diagram picture of the document into the form, to be changed and drawn again.
func editDiagram(im *canvas.Image, renderUI func()) {
	d := &diagramState
	d.editing, d.kind, d.source, d.alt, d.svg, d.drawnBy = im, im.Diagram, im.LongDesc, im.Alt, "", ""
	d.msg = T("diagram.editing", "Change the description and draw the diagram again; it then takes the place of the picture.")
	renderUI()
}

func diagramAddon() bool { return hasAddon("diagram", "render") }

// diagramKindLabel names a kind. The ids are spelled out so that the translation check sees them.
func diagramKindLabel(kind string) string {
	if kind == "map" {
		return T("diagram.kindMap", "Map")
	}
	return T("diagram.kindFlow", "Flowchart")
}

// validSVG says the add-on's answer looks like a drawing.
func validSVG(s string) bool {
	t := strings.TrimSpace(s)
	if len(t) == 0 || len(t) > maxSVG {
		return false
	}
	if strings.HasPrefix(t, "<?xml") {
		if i := strings.Index(t, "?>"); i >= 0 {
			t = strings.TrimSpace(t[i+2:])
		}
	}
	return strings.HasPrefix(t, "<svg")
}

// drawDiagram asks the add-on for the drawing of the source.
func drawDiagram(renderUI func()) {
	d := &diagramState
	if strings.TrimSpace(d.source) == "" {
		d.msg = T("diagram.needSource", "Describe the diagram first.")
		return
	}
	d.busy, d.msg, d.svg = true, "", ""
	kind, source := d.kind, d.source
	renderUI()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		type result struct {
			v   js.Value
			err error
		}
		done := make(chan result, 1)
		go func() {
			v, err := domrt.Await(addon("diagram").Call("render", map[string]any{"kind": kind, "source": source, "language": locale}))
			done <- result{v, err}
		}()
		select {
		case <-ctx.Done():
			d.msg = fmt.Sprintf(T("diagram.failed", "The diagram could not be drawn: %s"), ctx.Err())
		case r := <-done:
			switch {
			case r.err != nil:
				d.msg = fmt.Sprintf(T("diagram.failed", "The diagram could not be drawn: %s"), r.err.Error())
			case r.v.Get("svg").Type() != js.TypeString || !validSVG(r.v.Get("svg").String()):
				d.msg = T("diagram.badSVG", "The add-on did not give a drawing.")
			default:
				d.svg, d.drawnBy = r.v.Get("svg").String(), kind+"\x00"+source
			}
		}
		d.busy = false
		renderUI()
	}()
}

// diagramSection is the part of the Pictures tab for diagrams, shown when the page has the add-on.
func diagramSection(u *ui, state *State, flow func() *canvas.TextFlow, redraw, renderUI func()) []xango.Node {
	if !diagramAddon() {
		return nil
	}
	d := &diagramState
	if d.kind == "" {
		d.kind = "flowchart"
	}
	canEdit := state.Role.CanEdit() && state.Mode == canvas.ModeEdit
	var kinds []xango.Node
	for _, k := range []string{"flowchart", "map"} {
		attrs := []xango.Attr{xango.A("value", k)}
		if k == d.kind {
			attrs = append(attrs, xango.A("selected", "selected"))
		}
		kinds = append(kinds, xango.El("option", attrs, xango.Text(diagramKindLabel(k))))
	}
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("diagram.title", "Diagrams"))),
		xango.El("label", nil, xango.Text(T("diagram.kind", "Kind")+" "),
			xango.El("select", []xango.Attr{xango.A("id", "diagram-kind"), xango.On("change", func(ev xango.Event) { d.kind = xango.EventValue[string](ev); renderUI() })}, kinds...)),
		textarea("diagram-source", T("diagram.source", "Description of the diagram"), d.source, T("diagram.hint", "In the notation of the add-on, for example: A --> B --> C"), "5", func(v string) { d.source = v }),
		btn("diagram-draw", T("diagram.draw", "Draw it"), func() { drawDiagram(renderUI) }, d.busy),
	}
	if d.editing != nil {
		nodes = append(nodes, btn("diagram-cancel-edit", T("diagram.cancelEdit", "Leave the picture as it is"), func() { d.editing, d.svg, d.msg = nil, "", ""; renderUI() }, false))
	}
	if d.busy {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "diagram-working"), xango.A("role", "status")}, xango.Text(T("diagram.working", "Drawing it on this device…"))))
	}
	if d.msg != "" {
		nodes = append(nodes, para("diagram-msg", d.msg, true))
	}
	if d.svg != "" {
		stale := d.drawnBy != d.kind+"\x00"+d.source
		nodes = append(nodes,
			xango.El("img", []xango.Attr{xango.A("id", "diagram-preview"), xango.A("alt", T("diagram.previewAlt", "The diagram as drawn")), xango.A("style", "max-width:100%;max-height:260px;background:#fff;border:1px solid #d0d7de"),
				xango.A("src", "data:image/svg+xml;base64,"+base64.StdEncoding.EncodeToString([]byte(d.svg)))}),
			textarea("diagram-alt", T("diagram.altLabel", "Description of the drawing, in words, for people who cannot see it"), d.alt, T("diagram.altHint", "What the diagram shows and how its parts connect"), "3", func(v string) { d.alt = v; renderUI() }))
		if stale {
			nodes = append(nodes, para("diagram-stale", T("diagram.stale", "The description changed since it was drawn: draw it again."), false))
		}
		insertLabel := T("diagram.insert", "Put the diagram in the document")
		if d.editing != nil {
			insertLabel = T("diagram.replace", "Replace the diagram in the document")
		}
		nodes = append(nodes, btn("diagram-insert", insertLabel, func() {
			fl := flow()
			if fl == nil {
				return
			}
			if strings.TrimSpace(d.alt) == "" {
				d.msg = T("diagram.needAlt", "A diagram needs a description in words: write what it shows.")
				renderUI()
				return
			}
			alt, source, kind := strings.TrimSpace(d.alt), d.source, d.kind
			if target := d.editing; target != nil {
				replaceDiagram(target, []byte(d.svg), alt, source, kind, fl, redraw, renderUI)
				d.svg, d.alt, d.editing, d.msg = "", "", nil, T("diagram.replaced", "The diagram in the document was drawn again.")
				return
			}
			u.insertBytes(fl, "image/svg+xml", []byte(d.svg), redraw, renderUI, func(im *canvas.Image) {
				im.Alt, im.LongDesc, im.Diagram = alt, source, kind
			})
			d.svg, d.alt, d.msg = "", "", T("diagram.inserted", "The diagram is in the document. Its description is kept as the picture's long description.")
		}, !canEdit || stale))
	}
	return nodes
}

// replaceDiagram swaps the drawing of a diagram picture of the document for a new one, keeping its place, width and legend.
func replaceDiagram(im *canvas.Image, svg []byte, alt, source, kind string, fl *canvas.TextFlow, redraw, renderUI func()) {
	url := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(svg)
	convertToPNG(url, 1024, func(png string, ok bool) {
		if ok {
			url = png
		}
		canvas.LoadImage(url, func(src *canvas.HTMLImage) {
			if w, h := src.Size(); w > 0 && h > 0 && im.W > 0 {
				im.H = im.W * h / w // the new drawing may have another shape: the width stays, the height follows
			}
			im.Src, im.Alt, im.LongDesc, im.Diagram = src, alt, source, kind
			fl.Touch()
			redraw()
			renderUI()
		}, func() {
			diagramState.msg = T("diagram.badSVG", "The add-on did not give a drawing.")
			renderUI()
		})
	})
}

// diagramEditControl is the button on a diagram picture that loads its description back into the diagram form.
func diagramEditControl(i int, im *canvas.Image, canEdit bool, renderUI func()) xango.Node {
	if im.Diagram == "" || !diagramAddon() {
		return xango.Frag()
	}
	return btn(fmt.Sprintf("img-diagram-%d", i), T("diagram.edit", "Change the diagram"), func() { editDiagram(im, renderUI) }, !canEdit)
}
