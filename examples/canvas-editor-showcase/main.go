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

	"github.com/elaous/gojiberry/canvas"
	cgox "github.com/elaous/gojiberry/canvas/gox"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/domrt/browser"
	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/gojiberry/gox"
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

	hoverLink string
}

func main() {
	doc := browser.Document()
	state := State{Mode: canvas.ModeView, Pages: 3, Source: sample, Note: "Floating note: click to edit, try (brackets and \"quotes\"."}
	u := &ui{title: "Quarterly report", menu: -1, tab: "format"}
	repo := fonts.Multi(fonts.WebSafe())
	u.catalog, _ = repo.Fonts(context.Background())

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
	refocus := func() { inputEl.Call("focus", map[string]any{"preventScroll": true}) }

	cb := Callbacks{
		OnHoverLink: func(url string) { u.hoverLink = url; renderUI() },
	}
	renderCanvas := func() {
		if err := root.Render(Doc(state, cb)); err != nil {
			doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
			return
		}
		flow = root.Find(cgox.TagTextFlow, 0).(*canvas.TextFlow)
		stage.Draw()
	}

	setMode := func(m canvas.Mode) {
		state.Mode = m
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
			{Label: "Edit", Items: components.CommandMenu(stage.Features.Commands(), keymap, mac, run)},
			{Label: "Insert", Items: []components.MenuItem{
				{Label: "Symbol…", OnSelect: func() { u.tab = "symbols"; renderUI() }},
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
			tabButton(u, "format", "Format", renderUI), tabButton(u, "symbols", "Symbols", renderUI), tabButton(u, "keys", "Keys", renderUI),
		)

		var panel gox.Node
		switch u.tab {
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
						u.open = false
						u.recent = append([]string{f.Family}, slices.DeleteFunc(slices.Clone(u.recent), func(s string) bool { return s == f.Family })...)
						renderUI()
					},
				},
			})
		}
		side := gox.Frag(panel,
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
		var link map[string]any
		headers, watermarks := []string{}, []string{}
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
			text = flow.EditText()
			sv := root.Find(cgox.TagScrollView, 0).(*canvas.ScrollView)
			for _, ln := range flow.Layout().Lines {
				if ln.Marker != nil {
					markers = append(markers, ln.Marker.Text)
				}
				if ln.DropCap != nil {
					dropCaps++
				}
				for _, r := range ln.Runs {
					if r.Link != "" && link == nil {
						link = map[string]any{"url": r.Link, "x": ln.X + r.X + 4 + sv.X - sv.ScrollX, "y": ln.Y + 5 + sv.Y - sv.ScrollY}
					}
				}
			}
		}
		b, _ := json.Marshal(map[string]any{
			"mode": state.Mode.String(), "features": names, "commands": len(stage.Features.Commands()),
			"elements": cgox.Elements(), "editing": editing, "cursors": cursors, "text": text,
			"pages": state.Pages, "format": u.format, "markers": markers, "dropCaps": dropCaps,
			"link": link, "headers": headers, "watermarks": watermarks, "spread": spread, "hoverLink": u.hoverLink, "tab": u.tab, "menu": u.menu, "title": u.title,
		})
		doc.GetElementByID("state").SetTextContent(string(b))
	}

	renderCanvas()
	renderUI()
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
