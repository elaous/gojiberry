//go:build js && wasm

package main

import (
	"fmt"
	"net/url"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/i18n"
	"github.com/elaous/gojiberry/share"
	"github.com/elaous/gojiberry/xango"
)

// saveLabels are the English words for the save states.
var saveLabels = map[string]string{"saved": "Saved", "saving": "Saving…", "unsaved": "Unsaved changes", "failed": "Save failed"}

// statusBar is the status bar: a selection's counts when there is one, the page
// setup, the pages the text reaches, where saving stands, the heading the caret
// is under, the counts and the mode.
func statusBar(flow *canvas.TextFlow, st canvas.FlowStats, sheets int, mode canvas.Mode, setup components.PageSetup, save string) components.StatusBarProps {
	var items []components.StatusItem
	if st.HasSelection {
		items = append(items, components.StatusItem{ID: "selection", Text: T("status.selection", "Selection: %d words, %d characters", st.Selection.Words, st.Selection.Characters)})
	}
	items = append(items,
		components.StatusItem{ID: "setup", Text: setup.Label(), Title: T("status.pageSetup", "Page setup")},
		components.StatusItem{ID: "pages", Text: T("status.pages", "%d of %d pages", max(st.Pages, 1), sheets)},
	)
	if save != "" {
		items = append(items, components.StatusItem{ID: "save", Live: true, State: save, Text: T("status.save."+save, saveLabels[save])})
	}
	if flow != nil {
		if cs := flow.Cursors(); len(cs) > 0 {
			items = append(items, components.StatusItem{ID: "breadcrumb", Text: components.HeadingBreadcrumb(flow.Outline(), flow.OutlineIndexAt(cs[0].Caret))})
		}
	}
	return components.StatusBarProps{Items: append(items,
		components.StatusItem{ID: "words", Text: T("status.words", "%d words", st.Text.Words)},
		components.StatusItem{ID: "characters", Text: T("status.characters", "%d characters", st.Text.Characters)},
		components.StatusItem{ID: "mode", Text: T("status.mode."+mode.String(), mode.String()+" mode")})}
}

// readTime formats a reading time the way a status bar would.
func readTime(st canvas.TextStats) string {
	if m := st.ReadingTime().Minutes(); m >= 1 {
		return fmt.Sprintf("%.0f min", m)
	}
	return "under a minute"
}

// statsState is the counts for #state (the e2e tests read it).
func statsState(st canvas.FlowStats) map[string]any {
	out := map[string]any{"words": st.Text.Words, "characters": st.Text.Characters, "charactersNoSpaces": st.Text.CharactersNoSpaces,
		"paragraphs": st.Text.Paragraphs, "sentences": st.Text.Sentences, "pages": st.Pages, "hasSelection": st.HasSelection}
	if st.HasSelection {
		out["selWords"], out["selCharacters"] = st.Selection.Words, st.Selection.Characters
	}
	return out
}

// statsPanel is the Stats tab: a table of the counts, for the whole text and
// for the selection.
func statsPanel(st canvas.FlowStats, sheets int) xango.Node {
	row := func(label string, whole, sel any) xango.Node {
		selCell := xango.El("td", []xango.Attr{xango.A("style", "text-align:right;color:#6b7280")}, xango.Text("–"))
		if st.HasSelection {
			selCell = xango.El("td", []xango.Attr{xango.A("style", "text-align:right")}, xango.Text(fmt.Sprint(sel)))
		}
		return xango.El("tr", nil, xango.El("td", nil, xango.Text(label)), xango.El("td", []xango.Attr{xango.A("style", "text-align:right")}, xango.Text(fmt.Sprint(whole))), selCell)
	}
	return xango.El("table", []xango.Attr{xango.A("id", "stats"), xango.A("style", "width:100%;border-collapse:collapse")},
		xango.El("tr", nil, xango.El("th", []xango.Attr{xango.A("style", "text-align:left")}, xango.Text("")), xango.El("th", nil, xango.Text("Document")), xango.El("th", nil, xango.Text("Selection"))),
		row("Words", st.Text.Words, st.Selection.Words),
		row("Characters", st.Text.Characters, st.Selection.Characters),
		row("Characters (no spaces)", st.Text.CharactersNoSpaces, st.Selection.CharactersNoSpaces),
		row("Paragraphs", st.Text.Paragraphs, st.Selection.Paragraphs),
		row("Sentences", st.Text.Sentences, st.Selection.Sentences),
		row("Pages used", fmt.Sprintf("%d of %d", max(st.Pages, 1), sheets), ""),
		row("Reading time", readTime(st.Text), readTime(st.Selection)),
	)
}

// shareParams are the parameters of the current page a shared link keeps: which
// document and hub, over what; never the sign-in token or who you are.
func shareParams() url.Values {
	q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
	out := url.Values{}
	if doc := pageDoc(); doc != "" {
		out.Set("doc", doc)
	}
	for _, k := range []string{"hub", "transport"} {
		if q.Call("has", k).Bool() {
			out.Set(k, q.Call("get", k).String())
		}
	}
	return out
}

// pageDoc is the shared document the page was opened on: /d/{doc} in the path,
// or the older ?doc= in the query.
func pageDoc() string {
	loc := js.Global().Get("location")
	if _, doc := share.SplitPath(loc.Get("pathname").String()); doc != "" {
		return doc
	}
	q := js.Global().Get("URLSearchParams").New(loc.Get("search"))
	if q.Call("has", "doc").Bool() {
		return q.Call("get", "doc").String()
	}
	return ""
}

// pageBase is the page's address without a /d/{doc} on the end.
func pageBase() string {
	loc := js.Global().Get("location")
	base, _ := share.SplitPath(loc.Get("pathname").String())
	return loc.Get("origin").String() + base
}

// sharePanel is the Share tab: the selected text, a link to it, and buttons to
// copy the link or hand it to the device's share sheet.
func sharePanel(u *ui, flow *canvas.TextFlow, origin string, renderUI func()) xango.Node {
	from, to := flow.Selection()
	sel := share.Of(flow.EditText(), from, to)
	if sel.Empty() {
		u.shareLink = ""
		return xango.El("p", []xango.Attr{xango.A("id", "share-hint")}, xango.Text("Select some text to get a link to it."))
	}
	u.shareLink = share.Link(pageBase(), shareParams(), sel)
	quote := sel.Quote
	if sel.To-sel.From > share.QuoteRunes {
		quote += "…"
	}
	button := func(id, label string, f func()) xango.Node {
		return xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-keybindings-button"), xango.A("id", id),
			xango.On("click", func(xango.Event) { f() })}, xango.Text(label))
	}
	buttons := []xango.Node{button("share-copy", "Copy link", func() {
		if nav := js.Global().Get("navigator"); !nav.Get("clipboard").IsUndefined() {
			nav.Get("clipboard").Call("writeText", u.shareLink)
		}
		u.message = "Link copied"
		renderUI()
	})}
	if !js.Global().Get("navigator").Get("share").IsUndefined() {
		buttons = append(buttons, button("share-native", "Share…", func() {
			js.Global().Get("navigator").Call("share", map[string]any{"title": u.title, "text": "“" + sel.Quote + "”", "url": u.shareLink})
		}))
	}
	return xango.Frag(
		xango.El("blockquote", []xango.Attr{xango.A("id", "share-quote"), xango.A("style", "margin:0 0 0.8em;padding-left:0.8em;border-left:3px solid #7c5cff")}, xango.Text(quote)),
		xango.El("input", []xango.Attr{xango.A("id", "share-link"), xango.A("readonly", "readonly"), xango.A("style", "width:100%"), xango.A("value", u.shareLink)}),
		xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;margin-top:0.6em")}, buttons...),
	)
}

// openSharedSelection selects the text a shared link points at, once the
// text is there (a shared document's arrives after the page does).
func openSharedSelection(u *ui, flow func() *canvas.TextFlow, scroller func() *canvas.ScrollView, redraw, renderUI func()) {
	q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
	sel, ok := share.Parse(url.Values{"sel": {q.Call("get", "sel").String()}, "q": {strings.TrimPrefix(q.Call("get", "q").String(), "<null>")}})
	if q.Call("get", "sel").IsNull() || !ok {
		return
	}
	for range 50 { // up to five seconds
		time.Sleep(100 * time.Millisecond)
		fl := flow()
		if fl == nil {
			continue
		}
		if from, to, ok := sel.Resolve(fl.EditText()); ok {
			u.tab = "share"
			fl.GotoOffset(from) // moves the caret, so select after
			fl.SetSelection(from, to)
			scroller().RevealPending()
			redraw()
			renderUI()
			return
		}
	}
}

// --- the interface language ---------------------------------------------

var (
	catalog = i18n.Default()
	locale  = "en"
)

// T is the text for a message id in the interface language, or def (English).
func T(id, def string, args ...any) string { return catalog.Text(locale, id, def, args...) }

// languageNames are the interface languages on offer, by their own names.
var languageNames = map[string]string{"en": "English", "es": "Español", "fr": "Français", "de": "Deutsch"}

// setLocale changes the interface language: the components, the page's lang and
// reading direction.
func setLocale(code string) {
	locale = code
	components.Translate = func(id, def string) string { return catalog.Text(locale, id, def) }
	root := js.Global().Get("document").Get("documentElement")
	root.Call("setAttribute", "lang", code)
	root.Call("setAttribute", "dir", i18n.Direction(code))
}

// initLocale picks the language from ?lang=, else the browser's preferences.
func initLocale() {
	avail := append([]string{"en"}, catalog.Languages()...)
	var prefs []string
	q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
	if q.Call("has", "lang").Bool() {
		prefs = append(prefs, q.Call("get", "lang").String())
	}
	if langs := js.Global().Get("navigator").Get("languages"); langs.Truthy() {
		for i := range langs.Length() {
			prefs = append(prefs, langs.Index(i).String())
		}
	}
	setLocale(i18n.Match(prefs, avail, "en"))
}

// languageMenu lists the interface languages, the current one ticked.
func languageMenu(u *ui, renderUI func()) []components.MenuItem {
	codes := append([]string{"en"}, catalog.Languages()...)
	var items []components.MenuItem
	for _, c := range codes {
		name := languageNames[c]
		if name == "" {
			name = c
		}
		items = append(items, components.MenuItem{Label: name, Checked: c == locale, OnSelect: func() {
			setLocale(c)
			u.announce(T("a11y.language", "Interface language: %s", languageNames[c]))
			renderUI()
		}})
	}
	return items
}

// --- accessibility -------------------------------------------------------

// a11yBlocks is the document as the blocks a screen reader is given, found
// again only when the text changes.
func (u *ui) a11yBlocks(flow *canvas.TextFlow, title string) []components.A11yBlock {
	if flow == nil {
		return nil
	}
	sig := flow.EditText()
	if u.a11ySig == sig && u.a11yCache != nil {
		return u.a11yCache
	}
	snap := export.FromDocument(docfile.FromFlow(title, flow))
	var out []components.A11yBlock
	for i := 0; i < len(snap.Blocks); i++ {
		b := snap.Blocks[i]
		text := ""
		for _, s := range b.Segments {
			if s.SoftBreak {
				text += " "
			} else {
				text += s.Text
			}
		}
		switch b.Kind {
		case export.Heading1, export.Heading2, export.Heading3:
			out = append(out, components.A11yBlock{Kind: components.A11yHeading, Level: b.Level, Text: text})
		case export.BulletItem, export.NumberedItem:
			out = append(out, components.A11yBlock{Kind: components.A11yListItem, Level: b.Level, Ordered: b.Kind == export.NumberedItem, Text: text})
		case export.Quote:
			out = append(out, components.A11yBlock{Kind: components.A11yQuote, Text: text})
		case export.Code:
			out = append(out, components.A11yBlock{Kind: components.A11yCode, Text: text})
		case export.Image:
			out = append(out, components.A11yBlock{Kind: components.A11yParagraph, Text: T("a11y.image", "Image")})
		case export.TableCell:
			if b.TableCol == 0 {
				row := components.A11yBlock{Kind: components.A11yTableRow}
				for c := 0; c < b.TableCols && i+c < len(snap.Blocks); c++ {
					var cell string
					for _, s := range snap.Blocks[i+c].Segments {
						cell += s.Text
					}
					row.Cells = append(row.Cells, cell)
				}
				out = append(out, row)
			}
		default:
			if text != "" {
				out = append(out, components.A11yBlock{Kind: components.A11yParagraph, Text: text})
			}
		}
	}
	u.a11ySig, u.a11yCache = sig, out
	return out
}

// announce has a screen reader say msg.
func (u *ui) announce(msg string) { u.announcement = msg }

// selectedText is what is selected, cut short, for the screen reader's copy of
// the document.
func selectedText(flow *canvas.TextFlow) string {
	if flow == nil || !flow.HasSelection() {
		return ""
	}
	s := flow.SelectedText()
	if rs := []rune(s); len(rs) > 200 {
		return string(rs[:200]) + "…"
	}
	return s
}

// labelCanvas gives the canvas and its hidden input names and a description a
// screen reader can use: the canvas is a text box whose text is the hidden copy.
func labelCanvas(canvasEl, inputEl js.Value) {
	for _, el := range []js.Value{canvasEl, inputEl} {
		el.Call("setAttribute", "aria-describedby", "a11y-text")
		el.Call("setAttribute", "aria-multiline", "true")
	}
	canvasEl.Call("setAttribute", "role", "textbox")
	canvasEl.Call("setAttribute", "aria-readonly", "true") // typing goes through the input
	canvasEl.Call("setAttribute", "aria-label", T("a11y.canvas", "Document"))
	inputEl.Call("setAttribute", "aria-label", T("a11y.input", "Document editor"))
}

// a11yObjects is the floating text, tables and pictures of the page, which are
// not in the text flow, as blocks for the screen reader's copy.
func a11yObjects(root *cxango.Root) []components.A11yBlock {
	var out []components.A11yBlock
	for n := 0; ; n++ {
		ft, ok := root.Find(cxango.TagFloatingText, n).(*canvas.FloatingText)
		if !ok {
			break
		}
		if t := ft.Text(); t != "" {
			out = append(out, components.A11yBlock{Kind: components.A11yParagraph, Text: T("a11y.note", "Note: %s", t)})
		}
	}
	for n := 0; ; n++ {
		tv, ok := root.Find(cxango.TagTable, n).(*canvas.TableView)
		if !ok {
			break
		}
		for _, row := range tv.Table.Cells {
			out = append(out, components.A11yBlock{Kind: components.A11yTableRow, Cells: append([]string(nil), row...)})
		}
	}
	for n := 0; root.Find(cxango.TagImage, n) != nil; n++ {
		out = append(out, components.A11yBlock{Kind: components.A11yParagraph, Text: T("a11y.image", "Image")})
	}
	return out
}
