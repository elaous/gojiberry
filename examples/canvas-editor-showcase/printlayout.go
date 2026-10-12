//go:build js && wasm

package main

import (
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/xango"
)

// printPanel is the print layout part of the Page tab: the running header and footer (with the fields
// {page}, {pages} and {title}, a tab between the left, centre and right parts) and whether a table of
// contents starts the document. The settings are kept in the document (docfile.PageLayout), so the
// PDF, Word, RTF and HTML exports carry them.
func printPanel(u *ui, renderUI func()) xango.Node {
	field := func(id, label string, value string, set func(string)) xango.Node {
		return xango.El("label", []xango.Attr{xango.A("class", "gj-pagesetup-field")},
			xango.El("span", nil, xango.Text(label)),
			xango.El("input", []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-print-"+id), xango.A("value", value),
				xango.A("autocomplete", "off"),
				xango.On("change", func(e xango.Event) { set(xango.EventValue[string](e)) })}))
	}
	return xango.El("section", []xango.Attr{xango.A("class", "gj-print-layout"), xango.A("aria-label", T("print.title", "Print layout"))},
		xango.El("h3", nil, xango.Text(T("print.title", "Print layout"))),
		field("header", T("print.header", "Header"), u.layout.Header, func(v string) { u.layout.Header = v; u.layout = u.layout.Normalized(); renderUI() }),
		field("footer", T("print.footer", "Footer"), u.layout.Footer, func(v string) { u.layout.Footer = v; u.layout = u.layout.Normalized(); renderUI() }),
		xango.El("p", []xango.Attr{xango.A("class", "gj-print-help"), xango.A("style", "font-size:12px;margin:0.4em 0")},
			xango.Text(T("print.help", "Fields: {page}, {pages} and {title}. A tab separates the left, centre and right parts."))),
		components.Checkbox(components.CheckboxProps{
			Label:    T("print.toc", "Start with a table of contents"),
			Checked:  u.layout.TOC,
			OnChange: func(on bool) { u.layout.TOC = on; renderUI() },
		}))
}

// applyLayout stores the print layout and the sheet in a document that is being exported or saved.
func (u *ui) applyLayout(d *docfile.Document, width, height float64, margins [4]float64) {
	l := u.layout
	l.Width, l.Height, l.Margins = width, height, margins
	d.SetPageLayout(l)
}
