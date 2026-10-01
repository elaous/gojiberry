//go:build js && wasm

// Command canvas-document-demo is a small word processor's page view,
// drawn entirely on a <canvas>: a ScrollView over several PageSet
// sheets (margins, borders, headers, footers, page numbers), a TextFlow
// that runs continuously across the pages -- with column configs, user
// page and column breaks, and natural page breaks -- an Image and a
// FloatingText that the text wraps around. #state mirrors the layout as
// JSON (the e2e test reads it).
package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"syscall/js"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/domrt/browser"
)

const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="240" height="160">
<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
<stop offset="0" stop-color="#ff7a59"/><stop offset="1" stop-color="#3b2bd9"/></linearGradient></defs>
<rect width="240" height="160" fill="url(#g)"/>
<circle cx="80" cy="60" r="34" fill="#ffd23f"/>
<path d="M0 160 L90 90 L150 140 L190 110 L240 160 Z" fill="#1b1b3a"/></svg>`

var markdownSource = "# Quarterly Report\n\n" +
	"A **Markdown** document flowing across pages: ~~strike~~, ++underline++, ==highlight==, x^2^, H~2~O, `code` and [links](https://example.com). " +
	"The text carries on at the top of the next page when a page is full.\n\n" +
	"![sun](sun) An inline image sits in the text like a character, scaled down to fit.\n\n" +
	"\\columns 2 gap=20\n" +
	"Columns keep their widths and gap until a new column config is set. Columns keep their widths and gap until a new column config is set. Columns keep their widths and gap until a new column config is set.\n" +
	"\\columnbreak\n" +
	"The column break above sent this text on to the next column (from the last column, the next page). Drag the grip above a column gap, in edit mode, to resize the columns. Drag the grip above a column gap to resize.\n\n" +
	"\\columns 1\n" +
	"| Region | Q1 | Q2 | Q3 |\n|---|---|---|---|\n| North | 12 | 15 | 18 |\n| South | 9 | 11 | 14 |\n\n" +
	"- bullets and numbered lists\n- [x] task lists\n\n" +
	"\\pagebreak\n" +
	"## After a page break\n\n" +
	para("This text starts on a fresh page because the break above forced it, and it runs on until the page is full, where the layout makes a natural page break of its own.", 3)

func para(sentence string, n int) string {
	return strings.TrimSpace(strings.Repeat(sentence+" ", n))
}

func main() {
	doc := browser.Document()

	margin := canvas.Insets{Left: 50, Top: 60, Right: 50, Bottom: 60}
	page := func() canvas.Page { return canvas.Page{Width: 560, Height: 420, Margin: margin} }
	pages := []canvas.Page{page(), page(), page(), page()}
	pages[0].Header = &canvas.HeaderFooter{Text: "Gojiberry Quarterly"}
	pages[0].Footer = &canvas.HeaderFooter{Text: "Confidential"}
	pages[0].PageNumber = &canvas.PageNumberConfig{Show: false}
	pages[1].PageNumber = &canvas.PageNumberConfig{Show: true, Start: 1, Format: "Page {n}", Align: canvas.AlignRight}
	pages[3].Header = &canvas.HeaderFooter{} // no header from here on
	pages[3].PageNumber = &canvas.PageNumberConfig{Show: true, Start: 1, Style: canvas.NumLowerRoman, Format: "— {n} —", Align: canvas.AlignCenter}

	ps := canvas.NewPageSet(pages...)
	ps.Width = 880

	flow := canvas.NewTextFlow("", 0, 0, 0, 0)
	flow.Pages = ps

	// An inline image sits in the text; the Markdown resolver hands the
	// flow this object for ![sun](sun) once the bitmap has loaded.
	var inline *canvas.Image
	flow.Transform = canvas.Markdown{
		Images: func(url, alt string) canvas.InlineObject {
			if inline == nil {
				return nil
			}
			return inline
		},
	}
	flow.Source = markdownSource

	var img *canvas.Image
	ft := canvas.NewFloatingText("Floating note: click to edit, select text and use B / I.", 0, 0, 190, 96)
	ft.Anchor = &canvas.Anchor{Page: 2, X: 40, Y: 110}
	ft.Wrap = true
	ft.BoundsW = 880
	ft.SetFlow(flow)
	ft.Vocabulary = []string{"Quarterly", "Markdown"}

	// A floating table, anchored to the last page, that text wraps around.
	table := canvas.NewTableView(canvas.NewTableFrom([][]string{
		{"Metric", "Value"}, {"Pages", "4"}, {"Columns", "2"},
	}), nil)
	table.Anchor = &canvas.Anchor{Page: 3, X: 300, Y: 230}
	table.Wrap = true
	table.SetFlow(flow)

	sv := canvas.NewScrollView(10, 10, 900, 620)
	sv.Add(ps, 0, 0)
	sv.Add(flow, 0, 0)
	sv.Add(table, 0, 0)
	sv.Add(ft, 0, 0)
	w, h := ps.Size()
	sv.SetContentSize(w, h)

	stage := canvas.NewStage(nil, 0, 0, sv)
	redraw, err := stage.Bind(doc.GetElementByID("stage"), doc.GetElementByID("input"))
	if err != nil {
		doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
		return
	}

	report := func() {
		lay := flow.Layout()
		type line struct {
			Page int     `json:"page"`
			Col  int     `json:"col"`
			X    float64 `json:"x"`
			Y    float64 `json:"y"`
			W    float64 `json:"w"`
			H    float64 `json:"h"`
		}
		lines := make([]line, len(lay.Lines))
		for i, l := range lay.Lines {
			lines[i] = line{l.Page, l.Col, l.X, l.Y, l.W, l.H}
		}
		deco := map[string]int{}
		for _, l := range lay.Lines {
			for _, r := range l.Runs {
				if r.Underline {
					deco["underline"]++
				}
				if r.Strike {
					deco["strike"]++
				}
				if r.DY < 0 {
					deco["super"]++
				}
				if r.DY > 0 {
					deco["sub"]++
				}
				if r.Background != "" {
					deco["background"]++
				}
			}
		}
		var objects []map[string]any
		for _, l := range lay.Lines {
			for _, o := range l.Objects {
				kind := "other"
				switch o.Object.(type) {
				case *canvas.TableObject:
					kind = "table"
				case *canvas.Image:
					kind = "image"
				case *canvas.Rule:
					kind = "rule"
				}
				objects = append(objects, map[string]any{"kind": kind, "page": l.Page, "x": o.X, "y": o.Y, "w": o.W, "h": o.H})
			}
		}
		var breaks, bands []map[string]any
		for _, b := range lay.Breaks {
			breaks = append(breaks, map[string]any{"kind": b.Kind.String(), "page": b.Page, "col": b.Col})
		}
		for _, b := range lay.Bands {
			bands = append(bands, map[string]any{
				"page": b.Page, "top": b.Top, "xs": b.Xs, "widths": b.Widths, "gap": b.Gap, "cfg": b.Cfg,
			})
		}
		numbers := make([]string, len(ps.Pages))
		for i := range numbers {
			numbers[i] = ps.PageNumberText(i)
		}
		headers, footers := make([]string, len(ps.Pages)), make([]string, len(ps.Pages))
		numberRects := make([][]float64, len(ps.Pages))
		for i := range ps.Pages {
			if h := ps.ResolvedHeader(i); h != nil {
				headers[i] = h.Text
			}
			if f := ps.ResolvedFooter(i); f != nil {
				footers[i] = f.Text
			}
			if x, y, w, h, ok := ps.PageNumberRect(i); ok {
				numberRects[i] = []float64{x, y, w, h}
			}
		}
		selPage, hasSel := ps.SelectedPageNumber()
		mx, my, mw, _ := ps.Menu().Rect()
		editPage, editFooter, editing := ps.Editing()
		from, to := ft.Selection()
		pm := ft.PaletteMenu()
		px, py, pw, _ := pm.Rect()
		bx, by, bw, bh := ft.ToolbarButtonRect(6)
		mx2, my2, mw2, mh2 := ft.ToolbarButtonRect(7)
		sx, sy, sw, _ := ft.SuggestionMenu().Rect()
		tx, ty, tw, th := table.Rect()
		selFrom, selTo := flow.Selection()
		fmtClip, hasFmt := stage.Formats.Get()
		st := map[string]any{
			"mode": ft.Mode().String(), "scroll": []float64{sv.ScrollX, sv.ScrollY},
			"viewport": []float64{sv.X, sv.Y, sv.Width, sv.Height}, "content": []float64{w, h},
			"pageCount": len(ps.Pages), "pagesUsed": lay.Pages, "overflow": lay.Overflow,
			"headers": headers, "footers": footers, "numberRects": numberRects,
			"numberSelected":  map[string]any{"ok": hasSel, "page": selPage},
			"menu":            map[string]any{"open": ps.Menu().IsOpen(), "rect": []float64{mx, my, mw}, "labels": ps.Menu().Labels()},
			"hfEdit":          map[string]any{"editing": editing, "page": editPage, "footer": editFooter},
			"table":           map[string]any{"rect": []float64{tx, ty, tw, th}, "anchor": table.Anchor, "wrap": table.Wrap, "cells": table.Table.Cells},
			"flowSelection":   map[string]any{"from": selFrom, "to": selTo, "text": flow.SelectedText()},
			"formatClipboard": map[string]any{"has": hasFmt, "format": fmtClip},
			"lines":           lines, "objects": objects, "decorations": deco, "breaks": breaks, "bands": bands, "pageNumbers": numbers,
			"pageRects": func() [][]float64 {
				var out [][]float64
				for i := range ps.Pages {
					x, y, pw, ph := ps.PageRect(i)
					out = append(out, []float64{x, y, pw, ph})
				}
				return out
			}(),
			"floating": map[string]any{
				"x": ft.X, "y": ft.Y, "w": ft.Width, "h": ft.Height, "text": ft.Text(), "wrap": ft.Wrap, "anchor": ft.Anchor,
				"editing": ft.Editing(), "selection": []int{from, to}, "items": ft.Items(),
				"caret": ft.Caret(), "selectedText": ft.SelectedText(),
				"palette":     map[string]any{"open": pm.IsOpen(), "rect": []float64{px, py, pw}, "labels": pm.Labels()},
				"suggestions": append([]string{}, ft.Suggestions()...), "suggestRect": []float64{sx, sy, sw},
				"buttons": map[string]any{"color": []float64{bx, by, bw, bh}, "mark": []float64{mx2, my2, mw2, mh2}},
			},
		}
		if img != nil {
			vx, vy, vw, vh := img.Visible()
			st["image"] = map[string]any{
				"x": img.X, "y": img.Y, "visible": []float64{vx, vy, vw, vh}, "wrap": img.Wrap, "anchor": img.Anchor,
				"tool": [...]string{"move", "clip", "alpha"}[img.Tool()], "alpha": img.Alpha,
			}
		}
		b, _ := json.Marshal(st)
		doc.GetElementByID("state").SetTextContent(string(b))
	}
	stage.OnDraw = report

	canvas.LoadImage("data:image/svg+xml;base64,"+base64.StdEncoding.EncodeToString([]byte(svg)),
		func(src *canvas.HTMLImage) {
			// A floating image, anchored to page 1: text wraps around it.
			img = canvas.NewImage(src, 0, 0)
			img.W, img.H = 120, 80
			img.Anchor = &canvas.Anchor{Page: 1, X: 330, Y: 150}
			img.BoundsW = 880
			img.Wrap = true
			img.SetFlow(flow)
			sv.Add(img, 0, 0)

			// And an inline one, placed in the text by the Markdown ![sun](sun).
			inline = canvas.NewImage(src, 0, 0)
			inline.W, inline.H = 160, 107
			inline.Placement = canvas.PlaceInline
			inline.SetFlow(flow)
			sv.Add(inline, 0, 0)
			flow.InvalidateTransform()
			redraw()
		}, nil)

	btn := doc.GetElementByID("mode")
	btn.AddEventListener("click", js.FuncOf(func(js.Value, []js.Value) any {
		m := canvas.ModeEdit
		if ft.Mode() == canvas.ModeEdit {
			m = canvas.ModeView
		}
		sv.SetContentMode(m)
		btn.SetTextContent("Mode: " + m.String() + " (click to switch)")
		redraw()
		return nil
	}))
	redraw()
	select {}
}
