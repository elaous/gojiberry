package components

import (
	"fmt"
	"time"

	"github.com/elaous/gojiberry/gox"
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
func Saver(props SaverProps) gox.Node {
	choice := func(label string, items []SaverChoice, current string, on func(string)) gox.Node {
		var opts []gox.Node
		for _, c := range items {
			attrs := []gox.Attr{gox.A("value", c.Name)}
			if c.Name == current {
				attrs = append(attrs, gox.A("selected", "selected"))
			}
			opts = append(opts, gox.El("option", attrs, gox.Text(c.Label)))
		}
		attrs := []gox.Attr{gox.A("class", "gj-saver-select"), gox.A("aria-label", label), gox.A("data-field", label)}
		if on != nil {
			attrs = append(attrs, gox.On("change", func(e gox.Event) { on(gox.EventValue[string](e)) }))
		}
		return gox.El("label", []gox.Attr{gox.A("class", "gj-saver-field")},
			gox.El("span", []gox.Attr{gox.A("class", "gj-saver-label")}, gox.Text(label)),
			gox.El("select", attrs, opts...))
	}
	button := func(class, label string, disabled bool, on func()) gox.Node {
		attrs := []gox.Attr{gox.A("type", "button"), gox.A("class", class)}
		if disabled || on == nil {
			attrs = append(attrs, gox.A("disabled", "disabled"))
		} else {
			attrs = append(attrs, gox.On("click", func(gox.Event) { on() }))
		}
		return gox.El("button", attrs, gox.Text(label))
	}

	nameAttrs := []gox.Attr{gox.A("type", "text"), gox.A("class", "gj-saver-name"), gox.A("value", props.Name),
		gox.A("placeholder", "Document name"), gox.A("aria-label", "Document name")}
	if props.OnName != nil {
		nameAttrs = append(nameAttrs, gox.On("input", func(e gox.Event) { props.OnName(gox.EventValue[string](e)) }))
	}

	children := []gox.Node{
		gox.El("div", []gox.Attr{gox.A("class", "gj-saver-row")},
			choice("Save to", props.Targets, props.Target, props.OnTarget),
			choice("Format", props.Formats, props.Format, props.OnFormat)),
		gox.El("div", []gox.Attr{gox.A("class", "gj-saver-row")},
			gox.El("input", nameAttrs),
			button("gj-saver-save", "Save", props.Busy || props.Name == "", props.OnSave)),
	}
	if props.Status != "" {
		class := "gj-saver-status"
		if props.Failed {
			class += " gj-saver-failed"
		}
		children = append(children, gox.El("div", []gox.Attr{gox.A("class", class), gox.A("role", "status")}, gox.Text(props.Status)))
	}

	var rows []gox.Node
	for _, e := range props.Entries {
		rows = append(rows, gox.El("li", []gox.Attr{gox.A("class", "gj-saver-entry"), gox.A("data-key", e.Key)},
			gox.El("span", []gox.Attr{gox.A("class", "gj-saver-entry-name")}, gox.Text(e.Name)),
			gox.El("span", []gox.Attr{gox.A("class", "gj-saver-entry-meta")}, gox.Text(entryMeta(e))),
			button("gj-saver-open", "Open", props.Busy, openFn(props.OnOpen, e.Key)),
			button("gj-saver-delete", "Delete", props.Busy, openFn(props.OnDelete, e.Key))))
	}
	list := gox.El("div", []gox.Attr{gox.A("class", "gj-saver-saved")},
		gox.El("div", []gox.Attr{gox.A("class", "gj-saver-saved-head")},
			gox.El("h4", []gox.Attr{gox.A("class", "gj-saver-title")}, gox.Text("Saved here")),
			button("gj-saver-refresh", "Refresh", props.Busy, props.OnRefresh)))
	if len(rows) == 0 {
		list.Children = append(list.Children, gox.El("p", []gox.Attr{gox.A("class", "gj-saver-empty")}, gox.Text("Nothing saved yet.")))
	} else {
		list.Children = append(list.Children, gox.El("ul", []gox.Attr{gox.A("class", "gj-saver-list")}, rows...))
	}
	children = append(children, list)
	return gox.El("div", []gox.Attr{gox.A("class", "gj-saver")}, children...)
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
