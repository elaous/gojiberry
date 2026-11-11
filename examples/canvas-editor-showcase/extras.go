//go:build js && wasm

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"net/url"
	"slices"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/a11ycheck"
	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/collab/client"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/export/remote"
	"github.com/elaous/gojiberry/i18n"
	"github.com/elaous/gojiberry/lang"
	"github.com/elaous/gojiberry/share"
	"github.com/elaous/gojiberry/xango"
)

var saveLabels = map[string]string{"saved": "Saved", "saving": "Saving…", "unsaved": "Unsaved changes", "failed": "Save failed"}

func statusBar(flow *canvas.TextFlow, st canvas.FlowStats, sheets int, mode canvas.Mode, setup components.PageSetup, save string, sync *components.StatusItem) components.StatusBarProps {
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
	if sync != nil { // a shared document: where its changes stand
		items = append(items, *sync)
	}
	if flow != nil {
		if cs := flow.Cursors(); len(cs) > 0 {
			items = append(items, components.StatusItem{ID: "breadcrumb", Text: components.HeadingBreadcrumb(flow.Outline(), flow.OutlineIndexAt(cs[0].Caret))})
		}
	}
	return components.StatusBarProps{Items: append(items,
		components.StatusItem{ID: "words", Text: Tn("status.words", "%d word", "%d words", st.Text.Words)},
		components.StatusItem{ID: "characters", Text: Tn("status.characters", "%d character", "%d characters", st.Text.Characters)},
		components.StatusItem{ID: "mode", Text: T("status.mode."+mode.String(), mode.String()+" mode")})}
}

func readTime(st canvas.TextStats) string {
	if m := st.ReadingTime().Minutes(); m >= 1 {
		return fmt.Sprintf("%.0f min", m)
	}
	return "under a minute"
}

func statsState(st canvas.FlowStats) map[string]any {
	out := map[string]any{"words": st.Text.Words, "characters": st.Text.Characters, "charactersNoSpaces": st.Text.CharactersNoSpaces,
		"paragraphs": st.Text.Paragraphs, "sentences": st.Text.Sentences, "pages": st.Pages, "hasSelection": st.HasSelection}
	if st.HasSelection {
		out["selWords"], out["selCharacters"] = st.Selection.Words, st.Selection.Characters
	}
	return out
}

func statsPanel(st canvas.FlowStats, sheets int) xango.Node {
	row := func(label string, whole, sel any) xango.Node {
		selCell := xango.El("td", []xango.Attr{xango.A("style", "text-align:right;color:#6b7280")}, xango.Text("–"))
		if st.HasSelection {
			selCell = xango.El("td", []xango.Attr{xango.A("style", "text-align:right")}, xango.Text(fmt.Sprint(sel)))
		}
		return xango.El("tr", nil, xango.El("td", nil, xango.Text(label)), xango.El("td", []xango.Attr{xango.A("style", "text-align:right")}, xango.Text(fmt.Sprint(whole))), selCell)
	}
	return xango.El("table", []xango.Attr{xango.A("id", "stats"), xango.A("style", "width:100%;border-collapse:collapse")},
		xango.El("tr", nil, xango.El("th", []xango.Attr{xango.A("style", "text-align:left")}, xango.El("span", []xango.Attr{xango.A("class", "gj-sr-only")}, xango.Text("Measure"))), xango.El("th", nil, xango.Text("Document")), xango.El("th", nil, xango.Text("Selection"))),
		row("Words", st.Text.Words, st.Selection.Words),
		row("Characters", st.Text.Characters, st.Selection.Characters),
		row("Characters (no spaces)", st.Text.CharactersNoSpaces, st.Selection.CharactersNoSpaces),
		row("Paragraphs", st.Text.Paragraphs, st.Selection.Paragraphs),
		row("Sentences", st.Text.Sentences, st.Selection.Sentences),
		row("Pages used", fmt.Sprintf("%d of %d", max(st.Pages, 1), sheets), ""),
		row("Reading time", readTime(st.Text), readTime(st.Selection)),
	)
}

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

func pageBase() string {
	loc := js.Global().Get("location")
	base, _ := share.SplitPath(loc.Get("pathname").String())
	return loc.Get("origin").String() + base
}

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

var (
	catalog = i18n.Default()
	locale  = "en"
)

func T(id, def string, args ...any) string { return catalog.Text(locale, id, def, args...) }

func Tn(id, one, other string, n int, args ...any) string {
	return catalog.Plural(locale, id, one, other, n, args...)
}

var languageNames = map[string]string{"en": "English", "es": "Español", "fr": "Français", "de": "Deutsch", "pt": "Português", "ar": "العربية", "he": "עברית", "en-x-plain": "English, plain words"}

func setLocale(code string) {
	locale = code
	components.Translate = func(id, def string) string { return catalog.Text(locale, id, def) }
	components.FormatTime = func(t time.Time, _ bool) string { return i18n.DateTime(locale, t.Local()) }
	components.TranslatePlural = func(id, one, other string, n int) string { return catalog.Plural(locale, id, one, other, n) }
	root := js.Global().Get("document").Get("documentElement")
	root.Call("setAttribute", "lang", code)
	root.Call("setAttribute", "dir", i18n.Direction(code))
}

func initLocale() {
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
	if curPrefs.Lang != "" && !q.Call("has", "lang").Bool() {
		prefs = append([]string{curPrefs.Lang}, prefs...) // no ?lang=: the language the person chose, then the browser's
	}
	useLocale(i18n.Match(prefs, langCodes(), "en"))
}

func languageMenu(u *ui, renderUI func()) []components.MenuItem {
	codes := langCodes()
	var items []components.MenuItem
	for _, c := range codes {
		name := languageNames[c]
		if name == "" {
			name = c
		}
		items = append(items, components.MenuItem{Label: name, Checked: c == locale, OnSelect: func() {
			p := curPrefs
			p.Lang = c
			setPrefs(p) // kept, and the interface language changes
			u.announce(T("a11y.language", "Interface language: %s", languageNames[c]))
			renderUI()
		}})
	}
	return items
}

func (u *ui) a11yBlocks(flow *canvas.TextFlow, title string) []components.A11yBlock {
	if flow == nil {
		return nil
	}
	doc := docfile.FromFlow(title, flow)
	content, _ := json.Marshal(doc.Content)
	h := fnv.New64a()
	_, _ = h.Write([]byte(title))
	_, _ = h.Write(content)
	sig := fmt.Sprint(h.Sum64())
	if u.a11ySig == sig && u.a11yCache != nil {
		return u.a11yCache
	}
	snap := export.FromDocumentTracked(doc)
	var out []components.A11yBlock
	var cur export.Block
	add := func(blk components.A11yBlock) {
		if cur.Start >= 0 {
			blk.Mapped, blk.Start, blk.Lead = true, cur.Start, cur.Lead
			blk.Object = cur.Kind == export.Image || cur.Kind == export.TableCell || cur.Equation != ""
		}
		out = append(out, blk)
	}
	for i := 0; i < len(snap.Blocks); i++ {
		b := snap.Blocks[i]
		cur = b
		text := ""
		for _, s := range b.Segments {
			switch {
			case s.Format.Change == export.Deleted:
			case s.SoftBreak:
				text += " "
			default:
				text += s.Text
			}
		}
		runs := a11yRuns(b.Segments)
		switch b.Kind {
		case export.Heading1, export.Heading2, export.Heading3:
			add(components.A11yBlock{Kind: components.A11yHeading, Level: b.Level, Text: text, Runs: runs})
		case export.BulletItem, export.NumberedItem:
			add(components.A11yBlock{Kind: components.A11yListItem, Level: b.Level, Ordered: b.Kind == export.NumberedItem, Text: text, Runs: runs})
		case export.Quote:
			add(components.A11yBlock{Kind: components.A11yQuote, Text: text, Runs: runs})
		case export.Code:
			add(components.A11yBlock{Kind: components.A11yCode, Text: text})
		case export.Paragraph:
			switch {
			case b.Equation != "":
				add(components.A11yBlock{Kind: components.A11yParagraph, Text: T("a11y.equation", "Equation: %s", text)})
			case text != "":
				add(components.A11yBlock{Kind: components.A11yParagraph, Text: text, Runs: runs})
			}
		case export.Image:
			switch {
			case b.ImageDecorative && b.ImageAlt == "":
			case b.ImageAlt != "":
				add(components.A11yBlock{Kind: components.A11yParagraph, Text: T("a11y.imageAlt", "Image: %s", b.ImageAlt)})
			default:
				add(components.A11yBlock{Kind: components.A11yParagraph, Text: T("a11y.imageNoAlt", "Image without a description")})
			}
		case export.TableCell:
			if b.TableCol == 0 {
				row := components.A11yBlock{Kind: components.A11yTableRow}
				for c := 0; c < b.TableCols && i+c < len(snap.Blocks); c++ {
					var cell string
					for _, s := range snap.Blocks[i+c].Segments {
						if s.Format.Change != export.Deleted {
							cell += s.Text
						}
					}
					row.Cells = append(row.Cells, cell)
				}
				add(row)
			}
		default:
			if text != "" {
				add(components.A11yBlock{Kind: components.A11yParagraph, Text: text, Runs: runs})
			}
		}
	}
	u.a11ySig, u.a11yCache = sig, out
	return out
}

func (u *ui) announce(msg string) { u.announcement = msg }

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

func a11yObjects(root *cxango.Root) []placedBlock {
	var out []placedBlock
	for n := 0; ; n++ {
		ft, ok := root.Find(cxango.TagFloatingText, n).(*canvas.FloatingText)
		if !ok {
			break
		}
		if t := ft.Text(); t != "" {
			out = append(out, placedBlock{block: components.A11yBlock{Kind: components.A11yParagraph, Text: T("a11y.note", "Note: %s", t)}, x: ft.X, y: ft.Y})
		}
	}
	for n := 0; ; n++ {
		tv, ok := root.Find(cxango.TagTable, n).(*canvas.TableView)
		if !ok {
			break
		}
		for _, row := range tv.Table.Values() {
			out = append(out, placedBlock{block: components.A11yBlock{Kind: components.A11yTableRow, Cells: append([]string(nil), row...)}, x: tv.X, y: tv.Y})
		}
	}
	for n := 0; root.Find(cxango.TagImage, n) != nil; n++ {
		var x, y float64
		if im, ok := root.Find(cxango.TagImage, n).(*canvas.Image); ok {
			x, y = im.X, im.Y
			switch {
			case im.Decorative && im.Alt == "":
				continue // nothing a reader needs
			case im.Alt != "":
				out = append(out, placedBlock{block: components.A11yBlock{Kind: components.A11yParagraph, Text: T("a11y.imageAlt", "Image: %s", im.Alt)}, x: x, y: y})
				continue
			}
		}
		out = append(out, placedBlock{block: components.A11yBlock{Kind: components.A11yParagraph, Text: T("a11y.image", "Image")}, x: x, y: y})
	}
	return out
}

func floatingObjects(root *cxango.Root) []a11ycheck.Object {
	var out []a11ycheck.Object
	for n := 0; root.Find(cxango.TagImage, n) != nil; n++ {
		if im, ok := root.Find(cxango.TagImage, n).(*canvas.Image); ok && im.Placement != canvas.PlaceInline {
			out = append(out, a11ycheck.Object{Kind: "image", Where: fmt.Sprintf("Picture %d", n+1), Alt: im.Alt, Decorative: im.Decorative})
		}
	}
	for n := 0; ; n++ {
		tv, ok := root.Find(cxango.TagTable, n).(*canvas.TableView)
		if !ok {
			break
		}
		out = append(out, a11ycheck.Object{Kind: "table", Where: fmt.Sprintf("Table %d", n+1), Rows: tv.Table.Values()})
	}
	return out
}

func pageEndpoint(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	origin := js.Global().Get("location").Get("origin").String()
	got, ok := allowedEndpoint(raw, origin)
	if !ok {
		js.Global().Get("console").Call("warn", "ignored a backend address in the page's query string: only this origin and loopback hosts are allowed ("+raw+")")
		return ""
	}
	return got
}

func a11yRuns(segs []export.Segment) []components.A11yRun {
	tagged := false
	for _, s := range segs {
		tagged = tagged || s.Format.Lang != "" || s.Format.Change != export.NoChange || s.Format.Href != "" || s.Format.Comment != "" || s.Format.NoteRef > 0
	}
	if !tagged {
		return nil
	}
	var out []components.A11yRun
	for _, s := range segs {
		r := components.A11yRun{Text: s.Text, Lang: s.Format.Lang, Href: s.Format.Href, Comments: s.Format.Comment}
		if s.Format.NoteRef > 0 {
			r.Stream = 1
		}
		switch s.Format.Change {
		case export.Inserted:
			r.Change, r.Author = "ins", s.Format.Author
		case export.Deleted:
			r.Change, r.Author = "del", s.Format.Author
		}
		if s.SoftBreak {
			r.Text = " "
			if n := len(out); n > 0 {
				r = out[n-1] // a break stays with the text before it
				r.Text = " "
			}
		}
		if n := len(out); n > 0 && sameRunKind(out[n-1], r) {
			out[n-1].Text += r.Text
		} else {
			out = append(out, r)
		}
	}
	return out
}

func sameRunKind(a, b components.A11yRun) bool {
	if a.Stream > 0 || b.Stream > 0 {
		return false
	}
	a.Text, b.Text = "", ""
	return a == b
}

func selectionLanguageMenu(u *ui, flow func() *canvas.TextFlow, redraw, refocus, renderUI func()) []components.MenuItem {
	set := func(tag, name string) func() {
		return func() {
			if fl := flow(); fl != nil && fl.SetLanguage(tag) {
				u.announce(T("a11y.selLang", "Language of the selection: %s", name))
				redraw()
				refocus()
				renderUI()
			}
		}
	}
	doc := T("menu.sameLang", "Same as the document")
	items := []components.MenuItem{{Label: doc, OnSelect: set("", doc)}, {Separator: true}}
	codes := make([]string, 0, len(lang.Names))
	for c := range lang.Names {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	for _, c := range codes {
		items = append(items, components.MenuItem{Label: lang.Names[c], OnSelect: set(c, lang.Names[c])})
	}
	return items
}

type checkState struct {
	ran      bool
	findings []a11ycheck.Finding
	override bool // the next export is allowed whatever the gate says
}

const exportGateKey = "showcase:export-gate"

const exportGateMetaName = "gojiberry-min-export-gate"

func operatorGateFloor() a11ycheck.GateMode {
	defer func() { _ = recover() }()
	m := js.Global().Get("document").Call("querySelector", `meta[name="`+exportGateMetaName+`"]`)
	if m.IsNull() || m.IsUndefined() {
		return a11ycheck.GateAllow
	}
	raw := m.Call("getAttribute", "content")
	if raw.IsNull() || raw.IsUndefined() {
		return a11ycheck.GateAllow
	}
	mode, ok := a11ycheck.ParseGateMode(raw.String())
	if !ok {
		js.Global().Get("console").Call("warn", exportGateMetaName+": "+raw.String()+" is not a gate (use warn or block); read as block")
		return a11ycheck.GateBlock
	}
	return mode
}

func exportGate() a11ycheck.GateMode {
	person := a11ycheck.GateAllow
	switch m := a11ycheck.GateMode(lsGet(exportGateKey)); m {
	case a11ycheck.GateWarn, a11ycheck.GateBlock:
		person = m
	}
	return person.AtLeast(operatorGateFloor())
}

func (u *ui) gatedExport(format string, doc docfile.Document) (string, []byte, string, error) {
	mode := exportGate()
	gateSaid := false
	if mode != a11ycheck.GateAllow {
		findings := a11ycheck.Check(doc, a11ycheck.Options{})
		floor := operatorGateFloor()
		allow, errs := mode.Decide(findings, u.check.override && a11ycheck.OverrideAllowed(floor))
		if !allow {
			if floor == a11ycheck.GateBlock {
				return "", nil, "", errors.New(T("check.blockedByOperator", "Not exported: the document has %d accessibility errors, and this site does not allow exporting a document with them. Fix them in the Check tab.", errs))
			}
			return "", nil, "", errors.New(T("check.blocked", "Not exported: the document has %d accessibility errors. Fix them in the Check tab, or allow this export there.", errs))
		}
		u.check.override = false
		if errs > 0 {
			u.announce(T("check.exportedWith", "Exported with %d accessibility errors", errs))
			gateSaid = true
		}
	}
	file, data, mime, err := u.transfer.Export(format, doc)
	if err == nil {
		rep, _ := remote.Fidelity(callFiles, format, doc)
		if n := len(rep.Losses); n > 0 && !gateSaid {
			u.announce(Tn("fidelity.announce", "Exported: %d thing left out or simplified in the %s file. The Files tab lists them.", "Exported: %d things left out or simplified in the %s file. The Files tab lists them.", n, format))
		}
	}
	return file, data, mime, err
}

func checkPanel(u *ui, state *State, flow func() *canvas.TextFlow, scroller func() *canvas.ScrollView, redraw, renderUI func()) xango.Node {
	props := components.CheckPanelProps{Checked: u.check.ran}
	for _, f := range u.check.findings {
		props.Findings = append(props.Findings, components.CheckFinding{
			Rule: f.Rule, Severity: f.Severity.String(), Message: f.Message, Fix: f.Fix, Text: f.Text, FixLabel: fixLabel(f)})
	}
	props.Gate, props.Overridden = string(exportGate()), u.check.override
	props.GateFloor = string(operatorGateFloor())
	props.OnGate = func(mode string) {
		if m := a11ycheck.GateMode(mode); m.AtLeast(operatorGateFloor()) != m {
			return // below the operator's minimum
		}
		lsSet(exportGateKey, mode)
		u.check.override = false
		renderUI()
	}
	props.OnOverride = func() { u.check.override = !u.check.override; renderUI() }
	run := func() {
		fl := flow()
		if fl == nil {
			return
		}
		u.check.findings = a11ycheck.Check(docfile.FromFlow(u.title, fl), a11ycheck.Options{})
		if u.floating != nil { // the page's floating pictures and tables follow the text's findings
			u.check.findings = append(u.check.findings, a11ycheck.CheckObjects(u.floating())...)
		}
		u.check.ran = true
		switch n := len(u.check.findings); n {
		case 0:
			u.announce(T("check.announceNone", "Accessibility check: nothing found"))
		default:
			u.announce(T("check.announce", "Accessibility check: %d findings", n))
		}
		renderUI()
	}
	props.OnCheck = run
	props.OnFix = func(i int) {
		fl := flow()
		if fl == nil || i < 0 || i >= len(u.check.findings) || !state.Role.CanEdit() {
			return
		}
		f := u.check.findings[i]
		switch f.Action {
		case a11ycheck.ActionHeadingLevel:
			fl.SetCursors([]canvas.Cursor{canvas.At(f.Offset)})
			fl.ApplyParagraph(func(p *canvas.ParaFormat) { p.Style = fmt.Sprintf("Heading %d", f.Arg) })
		case a11ycheck.ActionToParagraph:
			if f.Length > 0 {
				fl.SetCursors([]canvas.Cursor{{Anchor: f.Offset, Caret: f.Offset + f.Length}})
				fl.ApplyFormat(func(c *canvas.Format) { c.Scale = 0 })
			}
			fl.SetCursors([]canvas.Cursor{canvas.At(f.Offset)})
			fl.ApplyParagraph(func(p *canvas.ParaFormat) { p.Style = "" })
		case a11ycheck.ActionDefaultColour:
			fl.SetCursors([]canvas.Cursor{{Anchor: f.Offset, Caret: f.Offset + f.Length}})
			fl.SetTextColor("")
		case a11ycheck.ActionToList:
			kind := canvas.ListBullet
			if f.Arg == 1 {
				kind = canvas.ListOrdered
			}
			fl.SetCursors([]canvas.Cursor{{Anchor: f.Offset, Caret: f.Offset + f.Length}})
			fl.ApplyParagraph(func(p *canvas.ParaFormat) { p.List.Kind = kind })
			for j := len(f.Spans) - 1; j >= 0; j-- {
				if sp := f.Spans[j]; sp[1] > 0 {
					fl.ReplaceRange(sp[0], sp[0]+sp[1], "")
				}
			}
			fl.SetCursors([]canvas.Cursor{canvas.At(f.Offset)})
		case a11ycheck.ActionDescribe:
			u.tab = "images"
			fl.GotoOffset(f.Offset)
			scroller().RevealPending()
			redraw()
			renderUI()
			return
		}
		redraw()
		run()
	}
	props.OnGo = func(i int) {
		fl := flow()
		if fl == nil || i < 0 || i >= len(u.check.findings) {
			return
		}
		if u.check.findings[i].Offset < 0 {
			u.announce(u.check.findings[i].Text)
			return
		}
		fl.GotoOffset(u.check.findings[i].Offset)
		scroller().RevealPending()
		redraw()
		renderUI()
	}
	return components.CheckPanel(props)
}

func fixLabel(f a11ycheck.Finding) string {
	switch f.Action {
	case a11ycheck.ActionHeadingLevel:
		return T("check.fixHeading", "Make it a level %d heading", f.Arg)
	case a11ycheck.ActionToParagraph:
		return T("check.fixParagraph", "Make it a plain paragraph")
	case a11ycheck.ActionDefaultColour:
		return T("check.fixColour", "Use the default text colour")
	case a11ycheck.ActionDescribe:
		return T("check.fixDescribe", "Describe it")
	case a11ycheck.ActionToList:
		return T("check.fixList", "Make them a list")
	}
	return ""
}

func documentLanguageMenu(u *ui, renderUI func()) []components.MenuItem {
	set := func(tag, name string) func() {
		return func() {
			u.docLang = tag
			u.announce(T("a11y.docLang", "Language of the document: %s", name))
			renderUI()
		}
	}
	none := T("menu.noLang", "Not set")
	items := []components.MenuItem{{Label: none, Checked: u.docLang == "", OnSelect: set("", none)}, {Separator: true}}
	codes := make([]string, 0, len(lang.Names))
	for c := range lang.Names {
		codes = append(codes, c)
	}
	slices.Sort(codes)
	for _, c := range codes {
		items = append(items, components.MenuItem{Label: lang.Names[c], Checked: u.docLang == c, OnSelect: set(c, lang.Names[c])})
	}
	return items
}

func (u *ui) noteCollab(renderUI func()) {
	s := u.live
	if s == nil {
		return
	}
	u.collab.Level = curPrefs.Collab
	names := make([]string, 0, len(s.Peers))
	for _, p := range s.Peers {
		names = append(names, p.Name)
	}
	problem := ""
	if s.Err != nil {
		problem = s.Err.Error()
	}
	u.noteComments()
	if s.Merged >= u.collabMerged {
		u.collab.NoteMerged(s.Merged - u.collabMerged)
	}
	if s.Rejected >= u.collabRejected {
		u.collab.NoteRejected(s.Rejected - u.collabRejected)
	}
	if s.Conflicts > u.collabConflicts {
		u.collab.NoteConflict(s.ConflictWith)
	}
	u.collabConflicts = s.Conflicts
	u.collabMerged, u.collabRejected = s.Merged, s.Rejected
	u.noteAway(s.Status, renderUI)
	if msg := u.collab.Update(time.Now(), s.Status == client.Online, names, problem); msg != "" {
		u.announce(strings.TrimSpace(u.mentionSaid + " " + msg))
	}
	u.mentionSaid = ""
	if u.collab.Pending() && !u.collabTimer {
		u.collabTimer = true
		time.AfterFunc(2100*time.Millisecond, func() {
			u.collabTimer = false
			u.noteCollab(renderUI)
			renderUI()
		})
	}
}

func (u *ui) sayWhere(flow *canvas.TextFlow, p components.PresencePeer) {
	name := p.Name
	if name == "" {
		name = p.User
	}
	if name == "" {
		name = T("collab.someone", "Someone")
	}
	var text string
	var cur *canvas.RemoteCursor
	if flow != nil {
		text = flow.EditText()
		for i := range flow.RemoteCursors {
			if rc := &flow.RemoteCursors[i]; rc.Name == name {
				cur = rc
				break
			}
		}
	}
	if cur == nil {
		u.announce(components.WhereSentence(name, text, 0, 0, false))
		return
	}
	u.announce(components.WhereSentence(name, text, cur.Anchor, cur.Caret, true))
}

func (u *ui) noteComments() {
	if u.stateRef == nil || u.stateRef.Comments == nil {
		return
	}
	seen := map[string]commentSeen{}
	prefix := u.stateRef.Comments.IDPrefix
	for _, c := range u.stateRef.Comments.List() {
		seen[c.ID] = commentSeen{replies: len(c.Replies), resolved: c.Resolved}
		if u.commentsSeen == nil {
			continue // the first look only remembers
		}
		was, known := u.commentsSeen[c.ID]
		if !known {
			if prefix == "" || !strings.HasPrefix(c.ID, prefix) { // a new comment that is not the local person's
				u.collab.NoteComment(authorOrSomeone(c.Author))
			}
			continue
		}
		for i := was.replies; i < len(c.Replies); i++ {
			u.collab.NoteReply(authorOrSomeone(c.Replies[i].Author))
		}
		if c.Resolved && !was.resolved {
			u.collab.NoteResolved()
		}
	}
	first := u.commentsSeen == nil
	u.commentsSeen = seen
	u.noteMentions(first)
}

func (u *ui) myHandle() string {
	if u.stateRef == nil || strings.TrimSpace(u.stateRef.Author) == "" {
		return ""
	}
	who := strings.TrimSpace(u.stateRef.Author)
	if u.people != nil {
		if h, ok := u.people.HandleOf(who); ok {
			return h
		}
	}
	if strings.ContainsAny(who, " @") { // not usable as a handle
		return ""
	}
	return who
}

func (u *ui) noteMentions(first bool) {
	h := u.myHandle()
	if h == "" || u.stateRef == nil || u.stateRef.Comments == nil {
		return
	}
	if u.mentionsTold == nil {
		u.mentionsTold = map[string]bool{}
	}
	for _, n := range u.stateRef.Comments.NewMentions(h, u.mentionsTold) {
		u.mentionsTold[n.Key()] = true
		if !first {
			u.mentionSaid = strings.TrimSpace(u.mentionSaid + " " + T("mention.told", "%s mentioned you in a comment.", authorOrSomeone(n.Author)))
			u.announce(u.mentionSaid)
		}
	}
}

type commentSeen struct {
	replies  int
	resolved bool
}

func authorOrSomeone(a string) string {
	if a == "" {
		return T("collab.someone", "Someone")
	}
	return a
}

func (u *ui) sealComments() {
	if u.stateRef == nil || u.stateRef.Comments == nil || u.commentsSeen == nil {
		return
	}
	for _, c := range u.stateRef.Comments.List() {
		if _, known := u.commentsSeen[c.ID]; known {
			u.commentsSeen[c.ID] = commentSeen{replies: len(c.Replies), resolved: c.Resolved}
		}
	}
}

func (u *ui) syncItem() *components.StatusItem {
	s := u.live
	if s == nil {
		return nil
	}
	problem := ""
	if s.Err != nil {
		problem = s.Err.Error()
	}
	it := components.SyncStatus(s.Status == client.Online, s.Status == client.Connecting, s.Pending, problem)
	return &it
}

func commentSignature(set *canvas.CommentSet) string {
	replies, resolved := 0, 0
	list := set.List()
	for _, c := range list {
		replies += len(c.Replies)
		if c.Resolved {
			resolved++
		}
	}
	return fmt.Sprintf("%d/%d/%d", len(list), replies, resolved)
}
