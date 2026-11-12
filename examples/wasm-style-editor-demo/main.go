//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/xango"
	"github.com/elaous/xango/domrt"
	"github.com/elaous/xango/domrt/browser"
)

type state struct {
	format  canvas.Format
	catalog []fonts.Font
	open    bool
	query   string
	source  string
	recent  []string
	status  string
}

func main() {
	document := browser.Document()
	container := document.GetElementByID("app")

	project := fonts.Static("Project fonts",
		fonts.Font{Family: "Fira Code", Category: "monospace", Variants: []fonts.Variant{{Weight: 400}, {Weight: 700}}},
		fonts.Font{Family: "Playfair Display", Category: "serif", Variants: []fonts.Variant{{Weight: 400}, {Weight: 700}}},
	)
	repo := fonts.Multi(fonts.WebSafe(), project)
	catalog, _ := repo.Fonts(context.Background())

	st := &state{catalog: catalog, format: canvas.Format{FontFamily: `"Georgia", serif`}}
	base := canvas.TextStyle{FontFamily: "sans-serif", FontSize: 18, FontWeight: 400, Color: "#222222"}

	var root *domrt.Root
	var render func()
	render = func() {
		node := xango.El("div", nil,
			components.StyleEditor(components.StyleEditorProps{
				Format: st.format,
				Base:   base,
				OnChange: func(f canvas.Format) {
					st.format = f
					render()
				},
				FontPicker: &components.FontSelectorProps{
					Fonts: st.catalog, Open: st.open, Query: st.query, Source: st.source, Recent: st.recent, Limit: 50,
					OnToggleOpen: func(open bool) { st.open = open; render() },
					OnQuery:      func(q string) { st.query = q; render() },
					OnSource:     func(s string) { st.source = s; render() },
					OnSelect: func(f fonts.Font) {
						st.open = false
						st.recent = append([]string{f.Family}, without(st.recent, f.Family)...)
						render()
					},
				},
			}),
			xango.El("button", []xango.Attr{
				xango.A("id", "load-device-fonts"), xango.A("type", "button"),
				xango.On("click", func(xango.Event) { go loadDeviceFonts(st, repo, render) }),
			}, xango.Text("Load fonts from this device")),
			xango.El("span", []xango.Attr{xango.A("id", "status")}, xango.Text(st.status)),
		)
		if root == nil {
			root = domrt.Mount(container.Element, node)
		} else {
			root.Update(node)
		}
		b, _ := json.Marshal(map[string]any{
			"format": st.format, "open": st.open, "query": st.query, "source": st.source,
			"recent": st.recent, "fonts": len(st.catalog), "status": st.status,
		})
		document.GetElementByID("state").SetTextContent(string(b))
	}
	render()
	select {}
}

func loadDeviceFonts(st *state, repo fonts.Repository, render func()) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	all, err := fonts.Multi(repo, fonts.BrowserLocal()).Fonts(ctx)
	switch {
	case err != nil && len(all) <= len(st.catalog):
		st.status = "Could not list device fonts: " + err.Error()
	default:
		st.catalog, st.status = all, "Loaded device fonts"
	}
	render()
}

func without(list []string, s string) []string {
	var out []string
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
