package components

import (
	"fmt"
	"time"

	"github.com/elaous/gojiberry/xango"
)

// SaverChoice is one option in a Saver's target or format menu.
type SaverChoice struct{ Name, Label string }

// SaverEntry is one saved document in a Saver's list.
type SaverEntry struct {
	Key, Name string
	Format    string // the format's label
	Size      int
	Updated   time.Time
}

// SaverProps configures Saver.
type SaverProps struct {
	// Targets are the places to save to (Postgres, SQLite, S3, ...) and
	// Formats the ways to write (JSON, protobuf text, protobuf binary).
	Targets, Formats []SaverChoice
	// Target, Format and Name are the current choices and the document
	// name.
	Target, Format, Name string
	// Entries are the documents saved in the Target.
	Entries []SaverEntry
	// Status is a message under the form ("Saved notes.json"); Failed
	// makes it an error. Busy disables the buttons while a save or load
	// runs.
	Status string
	Failed bool
	Busy   bool

	OnTarget  func(name string)
	OnFormat  func(name string)
	OnName    func(name string)
	OnSave    func()
	OnRefresh func()
	OnOpen    func(key string)
	OnDelete  func(key string)
}

// Saver renders the save form: pick a target and a format, name the
// document and save it, and below it the documents already saved there,
// each to open or delete. It is controlled (see the root README's
// controlled-component notes); saver.Session keeps its state and does the
// saving.
func Saver(props SaverProps) xango.Node {
	choice := func(label string, items []SaverChoice, current string, on func(string)) xango.Node {
		var opts []xango.Node
		for _, c := range items {
			attrs := []xango.Attr{xango.A("value", c.Name)}
			if c.Name == current {
				attrs = append(attrs, xango.A("selected", "selected"))
			}
			opts = append(opts, xango.El("option", attrs, xango.Text(c.Label)))
		}
		attrs := []xango.Attr{xango.A("class", "gj-saver-select"), xango.A("aria-label", label), xango.A("data-field", label)}
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
		xango.A("placeholder", "Document name"), xango.A("aria-label", "Document name")}
	if props.OnName != nil {
		nameAttrs = append(nameAttrs, xango.On("input", func(e xango.Event) { props.OnName(xango.EventValue[string](e)) }))
	}

	children := []xango.Node{
		xango.El("div", []xango.Attr{xango.A("class", "gj-saver-row")},
			choice("Save to", props.Targets, props.Target, props.OnTarget),
			choice("Format", props.Formats, props.Format, props.OnFormat)),
		xango.El("div", []xango.Attr{xango.A("class", "gj-saver-row")},
			xango.El("input", nameAttrs),
			button("gj-saver-save", "Save", props.Busy || props.Name == "", props.OnSave)),
	}
	if props.Status != "" {
		class := "gj-saver-status"
		if props.Failed {
			class += " gj-saver-failed"
		}
		children = append(children, xango.El("div", []xango.Attr{xango.A("class", class), xango.A("role", "status")}, xango.Text(props.Status)))
	}

	var rows []xango.Node
	for _, e := range props.Entries {
		rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "gj-saver-entry"), xango.A("data-key", e.Key)},
			xango.El("span", []xango.Attr{xango.A("class", "gj-saver-entry-name")}, xango.Text(e.Name)),
			xango.El("span", []xango.Attr{xango.A("class", "gj-saver-entry-meta")}, xango.Text(entryMeta(e))),
			button("gj-saver-open", "Open", props.Busy, openFn(props.OnOpen, e.Key)),
			button("gj-saver-delete", "Delete", props.Busy, openFn(props.OnDelete, e.Key))))
	}
	list := xango.El("div", []xango.Attr{xango.A("class", "gj-saver-saved")},
		xango.El("div", []xango.Attr{xango.A("class", "gj-saver-saved-head")},
			xango.El("h4", []xango.Attr{xango.A("class", "gj-saver-title")}, xango.Text("Saved here")),
			button("gj-saver-refresh", "Refresh", props.Busy, props.OnRefresh)))
	if len(rows) == 0 {
		list.Children = append(list.Children, xango.El("p", []xango.Attr{xango.A("class", "gj-saver-empty")}, xango.Text("Nothing saved yet.")))
	} else {
		list.Children = append(list.Children, xango.El("ul", []xango.Attr{xango.A("class", "gj-saver-list")}, rows...))
	}
	children = append(children, list)
	return xango.El("div", []xango.Attr{xango.A("class", "gj-saver")}, children...)
}

func openFn(on func(string), key string) func() {
	if on == nil {
		return nil
	}
	return func() { on(key) }
}

func entryMeta(e SaverEntry) string {
	s := e.Format + " · " + byteSize(e.Size)
	if !e.Updated.IsZero() {
		s += " · " + e.Updated.Format("2006-01-02 15:04")
	}
	return s
}

func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
