package components

import "github.com/elaous/gojiberry/xango"

// SaverChoice is one choice of a list: its name, what to show, and optionally the group
// it is shown under (a heading in a menu).
type SaverChoice struct{ Name, Label, Group string }

// SaverProps configures Saver.
type SaverProps struct {
	// Targets are the places to save to (Postgres, SQLite, S3, ...) and
	// Formats the ways to write (JSON, protobuf text, protobuf binary).
	Targets, Formats []SaverChoice
	// Target, Format and Name are the current choices and the document
	// name.
	Target, Format, Name string
	// Status is a message under the form ("Saved notes.json"); Failed
	// makes it an error. Busy disables the buttons while a save or load
	// runs.
	Status string
	Failed bool
	Busy   bool

	OnTarget func(name string)
	OnFormat func(name string)
	OnName   func(name string)
	OnSave   func()
	// OnSaveAsNew, if set, adds a button that saves the document as a new one
	// instead of replacing the saved document being edited.
	OnSaveAsNew func()

	// Docs, Versions and Trash are the document components the form is
	// composed with (see DocList, Versions and Trash, which can also be used on
	// their own). Versions is shown when it names a document; Trash when
	// something is in it.
	Docs     DocListProps
	Versions VersionsProps
	Trash    TrashProps
	// Asker, if set, is where Docs, Trash and People ask before destructive
	// actions (give them its Ask as Confirm); the form draws its dialog.
	Asker *Asker
	// Capacity, if set, shows how full the target is.
	Capacity *CapacityProps
}

// Saver renders the save form: pick a target and a format, name the
// document and save it, and below it the documents already saved there,
// each to open or delete. It is controlled (see the root README's
// controlled-component notes); saver.Session keeps its state and does the
// saving.
func Saver(props SaverProps) xango.Node {
	choice := func(field, label string, items []SaverChoice, current string, on func(string)) xango.Node {
		var opts []xango.Node
		for _, c := range items {
			attrs := []xango.Attr{xango.A("value", c.Name)}
			if c.Name == current {
				attrs = append(attrs, xango.A("selected", "selected"))
			}
			opts = append(opts, xango.El("option", attrs, xango.Text(c.Label)))
		}
		attrs := []xango.Attr{xango.A("class", "gj-saver-select"), xango.A("aria-label", label), xango.A("data-field", field)}
		if on != nil {
			attrs = append(attrs, xango.On("change", func(e xango.Event) { on(xango.EventValue[string](e)) }))
		}
		return xango.El("label", []xango.Attr{xango.A("class", "gj-saver-field")},
			xango.El("span", []xango.Attr{xango.A("class", "gj-saver-label")}, xango.Text(label)),
			xango.El("select", attrs, opts...))
	}
	button := func(class, label string, disabled bool, on func()) xango.Node {
		attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", class)}
		if disabled || on == nil {
			attrs = append(attrs, xango.A("disabled", "disabled"))
		} else {
			attrs = append(attrs, xango.On("click", func(xango.Event) { on() }))
		}
		return xango.El("button", attrs, xango.Text(label))
	}

	nameAttrs := []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-saver-name"), xango.A("value", props.Name),
		xango.A("placeholder", tr("saver.docName", "Document name")), xango.A("aria-label", tr("saver.docName", "Document name"))}
	if props.OnName != nil {
		nameAttrs = append(nameAttrs, xango.On("input", func(e xango.Event) { props.OnName(xango.EventValue[string](e)) }))
	}

	children := []xango.Node{
		xango.El("div", []xango.Attr{xango.A("class", "gj-saver-row")},
			choice("Save to", tr("saver.saveTo", "Save to"), props.Targets, props.Target, props.OnTarget),
			choice("Format", tr("saver.format", "Format"), props.Formats, props.Format, props.OnFormat)),
		xango.El("div", []xango.Attr{xango.A("class", "gj-saver-row")},
			xango.El("input", nameAttrs),
			button("gj-saver-save", tr("saver.save", "Save"), props.Busy || props.Name == "", props.OnSave),
			saveAsNew(props, button)),
	}
	if props.Status != "" {
		class := "gj-saver-status"
		if props.Failed {
			class += " gj-saver-failed"
		}
		children = append(children, xango.El("div", []xango.Attr{xango.A("class", class), xango.A("role", "status")}, xango.Text(props.Status)))
	}

	if props.Capacity != nil {
		children = append(children, Capacity(*props.Capacity))
	}
	children = append(children, DocList(props.Docs))
	if props.Versions.Name != "" {
		children = append(children, Versions(props.Versions))
	}
	if len(props.Trash.Docs) > 0 {
		children = append(children, Trash(props.Trash))
	}
	if props.Asker != nil {
		children = append(children, props.Asker.Dialog())
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-saver")}, children...)
}

func saveAsNew(props SaverProps, button func(class, label string, disabled bool, on func()) xango.Node) xango.Node {
	if props.OnSaveAsNew == nil {
		return xango.Text("")
	}
	return button("gj-saver-save-new", tr("saver.saveAsNew", "Save as new"), props.Busy || props.Name == "", props.OnSaveAsNew)
}
