//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/helper"
	"github.com/elaous/gojiberry/xango"
)

// Writing help (the llm add-on): ask for the selection to be summarised, made plainer or shorter, given another tone or
// explained. The answer is machine-made text: it is shown for the author to read and change, and goes into the document
// only when they press a button, never by itself.

var helpState struct {
	action  helper.Action
	result  string
	busy    bool
	msg     string
	from    int // the selection that was asked about
	to      int
	whole   string // the whole text when it was asked: the offsets mean something only in it
	replace bool   // the answer can replace the selection
}

// helpLabel names an action. The ids are spelled out so that the translation check sees them.
func helpLabel(a helper.Action) string {
	switch a {
	case helper.Summarise:
		return T("help.summarise", "Summarise")
	case helper.Plainer:
		return T("help.plainer", "Say it in plainer words")
	case helper.Shorter:
		return T("help.shorter", "Make it shorter")
	case helper.Formal:
		return T("help.formal", "Make it more formal")
	case helper.Friendly:
		return T("help.friendly", "Make it friendlier")
	case helper.Explain:
		return T("help.explain", "Explain it")
	}
	return string(a)
}

// writingHelp is the section of the Language tab, shown when the page has the llm add-on.
func writingHelp(u *ui, state *State, flow func() *canvas.TextFlow, redraw, renderUI func()) xango.Node {
	if !llmAddon() {
		return xango.Frag()
	}
	fl := flow()
	hasSel := false
	if fl != nil {
		from, to := fl.Selection()
		hasSel = to > from
	}
	canEdit := state.Role.CanEdit() && state.Mode == canvas.ModeEdit
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("help.title", "Writing help"))),
		para("help-about", T("help.about", "Select some text and ask. A model on this device writes an answer; it can be wrong, so read it before you use it. Nothing is put in the document unless you press a button."), false),
	}
	var bar []xango.Node
	for _, a := range helper.Actions {
		a := a
		bar = append(bar, btn("help-"+string(a), helpLabel(a), func() { askHelp(u, a, fl, renderUI) }, !hasSel || helpState.busy))
	}
	nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("id", "help-bar"), xango.A("role", "group"), xango.A("aria-label", T("help.bar", "Ask for")), xango.A("style", "display:flex;flex-wrap:wrap;gap:4px")}, bar...))
	if !hasSel {
		nodes = append(nodes, para("help-needsel", T("help.needSelection", "Select some text in the document first."), false))
	}
	if helpState.busy {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "help-working"), xango.A("role", "status")}, xango.Text(T("help.working", "Working on it on this device…"))))
	}
	if helpState.msg != "" {
		nodes = append(nodes, para("help-msg", helpState.msg, true))
	}
	if helpState.result != "" {
		nodes = append(nodes,
			textarea("help-result", fmt.Sprintf(T("help.resultLabel", "Answer: %s (machine-made, check it)"), helpLabel(helpState.action)), helpState.result, "", "6", func(v string) { helpState.result = v }))
		if helpState.replace {
			nodes = append(nodes, btn("help-replace", T("help.replace", "Replace the selection with it"), func() { useHelp(fl, true, redraw, renderUI) }, !canEdit))
		}
		nodes = append(nodes,
			btn("help-after", T("help.after", "Put it after the selection"), func() { useHelp(fl, false, redraw, renderUI) }, !canEdit),
			btn("help-discard", T("help.discard", "Throw it away"), func() { helpState.result = ""; renderUI() }, false))
	}
	return xango.El("div", []xango.Attr{xango.A("id", "help-panel")}, nodes...)
}

// askHelp sends the selection to the model with the action's prompt.
func askHelp(u *ui, a helper.Action, fl *canvas.TextFlow, renderUI func()) {
	if fl == nil || helpState.busy {
		return
	}
	text := fl.SelectedText()
	prompt, err := helper.Prompt(a, text, u.docLang)
	if err != nil {
		if _, long := err.(helper.ErrTooLong); long {
			helpState.msg = T("help.tooLong", "That is too much text for one request. Select a shorter part.")
		} else {
			helpState.msg = T("help.noText", "There is no text to work on.")
		}
		renderUI()
		return
	}
	helpState.action, helpState.busy, helpState.msg, helpState.result = a, true, "", ""
	helpState.from, helpState.to = fl.Selection()
	helpState.whole = fl.EditText()
	helpState.replace = a.Replaces()
	renderUI()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		answer, err := generate(ctx, prompt, "", 1024)
		helpState.busy = false
		answer = helper.Clean(answer)
		switch {
		case err != nil:
			helpState.msg = fmt.Sprintf(T("help.failed", "The model could not answer: %s"), err.Error())
		case strings.TrimSpace(answer) == "":
			helpState.msg = T("help.empty", "The model gave no answer.")
		default:
			helpState.result = answer
		}
		renderUI()
	}()
}

// useHelp puts the answer in the document: in place of the selection, or after it. It is refused when the text changed
// since the question, because the range no longer means what it did.
func useHelp(fl *canvas.TextFlow, replace bool, redraw, renderUI func()) {
	if fl == nil || helpState.result == "" {
		return
	}
	if fl.EditText() != helpState.whole {
		helpState.result, helpState.msg = "", T("help.changed", "The text changed since you asked: select it and ask again.")
		renderUI()
		return
	}
	if replace {
		fl.ReplaceRange(helpState.from, helpState.to, helpState.result)
	} else {
		fl.ReplaceRange(helpState.to, helpState.to, " "+helpState.result)
	}
	helpState.result, helpState.msg = "", ""
	redraw()
	renderUI()
}
