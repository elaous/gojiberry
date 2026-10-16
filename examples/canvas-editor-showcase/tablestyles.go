//go:build js && wasm

package main

import (
	"fmt"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/xango"
)

// tableStyleForm is the style being defined in the Styles tab.
type tableStyleForm struct {
	name, header, headerText, band string
	headerBold, firstBold          bool
	msg                            string
}

// tableInHand is the table the person was last working in (a cell of it entered), or nil: the focus may have moved to
// the panel since.
func tableInHand(root *cxango.Root) *canvas.TableView {
	var best *canvas.TableView
	for n := 0; ; n++ {
		tv, ok := root.Find(cxango.TagTable, n).(*canvas.TableView)
		if !ok {
			return best
		}
		if tv.EditedAt() > 0 && (best == nil || tv.EditedAt() > best.EditedAt()) {
			best = tv
		}
	}
}

// tableStylesPanel is the "Table styles" part of the Styles tab: the styles on offer (the built-in ones and the ones
// the person defined), the one the table in hand is dressed in marked, a way to define one, and to forget one.
func tableStylesPanel(u *ui, root func() *cxango.Root, redraw, renderUI func()) xango.Node {
	f := &u.tableStyle
	tv := tableInHand(root())
	nodes := []xango.Node{xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("tstyle.title", "Table styles")))}
	if tv == nil {
		nodes = append(nodes, para("tstyle-hint", T("tstyle.hint", "Click in a table, then choose a style for it."), false))
	}
	current := ""
	if tv != nil {
		current = tv.Table.SchemeOf()
	}
	var items []xango.Node
	for i, s := range canvas.TableSchemes() {
		i, s := i, s
		swatch := s.HeaderBG
		if swatch == "" {
			swatch = "#e5e7eb"
		}
		attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-keybindings-button"), xango.A("id", fmt.Sprintf("tstyle-%d", i)),
			xango.A("data-style", s.Name), xango.A("aria-pressed", fmt.Sprint(current == s.Name))}
		if tv == nil {
			attrs = append(attrs, xango.A("disabled", "disabled"))
		} else {
			attrs = append(attrs, xango.On("click", func(xango.Event) {
				tv.Dress(s)
				redraw()
				renderUI()
			}))
		}
		kids := []xango.Node{xango.El("button", attrs,
			xango.El("span", []xango.Attr{xango.A("aria-hidden", "true"), xango.A("style", "display:inline-block;width:12px;height:12px;margin-right:6px;vertical-align:middle;background:"+swatch)}),
			xango.Text(s.Name))}
		if i >= len(canvas.TableSchemes())-len(canvas.CustomTableSchemes()) {
			kids = append(kids, btn(fmt.Sprintf("tstyle-remove-%d", i), T("tstyle.remove", "Remove"), func() {
				canvas.RemoveTableScheme(s.Name)
				renderUI()
			}, false))
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item")}, kids...))
	}
	nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, items...))
	nodes = append(nodes,
		xango.El("h5", nil, xango.Text(T("tstyle.define", "Define a style"))),
		lineInput("tstyle-name", T("tstyle.name", "Name of the style"), f.name, "", func(v string) { f.name = v }),
		lineInput("tstyle-header", T("tstyle.header", "Header background (like #2563eb)"), f.header, "#2563eb", func(v string) { f.header = v }),
		lineInput("tstyle-headertext", T("tstyle.headerText", "Header text colour"), f.headerText, "#ffffff", func(v string) { f.headerText = v }),
		lineInput("tstyle-band", T("tstyle.band", "Background of every second row"), f.band, "#e8f0fe", func(v string) { f.band = v }),
		xango.El("div", []xango.Attr{xango.A("id", "tstyle-headerbold")}, components.Checkbox(components.CheckboxProps{
			Label: T("tstyle.headerBold", "Header in bold"), Checked: f.headerBold, OnChange: func(on bool) { f.headerBold = on; renderUI() }})),
		xango.El("div", []xango.Attr{xango.A("id", "tstyle-firstbold")}, components.Checkbox(components.CheckboxProps{
			Label: T("tstyle.firstBold", "First column in bold"), Checked: f.firstBold, OnChange: func(on bool) { f.firstBold = on; renderUI() }})),
		btn("tstyle-save", T("tstyle.save", "Save the style"), func() {
			err := canvas.AddTableScheme(canvas.TableScheme{Name: f.name, HeaderBG: f.header, HeaderColor: f.headerText,
				HeaderBold: f.headerBold, BandBG: f.band, FirstColBold: f.firstBold})
			if err != nil {
				f.msg = err.Error()
			} else {
				f.msg = T("tstyle.saved", "Style saved with the document.")
				f.name, f.header, f.headerText, f.band, f.headerBold, f.firstBold = "", "", "", "", false, false
			}
			renderUI()
		}, false))
	if f.msg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "tstyle-message"), xango.A("role", "status")}, xango.Text(f.msg)))
	}
	return xango.El("div", []xango.Attr{xango.A("id", "tstyle-panel")}, nodes...)
}
