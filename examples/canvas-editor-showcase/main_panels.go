//go:build js && wasm

package main

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/gojiberry/xango"
	"slices"
	"strconv"
	"strings"
	"syscall/js"
	"time"
)

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

// tabIcons are the icons of the sidebar tabs (Material Symbols names, as in opendocs).
var tabIcons = map[string]string{
	"format": "tune", "styles": "style", "symbols": "functions", "keys": "keyboard", "comments": "comment", "review": "rate_review", "outline": "toc", "check": "task_alt", "images": "image",
	"find": "search", "spell": "spellcheck", "language": "language", "stats": "numbers", "share": "ios_share", "people": "account_circle",
	"migrate": "upload_file", "page": "margin", "save": "description", "transfer": "input", "privacy": "lock", "speech": "mic", "prefs": "settings",
}

// sideTabs is the order of the side panel's tabs, which the arrow keys follow.
var sideTabs = []string{"format", "styles", "symbols", "keys", "comments", "review", "outline", "check", "images", "find", "spell", "language", "stats", "share", "people", "migrate", "page", "save", "transfer", "speech", "privacy", "prefs"}

func tabButton(u *ui, tab, label string, rerender func()) xango.Node {
	class := "gj-keybindings-button"
	selected := u.tab == tab || (u.tab == "" && tab == "format") // Format is the tab shown first
	if u.tab == tab {
		class += " gj-materialpicker-chip-on"
	}
	index := slices.Index(sideTabs, tab)
	kids := []xango.Node{xango.Text(components.Translate("tab."+tab, label))}
	if icon := tabIcons[tab]; icon != "" {
		kids = append([]xango.Node{components.Material(icon)}, kids...)
	}
	return xango.El("button", []xango.Attr{
		xango.A("type", "button"), xango.A("class", class), xango.A("data-tab", tab),
		xango.A("role", "tab"), xango.A("aria-selected", map[bool]string{true: "true", false: "false"}[selected]), components.TabIndex(selected),
		xango.On("click", func(xango.Event) { u.tab = tab; rerender() }),
		components.TabKeys(index, len(sideTabs), func(i int) { u.tab = sideTabs[i]; rerender() }),
	}, kids...)
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
func commentsPanel(u *ui, state *State, flow func() *canvas.TextFlow, run func(canvas.Command), redraw, refocus, renderUI func()) xango.Node {
	fl := flow()
	excerpts := map[string]string{}
	if fl != nil {
		excerpts = fl.CommentExcerpts()
	}
	set := state.Comments
	add := xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-keybindings-button"), xango.A("data-action", "add-comment"),
		xango.On("click", func(xango.Event) { run(canvas.CmdCommentAdd); redraw(); renderUI() })}, xango.Text("Comment on the selection"))
	touch := func() {
		if fl != nil {
			fl.Touch() // tell a shared document about it
		}
	}
	return xango.Frag(add, xango.El("div", []xango.Attr{xango.A("style", "margin-top:0.8em")}, components.CommentList(components.CommentListProps{
		Comments: set.List(), Excerpts: excerpts, Filter: u.commentFilter, Selected: u.selComment,
		CanEdit: state.Role.CanEdit(), CanReply: state.Role.CanComment(), Drafts: u.drafts,
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
			u.sealComments()
			touch()
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
			u.sealComments()
			touch()
			renderUI()
		},
		OnFilter: func(f string) { u.commentFilter = f; renderUI() },
	})))
}

// outlinePanel shows the headings (click to go there) and the bookmarks.
func outlinePanel(state *State, flow func() *canvas.TextFlow, scroller func() *canvas.ScrollView, run func(canvas.Command), redraw, refocus, renderUI func()) xango.Node {
	fl := flow()
	if fl == nil {
		return xango.Frag()
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
	return xango.Frag(
		tocControls(fl, redraw, renderUI),
		components.Outline(components.OutlineProps{
			Entries: fl.Outline(), Current: fl.OutlineIndexAt(caret),
			OnSelect: func(_ int, e canvas.OutlineEntry) { goTo(e.Start) },
		}),
		xango.El("div", []xango.Attr{xango.A("style", "margin-top:1em")}, components.FootnoteList(components.FootnoteListProps{
			Entries: fl.FootnoteList(), CanEdit: state.Role.CanEdit(),
			OnGoto: func(e canvas.FootnoteEntry) {
				fl.GotoOffset(e.Offset)
				scroller().RevealPending()
				redraw()
				renderUI()
			},
			OnEdit: func(e canvas.FootnoteEntry) {
				if v := js.Global().Call("prompt", "Footnote", e.Text); !v.IsNull() && v.String() != "" {
					fl.FootnoteSet().Add(canvas.Footnote{ID: e.ID, Text: v.String()})
					fl.Touch() // tell a shared document about it
					redraw()
					renderUI()
				}
			},
			OnDelete: func(e canvas.FootnoteEntry) {
				fl.SetCursors([]canvas.Cursor{canvas.At(e.Offset + 1)})
				run(canvas.CmdFootnoteDelete)
				renderUI()
			},
		})),
		xango.El("div", []xango.Attr{xango.A("style", "margin-top:1em")}, components.BookmarkList(components.BookmarksProps{
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

// tocControls is the look of the table of contents in the document, when it has one: a dotted leader or none, and how
// many heading levels it lists. Nothing shows while the document has no table of contents.
func tocControls(fl *canvas.TextFlow, redraw, renderUI func()) xango.Node {
	var toc *canvas.TOCObject
	for _, it := range fl.ContentItems() {
		if in, ok := it.(canvas.Inline); ok {
			if t, ok := in.Object.(*canvas.TOCObject); ok {
				toc = t
				break
			}
		}
	}
	// One element always, so the panel keeps its order as the table comes and goes.
	box := func(children ...xango.Node) xango.Node {
		return xango.El("div", []xango.Attr{xango.A("class", "gj-toc-controls")}, children...)
	}
	if toc == nil {
		return box()
	}
	changed := func() { fl.Touch(); redraw(); renderUI() }
	depth := strconv.Itoa(toc.Depth)
	return box(
		xango.El("h4", nil, xango.Text(T("outline.contents", "Table of contents"))),
		selectOf("toc-style", T("outline.contentsStyle", "Style"), toc.Style, [][2]string{
			{canvas.TOCDots, T("outline.styleDots", "Dotted leaders")},
			{canvas.TOCPlain, T("outline.stylePlain", "Plain")},
		}, func(v string) { toc.Style = v; changed() }),
		selectOf("toc-depth", T("outline.contentsDepth", "Levels"), depth, [][2]string{
			{"0", T("outline.depthAll", "All levels")},
			{"1", T("outline.depth1", "Heading 1 only")},
			{"2", T("outline.depth2", "Headings 1 and 2")},
		}, func(v string) { n, _ := strconv.Atoi(v); toc.Depth = n; changed() }),
	)
}
