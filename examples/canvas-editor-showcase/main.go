//go:build js && wasm

// Command canvas-editor-showcase is a small word processor with every
// piece we have built registered in one place: the canvas tree (app.gox)
// -- scrolling pages, an editable text flow with headers, footers and page
// numbers, a picture, a floating note and a floating table -- the
// registered editing features (multi-cursor, line operations, Markdown
// copy/paste), and DOM components around it: the style editor with the
// font selector, and the list of registered commands with their keys.
// Both trees render from one state. #state mirrors it as JSON (the e2e
// test reads it).
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	cgox "github.com/elaous/gojiberry/canvas/gox"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/domrt/browser"
	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/gojiberry/gox"
)

// ui is the DOM side's state.
type ui struct {
	format  canvas.Format
	catalog []fonts.Font
	open    bool
	query   string
	source  string
	recent  []string
}

func main() {
	doc := browser.Document()
	state := State{Mode: canvas.ModeView, Pages: 3, Source: sample, Note: "Floating note: click to edit, try (brackets and \"quotes\"."}
	u := &ui{}
	repo := fonts.Multi(fonts.WebSafe())
	u.catalog, _ = repo.Fonts(context.Background())

	stage := canvas.NewStage(nil, 0, 0)
	redraw, err := stage.Bind(doc.GetElementByID("stage"), doc.GetElementByID("input"))
	if err != nil {
		doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
		return
	}
	root := cgox.NewRoot(stage)
	root.Redraw = redraw

	var flow *canvas.TextFlow
	var renderUI func()
	cb := Callbacks{} // the app does not need to hear about edits, the canvas owns them

	renderCanvas := func() {
		if err := root.Render(Doc(state, cb)); err != nil {
			doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
			return
		}
		flow = root.Find(cgox.TagTextFlow, 0).(*canvas.TextFlow)
		stage.Draw()
	}

	var domRoot *domrt.Root
	renderUI = func() {
		commands := stage.Features.Commands()
		keys := map[canvas.Command]string{}
		for chord, cmd := range stage.Features.Keymap() {
			keys[cmd] = chord
		}
		modeLabel := "Mode: " + state.Mode.String() + " (click to switch)"
		node := gox.El("div", []gox.Attr{gox.A("class", "toolbar")},
			gox.El("button", []gox.Attr{gox.A("id", "mode"), gox.A("type", "button"),
				gox.On("click", func(gox.Event) {
					if state.Mode == canvas.ModeView {
						state.Mode = canvas.ModeEdit
					} else {
						state.Mode = canvas.ModeView
					}
					renderCanvas()
					renderUI()
				})}, gox.Text(modeLabel)),
			components.StyleEditor(components.StyleEditorProps{
				Format: u.format,
				Base:   canvas.TextStyle{FontFamily: "sans-serif", FontSize: 16, FontWeight: 400, Color: "#222222"},
				OnChange: func(f canvas.Format) {
					u.format = f
					if flow != nil && flow.Editing() {
						flow.PasteFormat(f)
						redraw()
					}
					renderUI()
				},
				FontPicker: &components.FontSelectorProps{
					Fonts: u.catalog, Open: u.open, Query: u.query, Source: u.source, Recent: u.recent, Limit: 30,
					OnToggleOpen: func(open bool) { u.open = open; renderUI() },
					OnQuery:      func(q string) { u.query = q; renderUI() },
					OnSource:     func(s string) { u.source = s; renderUI() },
					OnSelect: func(f fonts.Font) {
						u.open = false
						u.recent = append([]string{f.Family}, slices.DeleteFunc(slices.Clone(u.recent), func(s string) bool { return s == f.Family })...)
						renderUI()
					},
				},
			}),
			gox.El("h3", nil, gox.Text("Registered commands")),
			components.List(components.ListProps[canvas.CommandInfo]{
				Items: commands,
				Key:   func(c canvas.CommandInfo) string { return string(c.Name) },
				Item: func(c canvas.CommandInfo) gox.Node {
					return gox.El("span", []gox.Attr{gox.A("class", "command")},
						gox.Text(c.Title+" ("+c.Feature+") "), gox.El("kbd", nil, gox.Text(strings.ReplaceAll(keys[c.Name], "Mod", "Ctrl"))))
				},
			}),
		)
		if domRoot == nil {
			domRoot = domrt.Mount(doc.GetElementByID("app").Element, node)
		} else {
			domRoot.Update(node)
		}
	}

	stage.OnDraw = func() {
		// What the canvas shows of the selection feeds the style editor.
		if flow != nil && flow.Editing() && flow.HasSelection() {
			if f := flow.CommonFormat(); f != u.format {
				u.format = f
				renderUI()
			}
		}
		var names []string
		for _, f := range stage.Features.Features() {
			names = append(names, f.Name())
		}
		var cursors []canvas.Cursor
		editing := false
		if flow != nil {
			cursors, editing = flow.Cursors(), flow.Editing()
		}
		var text string
		if flow != nil {
			text = flow.EditText()
		}
		b, _ := json.Marshal(map[string]any{
			"mode": state.Mode.String(), "features": names, "commands": len(stage.Features.Commands()),
			"elements": cgox.Elements(), "editing": editing, "cursors": cursors, "text": text,
			"pages": state.Pages, "format": u.format,
		})
		doc.GetElementByID("state").SetTextContent(string(b))
	}

	renderCanvas()
	renderUI()
	canvas.LoadImage("data:image/svg+xml;base64,"+base64.StdEncoding.EncodeToString([]byte(svg)),
		func(src *canvas.HTMLImage) {
			state.Image = src
			renderCanvas()
		}, nil)
	select {}
}
