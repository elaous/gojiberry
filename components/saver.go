package components

import (
	"fmt"
	"strings"
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
	// Updated is when it was saved (for a trashed one, when it was trashed).
	Updated time.Time
	Tags    []string
}

// SaverVersion is one snapshot of the document being edited.
type SaverVersion struct {
	Key  string
	Time time.Time
	Size int
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

	// Versions are the snapshots of the document called Name, newest first;
	// Trashed are the documents in the trash. AllTags lists every tag in use and
	// TagFilter the one the list is narrowed to ("" for all).
	Versions  []SaverVersion
	Trashed   []SaverEntry
	AllTags   []string
	TagFilter string

	OnTarget  func(name string)
	OnFormat  func(name string)
	OnName    func(name string)
	OnSave    func()
	OnRefresh func()
	OnOpen    func(key string)
	OnDelete  func(key string)

	// OnDuplicate copies a document; OnTrash moves it to the trash (OnDelete
	// removes it for good); OnTags sets its tags; OnTagFilter narrows the list.
	OnDuplicate func(key string)
	OnTrash     func(key string)
	OnTags      func(key string, tags []string)
	OnTagFilter func(tag string)
	// OnSaveVersion snapshots the document being edited; OnOpenVersion opens a
	// snapshot and OnDeleteVersion removes it.
	OnSaveVersion   func()
	OnOpenVersion   func(key string)
	OnDeleteVersion func(key string)
	// OnRestore takes a document out of the trash; OnPurge deletes it for good
	// and OnEmptyTrash all of them.
	OnRestore    func(key string)
	OnPurge      func(key string)
	OnEmptyTrash func()
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
		if props.TagFilter != "" && !hasTag(e.Tags, props.TagFilter) {
			continue
		}
		tagAttrs := []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-saver-tags"), xango.A("placeholder", "tags, separated by commas"),
			xango.A("aria-label", "Tags of "+e.Name), xango.A("value", strings.Join(e.Tags, ", "))}
		if props.OnTags != nil {
			key := e.Key
			tagAttrs = append(tagAttrs, xango.On("change", func(ev xango.Event) { props.OnTags(key, splitTags(xango.EventValue[string](ev))) }))
		}
		rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "gj-saver-entry"), xango.A("data-key", e.Key)},
			xango.El("span", []xango.Attr{xango.A("class", "gj-saver-entry-name")}, xango.Text(e.Name)),
			xango.El("span", []xango.Attr{xango.A("class", "gj-saver-entry-meta")}, xango.Text(entryMeta(e))),
			button("gj-saver-open", "Open", props.Busy, openFn(props.OnOpen, e.Key)),
			button("gj-saver-duplicate", "Duplicate", props.Busy, openFn(props.OnDuplicate, e.Key)),
			button("gj-saver-trash", "Trash", props.Busy, openFn(props.OnTrash, e.Key)),
			button("gj-saver-delete", "Delete", props.Busy, openFn(props.OnDelete, e.Key)),
			xango.El("input", tagAttrs)))
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
	if len(props.AllTags) > 0 {
		chip := func(label, tag string) xango.Node {
			class := "gj-saver-tagchip"
			if props.TagFilter == tag {
				class += " gj-saver-tagchip-on"
			}
			attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", class), xango.A("aria-pressed", boolString(props.TagFilter == tag))}
			if props.OnTagFilter != nil {
				attrs = append(attrs, xango.On("click", func(xango.Event) { props.OnTagFilter(tag) }))
			}
			return xango.El("button", attrs, xango.Text(label))
		}
		chips := []xango.Node{chip("All", "")}
		for _, t := range props.AllTags {
			chips = append(chips, chip(t, t))
		}
		list.Children = append([]xango.Node{list.Children[0], xango.El("div", []xango.Attr{xango.A("class", "gj-saver-tagbar"), xango.A("role", "group"), xango.A("aria-label", "Filter by tag")}, chips...)}, list.Children[1:]...)
	}
	children = append(children, list)

	// Versions of the document being edited.
	if props.Name != "" {
		head := xango.El("div", []xango.Attr{xango.A("class", "gj-saver-saved-head")},
			xango.El("h4", []xango.Attr{xango.A("class", "gj-saver-title")}, xango.Text("Versions of "+props.Name)),
			button("gj-saver-version-save", "Save version", props.Busy, props.OnSaveVersion))
		var vrows []xango.Node
		for _, v := range props.Versions {
			vrows = append(vrows, xango.El("li", []xango.Attr{xango.A("class", "gj-saver-version"), xango.A("data-key", v.Key)},
				xango.El("span", []xango.Attr{xango.A("class", "gj-saver-entry-name")}, xango.Text(v.Time.Format("2006-01-02 15:04:05"))),
				xango.El("span", []xango.Attr{xango.A("class", "gj-saver-entry-meta")}, xango.Text(byteSize(v.Size))),
				button("gj-saver-version-open", "Open", props.Busy, openFn(props.OnOpenVersion, v.Key)),
				button("gj-saver-version-delete", "Delete", props.Busy, openFn(props.OnDeleteVersion, v.Key))))
		}
		sec := []xango.Node{head}
		if len(vrows) == 0 {
			sec = append(sec, xango.El("p", []xango.Attr{xango.A("class", "gj-saver-empty")}, xango.Text("No versions yet.")))
		} else {
			sec = append(sec, xango.El("ul", []xango.Attr{xango.A("class", "gj-saver-list")}, vrows...))
		}
		children = append(children, xango.El("div", []xango.Attr{xango.A("class", "gj-saver-versions")}, sec...))
	}

	// The trash.
	if len(props.Trashed) > 0 {
		var trows []xango.Node
		for _, e := range props.Trashed {
			trows = append(trows, xango.El("li", []xango.Attr{xango.A("class", "gj-saver-trashed"), xango.A("data-key", e.Key)},
				xango.El("span", []xango.Attr{xango.A("class", "gj-saver-entry-name")}, xango.Text(e.Name)),
				xango.El("span", []xango.Attr{xango.A("class", "gj-saver-entry-meta")}, xango.Text(entryMeta(e))),
				button("gj-saver-restore", "Restore", props.Busy, openFn(props.OnRestore, e.Key)),
				button("gj-saver-purge", "Delete forever", props.Busy, openFn(props.OnPurge, e.Key))))
		}
		children = append(children, xango.El("div", []xango.Attr{xango.A("class", "gj-saver-trashbin")},
			xango.El("div", []xango.Attr{xango.A("class", "gj-saver-saved-head")},
				xango.El("h4", []xango.Attr{xango.A("class", "gj-saver-title")}, xango.Text("Trash")),
				button("gj-saver-empty-trash", "Empty trash", props.Busy, props.OnEmptyTrash)),
			xango.El("ul", []xango.Attr{xango.A("class", "gj-saver-list")}, trows...)))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-saver")}, children...)
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

// splitTags reads a comma-separated list of tags.
func splitTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
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
