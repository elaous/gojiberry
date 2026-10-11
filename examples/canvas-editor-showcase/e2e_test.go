//go:build !(js && wasm)

package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/internal/e2etest"
)

// TestEndToEnd drives the showcase: the xango tree renders with every element
// registered, the features are registered, the DOM command list and style editor
// are there, and in edit mode the text flow is editable (typing, multi-cursor,
// line operations, page breaks, Markdown copy/paste), with the panels, the saver,
// transfer and roles.
func TestEndToEnd(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "")
	ok := d.OK
	S := d.State
	jsonIs := func(path string, want any) bool {
		b, _ := json.Marshal(want)
		return d.Is("JSON.stringify(s." + path + ") === " + e2etest.J(string(b)))
	}
	// scrollTop puts the document back at the top.
	scrollTop := func(ms int) {
		d.CanvasMove(450, 300)
		d.Wheel(0, -5000)
		d.Sleep(ms)
	}
	body := func() { d.CanvasClick(180+60, 24+60+80) } // a line of the text
	s := S()
	ok(s.Str("error") == "", "renders without error: "+s.Str("error"))
	ok(s.Str("mode") == "view" && s.Int("pages") == 3, "starts in view mode with three pages")
	ok(jsonIs("features", []string{"history", "multi-cursor", "line-ops", "markdown-clipboard", "breaks", "paragraphs", "text-case", "links", "styles", "comments", "bookmarks", "notes", "page-furniture", "find", "complete", "review", "columns"}), fmt.Sprint("features registered: ", s.Get("features")))
	for _, tag := range []string{"canvas.TextFlow", "canvas.Image", "canvas.FloatingText", "canvas.Table", "canvas.ScrollView"} {
		ok(d.Is("s.elements.includes("+e2etest.J(tag)+")"), tag+" is a registered element")
	}
	ok(s.Int("commands") >= 30, fmt.Sprint("commands registered: ", s.Get("commands")))
	ok(d.Value(".gj-docview-top input.gj-docview-title-edit") == "Quarterly report", "the DocumentView title is an editable box")
	ok(d.Count(".gj-docview-desk canvas#stage") == 1, "the canvas sits in the DocumentView desk")

	// The menu bar is built from the registered commands: one submenu per feature.
	d.Click(`button.gj-menubar-button:has-text("Edit")`)
	subs := d.Count(".gj-menubar-panel > .gj-menu > .gj-menu-item-sub")
	ok(subs == 17, fmt.Sprint("Edit has a submenu per feature: ", subs))
	shortcut := d.Text(`.gj-menubar-panel li:has-text("Move line up") .gj-menu-shortcut`)
	ok(shortcut == "Alt+↑", "a command shows its shortcut: "+shortcut)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Paragraphs")`)
	ok(d.Visible(`li:has-text("Bulleted list") .gj-menu-shortcut`), "a submenu opens on hover")
	d.MouseClick(600, 700, 1) // the backdrop closes the menu
	ok(d.Count(".gj-menubar-panel") == 0, "a click outside closes the menu")

	// The components' stylesheets come from CSS imports, installed by domrt.Mount.
	var sheets []string
	d.Eval(`Array.from(document.querySelectorAll('style[data-xango-style]')).map((e) => e.getAttribute('data-xango-style'))`, &sheets)
	hasStyleEditor := false
	for _, x := range sheets {
		hasStyleEditor = hasStyleEditor || strings.HasSuffix(x, "/styleeditor.css")
	}
	ok(len(sheets) > 10 && strings.HasSuffix(sheets[0], "/components/base.css") && hasStyleEditor, fmt.Sprint("component stylesheets installed in <head>: ", len(sheets)))
	accent := d.Str(`getComputedStyle(document.documentElement).getPropertyValue('--gj-accent').trim()`)
	ok(accent == "#2563eb", "the design tokens apply: "+accent)

	d.ToggleMode()
	s = S()
	ok(s.Str("mode") == "edit", "edit mode")

	// Click into the first paragraph (title is at the top of page 1) and type.
	before := s.Str("text")
	body()
	s = S()
	ok(s.Bool("editing") && d.Is("s.cursors.length === 1"), "clicking in the text puts the caret there")
	d.Type("QQ")
	s = S()
	ok(len(s.Str("text")) == len(before)+2 && strings.Contains(s.Str("text"), "QQ"), "typing edits the flow")
	d.Press("Backspace")
	s = S()
	ok(len(s.Str("text")) == len(before)+1 && strings.Contains(s.Str("text"), "Q") && !strings.Contains(s.Str("text"), "QQ"), "Backspace deletes")

	d.Press("Control+z")
	s = S()
	ok(len(s.Str("text")) == len(before)+2 && strings.Contains(s.Str("text"), "QQ"), "Ctrl+Z undoes the Backspace")
	d.Press("Control+z")
	s = S()
	ok(s.Str("text") == before, fmt.Sprintf("Ctrl+Z undoes the typing: %d vs %d", len(s.Str("text")), len(before)))
	d.Press("Control+Shift+z")
	d.Press("Control+y")
	s = S()
	ok(len(s.Str("text")) == len(before)+1, "Ctrl+Shift+Z and Ctrl+Y redo")

	// Multi-cursor: select the word at the caret, then every match.
	d.Press("Control+D")
	d.Press("Control+D")
	ok(d.Is("s.cursors.length >= 1"), "Ctrl+D works on the flow")

	// Page break: Ctrl+Enter makes a fourth page's worth of flow.
	body()
	d.Press("Control+Enter")
	ok(d.Is("s.cursors[0].After === true"), "the caret goes after the page break")
	d.Type("NEWPAGE")
	s = S()
	ok(strings.Contains(s.Str("text"), "NEWPAGE"), "typing after the break")

	// Line operations: move the line the caret is on.
	t0 := s.Str("text")
	d.Press("Alt+ArrowDown")
	s = S()
	ok(s.Str("text") != t0 && len(s.Str("text")) == len(t0), "Alt+Down moves the line (same length, different order)")
	d.Press("Alt+ArrowUp")
	ok(S().Str("text") == t0, "Alt+Up moves it back")
	d.Press("Alt+Shift+ArrowDown")
	ok(len(S().Str("text")) > len(t0), "Alt+Shift+Down copies the line")
	d.Press("Control+Shift+K")
	ok(len(S().Str("text")) == len(t0), "Ctrl+Shift+K deletes it again")

	// Styles: the gallery applies a paragraph style to the caret's paragraph.
	d.Click(`button[data-tab="styles"]`)
	ok(d.Count(`.gj-stylepicker-item[data-style="Heading 1"]`) == 1, "the style gallery lists the styles")
	body()
	var hBefore []float64
	d.Eval(`JSON.parse(document.getElementById('state').textContent).lineHeights`, &hBefore)
	sum := func(h []float64) (n float64) {
		for _, v := range h {
			n += v
		}
		return n
	}
	total := "s.lineHeights.reduce((a, b) => a + b, 0)"
	d.Click(`.gj-stylepicker-item[data-style="Heading 2"]`)
	d.Wait(total + " > " + fmt.Sprint(sum(hBefore)))
	ok(true, fmt.Sprint("Heading 2 makes the paragraph taller: ", sum(hBefore)))
	d.Click(`.gj-stylepicker-item[data-style="Normal"]`)
	d.Wait(total + " === " + fmt.Sprint(sum(hBefore)))
	ok(true, "Normal puts it back")

	// Symbols: pick one from the DOM picker into the canvas text.
	d.Click(`button[data-tab="symbols"]`)
	body()
	tLen := len(S().Str("text"))
	d.Click(`.gj-symbol[data-char="€"]`)
	d.Wait("s.text.includes('€')")
	s = S()
	ok(len(s.Str("text")) == tLen+len("€") && strings.Contains(s.Str("text"), "€"), fmt.Sprint("a symbol picked in the DOM lands at the canvas caret ", tLen, " -> ", len(s.Str("text")), " editing=", s.Get("editing")))
	d.Fill(".gj-symbolpicker-search", "omega")
	ok(d.Count(".gj-symbolpicker-grid .gj-symbol") == 2, "the symbol search filters by name")
	d.Click(`button[data-tab="format"]`)

	// A link: hovering it shows its address on the canvas and a preview card in the DOM.
	s = S()
	for i := 0; i < 20 && (s.Num("link.y") > 560 || s.Num("link.y") < 20); i++ {
		d.CanvasMove(450, 300)
		if s.Num("link.y") > 560 {
			d.Wheel(0, 300)
		} else {
			d.Wheel(0, -300)
		}
		d.Sleep(50)
		s = S()
	}
	ok(s.Str("link.url") == "https://github.com/elaous/gojiberry", fmt.Sprint("the Markdown link is a link: ", s.Get("link")))
	d.CanvasMove(s.Num("link.x"), s.Num("link.y"))
	d.Wait("s.hoverLink === 'https://github.com/elaous/gojiberry'")
	ok(S().Str("hoverLink") == "https://github.com/elaous/gojiberry", "hovering a link reports it")
	ok(d.Count(`#link-preview a.gj-linkpreview[href="https://github.com/elaous/gojiberry"]`) == 1, "and the DOM shows a preview card")
	d.CanvasMove(5, 5)
	d.WaitFor("the card to go", "!document.querySelector('#link-preview a.gj-linkpreview')")
	ok(d.Count("#link-preview a.gj-linkpreview") == 0, "the card goes when the pointer leaves")

	// Key bindings editor: rebind Copy line down to Ctrl+J.
	d.Click(`button[data-tab="keys"]`)
	d.Click(`[data-action="change:line.duplicate-down"]`)
	d.WaitFor("the key capture", "document.querySelector('.gj-keybindings-capture')")
	d.Press("Control+j")
	d.WaitFor("the new binding", "document.querySelector('.gj-keybindings-message')")
	ok(d.Count(`.gj-keybindings-message:has-text("Copy line down is now Ctrl+J")`) == 1, "the editor reports the new binding")
	ok(d.Count(`.gj-keybindings-row-modified:has-text("Copy line down")`) == 1, "and marks it modified")
	// The long key list may have scrolled the whole page (overflow:hidden still scrolls).
	d.Eval(`(() => { window.scrollTo(0, 0); document.querySelectorAll('*').forEach((e) => { if (e.scrollTop) e.scrollTop = 0; }); })()`, nil)
	scrollTop(50)
	body() // a body line
	lineText := S().Str("text")
	d.Press("Alt+Shift+ArrowDown")
	ok(S().Str("text") == lineText, "the old chord no longer copies the line")
	d.Press("Control+j")
	ok(len(S().Str("text")) > len(lineText), fmt.Sprintf("the new chord copies the line: %d -> %d", len(lineText), len(S().Str("text"))))
	d.Click(`[data-action="reset-all"]`)
	d.Click(`button[data-tab="format"]`)

	// Page furniture: the watermark starts on page 2; removing the header from one page.
	ok(jsonIs("watermarks", []string{"", "DRAFT", "DRAFT"}), fmt.Sprint("watermark from page 2 on: ", S().Get("watermarks")))
	ok(jsonIs("headers", []string{"Gojiberry editor", "Gojiberry editor", "Gojiberry editor"}), fmt.Sprint("headers: ", S().Get("headers")))
	d.Click(`button.gj-menubar-button:has-text("Edit")`)
	d.Hover(`.gj-menubar-panel li.gj-menu-item-sub:has-text("Page furniture")`)
	d.Click(`.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Remove watermark from all pages")`)
	ok(d.Is("s.watermarks.every((w) => w === '')"), fmt.Sprint("Remove watermark from all pages (from the menu): ", S().Get("watermarks")))
	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Click(`.gj-menubar-panel li:has-text("Two-page spread")`)
	ok(S().Bool("spread"), "the View menu turns on the two-page spread")
	d.Click(`button.gj-menubar-button:has-text("View")`)
	d.Click(`.gj-menubar-panel li:has-text("Two-page spread")`)
	ok(!S().Bool("spread"), "and off again")

	// Paragraph formatting: lists, numbering, drop cap.
	scrollTop(50)
	body()
	markers := func() int { return d.Int(`JSON.parse(document.getElementById('state').textContent).markers.length`) }
	bullets := func() int {
		return d.Int(`JSON.parse(document.getElementById('state').textContent).markers.filter((x) => x === '•').length`)
	}
	m0, b0 := markers(), bullets()
	d.Press("Control+Shift+8")
	ok(markers() == m0+1 && bullets() == b0+1, fmt.Sprint("Ctrl+Shift+8 makes the paragraph a bullet item: ", m0, " -> ", S().Get("markers")))
	d.Press("End")
	d.Press("Enter")
	ok(markers() == m0+2, "Enter continues the list")
	d.Press("Control+Shift+7")
	ok(d.Is("s.markers.includes('1.')"), fmt.Sprint("Ctrl+Shift+7 numbers the new item: ", S().Get("markers")))
	d.Press("Enter")
	d.Press("Enter")
	ok(markers() == m0+2, fmt.Sprint("Enter on an empty item ends the list: ", S().Get("markers")))
	body()
	d.Press("Control+e")
	ok(len(S().Str("text")) > 0, "Ctrl+E (centre) is handled without breaking the text")

	// Markdown clipboard.
	d.GrantClipboard()
	d.Eval(`navigator.clipboard.writeText('pasted **bold** text')`, nil)
	d.Press("Control+Shift+V")
	d.Wait("s.text.includes('pasted bold text')")
	ok(strings.Contains(S().Str("text"), "pasted bold text"), "Ctrl+Shift+V pastes Markdown as text, not marks")

	// Auto-closing pairs and completion in the text flow (at several cursors too).
	d.Focus("#input")
	d.Press("End")
	d.Type("(note")
	ok(strings.Contains(S().Str("text"), "(note)") && !strings.Contains(S().Str("text"), "(note))"), "typing ( closes the pair in the text flow")
	d.Type(")")
	ok(strings.Contains(S().Str("text"), "(note)") && !strings.Contains(S().Str("text"), "(note))"), "typing ) steps over the closer")
	d.Type(" feat")
	d.Press("Control+Space")
	d.WaitSelector(`#suggestions [role="option"]`)
	ok(strings.TrimSpace(d.Text(`#suggestions [aria-selected="true"]`)) == "feature", "typing a prefix offers completions from the vocabulary")
	d.Press("Tab")
	d.Wait("s.text.includes('(note) feature')")
	ok(d.Count(`#suggestions [role="option"]`) == 0, "Tab accepts the completion")

	// Outline and bookmarks: the headings are a list that jumps; a bookmark is a named place.
	d.Click(`button[data-tab="outline"]`)
	ok(d.Is("s.outline.includes('1:Gojiberry editor') && s.outline.includes('2:Second thoughts')"), fmt.Sprint("the outline lists the headings: ", S().Get("outline")))
	ok(d.Count(`.gj-outline-item[data-level="2"]`) >= 1, "the Outline component shows them with their levels")
	scroll0 := S().Num("scrollY")
	d.Click(`.gj-outline-item:has-text("Second thoughts")`)
	ok(S().Num("scrollY") > scroll0, fmt.Sprint("clicking a heading scrolls to it: ", scroll0, " -> ", S().Num("scrollY")))
	ok(d.Text(".gj-outline-item-on") == "Second thoughts", "and marks it current")
	d.Focus("#input")
	d.AnswerDialog("second")
	d.Press("Control+Alt+b")
	d.Wait("s.bookmarks.includes('second')")
	ok(d.Count(`.gj-bookmark[data-bookmark="second"], .gj-bookmark:has([data-bookmark="second"])`) == 1, "the bookmark is listed")
	ok(d.Text(".gj-bookmark-link") == "#second", "with the link form to use")
	scrollTop(100)
	ok(S().Num("scrollY") == 0, "back at the top")
	d.Click(".gj-bookmark-go")
	ok(S().Num("scrollY") > 0, fmt.Sprint("a bookmark takes you to its text: ", S().Num("scrollY")))
	d.Click(".gj-bookmark-remove")
	ok(d.Is("s.bookmarks.length === 0"), "Remove takes it off")

	// Footnotes, a table of contents and an index.
	scrollTop(100)
	body() // a body line
	d.Focus("#input")
	d.Press("End")
	d.AnswerDialog("Source: the **gojiberry** docs")
	d.Press("Control+Alt+f")
	d.Wait("s.footnotes.length === 1")
	ok(d.Text(".gj-footnote .gj-footnote-num") == "1", "the footnote is numbered in the list")
	ok(d.Text(".gj-footnote strong") == "gojiberry", "its text is Markdown")
	d.Click(`button.gj-menubar-button:has-text("Insert")`)
	d.Click(`.gj-menubar-panel li:has-text("Table of contents")`)
	d.Wait("s.tocRows >= 2")
	ok(S().Int("tocRows") >= 2, fmt.Sprint("a table of contents lists the headings: ", S().Get("tocRows")))
	d.Focus("#input")
	for range 5 {
		d.Focus("#input")
		d.Press("Shift+ArrowLeft")
	}
	d.AnswerDialog("terms")
	d.Press("Control+Alt+x")
	d.Wait("s.indexTerms.includes('terms')")
	ok(true, "the selection is marked as an index entry")
	d.Click(".gj-footnote-delete")
	d.Wait("s.footnotes.length === 0")
	ok(true, "deleting a footnote removes its mark and note")

	// Comments: comment the selection, see it listed with its text and mentions, hover it, reply, resolve, delete.
	scrollTop(100)
	body() // a body line
	d.Focus("#input")
	d.Press("End")
	for range 7 {
		d.Focus("#input")
		d.Press("Shift+ArrowLeft")
	}
	d.Focus("#input")
	d.AnswerDialog("Check **this** wording, @bob or ana@example.com")
	d.Press("Control+Alt+n")
	d.Wait("s.comments === 1")
	d.Click(`button[data-tab="comments"]`)
	ok(len([]rune(d.Text(".gj-comment-excerpt"))) == 7, "the comment card shows the commented text")
	ok(d.Text(".gj-comment .gj-markdown strong") == "this", "the body is rendered as Markdown")
	ok(d.Count(`.gj-comment .gj-mention[data-mention="bob"]`) == 1, "an @username is highlighted")
	ok(d.Count(`.gj-comment a.gj-mention-email[href="mailto:ana@example.com"]`) == 1, "an e-mail address is a mailto mention")
	ok(d.Text(".gj-comment-author") == "you@example.com", "the author is the current user")
	s = S()
	d.CanvasMove(s.Num("comment.x"), s.Num("comment.y"))
	d.Wait("s.hoverComment === s.comment.id")
	s = S()
	ok(s.Get("hoverComment") == s.Get("comment.id"), fmt.Sprint("hovering commented text finds its comment: ", s.Get("hoverComment")))
	tip := d.Pixel(int(s.Num("comment.x")+0.5), int(s.Num("comment.y")+0.5)+28)
	ok(tip[0] == 0xff && tip[1] == 0xfb && tip[2] == 0xe6, fmt.Sprint("the summary tip is drawn under the text: ", tip))
	d.Fill(".gj-comment-input", "Agreed")
	d.Click(".gj-comment-send")
	ok(d.Text(".gj-comment-reply") == "you@example.com Agreed", "a reply is added")
	d.Click(".gj-comment-resolve")
	ok(d.Count(".gj-comment") == 0, "a resolved comment leaves the open list")
	d.Click(`.gj-comment-filter[data-filter="resolved"]`)
	ok(d.Count(".gj-comment-resolved") == 1, "and shows under the Resolved filter")
	d.Click(".gj-comment-delete")
	ok(d.Is("s.comments === 0 && s.comment === null"), "Delete removes the comment and un-marks its text")

	// Lazy fonts: picking a font only adds it; the file is downloaded into
	// the store the first time the text uses it.
	var realFont []byte
	for _, p := range []string{"/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf", "/usr/share/fonts/truetype/freefont/FreeSerif.ttf"} {
		if b, err := os.ReadFile(p); err == nil {
			realFont = b
			break
		}
	}
	fontHits := d.Route("/fonts/lazy-serif.ttf", func() (int, string, []byte) {
		if realFont != nil {
			return 200, "font/ttf", realFont
		}
		return 200, "font/ttf", []byte("not a font")
	})
	d.Focus("#input")
	d.Press("ArrowRight") // a caret, nothing selected
	d.Click(`button[data-tab="format"]`)
	d.Click(".gj-fontselector-control")
	d.Fill(".gj-fontselector-search", "Lazy")
	d.Click(`.gj-fontselector-option[data-family="Lazy Serif"]`)
	d.Wait("s.fontsAdded.includes('Lazy Serif=')")
	ok(d.Is("s.fontsAdded.includes('Lazy Serif=')"), fmt.Sprint("the picked font is in the library, not yet downloaded: ", S().Get("fontsAdded")))
	ok(fontHits() == 0, "nothing was fetched yet: no text uses the font")
	d.Focus("#input")
	d.Type("lazy")
	d.WaitForTimeout("the font to be stored", `JSON.parse(document.getElementById('state').textContent).fontsAdded.some((f) => f.startsWith('Lazy Serif=fonts/'))`, 5e9)
	ok(fontHits() == 1, fmt.Sprint("the font file was fetched once, when the text used it: ", fontHits()))
	ok(d.Is("s.fontLog.some((l) => l.includes('Lazy Serif 400: downloaded into the store'))"), fmt.Sprint("the library logged the download: ", S().Get("fontLog")))
	ok(d.Is(`s.fontsAdded.some((k) => /^Lazy Serif=fonts\/Lazy Serif\/400-n-[0-9a-f]{8}\.ttf$/.test(k))`), fmt.Sprint("its store key is recorded: ", S().Get("fontsAdded")))
	if realFont != nil {
		d.WaitForTimeout("the browser to load the font", `Array.from(document.fonts).some((f) => f.family.replace(/"/g, '') === 'Lazy Serif' && f.status === 'loaded')`, 5e9)
		ok(true, "the browser loaded the downloaded font")
	}
	d.Press("ArrowRight")
	d.Sleep(100)
	ok(fontHits() == 1, "using it again does not download it again")

	// Saver: memory target, protobuf binary, then the browser's localStorage as JSON.
	d.Click(`button[data-tab="save"]`)
	ok(d.Count(`.gj-saver-select[data-field="Save to"] option`) == 2, "the Saver offers its targets")
	ok(d.Count(`.gj-saver-select[data-field="Format"] option`) == 4, "and the formats")
	ok(d.Disabled(".gj-saver-save"), "Save waits for a name")
	d.Fill(".gj-saver-name", "notes")
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.Select(`.gj-saver-select[data-field="Format"]`, "proto")
	d.Click(".gj-saver-save")
	d.WaitFor("the saved entry", `document.querySelector('.gj-doclist-item')`)
	ok(d.Text(".gj-saver-status") == "notes saved", "status: "+d.Text(".gj-saver-status"))
	ok(d.Count(".gj-doclist-item") == 1, "the downloaded font in the same store is not listed as a document")
	d.Select(`.gj-saver-select[data-field="Save to"]`, "browser")
	d.Select(`.gj-saver-select[data-field="Format"]`, "json")
	d.WaitFor("an empty list", "document.querySelectorAll('.gj-doclist-item').length === 0")
	d.Click(".gj-saver-save")
	d.WaitFor("the saved entry", `document.querySelector('.gj-doclist-item')`)
	savedID := d.Attr(".gj-doclist-item", "data-id")
	var stored string
	// The file's key carries a generation suffix: find it by its ID and extension.
	d.Eval(`(() => { const k = Object.keys(localStorage).find((k) => k.startsWith('showcase:`+savedID+`~') && k.endsWith('.json')); return JSON.parse(localStorage.getItem(k)).d })()`, &stored)
	raw, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Title   string
		Content []struct {
			Chars *struct{ Format struct{ FontFamily string } }
		}
		Fonts []struct {
			Family   string
			Variants []struct{ Key string }
		}
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatalf("saved document: %v: %s", err, raw)
	}
	ok(saved.Title == "Quarterly report" && len(saved.Content) > 0, "localStorage holds the document as JSON: "+saved.Title)
	fontKey := regexp.MustCompile(`^fonts/Lazy Serif/400-n-[0-9a-f]{8}\.ttf$`)
	recorded := false
	for _, f := range saved.Fonts {
		recorded = recorded || f.Family == "Lazy Serif" && len(f.Variants) > 0 && fontKey.MatchString(f.Variants[0].Key)
	}
	ok(recorded, "the saved document records the font and where its file is")
	inFamily := false
	for _, i := range saved.Content {
		inFamily = inFamily || i.Chars != nil && strings.Contains(i.Chars.Format.FontFamily, "Lazy Serif")
	}
	ok(inFamily, "text in the font is saved with its family")

	// Open: change the text, open the saved JSON, the text is back.
	textBefore := S().Str("text")
	d.Focus("#input")
	d.Press("Control+End")
	d.Type("ZZZ")
	ok(strings.Contains(S().Str("text"), "ZZZ"), "edited after saving")
	d.Click(".gj-doclist-item .gj-doclist-open")
	d.Wait("!s.text.includes('ZZZ')")
	ok(S().Str("text") == textBefore, "Open restores the saved text")
	d.Select(`.gj-saver-select[data-field="Save to"]`, "memory")
	d.WaitFor("the memory entry", `document.querySelector('.gj-doclist-item')`)
	d.Click(".gj-doclist-item .gj-doclist-delete")
	d.Click(`[role="alertdialog"] .gj-dialog-actions button:last-child`) // Delete for good asks first
	d.WaitFor("an empty list", "document.querySelectorAll('.gj-doclist-item').length === 0")
	ok(d.Text(".gj-saver-status") == "notes deleted", "Delete removes a saved document")

	// Import / Export.
	d.Click(`button[data-tab="transfer"]`)
	name, data := d.Download(func() { d.Click(`.gj-transfer-button[data-kind="export"][data-format="markdown"]`) })
	ok(name == "Quarterly report.md", "Export downloads a file named after the title: "+name)
	ok(strings.Contains(string(data), "pasted"), "the Markdown export has the text")
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="markdown"]`) }, "hello.md", []byte("# Imported\n\nfresh **words**"))
	d.Wait("s.text.includes('fresh words')")
	ok(d.Text("#transfer-message") == "Imported hello.md", "Import reads the file into the editor")

	// The other formats: each export is a real file of its kind, and a Word file comes back in.
	var docxBytes []byte
	for _, f := range []struct {
		format, ext string
		check       func([]byte) bool
	}{
		{"docx", ".docx", func(b []byte) bool { return strings.HasPrefix(string(b), "PK") }},
		{"pdf", ".pdf", func(b []byte) bool { return strings.HasPrefix(string(b), "%PDF-") }},
		{"epub", ".epub", func(b []byte) bool { return strings.HasPrefix(string(b), "PK") }},
		{"rtf", ".rtf", func(b []byte) bool { return strings.HasPrefix(string(b), `{\rtf1`) }},
		{"html", ".html", func(b []byte) bool {
			return strings.Contains(string(b), "fresh") && strings.Contains(string(b), "<strong>words</strong>")
		}},
	} {
		name, data := d.Download(func() { d.Click(fmt.Sprintf(`.gj-transfer-button[data-kind="export"][data-format="%s"]`, f.format)) })
		ok(name == "Quarterly report"+f.ext, f.format+" export is named after the title: "+name)
		ok(f.check(data), f.format+" export is a real "+f.format+" file")
		if f.format == "docx" {
			docxBytes = data
		}
	}
	d.ChooseFile(func() { d.Click(`.gj-transfer-button[data-kind="import"][data-format="docx"]`) }, "back.docx", docxBytes)
	d.Wait("s.text.includes('fresh words') && document.getElementById('transfer-message').textContent === 'Imported back.docx'")
	ok(true, "a Word file exported by the editor imports back with its text")

	// Roles: a viewer is held in view mode and cannot comment; an editor can edit but not manage.
	d.CanvasClick(100, 164) // into the imported text's second line
	d.Focus("#input")
	d.Press("End")
	d.AnswerDialog("One more")
	for range 3 {
		d.Focus("#input")
		d.Press("Shift+ArrowLeft") // the format panel re-renders on a heading selection
	}
	d.Focus("#input")
	d.Press("Control+Alt+n")
	d.Wait("s.comments === 1")
	d.Click(`button[data-role="viewer"]`)
	s = S()
	ok(s.Str("role") == "viewer" && s.Str("mode") == "view" && !s.Bool("editing"), "a viewer is put back in view mode")
	d.ToggleMode()
	s = S()
	ok(s.Str("mode") == "view", "and cannot switch to edit mode")
	typed := s.Str("text")
	body()
	d.Type("nope")
	ok(S().Str("text") == typed, "typing changes nothing for a viewer")
	d.Click(`button[data-tab="comments"]`)
	d.Click(`.gj-comment-filter[data-filter="all"]`)
	ok(d.Count(".gj-comment") == 1 && d.Count(".gj-comment-actions") == 0, "a viewer reads comments but has no controls")
	d.Click(`button[data-role="editor"]`)
	d.ToggleMode()
	s = S()
	ok(s.Str("role") == "editor" && s.Str("mode") == "edit", "an editor can edit again")
	ok(d.Count(".gj-comment-actions") == 1, "and gets the comment controls back")
	d.Click(`button[data-role="owner"]`)
	d.Press("Escape")
	d.NoErrors()
}
