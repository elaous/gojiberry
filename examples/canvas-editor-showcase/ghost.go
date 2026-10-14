//go:build js && wasm

package main

import (
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/complete"
	"github.com/elaous/gojiberry/components"
)

// The inline ghost-text suggestion (roadmap 79). With the preference "Show a suggestion after the
// caret" on, the best word that continues the one being typed is drawn faintly after the caret, by
// the canvas itself, in the font of the word it continues. It is a picture only: Tab puts it in, no other
// key does, Escape hides it until the next letter. A screen-reader user hears it once, politely, a
// moment after typing stops ("Suggestion: zebra. Press Tab to accept."). It is off by default, never
// shows while an input method is composing, a selection is held or the word list is open, and in the
// middle of a line it is a small tag under the caret (so it does not hide the words after it).

type ghostUI struct {
	state components.GhostState
	gen   int // bumped by every change, so a late announcement for an old suggestion says nothing
	// refresh recomputes the suggestion for the caret (set in main); flow and redraw reach the canvas.
	refresh func()
	flow    func() *canvas.TextFlow
	redraw  func()
}

func ghostOn() bool { return curPrefs.Ghost == "on" }

// hideGhost removes the suggestion from sight.
func (u *ui) hideGhost() {
	u.ghost.state.Clear()
	u.ghost.gen++
	if u.ghost.flow != nil {
		if f := u.ghost.flow(); f != nil && f.Ghost != "" {
			f.Ghost = ""
			u.ghost.redraw()
		}
	}
}

// ghostSuffix is what is shown now ("" none): the state of the suggestion for the tests.
func (u *ui) ghostSuffix() string {
	if u.ghost.state.Visible() {
		return u.ghost.state.Suffix
	}
	return ""
}

// refreshGhost recomputes the suggestion for the caret and shows it (or hides it). The faint text is
// drawn by the canvas itself (canvas.TextFlow.Ghost), in the font of the word it continues.
func (u *ui) refreshGhost(flow *canvas.TextFlow, redraw, renderUI func()) {
	g := &u.ghost
	g.gen++
	hide := func() {
		g.state.Clear()
		if flow != nil && flow.Ghost != "" {
			flow.Ghost = ""
			redraw()
		}
	}
	if !ghostOn() || flow == nil || u.sugg.list.Open {
		hide()
		return
	}
	if from, to := flow.Selection(); to > from || len(flow.Cursors()) != 1 {
		hide()
		return
	}
	text := []rune(flow.EditText())
	caret := flow.Cursors()[0].Caret
	prefix, _ := complete.PrefixAt(text, caret)
	g.state.Typed(prefix)
	words := u.completions(text, caret, prefix, 1, u.langAt(flow, caret))
	if len(words) == 0 || !g.state.Show(prefix, words[0], caret) {
		hide()
		return
	}
	flow.Ghost = g.state.Suffix
	redraw()
	// Said once, a moment after typing stops, so that fast typing is not interrupted by speech.
	gen := g.gen
	time.AfterFunc(600*time.Millisecond, func() {
		if g.gen != gen || !g.state.Visible() {
			return
		}
		if msg := g.state.Announcement(); msg != "" {
			u.announce(msg)
			renderUI()
		}
	})
}

// langAt is the language the word at the caret is written in: the run's own tag, else the document's.
func (u *ui) langAt(flow *canvas.TextFlow, caret int) string {
	if l := flow.LangAt(caret); l != "" {
		return l
	}
	return u.docLang
}

// acceptGhost puts the suggestion in at the caret; false when there is none for this caret.
func (u *ui) acceptGhost(flow *canvas.TextFlow, renderUI func()) bool {
	if flow == nil {
		return false
	}
	caret := flow.Cursors()[0].Caret
	word := u.ghost.state.Word
	text := u.ghost.state.Accept(caret)
	u.hideGhost()
	if text == "" {
		return false
	}
	flow.ReplaceRange(caret, caret, text)
	n := caret + len([]rune(text))
	flow.SetSelection(n, n)
	u.announce(T("suggest.inserted", "Inserted %s", word))
	renderUI()
	return true
}

// ghostKey gives the ghost text its keys before the canvas sees them; true when the key was for it.
// Tab accepts, Escape dismisses, and any other key that can change the text or the caret hides it
// (the key-up then offers the suggestion for the new word).
func (u *ui) ghostKey(e js.Value, flow *canvas.TextFlow, renderUI, redraw func()) bool {
	if !ghostOn() {
		return false
	}
	key := e.Get("key").String()
	switch key {
	case "Shift", "Control", "Alt", "Meta", "CapsLock", "AltGraph":
		return false
	}
	plain := !e.Get("ctrlKey").Truthy() && !e.Get("metaKey").Truthy() && !e.Get("altKey").Truthy() && !e.Get("shiftKey").Truthy()
	if u.ghost.state.Visible() && plain && !e.Get("isComposing").Truthy() && !u.sugg.list.Open {
		switch u.ghost.state.Key(key) {
		case components.GhostAccept:
			e.Call("preventDefault")
			e.Call("stopPropagation")
			if u.acceptGhost(flow, renderUI) {
				redraw()
			}
			return true
		case components.GhostDismiss:
			e.Call("preventDefault")
			e.Call("stopPropagation")
			u.hideGhost()
			return true
		}
	}
	u.hideGhost()
	return false
}

// watchGhostPointer hides the suggestion when the pointer moves the caret.
func (u *ui) watchGhostPointer() {
	js.Global().Get("document").Call("addEventListener", "mousedown", js.FuncOf(func(js.Value, []js.Value) any {
		u.hideGhost()
		return nil
	}), true)
}
