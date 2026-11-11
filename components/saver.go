package components

import "github.com/elaous/gojiberry/xango"

type SaverChoice struct{ Name, Label, Group string }

// SaverProps configures Saver.
type SaverProps struct {
	Targets, Formats     []SaverChoice
	Target, Format, Name string
	Status               string
	Failed               bool
	Busy                 bool

	OnTarget    func(name string)
	OnFormat    func(name string)
	OnName      func(name string)
	OnSave      func()
	OnSaveAsNew func()

	Docs     DocListProps
	Versions VersionsProps
	Trash    TrashProps
	Asker    *Asker
	// Capacity, if set, shows how full the target is.
	Capacity *CapacityProps
}

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
