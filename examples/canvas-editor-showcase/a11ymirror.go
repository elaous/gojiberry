//go:build js && wasm

package main

import (
	"fmt"
	"strconv"
	"syscall/js"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	cxango "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/components"
)

func (u *ui) mirrorBlocks(flow *canvas.TextFlow, root *cxango.Root) []components.A11yBlock {
	blocks := u.a11yBlocks(flow, u.title)
	if flow != nil {
		blocks = components.MarkCaret(blocks, flow.Cursors()[0].Caret)
	}
	ys := make([]float64, len(blocks))
	var starts []int
	var which []int
	for i, b := range blocks {
		ys[i] = -1
		if b.Mapped && !b.Object && flow != nil && !flow.Vertical { // (in vertical text the objects stay at the end)
			starts, which = append(starts, b.Start), append(which, i)
		}
	}
	if len(starts) > 0 {
		for k, p := range flow.CaretRects(starts) {
			if p.OK {
				ys[which[k]] = p.Y
			}
		}
	}
	return orderByPosition(blocks, ys, a11yObjects(root))
}

func mirrorComments(flow *canvas.TextFlow) []components.A11yComment {
	if flow == nil || flow.Comments == nil {
		return nil
	}
	excerpts := flow.CommentExcerpts()
	var out []components.A11yComment
	for _, c := range flow.Comments.Visible() {
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
	before := streamRunes(r.Call("cloneContents"))
	b.Runs = []components.A11yRun{{Stream: streamRunes(block.Call("cloneNode", true))}}
	return b.StreamOffsetRunes(before), true
}

func streamRunes(n js.Value) int {
	hints := n.Call("querySelectorAll", ".gj-sr-only")
	for i := hints.Length() - 1; i >= 0; i-- {
		hints.Index(i).Call("remove")
	}
	marks := n.Call("querySelectorAll", "[data-gj-stream]")
	width := 0
	for i := marks.Length() - 1; i >= 0; i-- {
		m := marks.Index(i)
		if m.Get("textContent").String() != "" { // a mark the range ends before is not counted
			w, _ := strconv.Atoi(m.Call("getAttribute", "data-gj-stream").String())
			width += w
		}
		m.Call("remove")
	}
	return utf8.RuneCountInString(n.Get("textContent").String()) + width
}

func (u *ui) watchMirrorSelection(flow func() *canvas.TextFlow, scroller func() *canvas.ScrollView, redraw, renderUI func()) {
	js.Global().Get("document").Call("addEventListener", "selectionchange", js.FuncOf(func(js.Value, []js.Value) any {
		fl := flow()
		if fl == nil || u.mirrorWriting || time.Since(u.mirrorWroteAt) < 150*time.Millisecond {
			return nil
		}
		sel := js.Global().Call("getSelection")
		if sel.IsNull() || sel.Get("rangeCount").Int() == 0 {
			return nil
		}
		anchor, ok := mirrorPosition(sel.Get("anchorNode"), sel.Get("anchorOffset").Int())
		if !ok {
			return nil
		}
		caret, ok := mirrorPosition(sel.Get("focusNode"), sel.Get("focusOffset").Int())
		if !ok {
			caret = anchor
		}
		cs := fl.Cursors()[0]
		if cs.Anchor == anchor && cs.Caret == caret {
			return nil
		}
		if cs.Anchor != cs.Caret && anchor == caret && (anchor == cs.Anchor || anchor == cs.Caret) && time.Since(u.mirrorWroteAt) < 3*time.Second {
			u.followCaret(fl, u.a11yCache)
			return nil
		}
		u.mirrorAt = fmt.Sprint(anchor, caret)
		// mirror already has this selection: do not re-apply it
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
		if mark := t.Get("parentElement").Call("closest", "[data-gj-stream]"); !mark.IsNull() && el.Call("contains", mark).Bool() {
			w, _ := strconv.Atoi(mark.Call("getAttribute", "data-gj-stream").String())
			if n < w {
				return t, 0
			}
			n -= w
			last, lastLen = t, len(utf16.Encode(rs))
			continue
		}
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
