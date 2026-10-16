//go:build js && wasm

// Command canvas-editor-showcase is a small word processor with every
// piece we have built registered in one place: the canvas tree (app.xg)
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
	"github.com/elaous/gojiberry/a11ycheck"
	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	collabxango "github.com/elaous/gojiberry/collab/xango"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/components/encerr"
	"github.com/elaous/gojiberry/console"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/domrt"
	"github.com/elaous/gojiberry/domrt/browser"
	"github.com/elaous/gojiberry/export/remote"
	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/gojiberry/migrate"
	"github.com/elaous/gojiberry/saver"
	"github.com/elaous/gojiberry/share"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
	"runtime"
	"slices"
	"strings"
	"syscall/js"
	"time"
)

// ui is the DOM side's state.
type ui struct {
	folder                       folderState // the folder on this computer, a save target
	title                        string
	menu                         int    // open menu, or -1
	tab                          string // sidebar tab: format, symbols, keys
	check                        checkState
	noteEd                       *components.NoteEditor // the dialog a footnote is typed in
	fidelity                     fidelityState          // the Import/Export tab's report of what a format loses
	collab                       components.CollabAnnouncer
	collabTimer                  bool
	current                      func() docfile.Document // the document as it is being edited now
	away                         awayState
	theme                        themeState
	themeLib                     themeLibState
	cfgx                         configExtra
	collabConflicts              int
	collabMerged, collabRejected int                        // the session's counts already announced (merged after offline, refused)
	typing                       components.TypingAnnouncer // what is said of typing and of the caret (preferences), see typing.go
	typingMsg                    string
	typingN                      int
	typingTimer                  bool
	docLang                      string             // the document's language (a BCP 47 tag), "" for none
	layout                       docfile.PageLayout // the header, footer and table of contents of the print layout
	// the Images tab: the last message, and whether the first undescribed picture takes the focus
	imageMsg     string
	media        mediaForm    // the clip being typed in the Pictures tab and the caption drafts
	elements     elementsForm // the equation being typed and the last message of the equation and code parts of the tab
	sugg         suggestState
	ghost        ghostUI
	stateRef     *State                 // the document state, for what the announcer needs of it
	commentSig   string                 // how many comments, replies and resolved threads there were when last looked at
	commentsSeen map[string]commentSeen // the comments already known (their replies and whether resolved), to find what others do
	imageFocus   bool
	floating     func() []a11ycheck.Object // the floating pictures and tables of the page, for the Check tab
	imageDraft   map[int]string            // the draft descriptions made on this device, by picture (imagehint)
	format       canvas.Format
	caretFmt     canvas.Format // the format at the caret, for the toolbar buttons that are on or off
	catalog      []fonts.Font
	open         bool
	query        string
	source       string
	recent       []string

	symQuery, symCat string
	symRecent        []string

	kbQuery   string
	recording canvas.Command
	kbMessage string

	hoverLink     string
	hoverComment  string
	selComment    string
	commentFilter string
	drafts        map[string]string
	replyNotes    map[string]string // comment id -> why its reply draft cannot be saved

	saver               *saver.Session
	live                *collabxango.State // when the page was opened on a shared document (/d/{doc})
	liveName, liveColor string
	transfer            *transfer.Registry
	migrate             *migrate.Session
	console             *console.Session
	pub                 *publicUI              // the Public link tab
	load                func(docfile.Document) // opens a document in the editor
	setRole             func(canvas.Role)      // makes the editor read only, or editable again
	persisted           bool                   // the browser promised to keep this origin's storage
	fontLog             []string               // what the font library did, newest last
	navSig              string                 // outline/bookmarks the Outline tab last showed
	prefsQuery          string                 // what is typed in the Preferences tab's search box
	find                findState
	a11ySig             string
	a11yCache           []components.A11yBlock
	mirrorWriting       bool      // the copy is being given a selection by the page (not by a reader)
	mirrorWroteAt       time.Time // when the page last wrote the copy's selection (its selectionchange comes later)
	mirrorSig, mirrorAt string    // where the caret was in the copy when last drawn, and the key of the caret last followed
	announcement        string
	spell               spellState
	lang                langState
	statsSig            string // the counts the status bar and Stats tab last showed
	shareLink           string // the link the Share tab shows
	people              *peopleState
	shareToken          string     // a share link the person chose to use (#share=); sent with every request to the server
	speech              *speechUI  // the Speech tab: phrases to record, and dictation through a recogniser the person names
	usage               *privacyUI // the Privacy tab: anonymous usage counts, off unless the person opts in
	sa                  searchAll
	enc                 *encUI // the encrypted browser store: off unless the person turns it on
	clearEditor         func() // empties the editor and what is derived from its text (Lock uses it)
	message             string // the Import/Export tab's last message
}

func main() {
	// gjHeap is the Go heap in use after a collection, in bytes: for the tests that measure what a setting saves.
	js.Global().Set("gjHeap", js.FuncOf(func(js.Value, []js.Value) any {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return float64(m.HeapAlloc)
	}))
	doc := browser.Document()
	state := State{Mode: canvas.ModeView, Pages: 3, Setup: components.DefaultPageSetup(), Source: sample, Note: "Floating note: click to edit, try (brackets and \"quotes\".", Styles: canvas.DefaultStyles(),
		Comments: canvas.NewCommentSet(), Author: "you@example.com", Footnotes: canvas.NewFootnoteSet()}
	u := &ui{title: "Quarterly report", menu: -1, tab: "format", drafts: map[string]string{}, replyNotes: map[string]string{}}
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
	remote.Register(u.transfer, callFiles) // HTML, RTF, EPUB, PDF, OpenDocument, Word out; Word, HTML, OpenDocument and PDF in: done by the file worker

	stage := canvas.NewStage(nil, 0, 0)
	canvasEl, inputEl := doc.GetElementByID("stage"), doc.GetElementByID("input")
	redraw, err := stage.Bind(canvasEl, inputEl)
	if err != nil {
		doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
		return
	}
	root := cxango.NewRoot(stage)
	u.floating = func() []a11ycheck.Object { return floatingObjects(root) }
	root.Redraw = redraw
	defaults := stage.EffectiveKeymap()
	stage.Keymap = decodeKeymap(lsGet(keymapKey)) // the person's own bindings from an earlier visit
	keepKeymap := func() { lsSet(keymapKey, encodeKeymap(stage.Keymap)) }

	var flow *canvas.TextFlow
	var renderUI func()

	// Lazy font download: a font picked in the selector is only Added;
	// the stage tells us the first time the document really uses a cut,
	// and only then is the file fetched into the store and loaded.
	stage.OnFontUse = func(use canvas.FontUse) {
		if !u.isStoredFont(use.Family) { // the person's own fonts are in the browser already
			lib.UseAsync(use.Family, use.Weight, use.Italic)
		}
	}
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

	u.noteEd = components.NewNoteEditor(func() { renderUI() })
	stage.EditNote = func(title, initial string, max int, done func(string, bool)) {
		u.noteEd.Edit(title, initial, max, func(text string, ok bool) {
			done(text, ok)
			renderUI()
			refocus()
		})
	}
	cb := Callbacks{
		OnFlow: func(*canvas.TextFlow) {
			u.saver.MarkEdited()
			if u.pub != nil {
				u.pub.edited()
			}
		},
		OnHoverLink:    func(url string) { u.hoverLink = url; renderUI() },
		OnHoverComment: func(id string) { u.hoverComment = id },
	}
	renderCanvas := func() {
		state.RunHeader, state.RunFooter, state.DocTitle = u.layout.Header, u.layout.Footer, u.title
		state.FirstDiffers, state.FirstHeader, state.FirstFooter = u.layout.FirstDiffers, u.layout.FirstHeader, u.layout.FirstFooter
		state.EvenDiffers, state.EvenHeader, state.EvenFooter = u.layout.EvenDiffers, u.layout.EvenHeader, u.layout.EvenFooter
		if err := root.Render(Doc(state, cb)); err != nil {
			doc.GetElementByID("state").SetTextContent(`{"error":"` + err.Error() + `"}`)
			return
		}
		flow = root.Find(cxango.TagTextFlow, 0).(*canvas.TextFlow)
		stage.Draw()
	}

	openDoc := func(d docfile.Document) {
		// The text flow takes its items from props, and only when they differ from
		// the last ones it was given: opening the same content again (a saved
		// version after edits) would leave the edits in place. Render it empty
		// first so the opened content is always applied.
		state.Items, state.Source = nil, ""
		renderCanvas()
		state.Items = docfile.ToItems(d.Content)
		// What the document says about itself replaces what the last one said: its
		// styles (the defaults when it has none), comments and footnotes. The sets are
		// filled in place; the page holds them for its whole life.
		if len(d.Styles) > 0 {
			state.Styles = docfile.ToStyles(d.Styles)
		} else {
			state.Styles = canvas.DefaultStyles()
		}
		state.Comments.ReplaceAll(docfile.ToComments(d.Comments).List())
		state.Footnotes.ReplaceAll(docfile.ToFootnotes(d.Footnotes).List())
		for _, f := range docfile.ToFonts(d.Fonts) {
			lib.Add(f)
		}
		if d.Title != "" {
			u.title = d.Title
		}
		u.docLang = d.Language()
		u.fidelity.restore(d)
		u.loadTheme(d)
		state.Theme = u.canvasTheme()
		u.layout = d.PageLayout()
		if flow != nil {
			flow.Vertical = u.layout.Vertical
		}
		// The sheet the document was saved with comes back with it (a size that is none of the papers on
		// offer, or one that was never set, leaves the sheet as it is).
		if ps, ok := components.PageSetupOf(u.layout.Width, u.layout.Height, u.layout.Margins); ok {
			if u.layout.Margins == ([4]float64{}) {
				ps.Margins = state.Setup.Margins
			}
			state.Setup = ps
		}
		u.layout.Width, u.layout.Height, u.layout.Margins = 0, 0, [4]float64{}
		stage.ForgetFonts() // the opened text may use fonts the library has
		renderCanvas()
		renderUI()
	}
	u.clearEditor = func() {
		// Locking closes the document that is open: the editor, and everything that was worked out
		// from its text (find results, spelling, outline and statistics caches, comments).
		u.title = ""
		openDoc(docfile.Document{})
		u.find, u.spell.spans, u.spell.words, u.spell.text = findState{}, nil, nil, ""
		// What was translated or found wrong in the text, and the replies typed to its comments, are
		// the document's text too: not left on screen behind a lock.
		u.lang.translated, u.lang.transText, u.lang.issues, u.lang.text = "", "", nil, ""
		u.lang.selFrom, u.lang.selTo, u.lang.checked, u.lang.err = 0, 0, false, ""
		u.drafts = map[string]string{}
		u.navSig, u.statsSig, u.a11ySig, u.a11yCache, u.shareLink = "", "", "", nil, ""
		renderUI()
	}
	currentDoc := func() docfile.Document {
		d := docfile.FromFlow(u.title, flow)
		d.Fonts = docfile.FromFonts(lib.Added())
		d.SetLanguage(u.docLang)
		d.SetImportReport(u.fidelity.kept())
		u.themeInto(&d)
		u.applyLayout(&d, state.PageWidth(), state.PageHeight(), state.Setup.Margins)
		return d
	}
	// The browser target (localStorage) cannot write the catalog conditionally, so two
	// tabs saving at the same moment can drop one catalog entry (Repair finds the
	// orphaned file next time). The memory target, a local directory, SQLite and the HTTP
	// store do write conditionally (docstore.ConditionalAdapter).
	browserStore := docstore.NewBrowserAdapter("showcase:")
	// The encrypted store lives in this browser, or, when the person chose that and confirmed the
	// server, on the server (encscope.go); the browser's own storage is then a plain target.
	var encRaw docstore.Adapter = browserStore
	if _, srv, ok := chosenServerStore(); ok {
		encRaw = srv
	}
	u.enc, err = newEnc(encRaw, func() { renderUI(); u.publishEncState() })
	if err != nil {
		// A key list that cannot be read is not guessed at: the page starts with encryption off
		// and says why in the Save tab.
		u.enc, _ = newEncOff(encRaw, func() { renderUI(); u.publishEncState() }, err)
	}
	targets := []saver.Target{
		u.enc.target(),
		{Name: "memory", Label: "Memory", Adapter: store, Capacity: 16 << 20},
	}
	if encTarget != "browser" {
		targets = append(targets, saver.Target{Name: "browser", Label: browserLabel, Adapter: browserStore, Capacity: 5 << 20})
	}
	u.current = currentDoc
	u.load = openDoc
	u.setRole = func(r canvas.Role) {
		state.Role = r
		stage.SetRole(r)
		if !r.CanEdit() {
			state.Mode = canvas.ModeView
		}
		renderCanvas()
		renderUI()
	}
	u.saver = saver.NewSession(&saver.Saver{Targets: targets}, currentDoc, openDoc)
	// Encryption is the page's own: the saver asks it before a document is made confidential and
	// to re-key a document's files when its level changes, and it seals confidential documents
	// into packs for the owner's keys while unlocked.
	u.saver.Saver.ConfidentialCheck, u.saver.Saver.Rekey = u.enc.store.Check, u.enc.store.Rekey
	u.saver.Author = state.Author // recorded as who saved each version
	u.saver.PackKeys = u.enc.store.PackKeys
	u.enc.swap = u.encSwap
	u.enc.dir.ask = u.saver.Asker.AskWith
	u.enc.dir.render = u.enc.render
	u.enc.dir.changed = u.syncServerTarget
	u.applyServerTarget() // a server confirmed on an earlier visit is a target from the start
	u.enc.dir.loadOwner() // now that the page can be drawn again, the owner key kept out of reach can be loaded
	u.watchEncConfig()
	u.saver.ProtectionAvailable = func(target string) bool { return target == encTarget && u.enc.store.Enabled() }
	u.saver.OnChange = func() {
		t, _ := u.saver.Where()
		rememberTarget(t)
		renderUI()
	}
	u.restoreFolder()
	u.saver.Deliver = transfer.Download
	u.saver.Pick = transfer.PickFile
	u.saver.Export = u.gatedExport
	u.saver.Link = func(kind, id string) string {
		if kind == "folder" {
			return share.FolderPath(pageBase(), id)
		}
		return share.DocPath(pageBase(), id)
	}
	u.saver.Copy = func(text string) {
		if nav := js.Global().Get("navigator"); !nav.Get("clipboard").IsUndefined() {
			nav.Get("clipboard").Call("writeText", text)
		}
	}
	encerr.Install()                   // encrypt errors in words
	components.CopyText = u.saver.Copy // the "Copy details" button of an error
	u.migrate = newMigrate(u, func() { renderUI() })
	u.console = newConsole(u, func() { renderUI() })
	u.pub = newPublic(u, func() { renderUI() })
	u.people = newPeople("you@example.com", func() { renderUI() })
	state.Comments.Roster = u.people // comments name people by handle, never by an address that is not theirs to show
	u.people.links.use, u.people.links.stop = u.useShareLink, u.stopShareLink
	u.people.remote = func() (*docstore.HTTPAdapter, string, bool) {
		if encTarget == "server" || u.saver.Target() != serverTarget || u.saver.ID() == "" {
			return nil, "", false
		}
		t, ok := u.serverStore()
		if !ok {
			return nil, "", false
		}
		a, ok := t.Adapter.(*docstore.HTTPAdapter)
		return a, u.saver.ID(), ok
	}
	if tok := takeFragment(); tok != "" { // a share link in the address is offered in the People tab, never applied by itself
		u.people.links.incoming, u.tab = tok, "people"
	}
	u.speech = newSpeech(func() { renderUI() })
	u.speech.key = u.speechKeyFor
	u.usage = newPrivacy(u.tab, &u.enc.dir)
	u.usage.render = func() { renderUI(); redraw() } // redraw refreshes #state
	u.fidelity.render = func() { renderUI() }
	go u.usage.run()

	setMode := func(m canvas.Mode) {
		state.Mode = state.Role.Mode(m)
		u.announce(T("a11y.mode."+state.Mode.String(), strings.ToUpper(state.Mode.String()[:1])+state.Mode.String()[1:]+" mode"))
		renderCanvas()
		renderUI()
	}
	run := func(cmd canvas.Command) {
		stage.RunCommand(cmd)
		redraw()
		refocus()
	}

	var domRoot *domrt.Root
	u.cfgx.afterFont = func() { stage.ForgetFonts(); redraw(); renderUI() }
	onPrefsSaved = func(p prefs) { u.pushPrefs(p, renderUI) }
	renderUI = func() {
		u.configTick(renderUI)
		u.usage.noteTab(u.tab)
		u.saver.DownloadFormats = downloadChoices(u.transfer.Exporters()) // the headings follow the language
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
			{Label: T("menu.view", "View"), Items: []components.MenuItem{
				{Label: "Edit mode", Checked: state.Mode == canvas.ModeEdit, OnSelect: toggleMode},
				{Label: "Hyphenate long words", Checked: state.Hyphenate, OnSelect: func() { state.Hyphenate = !state.Hyphenate; renderCanvas(); renderUI() }},
				{Label: T("menu.language", "Interface language"), Items: languageMenu(u, renderUI)},
				{Label: T("menu.prefs", "Display preferences"), Items: prefsMenu(u, renderUI)},
				{Label: "Two-page spread", Checked: state.Spread, OnSelect: func() { state.Spread = !state.Spread; renderCanvas(); renderUI() }},
			}},
			{Label: T("menu.file", "File"), Items: []components.MenuItem{
				{Label: "Save…", OnSelect: func() { u.tab = "save"; renderUI() }},
				{Label: T("menu.pageSetup", "Page setup…"), OnSelect: func() { u.tab = "page"; renderUI() }},
				{Label: "Import / Export…", OnSelect: func() { u.tab = "transfer"; renderUI() }},
				{Label: T("menu.migrate", "Migrate…"), OnSelect: func() { u.tab = "migrate"; renderUI() }},
				{Label: T("menu.console", "Console…"), OnSelect: func() { u.tab = "console"; renderUI() }},
				{Label: T("menu.public", "Share with a public link…"), OnSelect: func() { u.tab = "public"; renderUI() }},
			}},
			{Label: T("menu.edit", "Edit"), Items: components.CommandMenu(stage.Features.Commands(), keymap, mac, run)},
			{Label: T("menu.insert", "Insert"), Items: []components.MenuItem{
				{Label: "Word count…", OnSelect: func() { u.tab = "stats"; renderUI() }},
				{Label: "Share selection…", OnSelect: func() { u.tab = "share"; renderUI() }},
				{Label: T("menu.privacy", "Usage statistics…"), OnSelect: func() { u.tab = "privacy"; renderUI() }},
				{Label: "Symbol…", OnSelect: func() { u.tab = "symbols"; renderUI() }},
				{Label: "Comment…", Shortcut: components.FormatChord("Mod+Alt+N", mac), OnSelect: func() { run(canvas.CmdCommentAdd); u.tab = "comments"; renderUI() }},
				{Label: "Bookmark…", Shortcut: components.FormatChord("Mod+Alt+B", mac), OnSelect: func() { run(canvas.CmdBookmarkAdd); u.tab = "outline"; renderUI() }},
				{Label: "Footnote…", Shortcut: components.FormatChord("Mod+Alt+F", mac), OnSelect: func() { run(canvas.CmdFootnoteAdd); u.tab = "outline"; renderUI() }},
				{Label: "Index entry…", Shortcut: components.FormatChord("Mod+Alt+X", mac), OnSelect: func() { run(canvas.CmdIndexMark) }},
				{Label: "Table of contents", OnSelect: func() { run(canvas.CmdTOCInsert) }},
				{Label: "Index", OnSelect: func() { run(canvas.CmdIndexInsert) }},
				{Label: "Link…", Shortcut: components.FormatChord("Mod+K", mac), OnSelect: func() { run(canvas.CmdEditLink) }},
				{Label: T("menu.picture", "Picture…"), OnSelect: func() { u.tab = "images"; renderUI() }},
				{Separator: true},
				{Label: "Page break", Shortcut: components.FormatChord("Mod+Enter", mac), OnSelect: func() { run(canvas.CmdPageBreak) }},
				{Label: "Column break", Shortcut: components.FormatChord("Mod+Shift+Enter", mac), OnSelect: func() { run(canvas.CmdColumnBreak) }},
			}},
			{Label: "Format", Items: []components.MenuItem{
				{Label: "Bold", Shortcut: components.FormatChord("Mod+B", mac), OnSelect: func() { run(canvas.CmdBold) }},
				{Label: "Italic", Shortcut: components.FormatChord("Mod+I", mac), OnSelect: func() { run(canvas.CmdItalic) }},
				{Label: "Underline", Shortcut: components.FormatChord("Mod+U", mac), OnSelect: func() { run(canvas.CmdUnderline) }},
				{Label: T("menu.docLangSet", "Language of the document"), Items: documentLanguageMenu(u, renderUI)},
				{Label: T("menu.selLang", "Language of the selection"), Items: selectionLanguageMenu(u, func() *canvas.TextFlow { return flow }, redraw, refocus, renderUI)},
				{Label: T("menu.vertical", "Vertical text"), Checked: u.layout.Vertical, OnSelect: func() {
					u.layout.Vertical = !u.layout.Vertical
					if flow != nil {
						flow.Vertical = u.layout.Vertical
					}
					redraw()
					renderUI()
				}},
				{Separator: true},
				{Label: "Style editor", OnSelect: func() { u.tab = "format"; renderUI() }},
			}},
			{Label: T("menu.read", "Read aloud"), Items: readMenu(u, flow, renderUI)},
			{Label: "Help", Items: []components.MenuItem{
				{Label: "Keyboard shortcuts", OnSelect: func() { u.tab = "keys"; renderUI() }},
			}},
		}
		menubar := components.MenuBar(components.MenuBarProps{Entries: menus, Open: u.menu, OnOpen: func(i int) { u.menu = i; renderUI() }})

		toolbar := xango.Frag(
			formatToolbar(&state, u.caretFmt, cmds, keymap, mac, run),
			xango.El("button", []xango.Attr{xango.A("id", "mode"), xango.A("type", "button"), xango.A("class", "gj-keybindings-button"),
				xango.On("click", func(xango.Event) { toggleMode() })}, xango.Text("Mode: "+state.Mode.String()+" (click to switch)")),
			// the tabs of the side panel: one row for the keyboard (Tab enters it once, the arrow keys move
			// along it), laid out as before (display:contents)
			xango.El("div", []xango.Attr{xango.A("role", "tablist"), xango.A("aria-label", components.Translate("tab.list", "Side panel")), xango.A("style", "display:contents")},
				tabButton(u, "format", "Format", renderUI), tabButton(u, "styles", "Styles", renderUI), tabButton(u, "symbols", "Symbols", renderUI), tabButton(u, "keys", "Keys", renderUI),
				tabButton(u, "comments", "Comments", renderUI), tabButton(u, "review", "Review", renderUI), tabButton(u, "outline", "Outline", renderUI), tabButton(u, "check", "Check", renderUI), tabButton(u, "images", "Pictures", renderUI),
				tabButton(u, "find", "Find", renderUI), tabButton(u, "spell", "Spelling", renderUI), tabButton(u, "language", "Language", renderUI), tabButton(u, "stats", "Stats", renderUI), tabButton(u, "share", "Share", renderUI), tabButton(u, "people", "People", renderUI),
				tabButton(u, "migrate", "Migrate", renderUI), tabButton(u, "console", "Console", renderUI), tabButton(u, "public", "Public link", renderUI), tabButton(u, "page", "Page", renderUI), tabButton(u, "save", "Save", renderUI), tabButton(u, "transfer", "Files", renderUI), tabButton(u, "speech", "Speech", renderUI), tabButton(u, "privacy", "Privacy", renderUI), tabButton(u, "prefs", "Preferences", renderUI)),
		)

		roleButtons := []xango.Node{xango.El("span", []xango.Attr{xango.A("style", "font-size:12px;color:#6b7280")}, xango.Text("Role:"))}
		for _, r := range []canvas.Role{canvas.RoleOwner, canvas.RoleEditor, canvas.RoleViewer} {
			r := r
			class := "gj-keybindings-button"
			if state.Role.String() == r.String() {
				class += " gj-materialpicker-chip-on"
			}
			roleButtons = append(roleButtons, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", class), xango.A("data-role", r.String()),
				xango.On("click", func(xango.Event) {
					state.Role = r
					stage.SetRole(r)
					if !r.CanEdit() {
						state.Mode = canvas.ModeView
					}
					renderCanvas()
					renderUI()
				})}, xango.Text(r.String())))
		}
		roleRow := xango.El("div", []xango.Attr{xango.A("id", "roles"), xango.A("style", "display:flex;gap:6px;align-items:center;margin-bottom:0.8em")}, roleButtons...)

		var panel xango.Node
		switch u.tab {
		case "outline":
			panel = outlinePanel(&state, func() *canvas.TextFlow { return flow }, func() *canvas.ScrollView { return root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView) }, run, redraw, refocus, renderUI)
		case "images":
			panel = imagesPanel(u, &state, func() *canvas.TextFlow { return flow }, func() *canvas.ScrollView { return root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView) }, redraw, renderUI)
		case "check":
			panel = checkPanel(u, &state, func() *canvas.TextFlow { return flow }, func() *canvas.ScrollView { return root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView) }, redraw, renderUI)
		case "review":
			panel = reviewPanel(u, &state, func() *canvas.TextFlow { return flow }, redraw, refocus, renderUI)
		case "comments":
			panel = commentsPanel(u, &state, func() *canvas.TextFlow { return flow }, run, redraw, refocus, renderUI)
		case "styles":
			if flow == nil {
				break
			}
			current := ""
			if fs := flow.ParagraphFormats(); len(fs) > 0 {
				current = fs[0].Style
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
			panel = xango.Frag(panel, themePanel(u, &state, renderCanvas, renderUI))
		case "find":
			panel = xango.Frag(findPanel(u, &state, func() *canvas.TextFlow { return flow }, func() { root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView).RevealPending() }, redraw, renderUI), u.searchAllPanel(renderUI))
		case "spell":
			if flow != nil {
				panel = spellPanel(u, &state, func() *canvas.TextFlow { return flow }, func() { root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView).RevealPending() }, redraw, renderUI)
			}
		case "language":
			if flow != nil {
				panel = langPanel(u, &state, func() *canvas.TextFlow { return flow }, func() { root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView).RevealPending() }, redraw, renderUI)
			}
		case "migrate":
			panel = components.Migrate(u.migrate.Props(func() { pickWordFiles(u) }))
		case "console":
			panel = components.ConsolePanel(u.console.Props())
		case "public":
			panel = publicPanel(u.pub)
		case "page":
			panel = xango.Frag(components.PageSetupPanel(components.PageSetupProps{Setup: state.Setup, OnChange: func(p components.PageSetup) {
				state.Setup = p
				renderCanvas()
				renderUI()
			}}), printPanel(u, func() { renderCanvas(); renderUI() }))
		case "stats":
			if flow != nil {
				panel = statsPanel(flow.Stats(), state.Pages)
			}
		case "people":
			panel = xango.Frag(u.people.panel(u.saver.Asker.Ask), u.encPeopleOrNone(), u.saver.Asker.Dialog())
		case "speech":
			panel = u.speech.panel(speechHost{
				Caret: func() int {
					if flow == nil {
						return 0
					}
					return flow.Cursors()[0].Caret
				},
				Insert: func(text string) bool {
					if flow != nil && flow.InsertText(text) {
						redraw()
						return true
					}
					return false
				},
				Selected: func() string {
					if flow == nil {
						return ""
					}
					from, to := flow.Selection()
					return string([]rune(flow.EditText())[from:to])
				},
				Slice: func(from, to int) string {
					if flow == nil {
						return ""
					}
					rs := []rune(flow.EditText())
					if from < 0 || to > len(rs) || from > to {
						return ""
					}
					return string(rs[from:to])
				},
				Replace: func(from, to int, text string) {
					if flow != nil {
						flow.ReplaceRange(from, to, text)
						redraw()
					}
				},
			})
		case "privacy":
			panel = u.usage.panel(u)
		case "prefs":
			panel = prefsPanel(u, renderUI)
		case "share":
			if flow != nil {
				panel = sharePanel(u, flow, origin, renderUI)
			}
		case "save":
			panel = xango.Frag(u.encPanel(), u.folderPanel(), components.Saver(u.saver.Props()))
		case "transfer":
			var imports, exports []components.SaverChoice
			for _, i := range u.transfer.Importers() {
				imports = append(imports, components.SaverChoice{Name: i.Format().Name, Label: i.Format().Label})
			}
			for _, e := range u.transfer.Exporters() {
				exports = append(exports, components.SaverChoice{Name: e.Format().Name, Label: e.Format().Label})
			}
			// What the chosen format loses is shown above the buttons, before anything is exported.
			fidelity := components.FidelityPanel(u.fidelity.props(currentDoc(), exports, func(name string) {
				u.fidelity.format = name
				renderUI()
			}))
			panel = xango.Frag(
				fidelity,
				components.Transfer(components.TransferProps{
					Imports: imports, Exports: exports,
					// The formats are done by the file worker, which a request waits for: both run in a goroutine
					// of their own, never in the handler JavaScript called.
					OnExport: func(name string) {
						u.fidelity.format = name
						doc := currentDoc()
						u.message = T("files.working", "Working…")
						go func() {
							file, data, mime, err := u.gatedExport(name, doc)
							if err != nil {
								u.message = err.Error()
							} else {
								transfer.Download(file, mime, data)
								u.message = fmt.Sprintf("Exported %s (%d bytes)", file, len(data))
								rep, _ := remote.Fidelity(callFiles, name, doc)
								if n := len(rep.Losses); n > 0 {
									u.message += ". " + Tn("fidelity.exported", "%d thing was left out or simplified in this format; the list is above.", "%d things were left out or simplified in this format; the list is above.", n)
								}
							}
							renderUI()
						}()
						renderUI()
					},
					OnImport: func(name string) {
						transfer.PickFile(u.transfer.Accept(), func(file string, data []byte, err error) {
							go func() {
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
								u.saver.Detach() // what was imported is not the saved document that was open
								openDoc(d)
								u.fidelity.noteImport(name, file, data) // after openDoc, which shows the report the document came with
							}()
						})
					},
				}),
				xango.El("p", []xango.Attr{xango.A("id", "transfer-message")}, xango.Text(u.message)),
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
					keepKeymap()
					renderUI()
				},
				OnClear: func(c canvas.Command) {
					stage.Rebind(c, "")
					u.kbMessage = titleOf(cmds, c) + " has no shortcut now"
					keepKeymap()
					renderUI()
				},
				OnReset: func(c canvas.Command) {
					resetBinding(stage, defaults, c)
					u.kbMessage = titleOf(cmds, c) + " is back to its default"
					keepKeymap()
					renderUI()
				},
				OnResetAll: func() {
					stage.ResetKeymap()
					u.kbMessage = "All shortcuts are back to their defaults"
					keepKeymap()
					renderUI()
				},
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
						if f.Source != myFonts {
							lib.Add(f) // downloaded only when the text uses it
						}
						u.open = false
						u.recent = append([]string{f.Family}, slices.DeleteFunc(slices.Clone(u.recent), func(s string) bool { return s == f.Family })...)
						renderUI()
					},
				},
			})
		}
		if u.tab == "format" || u.tab == "" {
			var rows []xango.Node
			for _, l := range u.fontLog {
				rows = append(rows, xango.El("li", nil, xango.Text(l)))
			}
			panel = xango.Frag(panel, xango.El("ul", []xango.Attr{xango.A("id", "font-log"), xango.A("style", "font-size:12px;color:#6b7280")}, rows...))
		}
		topRow := roleRow
		if u.live != nil {
			topRow = livePresence(u, func() *canvas.TextFlow { return flow }, renderUI)
		}
		// The side panel's own heading, so that the titles inside it (levels 3 and 4) follow the page's
		// level 2 without a skip.
		tabID := u.tab
		if tabID == "" {
			tabID = "format"
		}
		panelHead := xango.El("h3", []xango.Attr{xango.A("class", "gj-sr-only")}, xango.Text(components.Translate("tab."+tabID, strings.ToUpper(tabID[:1])+tabID[1:])))
		side := xango.Frag(topRow, panelHead, panel,
			xango.El("div", []xango.Attr{xango.A("id", "link-preview"), xango.A("style", "margin-top:1em")},
				components.LinkPreview(components.LinkPreviewProps{
					URL: u.hoverLink, Title: "Gojiberry", Description: "Go components that render to HTML, the DOM, Gio and a canvas.",
					SiteName: "GitHub",
				})))

		var stats canvas.FlowStats
		if flow != nil {
			stats = flow.Stats()
		}
		bar := components.StatusBar(statusBar(flow, stats, state.Pages, state.Mode, state.Setup, u.saver.SaveState(), u.syncItem()))
		node := components.DocumentView(components.DocumentViewProps{
			Title: u.title, OnTitle: func(t string) {
				u.title = t
				if strings.Contains(u.layout.Header+u.layout.Footer, "{title}") {
					renderCanvas()
				}
				renderUI()
			},
			MenuBar: menubar, Toolbar: toolbar,
			Canvas: xango.Frag(
				readBar(u, renderUI),
				xango.El("div", []xango.Attr{xango.A("id", "canvas-host")}),
				u.suggestionsNode(renderUI),
				components.AccessibleText(components.AccessibleTextProps{
					Blocks: u.mirrorBlocks(flow, root), Comments: mirrorComments(flow), Selection: selectedText(flow), Lang: u.docLang,
					OnGoto: goToHeading(func() *canvas.TextFlow { return flow }, func() *canvas.ScrollView { return root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView) }, redraw),
				}),
				components.Announcer(u.announcement),
				components.TypingLive(u.typingMsg, u.typingN),
				u.noteEd.Dialog()),
			Sidebar: &side, StatusBar: &bar,
		})
		labelCanvas(canvasEl.Element, inputEl.Element)
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
		u.noteTyping(func() *canvas.TextFlow { return flow }, renderUI)
		// What the canvas shows of the selection feeds the style editor.
		if flow != nil && flow.Editing() { // the toolbar's bold, italic and the like follow the caret
			cf := flow.CaretFormatLast()
			if flow.HasSelection() { // over a selection, what all of it has in common
				cf = flow.CommonFormat()
			}
			if cf != u.caretFmt {
				u.caretFmt = cf
				renderUI()
			}
		}
		if flow != nil && flow.Editing() && flow.HasSelection() {
			if f := flow.CommonFormat(); f != u.format {
				u.format = f
				renderUI()
			}
		}
		if u.spell.enabled && flow != nil {
			if text := flow.EditText(); text != u.spell.text { // the text changed: check it again
				u.refreshSpell(flow)
				if u.tab == "spell" {
					renderUI()
				}
			}
		}
		if u.find.open && flow != nil {
			if text := flow.EditText(); text != u.find.text { // the text changed under the matches
				u.refreshFind(flow, nil, false)
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
		footnotes, indexTerms := []string{}, []string{}
		tocRows := -1
		headers, footers, watermarks := []string{}, []string{}, []string{}
		spread := false
		if ps, ok := root.Find(cxango.TagPage, 0).(*canvas.PageSet); ok {
			spread = ps.Spread
			for i := range ps.Pages {
				h, w := "", ""
				if hf := ps.ResolvedHeader(i); hf != nil {
					h = hf.Text
					if hf.HasParts() {
						h = strings.Join(hf.Parts[:], "\t")
					}
				}
				f := ""
				if hf := ps.ResolvedFooter(i); hf != nil && hf.HasParts() {
					f = strings.Join(hf.Parts[:], "\t")
				}
				footers = append(footers, f)
				if wm := ps.ResolvedWatermark(i); wm != nil {
					w = wm.Text
				}
				headers, watermarks = append(headers, h), append(watermarks, w)
			}
		}
		if flow != nil {
			cursors, editing = flow.Cursors(), flow.Editing()
			text = flow.EditText()
			sv := root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView)
			scrollY = sv.ScrollY
			current = flow.OutlineIndexAt(flow.Cursors()[0].Caret)
			for _, e := range flow.Outline() {
				outline = append(outline, fmt.Sprintf("%d:%s", e.Level, e.Text))
			}
			for _, b := range flow.Bookmarks() {
				bookmarks = append(bookmarks, b.Name)
			}
			for _, n := range flow.FootnoteList() {
				footnotes = append(footnotes, n.Text)
			}
			for _, e := range flow.IndexEntries() {
				indexTerms = append(indexTerms, e.Term)
			}
			for _, it := range flow.ContentItems() {
				if in, ok := it.(canvas.Inline); ok {
					if toc, ok := in.Object.(*canvas.TOCObject); ok {
						tocRows = len(toc.Entries())
					}
				}
			}
			for i, ln := range flow.Layout().Lines {
				if i < 24 {
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
						comment = map[string]any{"id": strings.SplitN(r.Comment, ",", 2)[0], "x": ln.X + r.X + 4 + sv.X - sv.ScrollX, "y": ln.Y + 5 + sv.Y - sv.ScrollY}
					}
					if r.Link != "" && link == nil {
						link = map[string]any{"url": r.Link, "x": ln.X + r.X + 4 + sv.X - sv.ScrollX, "y": ln.Y + 5 + sv.Y - sv.ScrollY}
					}
				}
			}
		}
		if sig := fmt.Sprint(outline, bookmarks, current, state.Comments.Len(), footnotes, proposalsSigFor(u.tab, flow)); (u.tab == "outline" || u.tab == "comments" || u.tab == "review") && sig != u.navSig {
			u.navSig = sig
			renderUI()
		}
		if u.live != nil && state.Comments != nil {
			// comments others add arrive with the document, not with the session's state
			if sig := commentSignature(state.Comments); sig != u.commentSig {
				u.commentSig = sig
				u.noteCollab(renderUI)
				renderUI()
			}
		}
		if sig := u.mirrorSignature(flow); sig != u.mirrorSig {
			u.mirrorSig = sig // the caret is in another block, or the selection or the comments changed
			renderUI()
		}
		if flow != nil {
			if cs := flow.Cursors()[0]; fmt.Sprint(cs.Anchor, cs.Caret) != u.mirrorAt {
				u.mirrorAt = fmt.Sprint(cs.Anchor, cs.Caret)
				u.followCaret(flow, u.a11yCache)
			}
		}
		var stats canvas.FlowStats
		columnCount := 0
		columnCfg := map[string]any{}
		if flow != nil {
			stats = flow.Stats()
			cfg := flow.ColumnsAt(flow.Cursors()[0].Caret)
			columnCount = cfg.Count
			columnCfg = map[string]any{"count": cfg.Count, "gap": cfg.Gap, "widths": cfg.Widths}
		}
		if sig := fmt.Sprint(stats); sig != u.statsSig {
			u.statsSig = sig
			renderUI()
		}
		b, _ := json.Marshal(map[string]any{
			"hyphenate": state.Hyphenate, "columns": columnCount, "columnConfig": columnCfg, "language": map[string]any{"translate": u.lang.translator != nil, "grammar": u.lang.grammar != nil, "issues": len(u.lang.issues), "translated": u.lang.translated, "error": u.lang.err, "busy": u.lang.busy},
			"stats": statsState(stats), "share": u.shareLink, "drafts": len(u.drafts), "people": u.people.list(), "links": u.people.links.state(), "usage": u.usage.state(), "encryption": u.enc.state(), "spell": map[string]any{"enabled": u.spell.enabled, "checked": u.spell.text != "", "words": len(u.spell.words), "spans": len(u.spell.spans)}, "find": map[string]any{"open": u.find.open, "count": len(u.find.matches), "current": u.find.cur, "error": u.find.err, "query": u.find.opts.Query},
			"mode": state.Mode.String(), "features": names, "commands": len(stage.Features.Commands()),
			"elements": cxango.Elements(), "editing": editing, "cursors": cursors, "text": text,
			"pages": state.Pages, "pageSetup": map[string]any{"label": state.Setup.Label(), "width": state.PageWidth(), "height": state.PageHeight(), "margins": state.Setup.Margins}, "saveState": u.saver.SaveState(), "persisted": u.persisted, "format": u.format, "markers": markers, "dropCaps": dropCaps, "lineHeights": lineHeights,
			"link": link, "headers": headers, "footers": footers, "watermarks": watermarks, "spread": spread, "hoverLink": u.hoverLink, "role": state.Role.String(), "comments": state.Comments.Len(), "hoverComment": u.hoverComment, "historyLimit": canvas.HistoryLimit(), "live": liveState(u, flow), "scrollY": scrollY, "outline": outline, "bookmarks": bookmarks, "footnotes": footnotes, "tocRows": tocRows, "indexTerms": indexTerms, "target": fmt.Sprintf("%T", stage.EditTarget()), "canPrompt": stage.Prompt != nil, "hasComment": stage.Features.Has(canvas.CmdCommentAdd), "comment": comment, "tab": u.tab, "menu": u.menu, "title": u.title,
			"fontLog": u.fontLog, "fontsAdded": fontKeys(lib), "ghost": u.ghostSuffix(),
		})
		doc.GetElementByID("state").SetTextContent(string(b))
	}

	initLocale()
	applyPrefs(curPrefs)
	state.NoAutoClose, state.TextScale, state.PeerPalette, state.StrongCaret = curPrefs.Close == "off", curPrefs.textScale(), curPrefs.peerPalette(), curPrefs.Contrast == "more"
	onLocale = renderUI
	onPrefs = func() {
		nc, ts, pp, sc := curPrefs.Close == "off", curPrefs.textScale(), curPrefs.peerPalette(), curPrefs.Contrast == "more"
		if nc != state.NoAutoClose || ts != state.TextScale || len(pp) != len(state.PeerPalette) || sc != state.StrongCaret {
			state.NoAutoClose, state.TextScale, state.PeerPalette, state.StrongCaret = nc, ts, pp, sc
			renderCanvas()
		}
	}
	u.setupLanguage(func(k string) string {
		q := js.Global().Get("URLSearchParams").New(js.Global().Get("location").Get("search"))
		if q.Call("has", k).Bool() {
			return q.Call("get", k).String()
		}
		return ""
	})
	scrollToMatch := func() { root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView).RevealPending() }
	state.FindFeature = canvas.FindFeature{
		OnOpen: func(replace bool) {
			u.find.open, u.find.replace, u.tab = true, replace, "find"
			if flow != nil {
				if sel := flow.SelectedText(); sel != "" && len([]rune(sel)) <= 80 && !strings.ContainsAny(sel, "\n") {
					u.find.opts.Query = sel // search for what is selected
				}
			}
			u.refreshFind(flow, scrollToMatch, true)
			redraw()
			renderUI()
		},
		OnStep: func(dir int) {
			if !u.find.open {
				u.find.open, u.tab = true, "find"
				u.refreshFind(flow, scrollToMatch, true)
			} else {
				u.stepFind(flow, scrollToMatch, dir)
			}
			redraw()
			renderUI()
		},
	}
	u.stateRef = &state
	state.CompleteFeature = canvas.CompleteFeature{OnOpen: func() { u.openSuggestions(flow, stage, canvasEl.Element, false, renderUI) }}
	state.Review = canvas.Review{
		OnMode: func(on bool) {
			if on {
				u.announce(T("review.modeOn", "Suggesting: your edits are suggestions"))
			} else {
				u.announce(T("review.modeOff", "Editing: your edits are made directly"))
			}
			renderUI()
		},
		OnSettle: func(accepted bool, p canvas.Proposal) {
			if accepted {
				u.announce(T("review.accepted", "Suggestion accepted"))
			} else {
				u.announce(T("review.rejected", "Suggestion rejected"))
			}
			renderUI()
		},
	}
	u.sugg.reopen = func(quiet bool) { u.openSuggestions(flow, stage, canvasEl.Element, quiet, renderUI) }
	u.watchMirrorSelection(func() *canvas.TextFlow { return flow }, func() *canvas.ScrollView { return root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView) }, redraw, renderUI)
	reading.mark = func(sf, st, wf, wt int, reveal bool) { // the sentence and word being read aloud, on the canvas
		if flow == nil {
			return
		}
		if sf < 0 {
			flow.ClearReadMarks()
		} else {
			flow.SetReadMarks(sf, st, wf, wt)
			if reveal {
				flow.Reveal(sf)
				if sv, ok := root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView); ok {
					sv.RevealPending()
				}
			}
		}
		redraw()
	}
	u.ghost.flow, u.ghost.redraw = func() *canvas.TextFlow { return flow }, redraw
	u.ghost.refresh = func() { u.refreshGhost(flow, redraw, renderUI) }
	u.watchGhostPointer()
	u.watchSuggestionKeys(inputEl.Element, func() *canvas.TextFlow { return flow }, redraw, renderUI)
	state.Live = liveProps(u, &state, origin, renderCanvas, renderUI)
	renderCanvas()
	renderUI()
	go u.pub.openLink(func() bool { return flow != nil })
	go openSharedSelection(u, func() *canvas.TextFlow { return flow }, func() *canvas.ScrollView { return root.Find(cxango.TagScrollView, 0).(*canvas.ScrollView) }, redraw, renderUI)
	go func() {
		// Ask the browser to keep what is saved in it (it may delete a site's storage when
		// the disk is short); the answer is in #state.
		u.persisted = docstore.RequestPersistence()
		renderUI()
	}()
	go func() {
		ctx := context.Background()
		_ = u.saver.Refresh(ctx)
		// A page opened on /f/{folder} shows that folder.
		if _, folder := share.SplitFolderPath(js.Global().Get("location").Get("pathname").String()); folder != "" {
			_ = u.saver.OpenFolder(ctx, folder)
		}
	}()
	canvas.LoadImage("data:image/svg+xml;base64,"+base64.StdEncoding.EncodeToString([]byte(svg)),
		func(src *canvas.HTMLImage) {
			state.Image = src
			renderCanvas()
		}, nil)
	select {}
}
