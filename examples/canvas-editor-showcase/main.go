//go:build js && wasm

// Command canvas-editor-showcase is a small word processor with every
// piece we have built registered in one place: the canvas tree (app.gox)
// -- scrolling pages, an editable text flow with headers, footers and page
// numbers, a picture, a floating note and a floating table -- the
// registered editing features (multi-cursor, line operations, paragraphs,
// lists, links, case, breaks, Markdown copy/paste), and DOM components
// around it in a DocumentView: a menu bar built from the registered
// commands, the style editor with the font selector, a symbol picker, a
// link preview and the key bindings editor. Both trees render from one
// state. #state mirrors it as JSON (the e2e test reads it).
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/canvas"
	cgox "github.com/elaous/gojiberry/canvas/gox"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/domrt/browser"
	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/gojiberry/gox"
	"github.com/elaous/gojiberry/saver"
	"github.com/elaous/gojiberry/transfer"
)

// ui is the DOM side's state.
type ui struct {
	title   string
	menu    int    // open menu, or -1
	tab     string // sidebar tab: format, symbols, keys
	format  canvas.Format
	catalog []fonts.Font
	open    bool
	query   string
	source  string
	recent  []string

	symQuery, symCat string
	symRecent        []string

	kbQuery   string
	recording canvas.Command
	kbMessage string

	hoverLink    string
	hoverComment string
	selComment   string
	showResolved bool
	drafts       map[string]string

	saver    *saver.Session
	transfer *transfer.Registry
	fontLog  []string // what the font library did, newest last
	navSig   string   // outline/bookmarks the Outline tab last showed
	message  string   // the Import/Export tab's last message
}

func main() {
	doc := browser.Document()
	state := State{Mode: canvas.ModeView, Pages: 3, Source: sample, Note: "Floating note: click to edit, try (brackets and \"quotes\".", Styles: canvas.DefaultStyles(),
		Comments: canvas.NewCommentSet(), Author: "you@example.com"}
	u := &ui{title: "Quarterly report", menu: -1, tab: "format", drafts: map[string]string{}}
	// The catalog has a font that is fetched from this site: picking it
	// only adds it to the library; the file is downloaded (into the font
	// store) the first time the text uses it.
	origin := js.Global().Get("location").Get("origin").String()
	repo := fonts.Multi(fonts.WebSafe(), fonts.Static("This site", fonts.Font{
		Family: "Lazy Serif", Category: "serif",
		Variants: []fonts.Variant{{Weight: 400, URL: origin + "/fonts/lazy-serif.ttf"}},
	}))
	u.catalog, _ = repo.Fonts(context.Background())
	// Documents and fonts are kept in the browser (localStorage) or in
	// memory; a docstore server in front of Postgres, SQLite or S3 is one
	// more target (docstore.NewHTTPAdapter).
	store := docstore.NewMemoryAdapter()
	lib := &fonts.Library{Blobs: saver.Blobs(store)}
	u.transfer = transfer.Default()

	stage := canvas.NewStage(nil, 0, 0)
	canvasEl, inputEl := doc.GetElementByID("stage"), doc.GetElementByID("input")
	redraw, err := stage.Bind(canvasEl, inputEl)
	if err != nil {
		doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
		return
	}
	root := cgox.NewRoot(stage)
	root.Redraw = redraw
	defaults := stage.EffectiveKeymap()

	var flow *canvas.TextFlow
	var renderUI func()

	// Lazy font download: a font picked in the selector is only Added;
	// the stage tells us the first time the document really uses a cut,
	// and only then is the file fetched into the store and loaded.
	stage.OnFontUse = func(use canvas.FontUse) { lib.UseAsync(use.Family, use.Weight, use.Italic) }
	logFont := func(s string) { u.fontLog = append(u.fontLog, s); renderUI() }
	lib.OnReady = func(r fonts.Ready) {
		how := "loaded from the store"
		if r.Downloaded {
			how = "downloaded into the store"
		}
		logFont(fmt.Sprintf("%s %d: %s (%d bytes)", r.Family, r.Variant.Weight, how, len(r.Data)))
		fonts.LoadFace(r.Family, r.Variant, r.Data, func(err error) {
			if err != nil {
				logFont(r.Family + ": " + err.Error())
			}
			redraw()
		})
	}
	lib.OnError = func(family string, err error) { logFont(family + ": " + err.Error()) }
	refocus := func() { inputEl.Call("focus", map[string]any{"preventScroll": true}) }

	cb := Callbacks{
		OnHoverLink:    func(url string) { u.hoverLink = url; renderUI() },
		OnHoverComment: func(id string) { u.hoverComment = id },
	}
	renderCanvas := func() {
		if err := root.Render(Doc(state, cb)); err != nil {
			doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
			return
		}
		flow = root.Find(cgox.TagTextFlow, 0).(*canvas.TextFlow)
		stage.Draw()
	}

	openDoc := func(d docfile.Document) {
		state.Items, state.Source = docfile.ToItems(d.Content), ""
		if len(d.Styles) > 0 {
			state.Styles = docfile.ToStyles(d.Styles)
		}
		for _, f := range docfile.ToFonts(d.Fonts) {
			lib.Add(f)
		}
		if d.Title != "" {
			u.title = d.Title
		}
		stage.ForgetFonts() // the opened text may use fonts the library has
		renderCanvas()
		renderUI()
	}
	currentDoc := func() docfile.Document {
		d := docfile.FromFlow(u.title, flow)
		d.Fonts = docfile.FromFonts(lib.Added())
		return d
	}
	u.saver = saver.NewSession(&saver.Saver{Targets: []saver.Target{
		{Name: "browser", Label: "This browser (localStorage)", Adapter: docstore.NewBrowserAdapter("showcase:")},
		{Name: "memory", Label: "Memory", Adapter: store},
	}}, currentDoc, openDoc)
	u.saver.OnChange = func() { renderUI() }

	setMode := func(m canvas.Mode) {
		state.Mode = state.Role.Mode(m)
		renderCanvas()
		renderUI()
	}
	run := func(cmd canvas.Command) {
		stage.RunCommand(cmd)
		redraw()
		refocus()
	}

	var domRoot *domrt.Root
	renderUI = func() {
		mac := false
		keymap := stage.EffectiveKeymap()
		cmds := stage.Features.Commands()
		cmds = append(cmds, builtinCommands()...)
		toggleMode := func() {
			if state.Mode == canvas.ModeView {
				setMode(canvas.ModeEdit)
			} else {
				setMode(canvas.ModeView)
			}
		}

		menus := []components.MenuBarEntry{
			{Label: "View", Items: []components.MenuItem{
				{Label: "Edit mode", Checked: state.Mode == canvas.ModeEdit, OnSelect: toggleMode},
				{Label: "Two-page spread", Checked: state.Spread, OnSelect: func() { state.Spread = !state.Spread; renderCanvas(); renderUI() }},
			}},
			{Label: "File", Items: []components.MenuItem{
				{Label: "Save…", OnSelect: func() { u.tab = "save"; renderUI() }},
				{Label: "Import / Export…", OnSelect: func() { u.tab = "transfer"; renderUI() }},
			}},
			{Label: "Edit", Items: components.CommandMenu(stage.Features.Commands(), keymap, mac, run)},
			{Label: "Insert", Items: []components.MenuItem{
				{Label: "Symbol…", OnSelect: func() { u.tab = "symbols"; renderUI() }},
				{Label: "Comment…", Shortcut: components.FormatChord("Mod+Alt+N", mac), OnSelect: func() { run(canvas.CmdCommentAdd); u.tab = "comments"; renderUI() }},
				{Label: "Bookmark…", Shortcut: components.FormatChord("Mod+Alt+B", mac), OnSelect: func() { run(canvas.CmdBookmarkAdd); u.tab = "outline"; renderUI() }},
				{Label: "Link…", Shortcut: components.FormatChord("Mod+K", mac), OnSelect: func() { run(canvas.CmdEditLink) }},
				{Separator: true},
				{Label: "Page break", Shortcut: components.FormatChord("Mod+Enter", mac), OnSelect: func() { run(canvas.CmdPageBreak) }},
				{Label: "Column break", Shortcut: components.FormatChord("Mod+Shift+Enter", mac), OnSelect: func() { run(canvas.CmdColumnBreak) }},
			}},
			{Label: "Format", Items: []components.MenuItem{
				{Label: "Bold", Shortcut: components.FormatChord("Mod+B", mac), OnSelect: func() { run(canvas.CmdBold) }},
				{Label: "Italic", Shortcut: components.FormatChord("Mod+I", mac), OnSelect: func() { run(canvas.CmdItalic) }},
				{Label: "Underline", Shortcut: components.FormatChord("Mod+U", mac), OnSelect: func() { run(canvas.CmdUnderline) }},
				{Separator: true},
				{Label: "Style editor", OnSelect: func() { u.tab = "format"; renderUI() }},
			}},
			{Label: "Help", Items: []components.MenuItem{
				{Label: "Keyboard shortcuts", OnSelect: func() { u.tab = "keys"; renderUI() }},
			}},
		}
		menubar := components.MenuBar(components.MenuBarProps{Entries: menus, Open: u.menu, OnOpen: func(i int) { u.menu = i; renderUI() }})

		toolbar := gox.Frag(
			gox.El("button", []gox.Attr{gox.A("id", "mode"), gox.A("type", "button"), gox.A("class", "gj-keybindings-button"),
				gox.On("click", func(gox.Event) { toggleMode() })}, gox.Text("Mode: "+state.Mode.String()+" (click to switch)")),
			tabButton(u, "format", "Format", renderUI), tabButton(u, "styles", "Styles", renderUI), tabButton(u, "symbols", "Symbols", renderUI), tabButton(u, "keys", "Keys", renderUI),
			tabButton(u, "comments", "Comments", renderUI), tabButton(u, "outline", "Outline", renderUI),
			tabButton(u, "save", "Save", renderUI), tabButton(u, "transfer", "Files", renderUI),
		)

		roleButtons := []gox.Node{gox.El("span", []gox.Attr{gox.A("style", "font-size:12px;color:#6b7280")}, gox.Text("Role:"))}
		for _, r := range []canvas.Role{canvas.RoleOwner, canvas.RoleEditor, canvas.RoleViewer} {
			r := r
			class := "gj-keybindings-button"
			if state.Role.String() == r.String() {
				class += " gj-symbolpicker-chip-on"
			}
			roleButtons = append(roleButtons, gox.El("button", []gox.Attr{gox.A("type", "button"), gox.A("class", class), gox.A("data-role", r.String()),
				gox.On("click", func(gox.Event) {
					state.Role = r
					stage.SetRole(r)
					if !r.CanEdit() {
						state.Mode = canvas.ModeView
					}
					renderCanvas()
					renderUI()
				})}, gox.Text(r.String())))
		}
		roleRow := gox.El("div", []gox.Attr{gox.A("id", "roles"), gox.A("style", "display:flex;gap:6px;align-items:center;margin-bottom:0.8em")}, roleButtons...)

		var panel gox.Node
		switch u.tab {
		case "outline":
			panel = outlinePanel(&state, func() *canvas.TextFlow { return flow }, func() *canvas.ScrollView { return root.Find(cgox.TagScrollView, 0).(*canvas.ScrollView) }, redraw, refocus, renderUI)
		case "comments":
			panel = commentsPanel(u, &state, func() *canvas.TextFlow { return flow }, redraw, refocus, renderUI)
		case "styles":
			current := ""
			if flow != nil {
				if fs := flow.ParagraphFormats(); len(fs) > 0 {
					current = fs[0].Style
				}
			}
			panel = components.StylePicker(components.StylePickerProps{
				Styles: flow.Styles, Current: current,
				OnPick: func(name string, paragraph bool) {
					if paragraph {
						flow.ApplyParagraph(func(p *canvas.ParaFormat) {
							if name == "Normal" {
								name = ""
							}
							p.Style = name
						})
					} else {
						flow.ApplyFormat(func(f *canvas.Format) { f.Style = name })
					}
					redraw()
					refocus()
					renderUI()
				},
			})
		case "save":
			panel = components.Saver(u.saver.Props())
		case "transfer":
			var imports, exports []components.SaverChoice
			for _, i := range u.transfer.Importers() {
				imports = append(imports, components.SaverChoice{Name: i.Format().Name, Label: i.Format().Label})
			}
			for _, e := range u.transfer.Exporters() {
				exports = append(exports, components.SaverChoice{Name: e.Format().Name, Label: e.Format().Label})
			}
			panel = gox.Frag(
				components.Transfer(components.TransferProps{
					Imports: imports, Exports: exports,
					OnExport: func(name string) {
						file, data, mime, err := u.transfer.Export(name, currentDoc())
						if err != nil {
							u.message = err.Error()
						} else {
							transfer.Download(file, mime, data)
							u.message = fmt.Sprintf("Exported %s (%d bytes)", file, len(data))
						}
						renderUI()
					},
					OnImport: func(name string) {
						transfer.PickFile(u.transfer.Accept(), func(file string, data []byte, err error) {
							var d docfile.Document
							if err == nil {
								d, err = u.transfer.Import(name, file, data)
							}
							if err != nil {
								u.message = err.Error()
								renderUI()
								return
							}
							u.message = "Imported " + file
							openDoc(d)
						})
					},
				}),
				gox.El("p", []gox.Attr{gox.A("id", "transfer-message")}, gox.Text(u.message)),
			)
		case "symbols":
			panel = components.SymbolPicker(components.SymbolPickerProps{
				Query: u.symQuery, Category: u.symCat, Recent: u.symRecent,
				OnQuery:    func(q string) { u.symQuery = q; renderUI() },
				OnCategory: func(c string) { u.symCat = c; renderUI() },
				OnPick: func(ch string) {
					if flow != nil && flow.InsertText(ch) {
						u.symRecent = append([]string{ch}, slices.DeleteFunc(slices.Clone(u.symRecent), func(s string) bool { return s == ch })...)
						if len(u.symRecent) > 12 {
							u.symRecent = u.symRecent[:12]
						}
						redraw()
					}
					refocus()
					renderUI()
				},
			})
		case "keys":
			panel = components.KeyBindings(components.KeyBindingsProps{
				Commands: cmds, Keymap: keymap, Defaults: defaults, Query: u.kbQuery, Recording: u.recording, Message: u.kbMessage, Mac: mac,
				OnQuery:  func(q string) { u.kbQuery = q; renderUI() },
				OnRecord: func(c canvas.Command) { u.recording, u.kbMessage = c, ""; renderUI() },
				OnBind: func(c canvas.Command, chord string) {
					prev := stage.Rebind(c, chord)
					u.recording, u.kbMessage = "", fmt.Sprintf("%s is now %s", titleOf(cmds, c), components.FormatChord(chord, mac))
					if prev != "" && prev != c {
						u.kbMessage += fmt.Sprintf("; it was %s's", titleOf(cmds, prev))
					}
					renderUI()
				},
				OnClear: func(c canvas.Command) {
					stage.Rebind(c, "")
					u.kbMessage = titleOf(cmds, c) + " has no shortcut now"
					renderUI()
				},
				OnReset: func(c canvas.Command) {
					resetBinding(stage, defaults, c)
					u.kbMessage = titleOf(cmds, c) + " is back to its default"
					renderUI()
				},
				OnResetAll: func() { stage.ResetKeymap(); u.kbMessage = "All shortcuts are back to their defaults"; renderUI() },
			})
		default:
			panel = components.StyleEditor(components.StyleEditorProps{
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
						lib.Add(f) // downloaded only when the text uses it
						u.open = false
						u.recent = append([]string{f.Family}, slices.DeleteFunc(slices.Clone(u.recent), func(s string) bool { return s == f.Family })...)
						renderUI()
					},
				},
			})
		}
		if u.tab == "format" || u.tab == "" {
			var rows []gox.Node
			for _, l := range u.fontLog {
				rows = append(rows, gox.El("li", nil, gox.Text(l)))
			}
			panel = gox.Frag(panel, gox.El("ul", []gox.Attr{gox.A("id", "font-log"), gox.A("style", "font-size:12px;color:#6b7280")}, rows...))
		}
		side := gox.Frag(roleRow, panel,
			gox.El("div", []gox.Attr{gox.A("id", "link-preview"), gox.A("style", "margin-top:1em")},
				components.LinkPreview(components.LinkPreviewProps{
					URL: u.hoverLink, Title: "Gojiberry", Description: "Go components that render to HTML, the DOM, Gio and a canvas.",
					SiteName: "GitHub",
				})))

		words := 0
		if flow != nil {
			words = len(strings.Fields(flow.EditText()))
		}
		pages := state.Pages
		status := fmt.Sprintf("%d pages · %d words · %s mode", pages, words, state.Mode)
		node := components.DocumentView(components.DocumentViewProps{
			Title: u.title, OnTitle: func(t string) { u.title = t; renderUI() },
			MenuBar: menubar, Toolbar: toolbar,
			Canvas:  gox.El("div", []gox.Attr{gox.A("id", "canvas-host")}),
			Sidebar: &side, Status: status,
		})
		if domRoot == nil {
			domRoot = domrt.Mount(doc.GetElementByID("app").Element, node)
			// The canvas was made by the page; put it where the layout wants it.
			doc.GetElementByID("canvas-host").Call("appendChild", canvasEl.Element)
			doc.GetElementByID("canvas-host").Call("appendChild", inputEl.Element)
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
		var text string
		markers, dropCaps := []string{}, 0
		lineHeights := []float64{}
		var link, comment map[string]any
		scrollY := 0.0
		current := -1
		outline, bookmarks := []string{}, []string{}
		headers, watermarks := []string{}, []string{}
		suggestions := []string{}
		spread := false
		if ps, ok := root.Find(cgox.TagPage, 0).(*canvas.PageSet); ok {
			spread = ps.Spread
			for i := range ps.Pages {
				h, w := "", ""
				if hf := ps.ResolvedHeader(i); hf != nil {
					h = hf.Text
				}
				if wm := ps.ResolvedWatermark(i); wm != nil {
					w = wm.Text
				}
				headers, watermarks = append(headers, h), append(watermarks, w)
			}
		}
		if flow != nil {
			cursors, editing = flow.Cursors(), flow.Editing()
			suggestions = append(suggestions, flow.Suggestions()...)
			text = flow.EditText()
			sv := root.Find(cgox.TagScrollView, 0).(*canvas.ScrollView)
			scrollY = sv.ScrollY
			current = flow.OutlineIndexAt(flow.Cursors()[0].Caret)
			for _, e := range flow.Outline() {
				outline = append(outline, fmt.Sprintf("%d:%s", e.Level, e.Text))
			}
			for _, b := range flow.Bookmarks() {
				bookmarks = append(bookmarks, b.Name)
			}
			for i, ln := range flow.Layout().Lines {
				if i < 8 {
					lineHeights = append(lineHeights, ln.H)
				}
				if ln.Marker != nil {
					markers = append(markers, ln.Marker.Text)
				}
				if ln.DropCap != nil {
					dropCaps++
				}
				for _, r := range ln.Runs {
					if r.Comment != "" && comment == nil {
						comment = map[string]any{"id": r.Comment, "x": ln.X + r.X + 4 + sv.X - sv.ScrollX, "y": ln.Y + 5 + sv.Y - sv.ScrollY}
					}
					if r.Link != "" && link == nil {
						link = map[string]any{"url": r.Link, "x": ln.X + r.X + 4 + sv.X - sv.ScrollX, "y": ln.Y + 5 + sv.Y - sv.ScrollY}
					}
				}
			}
		}
		if sig := fmt.Sprint(outline, bookmarks, current, state.Comments.Len()); (u.tab == "outline" || u.tab == "comments") && sig != u.navSig {
			u.navSig = sig
			renderUI()
		}
		b, _ := json.Marshal(map[string]any{
			"mode": state.Mode.String(), "features": names, "commands": len(stage.Features.Commands()),
			"elements": cgox.Elements(), "editing": editing, "cursors": cursors, "text": text,
			"pages": state.Pages, "format": u.format, "markers": markers, "dropCaps": dropCaps, "lineHeights": lineHeights,
			"link": link, "headers": headers, "watermarks": watermarks, "spread": spread, "hoverLink": u.hoverLink, "suggestions": suggestions, "role": state.Role.String(), "comments": state.Comments.Len(), "hoverComment": u.hoverComment, "scrollY": scrollY, "outline": outline, "bookmarks": bookmarks, "target": fmt.Sprintf("%T", stage.EditTarget()), "canPrompt": stage.Prompt != nil, "hasComment": stage.Features.Has(canvas.CmdCommentAdd), "comment": comment, "tab": u.tab, "menu": u.menu, "title": u.title,
			"fontLog": u.fontLog, "fontsAdded": fontKeys(lib),
		})
		doc.GetElementByID("state").SetTextContent(string(b))
	}

	renderCanvas()
	renderUI()
	go u.saver.Refresh(context.Background())
	canvas.LoadImage("data:image/svg+xml;base64,"+base64.StdEncoding.EncodeToString([]byte(svg)),
		func(src *canvas.HTMLImage) {
			state.Image = src
			renderCanvas()
		}, nil)
	select {}
}

// builtinCommands are the commands that are not registered by a feature.
func builtinCommands() []canvas.CommandInfo {
	return []canvas.CommandInfo{
		{Name: canvas.CmdBold, Title: "Bold", Feature: "format"},
		{Name: canvas.CmdItalic, Title: "Italic", Feature: "format"},
		{Name: canvas.CmdUnderline, Title: "Underline", Feature: "format"},
		{Name: canvas.CmdStrike, Title: "Strikethrough", Feature: "format"},
		{Name: canvas.CmdSuperscript, Title: "Superscript", Feature: "format"},
		{Name: canvas.CmdSubscript, Title: "Subscript", Feature: "format"},
		{Name: canvas.CmdSelectAll, Title: "Select all", Feature: "format"},
		{Name: canvas.CmdCopyFormat, Title: "Copy format", Feature: "format"},
		{Name: canvas.CmdPasteFormat, Title: "Paste format", Feature: "format"},
	}
}

func titleOf(cmds []canvas.CommandInfo, c canvas.Command) string {
	for _, ci := range cmds {
		if ci.Name == c {
			return ci.Title
		}
	}
	return string(c)
}

// resetBinding puts cmd's chords back to their defaults.
func resetBinding(stage *canvas.Stage, defaults canvas.Keymap, cmd canvas.Command) {
	stage.Rebind(cmd, "")
	for _, chord := range defaults.Chords(cmd) {
		stage.Keymap[chord] = cmd
	}
}

func tabButton(u *ui, tab, label string, rerender func()) gox.Node {
	class := "gj-keybindings-button"
	if u.tab == tab {
		class += " gj-symbolpicker-chip-on"
	}
	return gox.El("button", []gox.Attr{
		gox.A("type", "button"), gox.A("class", class), gox.A("data-tab", tab),
		gox.On("click", func(gox.Event) { u.tab = tab; rerender() }),
	}, gox.Text(label))
}

// fontKeys lists "family=key" for every downloaded cut and "family" for the
// added fonts with none yet.
func fontKeys(lib *fonts.Library) []string {
	out := []string{}
	for _, f := range lib.Added() {
		keys := []string{}
		for _, v := range f.Variants {
			if v.Key != "" {
				keys = append(keys, v.Key)
			}
		}
		out = append(out, f.Family+"="+strings.Join(keys, ","))
	}
	return out
}

// commentsPanel lists the comments with their text; an owner or editor can
// reply, resolve and delete, a viewer only reads.
func commentsPanel(u *ui, state *State, flow func() *canvas.TextFlow, redraw, refocus, renderUI func()) gox.Node {
	fl := flow()
	excerpts := map[string]string{}
	if fl != nil {
		excerpts = fl.CommentExcerpts()
	}
	set := state.Comments
	return components.CommentList(components.CommentListProps{
		Comments: set.List(), Excerpts: excerpts, ShowResolved: u.showResolved, Selected: u.selComment,
		CanEdit: state.Role.CanComment(), Drafts: u.drafts,
		OnSelect: func(id string) {
			u.selComment = id
			if fl != nil {
				fl.SelectComment(id)
			}
			redraw()
			renderUI()
		},
		OnResolve: func(id string) {
			set.Update(id, func(c *canvas.Comment) { c.Resolved = !c.Resolved })
			redraw()
			renderUI()
		},
		OnDelete: func(id string) {
			if fl != nil {
				fl.DeleteComment(id)
			} else {
				set.Delete(id)
			}
			redraw()
			renderUI()
		},
		OnReplyDraft: func(id, text string) { u.drafts[id] = text; renderUI() },
		OnReply: func(id, text string) {
			set.Update(id, func(c *canvas.Comment) {
				c.Replies = append(c.Replies, canvas.Reply{Author: state.Author, Text: text, Created: time.Now()})
			})
			delete(u.drafts, id)
			renderUI()
		},
		OnToggleShowResolved: func(show bool) { u.showResolved = show; renderUI() },
	})
}

// outlinePanel shows the headings (click to go there) and the bookmarks.
func outlinePanel(state *State, flow func() *canvas.TextFlow, scroller func() *canvas.ScrollView, redraw, refocus, renderUI func()) gox.Node {
	fl := flow()
	if fl == nil {
		return gox.Frag()
	}
	caret := fl.Cursors()[0].Caret
	goTo := func(offset int) {
		fl.GotoOffset(offset)
		scroller().RevealPending()
		redraw()
		renderUI()
	}
	runes := []rune(fl.EditText())
	excerpts := map[string]string{}
	for _, b := range fl.Bookmarks() {
		excerpts[b.Name] = string(runes[b.Start:min(b.End, len(runes))])
	}
	return gox.Frag(
		components.Outline(components.OutlineProps{
			Entries: fl.Outline(), Current: fl.OutlineIndexAt(caret),
			OnSelect: func(_ int, e canvas.OutlineEntry) { goTo(e.Start) },
		}),
		gox.El("div", []gox.Attr{gox.A("style", "margin-top:1em")}, components.BookmarkList(components.BookmarksProps{
			Bookmarks: fl.Bookmarks(), Excerpts: excerpts, CanEdit: state.Role.CanEdit(),
			OnGoto: func(name string) { fl.GotoBookmark(name); scroller().RevealPending(); redraw(); renderUI() },
			OnRemove: func(name string) {
				fl.RemoveBookmark(name)
				redraw()
				renderUI()
			},
		})),
	)
}
