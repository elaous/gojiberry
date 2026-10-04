//go:build js && wasm

package main

import (
	"fmt"
	"net/url"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/gox"
	"github.com/elaous/gojiberry/share"
)

// statusLine is the status bar: the pages the text reaches, its counts, and
// the selection's when there is one.
func statusLine(st canvas.FlowStats, sheets int, mode canvas.Mode) string {
	pages := max(st.Pages, 1)
	line := fmt.Sprintf("%d of %d pages · %d words · %d characters", pages, sheets, st.Text.Words, st.Text.Characters)
	if st.HasSelection {
		line = fmt.Sprintf("Selection: %d words, %d characters · ", st.Selection.Words, st.Selection.Characters) + line
	}
	return line + " · " + mode.String() + " mode"
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
func statsPanel(st canvas.FlowStats, sheets int) gox.Node {
	row := func(label string, whole, sel any) gox.Node {
		selCell := gox.El("td", []gox.Attr{gox.A("style", "text-align:right;color:#6b7280")}, gox.Text("–"))
		if st.HasSelection {
			selCell = gox.El("td", []gox.Attr{gox.A("style", "text-align:right")}, gox.Text(fmt.Sprint(sel)))
		}
		return gox.El("tr", nil, gox.El("td", nil, gox.Text(label)), gox.El("td", []gox.Attr{gox.A("style", "text-align:right")}, gox.Text(fmt.Sprint(whole))), selCell)
	}
	return gox.El("table", []gox.Attr{gox.A("id", "stats"), gox.A("style", "width:100%;border-collapse:collapse")},
		gox.El("tr", nil, gox.El("th", []gox.Attr{gox.A("style", "text-align:left")}, gox.Text("")), gox.El("th", nil, gox.Text("Document")), gox.El("th", nil, gox.Text("Selection"))),
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
	for _, k := range []string{"doc", "hub", "transport"} {
		if q.Call("has", k).Bool() {
			out.Set(k, q.Call("get", k).String())
		}
	}
	return out
}

func pageBase() string {
	loc := js.Global().Get("location")
	return loc.Get("origin").String() + loc.Get("pathname").String()
}

// sharePanel is the Share tab: the selected text, a link to it, and buttons to
// copy the link or hand it to the device's share sheet.
func sharePanel(u *ui, flow *canvas.TextFlow, origin string, renderUI func()) gox.Node {
	from, to := flow.Selection()
	sel := share.Of(flow.EditText(), from, to)
	if sel.Empty() {
		u.shareLink = ""
		return gox.El("p", []gox.Attr{gox.A("id", "share-hint")}, gox.Text("Select some text to get a link to it."))
	}
	u.shareLink = share.Link(pageBase(), shareParams(), sel)
	quote := sel.Quote
	if sel.To-sel.From > share.QuoteRunes {
		quote += "…"
	}
	button := func(id, label string, f func()) gox.Node {
		return gox.El("button", []gox.Attr{gox.A("type", "button"), gox.A("class", "gj-keybindings-button"), gox.A("id", id),
			gox.On("click", func(gox.Event) { f() })}, gox.Text(label))
	}
	buttons := []gox.Node{button("share-copy", "Copy link", func() {
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
	return gox.Frag(
		gox.El("blockquote", []gox.Attr{gox.A("id", "share-quote"), gox.A("style", "margin:0 0 0.8em;padding-left:0.8em;border-left:3px solid #7c5cff")}, gox.Text(quote)),
		gox.El("input", []gox.Attr{gox.A("id", "share-link"), gox.A("readonly", "readonly"), gox.A("style", "width:100%"), gox.A("value", u.shareLink)}),
		gox.El("div", []gox.Attr{gox.A("style", "display:flex;gap:6px;margin-top:0.6em")}, buttons...),
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
