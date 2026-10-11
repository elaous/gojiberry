//go:build js && wasm

package main

import (
	"strconv"
	"syscall/js"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/complete"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/xango"
)

// Word suggestions at the caret (roadmap 79). Ctrl/Cmd+Space opens the list of words that continue
// the one being typed; it is built from the document's own words, most used and most recent first.
// The list never takes the focus: the input element keeps it, is marked as the combobox and names the
// highlighted option, and while the list is open Up, Down, Home, End, Tab, Enter and Escape are for
// the list. Typing goes on and narrows the list; leaving the word closes it. Nothing is sent
// anywhere, and nothing opens while an input method is composing.

type suggestState struct {
	list components.SuggestState
	from int // where the word being completed starts
	x, y float64
	flow func() *canvas.TextFlow
	// reopen opens the list at the caret (set in main); quiet says nothing when there is nothing to offer.
	reopen func(quiet bool)
	timer  bool // a delayed automatic open is waiting
}

func (u *ui) suggestionsNode(renderUI func()) xango.Node {
	// A host element that is always there keeps the page's structure the same whether or not the list
	// is open (the canvas and its input, beside it, must never be rebuilt: that would drop the focus).
	return xango.El("div", []xango.Attr{xango.A("class", "gj-suggest-host")}, components.Suggestions(components.SuggestionsProps{
		ID: "suggestions", State: u.sugg.list, X: u.sugg.x, Y: u.sugg.y,
		OnPick: func(i int) { u.sugg.list.Active = i; u.acceptSuggestion(nil, renderUI) },
	}))
}

// openSuggestions builds the list for the word before the caret and shows it there.
func (u *ui) openSuggestions(flow *canvas.TextFlow, stage *canvas.Stage, canvasEl js.Value, quiet bool, renderUI func()) {
	if flow == nil || !u.refreshSuggestions(flow) {
		if !quiet { // asked for with the keyboard: say there is nothing; opened by itself: say nothing
			u.announce(T("suggest.none", "No suggestions"))
			renderUI()
		}
		return
	}
	if x, y, h, ok := stage.CaretBox(); ok {
		r := canvasEl.Call("getBoundingClientRect")
		u.sugg.x, u.sugg.y = r.Get("left").Float()+x, r.Get("top").Float()+y+h
	}
	u.sugg.list.Numbers = false
	u.announce(u.sugg.list.Announcement(false))
	syncComboboxAttrs(true, u.sugg.list)
	renderUI()
}

// refreshSuggestions recomputes the list for the caret; false when there is nothing to offer.
func (u *ui) refreshSuggestions(flow *canvas.TextFlow) bool {
	text := []rune(flow.EditText())
	caret := flow.Cursors()[0].Caret
	prefix, from := complete.PrefixAt(text, caret)
	var ix complete.Index
	ix.AddText(string(text[:from]))
	ix.AddText(string(text[complete.WordEnd(text, caret):])) // the words around the one being typed
	var words []string
	for _, s := range ix.Suggest(prefix, complete.Options{Max: 7}) {
		words = append(words, s.Word)
	}
	u.sugg.from = from
	u.sugg.list.Show(words)
	if !u.sugg.list.Open {
		syncComboboxAttrs(false, u.sugg.list)
	}
	return u.sugg.list.Open
}

// acceptSuggestion puts the highlighted word in place of the word being typed.
func (u *ui) acceptSuggestion(flow *canvas.TextFlow, renderUI func()) {
	word := u.sugg.list.Selected()
	if word == "" {
		return
	}
	if flow == nil && u.sugg.flow != nil {
		flow = u.sugg.flow()
	}
	if flow != nil {
		text := []rune(flow.EditText())
		end := complete.WordEnd(text, flow.Cursors()[0].Caret)
		flow.ReplaceRange(u.sugg.from, end, word)
		n := u.sugg.from + len([]rune(word))
		flow.SetSelection(n, n)
	}
	u.closeSuggestions()
	u.announce(T("suggest.inserted", "Inserted %s", word))
	renderUI()
}

func (u *ui) closeSuggestions() {
	u.sugg.list.Close()
	syncComboboxAttrs(false, u.sugg.list)
}

// syncComboboxAttrs marks the input element as the combobox the list belongs to, or clears it.
func syncComboboxAttrs(open bool, s components.SuggestState) {
	in := js.Global().Get("document").Call("getElementById", "input")
	if in.IsNull() {
		return
	}
	if !open || len(s.Words) == 0 {
		for _, a := range []string{"role", "aria-autocomplete", "aria-haspopup", "aria-controls", "aria-expanded", "aria-activedescendant"} {
			if a != "role" { // the input's own role, if it has one, stays
				in.Call("removeAttribute", a)
			}
		}
		return
	}
	in.Call("setAttribute", "aria-autocomplete", "list")
	in.Call("setAttribute", "aria-haspopup", "listbox")
	in.Call("setAttribute", "aria-controls", "suggestions")
	in.Call("setAttribute", "aria-expanded", "true")
	in.Call("setAttribute", "aria-activedescendant", "suggestions-opt-"+strconv.Itoa(s.Active))
}

// watchSuggestionKeys gives the open list its keys before the canvas sees them, and narrows or closes
// it as the caret moves.
func (u *ui) watchSuggestionKeys(in js.Value, flow func() *canvas.TextFlow, redraw, renderUI func()) {
	u.sugg.flow = flow
	in.Call("addEventListener", "keydown", js.FuncOf(func(_ js.Value, args []js.Value) any {
		e := args[0]
		if !u.sugg.list.Open || e.Get("isComposing").Truthy() || e.Get("ctrlKey").Truthy() || e.Get("metaKey").Truthy() || e.Get("altKey").Truthy() {
			return nil
		}
		act := u.sugg.list.Key(e.Get("key").String())
		switch act {
		case components.SuggestMoved:
			e.Call("preventDefault")
			e.Call("stopPropagation")
			u.announce(u.sugg.list.Announcement(true))
			syncComboboxAttrs(true, u.sugg.list)
		case components.SuggestAccept:
			e.Call("preventDefault")
			e.Call("stopPropagation")
			u.acceptSuggestion(flow(), renderUI)
			redraw()
			return nil
		case components.SuggestDismiss:
			e.Call("preventDefault")
			e.Call("stopPropagation")
			u.closeSuggestions()
			u.announce(T("suggest.closed", "Suggestions closed"))
		default:
			return nil
		}
		renderUI()
		return nil
	}), true)
	// After the key has been applied: a longer prefix narrows the list, leaving the word closes it; with
	// the "suggest as I type" preference the list opens by itself after a short pause in typing.
	in.Call("addEventListener", "keyup", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if !u.sugg.list.Open {
			if curPrefs.Suggest == "auto" && u.sugg.reopen != nil && !u.sugg.timer && len(args[0].Get("key").String()) == 1 && !args[0].Get("isComposing").Truthy() {
				u.sugg.timer = true
				time.AfterFunc(600*time.Millisecond, func() {
					u.sugg.timer = false
					if !u.sugg.list.Open && curPrefs.Suggest == "auto" {
						u.sugg.reopen(true)
					}
				})
			}
			return nil
		}
		switch args[0].Get("key").String() {
		case "ArrowDown", "ArrowUp", "Home", "End", "Escape", "Shift", "Control", "Alt", "Meta", "Tab", "Enter":
			return nil
		}
		if f := flow(); f != nil && u.refreshSuggestions(f) {
			syncComboboxAttrs(true, u.sugg.list)
		} else {
			u.closeSuggestions()
		}
		renderUI()
		return nil
	}), false)
}
