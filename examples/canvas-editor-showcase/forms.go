//go:build js && wasm

package main

import (
	"fmt"
	"strings"
	"syscall/js"
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/formdata"
	"github.com/elaous/gojiberry/transfer"
	"github.com/elaous/gojiberry/xango"
)

// The Forms tab: a bar of the field types to add at the caret, the fields of the document with what is in them, and the
// two things the eye and the pen on a field ask for: filling it in (a box fit for its kind, with the problem with the
// value said at once) and changing what the field is (its label, kind, placeholder, pattern, options). Insert > Form
// fields has the same kinds, and Clear form fields. A locked field has no pen and its properties cannot be changed; the
// owner locks and unlocks.

// formsState is what the tab is showing: the field being filled in or changed, and the last message.
type formsState struct {
	target *canvas.FormFieldObject
	edit   bool // the pen, not the eye
	msg    string
	focus  bool
	// submitTo is the address the downloaded web form sends the answers to.
	submitTo string
}

// formKindLabel is the name of a kind. The ids are spelled out so that the translation check sees them.
func formKindLabel(k canvas.FormKind) string {
	switch k {
	case canvas.FormTextArea:
		return T("forms.kind.textarea", "Several lines")
	case canvas.FormEmail:
		return T("forms.kind.email", "E-mail")
	case canvas.FormNumber:
		return T("forms.kind.number", "Number")
	case canvas.FormPhone:
		return T("forms.kind.phone", "Phone")
	case canvas.FormURL:
		return T("forms.kind.url", "Web address")
	case canvas.FormDate:
		return T("forms.kind.date", "Date")
	case canvas.FormCheckbox:
		return T("forms.kind.checkbox", "Checkbox")
	case canvas.FormSelect:
		return T("forms.kind.select", "Choice list")
	}
	return T("forms.kind.text", "Text")
}

// formProblemText says what is wrong with a value, in words.
func formProblemText(code string) string {
	switch code {
	case canvas.FormProblemRequired:
		return T("forms.problem.required", "This field is required.")
	case canvas.FormProblemEmail:
		return T("forms.problem.email", "That is not an e-mail address.")
	case canvas.FormProblemNumber:
		return T("forms.problem.number", "That is not a number.")
	case canvas.FormProblemPhone:
		return T("forms.problem.phone", "That is not a phone number.")
	case canvas.FormProblemURL:
		return T("forms.problem.url", "That is not a web address (it starts with http:// or https://).")
	case canvas.FormProblemDate:
		return T("forms.problem.date", "That is not a date (year-month-day).")
	case canvas.FormProblemChoice:
		return T("forms.problem.choice", "That is not one of the choices.")
	case canvas.FormProblemPattern:
		return T("forms.problem.pattern", "That does not match the pattern the field asks for.")
	}
	return ""
}

// wireForms makes the eye and the pen of every field in the flow open the Forms tab.
func (u *ui) wireForms(fl *canvas.TextFlow, redraw, renderUI func()) {
	if fl == nil {
		return
	}
	for _, ff := range fl.FormFields() {
		ff := ff
		ff.OnFill = func(*canvas.FormFieldObject) {
			u.forms = formsState{target: ff, focus: true}
			u.tab = "forms"
			renderUI()
		}
		ff.OnEdit = func(*canvas.FormFieldObject) {
			if !u.canEditFormProps(ff) {
				return
			}
			u.forms = formsState{target: ff, edit: true, focus: true}
			u.tab = "forms"
			renderUI()
		}
	}
}

// canEditFormProps says the properties of the field may be changed here: the person can edit, the document is in edit
// mode and the field is not locked.
func (u *ui) canEditFormProps(ff *canvas.FormFieldObject) bool {
	return u.stateRef != nil && u.stateRef.Role.CanEdit() && u.stateRef.Mode == canvas.ModeEdit && !ff.Locked
}

// addFormField puts a new field of the kind at the caret.
func (u *ui) addFormField(fl *canvas.TextFlow, kind canvas.FormKind, redraw, renderUI func()) {
	if fl == nil || u.stateRef == nil || !u.stateRef.Role.CanEdit() || u.stateRef.Mode != canvas.ModeEdit {
		u.forms.msg = T("forms.needEdit", "Switch to edit mode to add or change form fields.")
		u.tab = "forms"
		renderUI()
		return
	}
	ff := canvas.NewFormField(kind, formKindLabel(kind))
	if kind == canvas.FormSelect {
		ff.Options = []string{T("forms.option1", "Yes"), T("forms.option2", "No")}
	}
	n := len(fl.FormFields()) + 1
	ff.Name = fmt.Sprintf("field%d", n)
	pos := fl.Cursors()[0].Caret
	fl.Replace([]canvas.Edit{{From: pos, To: pos, Items: []canvas.Item{canvas.Inline{Object: ff}}}})
	fl.SetCursors([]canvas.Cursor{canvas.At(pos + 1)})
	u.wireForms(fl, redraw, renderUI)
	u.forms = formsState{target: ff, edit: true, focus: true, msg: T("forms.added", "Form field added. Give it a label that says what it asks for.")}
	u.tab = "forms"
	u.announce(u.forms.msg)
	redraw()
	renderUI()
}

// clearFormFields empties every field.
func (u *ui) clearFormFields(fl *canvas.TextFlow, redraw, renderUI func()) {
	if fl == nil {
		return
	}
	n := fl.ClearFormFields()
	u.forms.msg = Tn("forms.cleared", "Cleared %d form field.", "Cleared %d form fields.", n)
	u.announce(u.forms.msg)
	redraw()
	renderUI()
}

// formsMenu is Insert > Form fields.
func formsMenu(u *ui, fl func() *canvas.TextFlow, redraw, renderUI func()) []components.MenuItem {
	var items []components.MenuItem
	for _, k := range canvas.FormKinds {
		k := k
		items = append(items, components.MenuItem{Label: formKindLabel(k), OnSelect: func() { u.addFormField(fl(), k, redraw, renderUI) }})
	}
	return append(items, components.MenuItem{Separator: true},
		components.MenuItem{Label: T("forms.openTab", "Form fields…"), OnSelect: func() { u.tab = "forms"; renderUI() }},
		components.MenuItem{Label: T("forms.clear", "Clear form fields"), OnSelect: func() { u.clearFormFields(fl(), redraw, renderUI) }})
}

// formsPanel is the Forms tab.
func formsPanel(u *ui, state *State, flow func() *canvas.TextFlow, redraw, renderUI func()) xango.Node {
	fl := flow()
	canEdit := state.Role.CanEdit() && state.Mode == canvas.ModeEdit
	isOwner := state.Role == canvas.RoleOwner || state.Role == "" // no role set: the one person who has the document
	nodes := []xango.Node{xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(T("forms.title", "Form fields")))}

	// the bar of the kinds
	var bar []xango.Node
	for _, k := range canvas.FormKinds {
		k := k
		bar = append(bar, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-keybindings-button"), xango.A("data-kind", string(k)),
			xango.A("id", "forms-add-"+string(k)), xango.A("title", T("forms.addKind", "Add a field: %s", formKindLabel(k))),
			xango.On("click", func(xango.Event) { u.addFormField(fl, k, redraw, renderUI) })}, xango.Text(formKindLabel(k))))
	}
	nodes = append(nodes,
		xango.El("div", []xango.Attr{xango.A("id", "forms-bar"), xango.A("role", "toolbar"), xango.A("aria-label", T("forms.bar", "Field types")),
			xango.A("style", "display:flex;flex-wrap:wrap;gap:4px;margin-bottom:0.6em")}, bar...),
		xango.El("div", []xango.Attr{xango.A("style", "display:flex;flex-wrap:wrap;gap:6px")},
			btn("forms-clear", T("forms.clear", "Clear form fields"), func() { u.clearFormFields(fl, redraw, renderUI) }, fl == nil),
			btn("forms-answers-json", T("forms.answersJSON", "Download the answers (JSON)"), func() { u.downloadAnswers(fl, "json", renderUI) }, fl == nil || len(fl.FormFields()) == 0),
			btn("forms-answers-csv", T("forms.answersCSV", "Download the answers (CSV)"), func() { u.downloadAnswers(fl, "csv", renderUI) }, fl == nil || len(fl.FormFields()) == 0),
		),
	)
	nodes = append(nodes,
		xango.El("label", nil, xango.Text(T("forms.submitTo", "Address for the answers of a web form (https or mailto)")),
			lineInput("forms-submit-to", T("forms.submitTo", "Address for the answers of a web form (https or mailto)"), u.forms.submitTo, "https://", func(v string) { u.forms.submitTo = strings.TrimSpace(v) })),
		btn("forms-webform", T("forms.webForm", "Download as a web form"), func() { u.downloadWebForm(fl, renderUI) }, fl == nil || len(fl.FormFields()) == 0),
	)
	if !canEdit {
		nodes = append(nodes, para("forms-readonly", T("forms.needEdit", "Switch to edit mode to add or change form fields."), false))
	}
	if u.forms.msg != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "forms-message"), xango.A("role", "status")}, xango.Text(u.forms.msg)))
	}

	// the field being filled in or changed
	if t := u.forms.target; t != nil {
		if u.forms.edit && u.canEditFormProps(t) {
			nodes = append(nodes, formPropsEditor(u, fl, t, redraw, renderUI))
		} else {
			nodes = append(nodes, formFiller(u, fl, t, redraw, renderUI))
		}
	}

	// the fields of the document
	var fields []*canvas.FormFieldObject
	if fl != nil {
		fields = fl.FormFields()
	}
	if len(fields) == 0 {
		nodes = append(nodes, para("forms-none", T("forms.none", "There are no form fields in the document."), false))
	}
	var items []xango.Node
	for i, ff := range fields {
		i, ff := i, ff
		title := strings.TrimSpace(ff.Label)
		if title == "" {
			title = T("forms.unnamed", "Field %d (no label)", i+1)
		}
		summary := ff.Value
		switch {
		case ff.Kind == canvas.FormCheckbox && ff.Value == "true":
			summary = T("forms.ticked", "ticked")
		case ff.Kind == canvas.FormCheckbox:
			summary = ""
		}
		kids := []xango.Node{
			xango.El("strong", nil, xango.Text(title)),
			xango.El("span", []xango.Attr{xango.A("style", "margin-left:6px;color:#6b7280")}, xango.Text(formKindLabel(ff.Kind))),
		}
		if summary != "" {
			kids = append(kids, xango.El("div", []xango.Attr{xango.A("id", fmt.Sprintf("forms-value-%d", i))}, xango.Text(summary)))
		}
		if p := ff.Problem(); p != "" {
			kids = append(kids, para(fmt.Sprintf("forms-problem-%d", i), formProblemText(p), true))
		}
		if ff.Locked {
			kids = append(kids, para(fmt.Sprintf("forms-locked-%d", i), T("forms.locked", "Locked: its properties cannot be changed."), false))
		}
		kids = append(kids,
			btn(fmt.Sprintf("forms-fill-%d", i), T("forms.fill", "Fill in"), func() { u.forms = formsState{target: ff, focus: true}; renderUI() }, false))
		if !ff.Locked {
			kids = append(kids, btn(fmt.Sprintf("forms-edit-%d", i), T("forms.edit", "Change the field"), func() {
				u.forms = formsState{target: ff, edit: true, focus: true}
				renderUI()
			}, !canEdit))
		}
		lockLabel := T("forms.lock", "Lock")
		if ff.Locked {
			lockLabel = T("forms.unlock", "Unlock")
		}
		kids = append(kids, btn(fmt.Sprintf("forms-lock-%d", i), lockLabel, func() {
			if !isOwner {
				u.forms.msg = T("forms.ownerOnly", "Only the owner can lock or unlock a field.")
				renderUI()
				return
			}
			ff.Locked = !ff.Locked
			if ff.Locked && u.forms.target == ff && u.forms.edit {
				u.forms.edit = false
			}
			fl.Touch()
			redraw()
			renderUI()
		}, !canEdit))
		pos := formFieldOffset(fl, ff)
		kids = append(kids, btn(fmt.Sprintf("forms-go-%d", i), T("forms.go", "Go to it"), func() { fl.GotoOffset(pos); redraw(); renderUI() }, false))
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-doc-item"), xango.A("id", fmt.Sprintf("forms-item-%d", i))}, kids...))
	}
	if len(items) > 0 {
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list"), xango.A("id", "forms-list")}, items...))
	}
	return xango.El("div", []xango.Attr{xango.A("id", "forms-panel")}, nodes...)
}

// formFieldOffset is where the field is in the text, as a caret position.
func formFieldOffset(fl *canvas.TextFlow, target *canvas.FormFieldObject) int {
	offset := 0
	for _, it := range fl.Slice(0, utf8.RuneCountInString(fl.EditText())) {
		switch it := it.(type) {
		case canvas.Chars:
			offset += utf8.RuneCountInString(it.Text)
		case canvas.Inline:
			if it.Object == canvas.InlineObject(target) {
				return offset
			}
			offset++
		}
	}
	return 0
}

// formFiller is the box the eye opens: an input fit for the kind, with the problem with the value said as it is typed.
func formFiller(u *ui, fl *canvas.TextFlow, t *canvas.FormFieldObject, redraw, renderUI func()) xango.Node {
	label := strings.TrimSpace(t.Label)
	if label == "" {
		label = formKindLabel(t.Kind)
	}
	set := func(v string) {
		t.Value = v
		fl.Touch()
		redraw()
	}
	var input xango.Node
	switch t.Kind {
	case canvas.FormCheckbox:
		input = xango.El("div", []xango.Attr{xango.A("id", "forms-input")}, components.Checkbox(components.CheckboxProps{Label: label, Checked: t.Value == "true",
			OnChange: func(on bool) {
				if on {
					set("true")
				} else {
					set("")
				}
				renderUI()
			}}))
	case canvas.FormSelect:
		opts := []xango.Node{xango.El("option", []xango.Attr{xango.A("value", "")}, xango.Text(t.Placeholder))}
		for _, o := range t.Options {
			attrs := []xango.Attr{xango.A("value", o)}
			if o == t.Value {
				attrs = append(attrs, xango.A("selected", "selected"))
			}
			opts = append(opts, xango.El("option", attrs, xango.Text(o)))
		}
		input = xango.El("select", []xango.Attr{xango.A("id", "forms-input"), xango.A("aria-label", label),
			xango.On("change", func(ev xango.Event) { set(xango.EventValue[string](ev)); renderUI() })}, opts...)
	case canvas.FormTextArea:
		input = textarea("forms-input", label, t.Value, t.Placeholder, "4", func(v string) { set(v) })
	default:
		typ := map[canvas.FormKind]string{canvas.FormEmail: "email", canvas.FormNumber: "text", canvas.FormPhone: "tel", canvas.FormURL: "url", canvas.FormDate: "date"}[t.Kind]
		if typ == "" {
			typ = "text"
		}
		input = xango.El("input", []xango.Attr{xango.A("id", "forms-input"), xango.A("type", typ), xango.A("value", t.Value), xango.A("aria-label", label),
			xango.A("placeholder", t.Placeholder), xango.A("spellcheck", "false"), xango.A("style", "width:100%;box-sizing:border-box;font-size:13px"),
			xango.On("input", func(ev xango.Event) { set(xango.EventValue[string](ev)); renderUI() })})
	}
	kids := []xango.Node{
		xango.El("h5", []xango.Attr{xango.A("id", "forms-fill-title")}, xango.Text(T("forms.fillTitle", "Fill in: %s", label))),
		input,
	}
	if p := t.Problem(); p != "" && !t.Empty() || p == canvas.FormProblemRequired {
		kids = append(kids, para("forms-fill-problem", formProblemText(p), true))
	}
	kids = append(kids, btn("forms-fill-done", T("forms.done", "Done"), func() { u.forms.target = nil; renderUI() }, false))
	if u.forms.focus {
		u.forms.focus = false
		js.Global().Get("setTimeout").Invoke(js.FuncOf(func(js.Value, []js.Value) any {
			if el := js.Global().Get("document").Call("getElementById", "forms-input"); !el.IsNull() {
				el.Call("focus")
			}
			return nil
		}), 0)
	}
	return xango.El("section", []xango.Attr{xango.A("id", "forms-filler"), xango.A("class", "gj-doc-item")}, kids...)
}

// formPropsEditor is what the pen opens: the properties of the field.
func formPropsEditor(u *ui, fl *canvas.TextFlow, t *canvas.FormFieldObject, redraw, renderUI func()) xango.Node {
	touch := func() { fl.Touch(); redraw() }
	var kindOpts []xango.Node
	for _, k := range canvas.FormKinds {
		attrs := []xango.Attr{xango.A("value", string(k))}
		if k == t.Kind {
			attrs = append(attrs, xango.A("selected", "selected"))
		}
		kindOpts = append(kindOpts, xango.El("option", attrs, xango.Text(formKindLabel(k))))
	}
	kids := []xango.Node{
		xango.El("h5", []xango.Attr{xango.A("id", "forms-edit-title")}, xango.Text(T("forms.editTitle", "Change the field"))),
		xango.El("label", nil, xango.Text(T("forms.prop.kind", "Kind")),
			xango.El("select", []xango.Attr{xango.A("id", "forms-prop-kind"), xango.On("change", func(ev xango.Event) {
				t.Kind = canvas.FormKind(xango.EventValue[string](ev))
				touch()
				renderUI()
			})}, kindOpts...)),
		lineInput("forms-prop-label", T("forms.prop.label", "Label"), t.Label, "", func(v string) { t.Label = v; touch() }),
		lineInput("forms-prop-name", T("forms.prop.name", "Name (for the data the form gives back)"), t.Name, "", func(v string) { t.Name = v; touch() }),
		lineInput("forms-prop-placeholder", T("forms.prop.placeholder", "Placeholder"), t.Placeholder, "", func(v string) { t.Placeholder = v; touch() }),
		lineInput("forms-prop-pattern", T("forms.prop.pattern", "Pattern (a regular expression the whole value must match)"), t.Pattern, "", func(v string) {
			t.Pattern = v
			touch()
			renderUI()
		}),
	}
	if e := t.PatternError(); e != "" {
		kids = append(kids, para("forms-pattern-error", T("forms.patternBad", "That is not a valid regular expression: %s", e), true))
	}
	kids = append(kids, xango.El("div", []xango.Attr{xango.A("id", "forms-prop-required")}, components.Checkbox(components.CheckboxProps{
		Label: T("forms.prop.required", "Required"), Checked: t.Required, OnChange: func(on bool) { t.Required = on; touch(); renderUI() }})))
	if t.Kind == canvas.FormSelect {
		kids = append(kids, textarea("forms-prop-options", T("forms.prop.options", "Choices, one on each line"), strings.Join(t.Options, "\n"), "", "4", func(v string) {
			t.Options = nil
			for _, line := range strings.Split(v, "\n") {
				if line = strings.TrimSpace(line); line != "" {
					t.Options = append(t.Options, line)
				}
			}
			touch()
		}))
	}
	kids = append(kids, btn("forms-edit-done", T("forms.done", "Done"), func() { u.forms.target = nil; renderUI() }, false))
	return xango.El("section", []xango.Attr{xango.A("id", "forms-editor"), xango.A("class", "gj-doc-item")}, kids...)
}

// downloadAnswers saves the answers written in the fields as a JSON object or a CSV row. Fields with a problem are
// saved as they are, and the message says so.
func (u *ui) downloadAnswers(fl *canvas.TextFlow, format string, renderUI func()) {
	if fl == nil {
		return
	}
	var fields []formdata.Field
	problems := 0
	for _, ff := range fl.FormFields() {
		fields = append(fields, formdata.Field{Kind: string(ff.Kind), Name: ff.Name, Label: ff.Label, Value: ff.Value})
		if ff.Problem() != "" {
			problems++
		}
	}
	var data []byte
	var err error
	name, mime := "answers.json", "application/json"
	if format == "csv" {
		data, err = formdata.CSV(fields)
		name, mime = "answers.csv", "text/csv"
	} else {
		data, err = formdata.JSON(fields)
	}
	if err != nil {
		u.forms.msg = err.Error()
		renderUI()
		return
	}
	transfer.Download(name, mime, data)
	u.forms.msg = T("forms.answersSaved", "The answers were saved.")
	if problems > 0 {
		u.forms.msg = T("forms.answersProblems", "Some fields have a problem; the answers were saved as they are.")
	}
	u.announce(u.forms.msg)
	renderUI()
}

// downloadWebForm saves the document as a web page whose form fields send their answers to the address given.
func (u *ui) downloadWebForm(fl *canvas.TextFlow, renderUI func()) {
	if fl == nil {
		return
	}
	data, err := export.HTML{SubmitTo: u.forms.submitTo, SubmitLabel: T("forms.send", "Send")}.Export(export.FromDocument(docfile.FromFlow(u.title, fl)))
	if err != nil {
		u.forms.msg = T("forms.webFormBad", "The address must start with https:// or mailto: and have no spaces.")
	} else {
		transfer.Download("form.html", "text/html", data)
		u.forms.msg = T("forms.webFormSaved", "The web form was saved.")
	}
	u.announce(u.forms.msg)
	renderUI()
}
