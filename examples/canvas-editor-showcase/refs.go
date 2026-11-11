//go:build js && wasm

package main

import (
	"fmt"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/reference"
	"github.com/elaous/gojiberry/xango"
)

type refsState struct {
	style   reference.Style
	sources []reference.Source
	form    reference.Source
	authors string // one per line, "Last, First"
	page    string // for the next citation
	msg     string
	next    int
}

func (u *ui) loadSources(d docfile.Document) {
	u.refs = refsState{style: d.ReferenceStyle(), sources: d.Sources(), form: reference.Source{Kind: reference.Book}}
	u.refs.next = len(u.refs.sources)
}

func (u *ui) keepSources(d *docfile.Document) { d.SetSources(u.refs.style, u.refs.sources) }

func refKinds() [][2]string {
	return [][2]string{{string(reference.Book), T("ref.book", "Book")}, {string(reference.Article), T("ref.article", "Article")},
		{string(reference.Web), T("ref.web", "Web page")}, {string(reference.Report), T("ref.report", "Report")}}
}

func insertRuns(f *canvas.TextFlow, runs []reference.Run, para bool) {
	var items []canvas.Item
	for _, r := range runs {
		c := canvas.Chars{Text: r.Text}
		if r.Italic {
			c.Format.FontStyle = canvas.StyleItalic
		}
		items = append(items, c)
	}
	if para {
		items = append(items, canvas.Chars{Text: "\n"})
	}
	pos := f.Cursors()[0].Caret
	f.Replace([]canvas.Edit{{From: pos, To: pos, Items: items}})
	n := 0
	for _, it := range items {
		n += len([]rune(it.(canvas.Chars).Text))
	}
	f.SetCursors([]canvas.Cursor{canvas.At(pos + n)})
}

func refsPanel(u *ui, state *State, flow func() *canvas.TextFlow, redraw, renderUI func()) xango.Node {
	fl := flow()
	r := &u.refs
	if r.style == "" {
		r.style = reference.APA
	}
	if r.form.Kind == "" {
		r.form.Kind = reference.Book
	}
	canEdit := state.Role.CanEdit() && state.Mode == canvas.ModeEdit
	var styles [][2]string
	for _, s := range reference.Styles {
		styles = append(styles, [2]string{string(s.Style), s.Name})
	}
	f := &r.form
	field := func(id, label string, v *string) xango.Node {
		return lineInput("ref-"+id, label, *v, "", func(x string) { *v = x })
	}
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("ref.title", "References"))),
		selectOf("ref-style", T("ref.style", "Style"), string(r.style), styles, func(v string) { r.style = reference.Style(v); renderUI() }),
		xango.El("h5", nil, xango.Text(T("ref.add", "Add a source"))),
		selectOf("ref-kind", T("ref.kind", "Kind"), string(f.Kind), refKinds(), func(v string) { f.Kind = reference.Kind(v); renderUI() }),
		textarea("ref-authors", T("ref.authors", "Authors, one per line: Last, First"), r.authors, T("ref.authorsHint", "Silva, Ana Maria"), "3", func(v string) { r.authors = v }),
		field("title", T("ref.titleLabel", "Title"), &f.Title),
		field("container", T("ref.container", "Journal or web site"), &f.Container),
		field("publisher", T("ref.publisher", "Publisher"), &f.Publisher),
		field("place", T("ref.place", "Place"), &f.Place),
		field("year", T("ref.year", "Year"), &f.Year),
		field("edition", T("ref.edition", "Edition"), &f.Edition),
		field("volume", T("ref.volume", "Volume"), &f.Volume),
		field("issue", T("ref.issue", "Issue"), &f.Issue),
		field("pages", T("ref.pages", "Pages"), &f.Pages),
		field("url", T("ref.url", "Address (URL)"), &f.URL),
		field("doi", T("ref.doi", "DOI"), &f.DOI),
		field("accessed", T("ref.accessed", "Accessed (YYYY-MM-DD)"), &f.Accessed),
		btn("ref-add", T("ref.addBtn", "Add the source"), func() {
			if strings.TrimSpace(f.Title) == "" {
				r.msg = T("ref.needTitle", "A source needs a title.")
				renderUI()
				return
			}
			src := *f
			for _, a := range strings.Split(r.authors, "\n") {
				if a = strings.TrimSpace(a); a != "" {
					src.Authors = append(src.Authors, a)
				}
			}
			r.next++
			src.ID = fmt.Sprintf("s%d", r.next)
			r.sources = append(r.sources, src)
			r.form, r.authors, r.msg = reference.Source{Kind: src.Kind}, "", T("ref.added", "Source added.")
			if fl != nil {
				fl.Touch() // the sources are saved with the document
			}
			renderUI()
		}, !canEdit),
	}
	if r.msg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "ref-message"), xango.A("role", "status")}, xango.Text(r.msg)))
	}
	if len(r.sources) == 0 {
		return xango.El("div", []xango.Attr{xango.A("id", "ref-panel")}, append(nodes, para("ref-none", T("ref.none", "No sources yet."), false))...)
	}
	order := reference.Order(r.style, r.sources)
	nodes = append(nodes,
		xango.El("h5", nil, xango.Text(T("ref.sources", "Sources"))),
		lineInput("ref-page", T("ref.page", "Page for the next citation (optional)"), r.page, "", func(v string) { r.page = v }))
	var items []xango.Node
	for i, s := range r.sources {
		i, s := i, s
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item"), xango.A("id", fmt.Sprintf("ref-item-%d", i))},
			xango.El("span", []xango.Attr{xango.A("id", fmt.Sprintf("ref-entry-%d", i))}, xango.Text(reference.Text(reference.Entry(r.style, s, order[s.ID])))),
			btn(fmt.Sprintf("ref-cite-%d", i), T("ref.cite", "Cite it here"), func() {
				if fl == nil {
					return
				}
				insertRuns(fl, []reference.Run{{Text: reference.Citation(r.style, s, order[s.ID], r.page)}}, false)
				redraw()
				renderUI()
			}, !canEdit),
			btn(fmt.Sprintf("ref-remove-%d", i), T("ref.remove", "Remove"), func() {
				r.sources = append(r.sources[:i:i], r.sources[i+1:]...)
				if fl != nil {
					fl.Touch()
				}
				renderUI()
			}, !canEdit)))
	}
	nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, items...),
		btn("ref-bibliography", T("ref.bibliography", "Insert the list of references"), func() {
			if fl == nil {
				return
			}
			insertRuns(fl, []reference.Run{{Text: T("ref.heading", "References")}}, true)
			for _, e := range reference.Bibliography(r.style, r.sources) {
				insertRuns(fl, e, true)
			}
			redraw()
			renderUI()
		}, !canEdit))
	return xango.El("div", []xango.Attr{xango.A("id", "ref-panel")}, nodes...)
}
