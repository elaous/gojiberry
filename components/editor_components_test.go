package components

import (
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
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
		h(xango.Event{})
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
	clicks[1](xango.Event{}) // click Edit
	if len(opened) != 1 || opened[0] != 1 {
		t.Fatalf("click opens: %v", opened)
	}
	if hs := findHandlers(closedBar, "mouseenter"); len(hs) != 2 {
		t.Fatal("mouseenter handlers")
	} else {
		hs[0](xango.Event{}) // nothing is open: hovering does not open
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
	hs[0](xango.Event{})             // the backdrop closes
	hs[1](xango.Event{})             // File again toggles it closed
	hs[2](xango.Event{})             // choosing an item closes
	if len(opened) != 3 || opened[0] != -1 || opened[1] != -1 || opened[2] != -1 {
		t.Errorf("close calls = %v", opened)
	}
	enter := findHandlers(bar, "mouseenter")
	enter[1](xango.Event{}) // moving onto Edit while File is open switches
	if opened[len(opened)-1] != 1 {
		t.Errorf("hover switches: %v", opened)
	}
	enter[0](xango.Event{}) // onto the open entry: nothing
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
	findHandlers(n, "click")[0](xango.NewEvent("click", nil, false, func() {}, nil))
	findHandlers(n, "click")[1](xango.Event{})
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
	keydown := func(k xango.KeyData) {
		for _, a := range capture.Attrs {
			if a.Name == "keydown" {
				a.Handler(xango.NewEvent("keydown", k, false, func() {}, nil))
			}
		}
	}
	recorded = nil
	keydown(xango.KeyData{Key: "Shift", Shift: true})                         // modifier alone: ignored
	keydown(xango.KeyData{Key: "y", Code: "KeyY", Ctrl: true, Shift: true})   // a chord
	keydown(xango.KeyData{Key: "*", Code: "Digit8", Ctrl: true, Shift: true}) // Shift+8 is "*": read as 8
	keydown(xango.KeyData{Key: "x"})                                          // no modifier: ignored
	keydown(xango.KeyData{Key: "Escape"})
	if strings.Join(bound, ",") != "line.move-up=Mod+Shift+Y,line.move-up=Mod+Shift+8" || len(recorded) != 1 || recorded[0] != "" {
		t.Errorf("bound %v recorded %v", bound, recorded)
	}
	for _, a := range capture.Attrs {
		if a.Name == "blur" {
			a.Handler(xango.Event{})
		}
	}
	if len(recorded) != 2 {
		t.Error("losing focus cancels recording")
	}
	if k, ok := ChordFromKey(xango.KeyData{Key: " ", Code: "Space", Alt: true}); !ok || k != "Alt+Space" {
		t.Errorf("space = %q", k)
	}
}

func TestDocumentView(t *testing.T) {
	side := xango.Text("panel")
	titles := []string{}
	n := DocumentView(DocumentViewProps{
		Title: "Report", OnTitle: func(s string) { titles = append(titles, s) },
		MenuBar: xango.Text("MENU"), Toolbar: xango.Text("TOOLS"), Canvas: xango.Text("CANVAS"), Sidebar: &side, Status: "Page 1 of 2",
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
	html = render(t, DocumentView(DocumentViewProps{Title: "Plain", Canvas: xango.Text("c")}))
	if strings.Contains(html, "<input") || strings.Contains(html, "sidebar") || !strings.Contains(html, ">Plain<") {
		t.Errorf("minimal: %s", html)
	}
}

func TestStylePicker(t *testing.T) {
	var picked []string
	n := StylePicker(StylePickerProps{
		Styles:  canvas.DefaultStyles(),
		Current: "Heading 2",
		OnPick: func(name string, paragraph bool) {
			picked = append(picked, name+map[bool]string{true: "/p", false: "/c"}[paragraph])
		},
	})
	html := render(t, n)
	for _, want := range []string{"Paragraph styles", "Character styles", `data-style="Heading 1"`, `data-style="Emphasis"`, "gj-stylepicker-item-on", "italic"} {
		if !strings.Contains(html, want) {
			t.Errorf("lacks %q: %.300s", want, html)
		}
	}
	if strings.Count(html, "gj-stylepicker-item-on") != 1 {
		t.Error("only the current style is marked")
	}
	if !strings.Contains(html, "font-size:32px") && !strings.Contains(html, "font-size: 32px") {
		t.Errorf("a heading is previewed larger: %.400s", html)
	}
	fire(t, n, "data-style", "Heading 1", "click", nil)
	fire(t, n, "data-style", "Strong", "click", nil)
	if strings.Join(picked, ",") != "Heading 1/p,Strong/c" {
		t.Errorf("picked %v", picked)
	}
	html = render(t, StylePicker(StylePickerProps{Styles: []canvas.StyleDef{{Name: "Only", Paragraph: true}}}))
	if strings.Contains(html, "Character styles") || !strings.Contains(html, "Only") {
		t.Errorf("empty sections are left out: %s", html)
	}
}

func TestSaverRendersFormAndWiresCallbacks(t *testing.T) {
	var log []string
	n := Saver(SaverProps{
		Targets: []SaverChoice{{"pg", "Postgres"}, {"sqlite", "SQLite"}, {"s3", "S3"}},
		Formats: []SaverChoice{{"json", "JSON"}, {"proto", "Protobuf (binary)"}},
		Target:  "sqlite", Format: "proto", Name: "notes",
		Entries: []SaverEntry{{Key: "notes.json", Name: "notes", Format: "JSON", Size: 2500, Updated: time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)},
			{Key: "big.pb", Name: "big", Format: "Protobuf", Size: 3 << 20}, {Key: "tiny.pb", Name: "tiny", Format: "Protobuf", Size: 12}},
		Status:    "Saved notes.json",
		OnTarget:  func(s string) { log = append(log, "target:"+s) },
		OnFormat:  func(s string) { log = append(log, "format:"+s) },
		OnName:    func(s string) { log = append(log, "name:"+s) },
		OnSave:    func() { log = append(log, "save") },
		OnRefresh: func() { log = append(log, "refresh") },
		OnOpen:    func(k string) { log = append(log, "open:"+k) },
		OnDelete:  func(k string) { log = append(log, "delete:"+k) },
	})
	html := render(t, n)
	mustContain(t, html, "Postgres", "S3", `value="sqlite" selected="selected"`, `value="proto" selected="selected"`, `value="notes"`,
		"Saved notes.json", `role="status"`, "2.4 KB", "3.0 MB", "12 B", "2026-01-02 03:04", `data-key="notes.json"`)
	mustNotContain(t, html, "gj-saver-failed", "Nothing saved yet")
	for _, h := range findHandlers(n, "change") {
		h(xango.Event{Value: "pick"})
	}
	findHandlers(n, "input")[0](xango.Event{Value: "typed"})
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	want := "target:pick format:pick name:typed save refresh open:notes.json delete:notes.json open:big.pb delete:big.pb open:tiny.pb delete:tiny.pb"
	if got := strings.Join(log, " "); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestSaverEmptyBusyAndFailed(t *testing.T) {
	html := render(t, Saver(SaverProps{Busy: true, Status: "boom", Failed: true, Entries: nil,
		OnSave: func() {}, OnOpen: func(string) {}}))
	mustContain(t, html, "gj-saver-failed", "Nothing saved yet", `disabled="disabled"`)
	// No name: Save is disabled even when not busy; no handlers: all inert.
	n := Saver(SaverProps{OnSave: func() {}, Entries: []SaverEntry{{Key: "a.json", Name: "a", Format: "JSON"}}})
	if got := len(findHandlers(n, "click")); got != 0 {
		t.Fatalf("%d click handlers; Save needs a name and the rest have no callbacks", got)
	}
}

func TestTransferButtons(t *testing.T) {
	var got []string
	n := Transfer(TransferProps{
		Imports:  []SaverChoice{{"markdown", "Markdown"}, {"json", "JSON"}},
		Exports:  []SaverChoice{{"markdown", "Markdown"}},
		OnImport: func(f string) { got = append(got, "in:"+f) },
		OnExport: func(f string) { got = append(got, "out:"+f) },
	})
	mustContain(t, render(t, n), `aria-label="Import"`, `aria-label="Export"`, `data-kind="export"`, "Markdown")
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	if strings.Join(got, " ") != "in:markdown in:json out:markdown" {
		t.Fatal(got)
	}
	off := Transfer(TransferProps{Imports: []SaverChoice{{"a", "A"}}, OnImport: func(string) {}, Disabled: true})
	if len(findHandlers(off, "click")) != 0 {
		t.Fatal("disabled has no handlers")
	}
	mustContain(t, render(t, off), `disabled="disabled"`)
	if len(findHandlers(Transfer(TransferProps{Exports: []SaverChoice{{"a", "A"}}}), "click")) != 0 {
		t.Fatal("no callback, no handler")
	}
}

func TestMarkdownTextRendersAndEscapes(t *testing.T) {
	html := render(t, MarkdownText("## Title\n\n**bold** and *it* and _under_ ~~old~~ `code` [site](https://x.test/a?b=1)\nsecond line\n\n- one\n- two\n\n1. first\n\n> quoted <b>raw</b>\n\n[bad](javascript:alert(1)) hi @ana, ask ana@example.com <script>x</script>"))
	mustContain(t, html, `class="gj-md-heading"`, "<strong>bold</strong>", "<em>it</em>", "<em>under</em>", "<s>old</s>", "<code>code</code>",
		`href="https://x.test/a?b=1"`, "<br>", "<ul><li>one</li><li>two</li></ul>", "<ol><li>first</li></ol>", "<blockquote>quoted &lt;b&gt;raw&lt;/b&gt;</blockquote>",
		`class="gj-mention" data-mention="ana"`, `href="mailto:ana@example.com"`, "gj-mention-email", "&lt;script&gt;")
	mustNotContain(t, html, "javascript:", "<script>", "<b>raw")
	mustContain(t, html, "<p>bad") // an unsafe link keeps its text only
}

func TestCommentCardAndList(t *testing.T) {
	var log []string
	c := canvas.Comment{ID: "c1", Author: "ana", Text: "Please check **this**, @bob", Created: time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC),
		Replies: []canvas.Reply{{Author: "bob", Text: "on it"}, {Text: "x"}}}
	card := CommentCard(CommentCardProps{Comment: c, Excerpt: "the text", Selected: true, CanEdit: true, ReplyDraft: "ok",
		OnSelect: func(id string) { log = append(log, "select:"+id) }, OnResolve: func(id string) { log = append(log, "resolve:"+id) },
		OnDelete: func(id string) { log = append(log, "delete:"+id) }, OnReplyDraft: func(id, s string) { log = append(log, "draft:"+id+":"+s) },
		OnReply: func(id, s string) { log = append(log, "reply:"+id+":"+s) }})
	mustContain(t, render(t, card), "gj-comment-selected", `data-comment="c1"`, "ana", "2026-03-04 05:06", "the text", "<strong>this</strong>", `data-mention="bob"`,
		"gj-comment-replies", "Someone", ">Resolve<", `value="ok"`)
	findHandlers(card, "input")[0](xango.Event{Value: "typed"})
	for _, h := range findHandlers(card, "click") {
		h(xango.Event{})
	}
	if got := strings.Join(log, " "); got != "draft:c1:typed select:c1 reply:c1:ok resolve:c1 delete:c1" {
		t.Fatal(got)
	}
	// A viewer sees the comment but has no controls; resolved shows a badge and Reopen.
	view := render(t, CommentCard(CommentCardProps{Comment: canvas.Comment{ID: "c2", Resolved: true, Text: "t"}}))
	mustContain(t, view, "gj-comment-resolved", "Resolved", "Someone")
	mustNotContain(t, view, "Reply", "Delete")
	mustContain(t, render(t, CommentCard(CommentCardProps{Comment: canvas.Comment{ID: "c3", Resolved: true}, CanEdit: true})), ">Reopen<", "disabled")

	var shown []bool
	list := CommentList(CommentListProps{
		Comments: []canvas.Comment{c, {ID: "c2", Resolved: true, Text: "done"}}, Excerpts: map[string]string{"c1": "quoted"},
		Selected: "c1", CanEdit: true, Drafts: map[string]string{"c1": "d"}, OnToggleShowResolved: func(b bool) { shown = append(shown, b) }})
	html := render(t, list)
	mustContain(t, html, "Show resolved (1)", `aria-pressed="false"`, "quoted")
	mustNotContain(t, html, `data-comment="c2"`)
	findHandlers(list, "click")[0](xango.Event{})
	if len(shown) != 1 || !shown[0] {
		t.Fatal(shown)
	}
	all := render(t, CommentList(CommentListProps{Comments: []canvas.Comment{c, {ID: "c2", Resolved: true}}, ShowResolved: true}))
	mustContain(t, all, `data-comment="c2"`, `aria-pressed="true"`)
	mustContain(t, render(t, CommentList(CommentListProps{})), "No comments.", "Show resolved<")
}

func TestOutlineAndBookmarkComponents(t *testing.T) {
	var picked []string
	entries := []canvas.OutlineEntry{{Level: 1, Text: "Intro", Start: 0}, {Level: 3, Text: "Deep <b>", Start: 10}}
	n := Outline(OutlineProps{Entries: entries, Current: 1, OnSelect: func(i int, e canvas.OutlineEntry) { picked = append(picked, e.Text+itoa(i)) }})
	html := render(t, n)
	mustContain(t, html, `aria-label="Outline"`, `data-level="1"`, `data-level="3"`, "padding-left:8px", "padding-left:36px", `aria-current="location"`, "gj-outline-item-on", "Deep &lt;b&gt;")
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	if len(picked) != 2 || picked[1] != "Deep <b>1" {
		t.Fatal(picked)
	}
	mustContain(t, render(t, Outline(OutlineProps{Current: -1})), "No headings yet")
	mustNotContain(t, render(t, Outline(OutlineProps{Entries: entries[:1], Current: -1})), "aria-current")

	var log []string
	bl := BookmarkList(BookmarksProps{
		Bookmarks: []canvas.Bookmark{{Name: "intro", Start: 0, End: 5}}, Excerpts: map[string]string{"intro": "Hello"}, CanEdit: true,
		OnGoto: func(s string) { log = append(log, "go:"+s) }, OnRemove: func(s string) { log = append(log, "rm:"+s) }})
	mustContain(t, render(t, bl), `data-bookmark="intro"`, "Hello", "#intro", `data-remove="intro"`)
	for _, h := range findHandlers(bl, "click") {
		h(xango.Event{})
	}
	if strings.Join(log, " ") != "go:intro rm:intro" {
		t.Fatal(log)
	}
	view := BookmarkList(BookmarksProps{Bookmarks: []canvas.Bookmark{{Name: "a"}}})
	mustNotContain(t, render(t, view), "Remove")
	mustContain(t, render(t, BookmarkList(BookmarksProps{CanEdit: true, Bookmarks: []canvas.Bookmark{{Name: "a"}}})), "disabled")
	mustContain(t, render(t, BookmarkList(BookmarksProps{})), "No bookmarks")
}

func TestFootnoteListComponent(t *testing.T) {
	var log []string
	entries := []canvas.FootnoteEntry{{N: 1, ID: "n1", Text: "See **this**"}, {N: 2, ID: "n2", Text: "plain"}}
	n := FootnoteList(FootnoteListProps{Entries: entries, CanEdit: true,
		OnGoto: func(e canvas.FootnoteEntry) { log = append(log, "go:"+e.ID) }, OnEdit: func(e canvas.FootnoteEntry) { log = append(log, "edit:"+e.ID) },
		OnDelete: func(e canvas.FootnoteEntry) { log = append(log, "del:"+e.ID) }})
	mustContain(t, render(t, n), `data-note="n1"`, "<strong>this</strong>", ">2<", "gj-footnote-edit")
	for _, h := range findHandlers(n, "click") {
		h(xango.Event{})
	}
	if got := strings.Join(log, " "); got != "go:n1 edit:n1 del:n1 go:n2 edit:n2 del:n2" {
		t.Fatal(got)
	}
	view := FootnoteList(FootnoteListProps{Entries: entries})
	mustNotContain(t, render(t, view), "Edit", "Delete")
	mustContain(t, render(t, FootnoteList(FootnoteListProps{Entries: entries[:1], CanEdit: true})), "disabled")
	mustContain(t, render(t, FootnoteList(FootnoteListProps{})), "No footnotes")
}

func TestPresenceComponent(t *testing.T) {
	html := render(t, Presence(PresenceProps{
		Self:   PresencePeer{Name: "Ana Souza", Color: "#f00", Role: "owner"},
		Peers:  []PresencePeer{{Name: "bob@example.com", Role: "editor"}, {User: "vee@x.org"}, {}},
		Status: "online", Pending: 0, Role: "viewer",
	}))
	mustContain(t, html, "gj-presence-self", ">AS<", ">BE<", ">VX<", ">?<", `title="Ana Souza (owner)"`, `title="vee@x.org"`, "background:#f00", "background:#7c5cff",
		"gj-presence-online", ">Online<", "View only", `aria-label="People here"`)
	off := render(t, Presence(PresenceProps{Self: PresencePeer{Name: "x"}, Status: "offline", Pending: 3}))
	mustContain(t, off, "Offline · 3 waiting to send", "gj-presence-offline")
	mustNotContain(t, off, "View only")
	mustContain(t, render(t, Presence(PresenceProps{Self: PresencePeer{Name: "x"}, Status: "connecting"})), "Connecting…")
	mustContain(t, render(t, Presence(PresenceProps{Self: PresencePeer{Name: "x"}, Status: "odd"})), ">odd<")
	mustNotContain(t, render(t, Presence(PresenceProps{Self: PresencePeer{Name: "x"}})), "gj-presence-status")
}

func TestCommentCardReplyWithoutEditing(t *testing.T) {
	c := canvas.Comment{ID: "c1", Text: "hi"}
	html := render(t, CommentCard(CommentCardProps{Comment: c, CanReply: true}))
	mustContain(t, html, "gj-comment-reply-row")
	mustNotContain(t, html, "gj-comment-actions", "Delete")
	list := render(t, CommentList(CommentListProps{Comments: []canvas.Comment{c}, CanReply: true}))
	mustContain(t, list, "gj-comment-reply-row")
	mustNotContain(t, list, "Resolve")
}

func TestFindReplaceShowsCountOptionsAndReplaceRow(t *testing.T) {
	html := render(t, FindReplace(FindReplaceProps{Query: "cat", Count: 4, Current: 2, WholeWord: true}))
	for _, want := range []string{`class="gj-find"`, `value="cat"`, `2 of 4`, `gj-find-toggle gj-find-word gj-find-on`, `aria-pressed="true"`, `gj-find-replace-all`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	if strings.Contains(html, "gj-find-case gj-find-on") {
		t.Error("match case is off")
	}
	none := render(t, FindReplace(FindReplaceProps{Query: "zzz", ReadOnly: true}))
	if !strings.Contains(none, "No matches") || strings.Contains(none, "gj-find-replace-all") || !strings.Contains(none, `disabled="disabled"`) {
		t.Errorf("no matches / read only:\n%s", none)
	}
	bad := render(t, FindReplace(FindReplaceProps{Query: "(", Regex: true, Error: "missing closing )"}))
	if !strings.Contains(bad, "gj-find-error") || !strings.Contains(bad, "missing closing )") {
		t.Errorf("error:\n%s", bad)
	}
}

func TestSpellPanelListsWordsSuggestionsAndActions(t *testing.T) {
	html := render(t, SpellPanel(SpellPanelProps{Enabled: true, Language: "English", Detected: "Español", Items: []SpellItem{
		{Word: "teh", Count: 3, Suggestions: []string{"the", "tech"}},
		{Word: "xyzzy"},
	}}))
	for _, want := range []string{`checked="checked"`, `data-word="teh"`, `×3`, `gj-spell-suggestion`, `>the<`, `No suggestions`, `gj-spell-add`, `the text looks like Español`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	ro := render(t, SpellPanel(SpellPanelProps{Enabled: true, Language: "English", ReadOnly: true, Items: []SpellItem{{Word: "teh", Suggestions: []string{"the"}}}}))
	if strings.Contains(ro, "gj-spell-add") || strings.Contains(ro, `class="gj-spell-suggestion"`) || !strings.Contains(ro, "gj-spell-ignore") {
		t.Errorf("read-only:\n%s", ro)
	}
	clean := render(t, SpellPanel(SpellPanelProps{Enabled: true, Language: "English"}))
	off := render(t, SpellPanel(SpellPanelProps{}))
	if !strings.Contains(clean, "No misspelled words") || strings.Contains(off, "Dictionary") || strings.Contains(off, "checked=") {
		t.Errorf("clean:\n%s\noff:\n%s", clean, off)
	}
}
