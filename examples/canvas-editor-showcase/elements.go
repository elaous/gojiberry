//go:build js && wasm

package main

import (
	"fmt"
	"strings"
	"syscall/js"
	"unicode/utf8"

	"github.com/elaous/gojiberry/a11ycheck"
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/xango"
)

// The equation and code parts of the Pictures tab (roadmap 78): insert an equation, which needs a text
// alternative before it goes in; edit the formula and the alternative of each equation in the document; set
// the language of the code block at the caret. A picture's long description is in the picture list itself.

// elementsForm is the equation being typed in the tab, and the last message of its equation and code parts.
type elementsForm struct {
	formula, alt string
	inline       bool // the new equation goes in the line of text
	msg          string
	codeMsg      string
	focusAlt     bool
}

// equationRef is an equation in the text: where it is (a caret position) and the object to edit.
type equationRef struct {
	offset int
	eq     *canvas.EquationObject
}

// documentEquations lists the equations of the flow in order.
func documentEquations(fl *canvas.TextFlow) []equationRef {
	if fl == nil {
		return nil
	}
	var out []equationRef
	offset := 0
	for _, it := range fl.Slice(0, utf8.RuneCountInString(fl.EditText())) {
		switch it := it.(type) {
		case canvas.Chars:
			offset += utf8.RuneCountInString(it.Text)
		case canvas.Inline:
			if eq, ok := it.Object.(*canvas.EquationObject); ok {
				out = append(out, equationRef{offset, eq})
			}
			offset++
		}
	}
	return out
}

// insertEquation puts the typed equation at the caret. It is refused until it has a text alternative: the
// check that the Check tab makes (a11ycheck.EquationFindings) is made here on the equation about to go in.
func (u *ui) insertEquation(fl *canvas.TextFlow, redraw, renderUI func()) {
	f := &u.elements
	item := docfile.EquationItem{Formula: strings.TrimSpace(f.formula), Alt: strings.TrimSpace(f.alt)}
	switch {
	case item.Formula == "":
		f.msg = T("eq.needFormula", "Write the formula first.")
	case item.Alt == "":
		f.msg = T("eq.needAlt", "An equation needs a text alternative: write it in words, as you would read it aloud.")
		f.focusAlt = true
	default:
		refused := false
		for _, fi := range a11ycheck.EquationFindings(item, 0, 0) {
			if fi.Severity == a11ycheck.Error {
				f.msg, f.focusAlt, refused = fi.Message+" "+fi.Fix, true, true
			}
		}
		if !refused {
			pos := fl.Cursors()[0].Caret
			fl.Replace([]canvas.Edit{{From: pos, To: pos, Items: []canvas.Item{canvas.Inline{Object: &canvas.EquationObject{Formula: item.Formula, Alt: item.Alt, Inline: f.inline}, Block: !f.inline}}}})
			fl.SetCursors([]canvas.Cursor{canvas.At(pos + 1)})
			f.formula, f.alt, f.focusAlt, f.inline = "", "", false, false
			f.msg = T("eq.inserted", "Equation inserted.")
			u.announce(f.msg)
			redraw()
		}
	}
	renderUI()
}

// lineInput is a one-line text field.
func lineInput(id, label, value, placeholder string, on func(string)) xango.Node {
	return xango.El("input", []xango.Attr{xango.A("id", id), xango.A("type", "text"), xango.A("value", value), xango.A("spellcheck", "false"),
		xango.A("aria-label", label), xango.A("placeholder", placeholder), xango.A("style", "width:100%;box-sizing:border-box;font-size:13px"),
		xango.On("input", func(ev xango.Event) { on(xango.EventValue[string](ev)) })})
}

// elementsSections are the equation and code parts of the Pictures tab.
func elementsSections(u *ui, state *State, flow func() *canvas.TextFlow, redraw, renderUI func()) []xango.Node {
	fl := flow()
	canEdit := state.Role.CanEdit() && state.Mode == canvas.ModeEdit
	f := &u.elements
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("eq.title", "Equations"))),
		textarea("eq-formula", T("eq.formulaLabel", "Formula"), f.formula, T("eq.formulaHint", "For example: x^2 + y^2 = z^2, frac{a}{b}, sqrt{x}"), "2", func(v string) { f.formula = v }),
		textarea("eq-alt", T("eq.altLabel", "Text alternative of the new equation"), f.alt, T("eq.altHint", "The equation in words, as you would read it aloud"), "2", func(v string) { f.alt = v }),
		xango.El("div", []xango.Attr{xango.A("id", "eq-inline")}, components.Checkbox(components.CheckboxProps{
			Label: T("eq.inline", "In the line of text, not on a line of its own"), Checked: f.inline, Disabled: !canEdit,
			OnChange: func(on bool) { f.inline = on; renderUI() }})),
		btn("eq-insert", T("eq.insert", "Insert the equation"), func() { u.insertEquation(fl, redraw, renderUI) }, !canEdit),
	}
	if f.msg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "eq-message"), xango.A("role", "status")}, xango.Text(f.msg)))
	}
	eqs := documentEquations(fl)
	if len(eqs) == 0 {
		nodes = append(nodes, para("eq-none", T("eq.none", "There are no equations in the document."), false))
	}
	var items []xango.Node
	for i, r := range eqs {
		i, r := i, r
		kids := []xango.Node{
			xango.El("strong", nil, xango.Text(T("eq.n", "Equation %d", i+1))),
			btn(fmt.Sprintf("eq-go-%d", i), T("eq.go", "Go to it"), func() { fl.GotoOffset(r.offset); redraw(); renderUI() }, false),
			textarea(fmt.Sprintf("eq-formula-%d", i), T("eq.formulaLabelN", "Formula of equation %d", i+1), r.eq.Formula, "", "2", func(v string) {
				r.eq.Formula = v
				fl.Touch()
				redraw()
			}),
			textarea(fmt.Sprintf("eq-alt-%d", i), T("eq.altLabelN", "Text alternative of equation %d", i+1), r.eq.Alt, T("eq.altHint", "The equation in words, as you would read it aloud"), "2", func(v string) {
				r.eq.Alt = v
				fl.Touch()
				renderUI()
			}),
		}
		if strings.TrimSpace(r.eq.Alt) == "" {
			kids = append(kids, para(fmt.Sprintf("eq-missing-%d", i), T("eq.missing", "No text alternative yet."), true))
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item")}, kids...))
	}
	if len(items) > 0 {
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, items...))
	}
	if f.focusAlt {
		f.focusAlt = false
		js.Global().Call("setTimeout", js.FuncOf(func(js.Value, []js.Value) any {
			if el := js.Global().Get("document").Call("getElementById", "eq-alt"); el.Truthy() {
				el.Call("focus")
			}
			return nil
		}), 50)
	}

	// the language of the code block at the caret
	nodes = append(nodes, xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("code.title", "Code blocks"))))
	if fl != nil {
		pf := fl.ParagraphAt(fl.Cursors()[0].Caret)
		if strings.EqualFold(pf.Style, "Code") {
			nodes = append(nodes,
				lineInput("code-lang", T("code.langLabel", "Programming language of the code block at the caret"), pf.CodeLang, T("code.langHint", "go, python, js …"), func(v string) {
					lang := export.CodeLang(v)
					if strings.TrimSpace(v) != "" && lang == "" {
						f.codeMsg = T("code.langBad", "Use letters, digits and + - # only, up to 20 characters.")
						renderUI()
						return
					}
					f.codeMsg = ""
					fl.ApplyParagraph(func(p *canvas.ParaFormat) { p.CodeLang = lang })
					redraw()
				}))
		} else {
			nodes = append(nodes, para("code-none", T("code.none", "Put the caret in a code block (the Code block style) to give its language."), false))
		}
	}
	if f.codeMsg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "code-message"), xango.A("role", "status")}, xango.Text(f.codeMsg)))
	}
	return nodes
}
