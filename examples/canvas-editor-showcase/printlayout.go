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
		components.Checkbox(components.CheckboxProps{
			Label:    T("print.first", "Different first page"),
			Checked:  u.layout.FirstDiffers,
			OnChange: func(on bool) { u.layout.FirstDiffers = on; u.layout = u.layout.Normalized(); renderUI() },
		}),
		firstFields(u, field, renderUI),
		components.Checkbox(components.CheckboxProps{
			Label:    T("print.even", "Different odd and even pages"),
			Checked:  u.layout.EvenDiffers,
			OnChange: func(on bool) { u.layout.EvenDiffers = on; u.layout = u.layout.Normalized(); renderUI() },
		}),
		evenFields(u, field, renderUI),
		xango.El("p", []xango.Attr{xango.A("class", "gj-print-help"), xango.A("style", "font-size:12px;margin:0.4em 0")},
			xango.Text(T("print.help", "Fields: {page}, {pages} and {title}. A tab separates the left, centre and right parts."))),
		components.Checkbox(components.CheckboxProps{
			Label:    T("print.toc", "Start with a table of contents"),
			Checked:  u.layout.TOC,
			OnChange: func(on bool) { u.layout.TOC = on; renderUI() },
		}))
}

// firstFields are the header and footer of the first page, shown while "Different first page" is on
// (empty means none there, as on a title page).
func firstFields(u *ui, field func(id, label, value string, set func(string)) xango.Node, renderUI func()) xango.Node {
	// Always one element, so the panel keeps its order when the fields come and go.
	box := func(children ...xango.Node) xango.Node {
		return xango.El("div", []xango.Attr{xango.A("class", "gj-print-first")}, children...)
	}
	if !u.layout.FirstDiffers {
		return box()
	}
	return box(
		field("firstheader", T("print.firstHeader", "First page header"), u.layout.FirstHeader, func(v string) { u.layout.FirstHeader = v; u.layout = u.layout.Normalized(); renderUI() }),
		field("firstfooter", T("print.firstFooter", "First page footer"), u.layout.FirstFooter, func(v string) { u.layout.FirstFooter = v; u.layout = u.layout.Normalized(); renderUI() }),
	)
}

// evenFields are the header and footer of the even pages, shown while "Different odd and even pages" is on (the
// header and footer above are then the odd pages').
func evenFields(u *ui, field func(id, label, value string, set func(string)) xango.Node, renderUI func()) xango.Node {
	box := func(children ...xango.Node) xango.Node {
		return xango.El("div", []xango.Attr{xango.A("class", "gj-print-even")}, children...)
	}
	if !u.layout.EvenDiffers {
		return box()
	}
	return box(
		field("evenheader", T("print.evenHeader", "Even pages header"), u.layout.EvenHeader, func(v string) { u.layout.EvenHeader = v; u.layout = u.layout.Normalized(); renderUI() }),
		field("evenfooter", T("print.evenFooter", "Even pages footer"), u.layout.EvenFooter, func(v string) { u.layout.EvenFooter = v; u.layout = u.layout.Normalized(); renderUI() }),
	)
}

// applyLayout stores the print layout and the sheet in a document that is being exported or saved.
func (u *ui) applyLayout(d *docfile.Document, width, height float64, margins [4]float64) {
	l := u.layout
	l.Width, l.Height, l.Margins = width, height, margins
	d.SetPageLayout(l)
}
