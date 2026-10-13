//go:build js && wasm

package main

import (
	"fmt"
	"strconv"
	"syscall/js"
	"time"
	"unicode/utf16"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/components"
)

// The screen-reader copy of the document (components.AccessibleText) and the canvas are two views of
// one text. This file keeps their positions together: the block with the editor's caret is marked in the
// copy (and, when the keyboard is not in the editor, the copy's own selection follows the caret), and a
// selection made in the copy by a screen reader's cursor moves the editor's caret and selection.

// mirrorBlocks is the document's blocks for the copy, with the caret's block marked.
func (u *ui) mirrorBlocks(flow *canvas.TextFlow, root *cxango.Root) []components.A11yBlock {
	blocks := u.a11yBlocks(flow, u.title)
	if flow != nil {
		blocks = components.MarkCaret(blocks, flow.Cursors()[0].Caret)
	}
	return append(append([]components.A11yBlock(nil), blocks...), a11yObjects(root)...)
}

// mirrorComments are the document's comments as the copy lists them.
func mirrorComments(flow *canvas.TextFlow) []components.A11yComment {
	if flow == nil || flow.Comments == nil {
		return nil
	}
	excerpts := flow.CommentExcerpts()
	var out []components.A11yComment
	for _, c := range flow.Comments.List() {
		ac := components.A11yComment{ID: c.ID, Author: c.Author, Text: canvas.CommentSummary(c.Text, 1000), Resolved: c.Resolved}
		if ex := excerpts[c.ID]; ex != "" {
			ac.Excerpt = canvas.CommentSummary(ex, 200)
		}
		for _, r := range c.Replies {
			ac.Replies = append(ac.Replies, components.A11yReply{Author: r.Author, Text: canvas.CommentSummary(r.Text, 1000)})
		}
		out = append(out, ac)
	}
	return out
}

// mirrorSignature changes when the caret moves to another block of the copy, or the selection or the
// comments change, which is when the copy has to be drawn again.
func (u *ui) mirrorSignature(flow *canvas.TextFlow) string {
	if flow == nil {
		return ""
	}
	cs := flow.Cursors()[0]
	blocks := u.a11yCache
	sig := strconv.Itoa(components.A11yBlockAt(blocks, cs.Caret)) + ":" + strconv.FormatBool(cs.Anchor != cs.Caret) + ":"
	if flow.Comments != nil {
		for _, c := range flow.Comments.List() {
			sig += c.ID + strconv.Itoa(len(c.Replies)) + strconv.FormatBool(c.Resolved) + c.Text + ";"
		}
	}
	return sig
}

// goToHeading is what activating an entry of the outline does: the heading takes the keyboard focus
// (so a screen reader reads it) and the editor's caret goes to it.
func goToHeading(flow func() *canvas.TextFlow, scroller func() *canvas.ScrollView, redraw func()) func(string, int, bool) {
	return func(id string, start int, mapped bool) {
		if el := js.Global().Get("document").Call("getElementById", id); !el.IsNull() {
			el.Call("focus", map[string]any{"preventScroll": true})
		}
		if fl := flow(); fl != nil && mapped {
			fl.GotoOffset(start)
			scroller().RevealPending()
			redraw()
		}
	}
}

func mirrorRoot() js.Value { return js.Global().Get("document").Call("getElementById", "a11y-text") }

// textWithoutHints is the text of an element or a fragment as it is in the document, without the words that
// are there only for a reader ("Suggested addition by Ana:").
func textWithoutHints(n js.Value) string {
	hints := n.Call("querySelectorAll", ".gj-sr-only")
	for i := hints.Length() - 1; i >= 0; i-- {
		hints.Index(i).Call("remove")
	}
	return n.Get("textContent").String()
}

// mirrorPosition turns a position in the copy (a DOM node and the offset in it) into an offset of the
// editor's text stream; ok is false if the position is not in a mapped block.
func mirrorPosition(node js.Value, offset int) (int, bool) {
	root := mirrorRoot()
	if root.IsNull() || node.IsNull() || node.IsUndefined() || !root.Call("contains", node).Bool() {
		return 0, false
	}
	el := node
	if node.Get("nodeType").Int() != 1 {
		el = node.Get("parentElement")
	}
	block := el.Call("closest", "[data-gj-start]")
	if block.IsNull() {
		return 0, false
	}
	start, err := strconv.Atoi(block.Call("getAttribute", "data-gj-start").String())
	if err != nil {
		return 0, false
	}
	b := components.A11yBlock{Mapped: true, Start: start, Object: block.Call("hasAttribute", "data-gj-object").Bool()}
	if v := block.Call("getAttribute", "data-gj-lead"); !v.IsNull() {
		b.Lead, _ = strconv.Atoi(v.String())
	}
	if b.Object {
		return b.StreamOffset(""), true
	}
	doc := js.Global().Get("document")
	r := doc.Call("createRange")
	r.Call("setStart", block, 0)
	r.Call("setEnd", node, offset)
	before := textWithoutHints(r.Call("cloneContents"))
	b.Text = textWithoutHints(block.Call("cloneNode", true))
	return b.StreamOffset(before), true
}

// watchMirrorSelection makes a selection made in the copy (the way a screen reader's cursor moves through
// it) the editor's caret and selection.
func (u *ui) watchMirrorSelection(flow func() *canvas.TextFlow, scroller func() *canvas.ScrollView, redraw, renderUI func()) {
	js.Global().Get("document").Call("addEventListener", "selectionchange", js.FuncOf(func(js.Value, []js.Value) any {
		fl := flow()
		// selectionchange is queued: it arrives after followCaret has returned, so the flag alone does not
		// catch the change the page made itself. Ignore what comes right after the page wrote the selection.
		if fl == nil || u.mirrorWriting || time.Since(u.mirrorWroteAt) < 150*time.Millisecond {
			return nil
		}
		sel := js.Global().Call("getSelection")
		if sel.IsNull() || sel.Get("rangeCount").Int() == 0 {
			return nil
		}
		anchor, ok := mirrorPosition(sel.Get("anchorNode"), sel.Get("anchorOffset").Int())
		if !ok {
			return nil // the selection is somewhere else (in the editor's own field, say)
		}
		caret, ok := mirrorPosition(sel.Get("focusNode"), sel.Get("focusOffset").Int())
		if !ok {
			caret = anchor
		}
		cs := fl.Cursors()[0]
		if cs.Anchor == anchor && cs.Caret == caret {
			return nil
		}
		// The copy is drawn again when the caret moves to another block, and a selection in the nodes that were
		// replaced collapses to where they were: that arrives a moment after the page wrote it, as if the
		// reader's cursor had moved to the start. A collapse onto an end of the selection the page wrote a
		// moment ago is that, not a move: the selection is put back.
		if cs.Anchor != cs.Caret && anchor == caret && (anchor == cs.Anchor || anchor == cs.Caret) && time.Since(u.mirrorWroteAt) < 3*time.Second {
			u.followCaret(fl, u.a11yCache)
			return nil
		}
		u.mirrorAt = fmt.Sprint(anchor, caret) // the copy already has this selection: do not put it there again
		fl.GotoOffset(caret)
		if anchor != caret {
			fl.SetSelection(anchor, caret)
		}
		scroller().RevealPending()
		redraw()
		renderUI()
		return nil
	}))
}

// followCaret puts the copy's own selection where the editor's caret is, so a screen reader's cursor in
// the copy goes with it. It does nothing while the keyboard is in a field (the editor's own, which a
// screen reader follows by itself, or any other): moving the selection then would take it from the field.
func (u *ui) followCaret(fl *canvas.TextFlow, blocks []components.A11yBlock) {
	doc := js.Global().Get("document")
	if ae := doc.Get("activeElement"); !ae.IsNull() && !ae.IsUndefined() {
		switch ae.Get("tagName").String() {
		case "INPUT", "TEXTAREA", "SELECT":
			return
		}
		if ae.Get("isContentEditable").Bool() {
			return
		}
	}
	cs := fl.Cursors()[0]
	node, off, ok := streamToDOM(blocks, cs.Caret)
	if !ok {
		return
	}
	anode, aoff := node, off
	if cs.Anchor != cs.Caret {
		if n, o, ok := streamToDOM(blocks, cs.Anchor); ok {
			anode, aoff = n, o
		}
	}
	u.mirrorWriting = true
	defer func() { u.mirrorWriting = false }()
	u.mirrorWroteAt = time.Now()
	js.Global().Call("getSelection").Call("setBaseAndExtent", anode, aoff, node, off)
}

// streamToDOM is the position in the copy (a text node and the offset in it) of a stream offset.
func streamToDOM(blocks []components.A11yBlock, offset int) (js.Value, int, bool) {
	at := components.A11yBlockAt(blocks, offset)
	if at < 0 {
		return js.Value{}, 0, false
	}
	b := blocks[at]
	el := mirrorRoot().Call("querySelector", `[data-gj-start="`+strconv.Itoa(b.Start)+`"]`)
	if el.IsNull() {
		return js.Value{}, 0, false
	}
	node, off := domPosition(el, b.OffsetInBlock(offset))
	if node.IsNull() {
		return js.Value{}, 0, false
	}
	return node, off, true
}

// domPosition finds the text node of el, and the offset in it, that is n runes into el's text (without
// the words that are only for a reader). A block with no text gives the element itself.
func domPosition(el js.Value, n int) (js.Value, int) {
	doc := js.Global().Get("document")
	walker := doc.Call("createTreeWalker", el, 4) // NodeFilter.SHOW_TEXT
	var last js.Value
	lastLen := 0
	for {
		t := walker.Call("nextNode")
		if t.IsNull() {
			break
		}
		if !t.Get("parentElement").Call("closest", ".gj-sr-only").IsNull() {
			continue
		}
		rs := []rune(t.Get("nodeValue").String())
		if n <= len(rs) {
			return t, len(utf16.Encode(rs[:n]))
		}
		n -= len(rs)
		last, lastLen = t, len(utf16.Encode(rs))
	}
	if !last.IsUndefined() && !last.IsNull() {
		return last, lastLen
	}
	return el, 0
}
