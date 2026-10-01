package components

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/gox"
)

func TestMenuRendersItemsSubmenusAndRunsOnSelect(t *testing.T) {
	var picked []string
	closed := 0
	n := Menu(MenuProps{
		Label:   "Edit",
		OnClose: func() { closed++ },
		Items: []MenuItem{
			{Label: "Undo", Shortcut: "Ctrl+Z", OnSelect: func() { picked = append(picked, "undo") }},
			{Separator: true},
			{Label: "Bold", Checked: true, OnSelect: func() { picked = append(picked, "bold") }},
			{Label: "Locked", Disabled: true, OnSelect: func() { picked = append(picked, "locked") }},
			{Label: "Insert", Items: []MenuItem{
				{Label: "Symbol", OnSelect: func() { picked = append(picked, "symbol") }},
				{Label: "Deeper", Items: []MenuItem{{Label: "Leaf", OnSelect: func() { picked = append(picked, "leaf") }}}},
			}},
		},
	})
	html := render(t, n)
	for _, want := range []string{`role="menu"`, `aria-label="Edit"`, "gj-menu-shortcut", "Ctrl+Z", "gj-menu-separator", `aria-checked="true"`,
		`aria-disabled="true"`, "gj-menu-item-sub", `aria-haspopup="menu"`, "Leaf", "✓"} {
		if !strings.Contains(html, want) {
			t.Errorf("menu html lacks %q:\n%s", want, html)
		}
	}
	// 3 enabled leaf entries at the top level and below: Undo, Bold, Symbol, Leaf.
	hs := findHandlers(n, "click")
	if len(hs) != 4 {
		t.Fatalf("click handlers = %d, want 4 (disabled and submenu parents have none)", len(hs))
	}
	for _, h := range hs {
		h(gox.Event{})
	}
	if strings.Join(picked, ",") != "undo,bold,symbol,leaf" || closed != 4 {
		t.Errorf("picked %v, closed %d", picked, closed)
	}
}

func TestMenuBarOpensOneMenuAtATime(t *testing.T) {
	var opened []int
	entries := []MenuBarEntry{
		{Label: "File", Items: []MenuItem{{Label: "New", OnSelect: func() {}}}},
		{Label: "Edit", Items: []MenuItem{{Label: "Undo", OnSelect: func() {}}}},
	}
	closedBar := MenuBar(MenuBarProps{Entries: entries, Open: -1, OnOpen: func(i int) { opened = append(opened, i) }})
	html := render(t, closedBar)
	if strings.Contains(html, "gj-menubar-panel") || strings.Contains(html, "gj-menubar-backdrop") || !strings.Contains(html, `aria-expanded="false"`) {
		t.Errorf("nothing open: %s", html)
	}
	clicks := findHandlers(closedBar, "click")
	clicks[1](gox.Event{}) // click Edit
	if len(opened) != 1 || opened[0] != 1 {
		t.Fatalf("click opens: %v", opened)
	}
	if hs := findHandlers(closedBar, "mouseenter"); len(hs) != 2 {
		t.Fatal("mouseenter handlers")
	} else {
		hs[0](gox.Event{}) // nothing is open: hovering does not open
		if len(opened) != 1 {
			t.Errorf("hover with nothing open: %v", opened)
		}
	}

	opened = nil
	bar := MenuBar(MenuBarProps{Entries: entries, Open: 0, OnOpen: func(i int) { opened = append(opened, i) }})
	html = render(t, bar)
	if !strings.Contains(html, "gj-menubar-panel") || !strings.Contains(html, "New") || strings.Contains(html, "Undo") ||
		!strings.Contains(html, "gj-menubar-backdrop") || !strings.Contains(html, `aria-expanded="true"`) {
		t.Errorf("File open: %s", html)
	}
	hs := findHandlers(bar, "click") // backdrop, File, New (an item), Edit
	hs[0](gox.Event{})               // the backdrop closes
	hs[1](gox.Event{})               // File again toggles it closed
	hs[2](gox.Event{})               // choosing an item closes
	if len(opened) != 3 || opened[0] != -1 || opened[1] != -1 || opened[2] != -1 {
		t.Errorf("close calls = %v", opened)
	}
	enter := findHandlers(bar, "mouseenter")
	enter[1](gox.Event{}) // moving onto Edit while File is open switches
	if opened[len(opened)-1] != 1 {
		t.Errorf("hover switches: %v", opened)
	}
	enter[0](gox.Event{}) // onto the open entry: nothing
	if len(opened) != 4 {
		t.Errorf("hover on the open entry: %v", opened)
	}
}

func TestFormatChordAndCommandMenu(t *testing.T) {
	for chord, want := range map[string]string{
		"Mod+Alt+Shift+K": "Ctrl+Alt+Shift+K",
		"Alt+ArrowDown":   "Alt+↓",
		"Mod+Enter":       "Ctrl+Enter",
		"":                "",
	} {
		if got := FormatChord(chord, false); got != want {
			t.Errorf("FormatChord(%q) = %q, want %q", chord, got, want)
		}
	}
	if got := FormatChord("Mod+Alt+Shift+K", true); got != "⌘⌥⇧K" {
		t.Errorf("mac chord = %q", got)
	}
	st := canvas.NewStage(nil, 1, 1)
	var ran []canvas.Command
	items := CommandMenu(st.Features.Commands(), st.EffectiveKeymap(), false, func(c canvas.Command) { ran = append(ran, c) })
	var line *MenuItem
	for i, it := range items {
		if it.Label == "Line ops" {
			line = &items[i]
		}
	}
	if line == nil || len(line.Items) != 5 {
		t.Fatalf("one submenu per feature: %+v", items)
	}
	var up MenuItem
	for _, it := range line.Items {
		if it.Label == "Move line up" {
			up = it
		}
	}
	if up.Shortcut != "Alt+↑" {
		t.Errorf("shortcut = %q", up.Shortcut)
	}
	up.OnSelect()
	if len(ran) != 1 || ran[0] != canvas.CmdMoveLineUp {
		t.Errorf("ran = %v", ran)
	}
	if featureTitle("") != "Other" || featureTitle("markdown-clipboard") != "Markdown clipboard" {
		t.Error("featureTitle")
	}
}

func TestSymbolPicker(t *testing.T) {
	all := DefaultSymbols()
	if len(all) < 150 || len(SymbolCategories(all)) < 6 {
		t.Fatalf("%d symbols, %v", len(all), SymbolCategories(all))
	}
	if got := FilterSymbols(all, "greek", ""); len(got) == 0 {
		t.Error("category words match")
	}
	got := FilterSymbols(all, "left arrow", "")
	if len(got) == 0 || got[0].Char != "←" {
		t.Errorf("name search: %+v", got)
	}
	if got := FilterSymbols(all, "", "Currency"); len(got) != 10 {
		t.Errorf("category filter: %d", len(got))
	}
	if got := FilterSymbols(all, "€", ""); len(got) != 1 {
		t.Errorf("search by the character: %+v", got)
	}
	if got := FilterSymbols(all, "zzzz", ""); len(got) != 0 {
		t.Error("no match")
	}

	var picked, queries, cats []string
	n := SymbolPicker(SymbolPickerProps{
		Recent: []string{"★"}, Columns: 8,
		OnPick:     func(c string) { picked = append(picked, c) },
		OnQuery:    func(q string) { queries = append(queries, q) },
		OnCategory: func(c string) { cats = append(cats, c) },
	})
	html := render(t, n)
	if !strings.Contains(html, "gj-symbolpicker-recent") || !strings.Contains(html, "repeat(8, 1fr)") || !strings.Contains(html, `title="euro"`) {
		t.Errorf("picker html: %.300s", html)
	}
	fire(t, n, "data-category", "Math", "click", nil)
	fire(t, n, "class", "gj-symbolpicker-search", "input", "pi")
	fire(t, n, "data-char", "€", "click", nil)
	if strings.Join(picked, "") != "€" || strings.Join(queries, "") != "pi" || strings.Join(cats, "") != "Math" {
		t.Errorf("picked %v queries %v cats %v", picked, queries, cats)
	}
	html = render(t, SymbolPicker(SymbolPickerProps{Query: "pi", Category: "Math"}))
	if !strings.Contains(html, "gj-symbolpicker-chip-on") || strings.Contains(html, "gj-symbolpicker-recent") || strings.Contains(html, "€") {
		t.Errorf("filtered: %.300s", html)
	}
	if html := render(t, SymbolPicker(SymbolPickerProps{Query: "zzzz"})); !strings.Contains(html, "No symbols match") {
		t.Error("empty state")
	}
	if html := render(t, SymbolPicker(SymbolPickerProps{Symbols: []Symbol{{"x", "ex", "Latin"}}})); !strings.Contains(html, "ex") {
		t.Error("custom symbols")
	}
}

func TestLinkPreview(t *testing.T) {
	if render(t, LinkPreview(LinkPreviewProps{})) != "" {
		t.Error("no URL, no card")
	}
	html := render(t, LinkPreview(LinkPreviewProps{URL: "https://www.example.com/a/b"}))
	if !strings.Contains(html, `href="https://www.example.com/a/b"`) || !strings.Contains(html, ">example.com<") || !strings.Contains(html, `target="_blank"`) {
		t.Errorf("host fallback: %s", html)
	}
	opened := 0
	closed := 0
	n := LinkPreview(LinkPreviewProps{
		URL: "https://x.y/", Title: "Title", Description: "About it", Image: "https://x.y/i.png", SiteName: "X", OnOpen: func() { opened++ }, OnClose: func() { closed++ },
	})
	html = render(t, n)
	for _, want := range []string{"Title", "About it", "https://x.y/i.png", ">X<", "gj-linkpreview-close"} {
		if !strings.Contains(html, want) {
			t.Errorf("lacks %q: %s", want, html)
		}
	}
	findHandlers(n, "click")[0](gox.NewEvent("click", nil, false, func() {}, nil))
	findHandlers(n, "click")[1](gox.Event{})
	if opened != 1 || closed != 1 {
		t.Errorf("opened %d closed %d", opened, closed)
	}
	html = render(t, LinkPreview(LinkPreviewProps{URL: "https://x.y/", Description: "hidden", Loading: true}))
	if !strings.Contains(html, "gj-linkpreview-loading") || strings.Contains(html, "hidden") {
		t.Errorf("loading: %s", html)
	}
	if LinkHost("not a url") != "not a url" || LinkHost("https://www.a.b/c") != "a.b" {
		t.Error("LinkHost")
	}
}

func TestKeyBindings(t *testing.T) {
	cmds := []canvas.CommandInfo{
		{Name: canvas.CmdMoveLineUp, Title: "Move line up", Feature: "line-ops"},
		{Name: canvas.CmdAddNextMatch, Title: "Add selection to next match", Feature: "multi-cursor"},
		{Name: "x.none", Title: "No keys", Feature: "other"},
	}
	defaults := canvas.Keymap{"Alt+ArrowUp": canvas.CmdMoveLineUp, "Mod+D": canvas.CmdAddNextMatch}
	current := canvas.Keymap{"Mod+J": canvas.CmdMoveLineUp, "Mod+D": canvas.CmdAddNextMatch}
	var recorded []canvas.Command
	var bound []string
	var cleared, reset []canvas.Command
	all := 0
	props := KeyBindingsProps{
		Commands: cmds, Keymap: current, Defaults: defaults,
		OnRecord:   func(c canvas.Command) { recorded = append(recorded, c) },
		OnBind:     func(c canvas.Command, chord string) { bound = append(bound, string(c)+"="+chord) },
		OnClear:    func(c canvas.Command) { cleared = append(cleared, c) },
		OnReset:    func(c canvas.Command) { reset = append(reset, c) },
		OnResetAll: func() { all++ },
	}
	n := KeyBindings(props)
	html := render(t, n)
	for _, want := range []string{"Ctrl+J", "Ctrl+D", "No keys", "gj-keybindings-row-modified", "—", "Move line up", "Line ops"} {
		if !strings.Contains(html, want) {
			t.Errorf("lacks %q: %s", want, html)
		}
	}
	if strings.Count(html, "gj-keybindings-row-modified") != 1 {
		t.Error("only the changed binding is marked modified")
	}
	fire(t, n, "data-action", "change:"+string(canvas.CmdMoveLineUp), "click", nil)
	fire(t, n, "data-action", "clear:"+string(canvas.CmdMoveLineUp), "click", nil)
	fire(t, n, "data-action", "reset:"+string(canvas.CmdMoveLineUp), "click", nil)
	fire(t, n, "data-action", "reset-all", "click", nil)
	if len(recorded) != 1 || len(cleared) != 1 || len(reset) != 1 || all != 1 {
		t.Errorf("buttons: %v %v %v %d", recorded, cleared, reset, all)
	}
	if _, ok := byAttr(n, "data-action", "reset:"+string(canvas.CmdAddNextMatch)); ok {
		t.Error("an unmodified command has no Reset")
	}
	if _, ok := byAttr(n, "data-action", "clear:x.none"); ok {
		t.Error("nothing to clear on an unbound command")
	}

	// Search filters rows.
	props.Query = "ctrl+d"
	html = render(t, KeyBindings(props))
	if strings.Contains(html, "Move line up") || !strings.Contains(html, "Add selection") {
		t.Errorf("filtered by key: %s", html)
	}
	props.Query = ""

	// Recording: the capture box takes the next chord.
	props.Recording = canvas.CmdMoveLineUp
	props.Message = "Ctrl+Shift+Y was used by X"
	n = KeyBindings(props)
	html = render(t, n)
	if !strings.Contains(html, "gj-keybindings-capture") || !strings.Contains(html, `autofocus="true"`) || !strings.Contains(html, "was used by X") {
		t.Errorf("recording: %s", html)
	}
	capture, _ := byAttr(n, "data-capture", string(canvas.CmdMoveLineUp))
	keydown := func(k gox.KeyData) {
		for _, a := range capture.Attrs {
			if a.Name == "keydown" {
				a.Handler(gox.NewEvent("keydown", k, false, func() {}, nil))
			}
		}
	}
	recorded = nil
	keydown(gox.KeyData{Key: "Shift", Shift: true})                         // modifier alone: ignored
	keydown(gox.KeyData{Key: "y", Code: "KeyY", Ctrl: true, Shift: true})   // a chord
	keydown(gox.KeyData{Key: "*", Code: "Digit8", Ctrl: true, Shift: true}) // Shift+8 is "*": read as 8
	keydown(gox.KeyData{Key: "x"})                                          // no modifier: ignored
	keydown(gox.KeyData{Key: "Escape"})
	if strings.Join(bound, ",") != "line.move-up=Mod+Shift+Y,line.move-up=Mod+Shift+8" || len(recorded) != 1 || recorded[0] != "" {
		t.Errorf("bound %v recorded %v", bound, recorded)
	}
	for _, a := range capture.Attrs {
		if a.Name == "blur" {
			a.Handler(gox.Event{})
		}
	}
	if len(recorded) != 2 {
		t.Error("losing focus cancels recording")
	}
	if k, ok := ChordFromKey(gox.KeyData{Key: " ", Code: "Space", Alt: true}); !ok || k != "Alt+Space" {
		t.Errorf("space = %q", k)
	}
}

func TestDocumentView(t *testing.T) {
	side := gox.Text("panel")
	titles := []string{}
	n := DocumentView(DocumentViewProps{
		Title: "Report", OnTitle: func(s string) { titles = append(titles, s) },
		MenuBar: gox.Text("MENU"), Toolbar: gox.Text("TOOLS"), Canvas: gox.Text("CANVAS"), Sidebar: &side, Status: "Page 1 of 2",
	})
	html := render(t, n)
	for _, want := range []string{"gj-docview", "gj-docview-top", `value="Report"`, "MENU", "TOOLS", "CANVAS", "gj-docview-sidebar", "panel", "Page 1 of 2", `role="toolbar"`} {
		if !strings.Contains(html, want) {
			t.Errorf("lacks %q: %s", want, html)
		}
	}
	fire(t, n, "aria-label", "Document title", "change", "New name")
	if len(titles) != 1 || titles[0] != "New name" {
		t.Errorf("titles = %v", titles)
	}
	html = render(t, DocumentView(DocumentViewProps{Title: "Plain", Canvas: gox.Text("c")}))
	if strings.Contains(html, "<input") || strings.Contains(html, "sidebar") || !strings.Contains(html, ">Plain<") {
		t.Errorf("minimal: %s", html)
	}
}
