package components

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/elaous/gojiberry/xango"
)

// The document components -- DocList, Trash, Versions, TagFilter -- are plug-in
// pieces: each shows one part of a document's life and takes the operations it
// offers as functions, so it works with any backend (a saver.Session, a REST API,
// a test double). An operation that is nil is not offered: its control is not
// drawn. They are controlled components: the host owns the data they show.
//
//	<Trash docs={gone} restore={restore} delete={purge} pending={p}>
//	    <DocList docs={live} trash={trash} open={open} />
//	</Trash>
//
// is, in Go,
//
//	Trash(TrashProps{Docs: gone, Restore: restore, Delete: purge, Pending: p,
//	    Children: []xango.Node{DocList(DocListProps{Docs: live, Trash: trash, Open: open})}})

// Doc is a document as the components list it. ID is a UUID that never changes;
// Name is a label, and two documents may have the same one.
type Doc struct {
	ID, Name string
	Format   string // the file format's label ("JSON")
	Size     int
	Updated  time.Time
	// Trashed is when the document was moved to the trash; zero if it is not there.
	Trashed time.Time
	Tags    []string
}

// DocVersion is a snapshot of a document.
type DocVersion struct {
	Key  string // what the version operations take
	Time time.Time
	Size int
}

// DocsAction is an operation on documents by ID, like Delete, Restore or Trash.
// It may take a while (a server call): components run it in the background, with
// Pending showing that it is running, and show its error if it fails.
type DocsAction func(ctx context.Context, ids []string) error

// DocAction is an operation on one document (or version) by ID (or key).
type DocAction func(ctx context.Context, id string) error

// Pending is the Go side of a Promise: it runs an operation in the background
// and remembers whether it is still running and how it ended, which the
// components show. Make one with NewPending, whose onChange the host uses to
// render again; one Pending may be shared by every component on a page.
type Pending struct {
	mu     sync.Mutex
	busy   int
	err    error
	change func()
	// Inline runs operations on the calling goroutine (for tests).
	Inline bool
}

// NewPending makes a Pending that calls onChange whenever its state changes (from
// whichever goroutine the operation ends on).
func NewPending(onChange func()) *Pending { return &Pending{change: onChange} }

// Run starts f and returns at once; a nil Pending just runs it. While f runs
// Busy is true; when it fails Err is its error until the next Run.
func (p *Pending) Run(f func(ctx context.Context) error) {
	if p == nil {
		go func() { _ = f(context.Background()) }()
		return
	}
	p.mu.Lock()
	if p.busy == 0 {
		p.err = nil // a new burst of work: the last one's error has been seen
	}
	p.busy++
	p.mu.Unlock()
	p.notify()
	job := func() {
		err := f(context.Background())
		p.mu.Lock()
		p.busy--
		if err != nil {
			p.err = err // a later success does not hide it
		}
		p.mu.Unlock()
		p.notify()
	}
	if p.Inline {
		job()
		return
	}
	go job()
}

func (p *Pending) notify() {
	if p.change != nil {
		p.change()
	}
}

// Busy reports whether an operation is running.
func (p *Pending) Busy() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.busy > 0
}

// Err is the error the last operation ended with.
func (p *Pending) Err() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.err
}

// act is the click handler for an operation on ids; nil when the operation is not
// offered.
func act(p *Pending, a DocsAction, ids ...string) func() {
	if a == nil {
		return nil
	}
	return func() { p.Run(func(ctx context.Context) error { return a(ctx, ids) }) }
}

func actOne(p *Pending, a DocAction, id string) func() {
	if a == nil {
		return nil
	}
	return func() { p.Run(func(ctx context.Context) error { return a(ctx, id) }) }
}

func docButton(class, label string, disabled bool, on func()) xango.Node {
	attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", class)}
	if disabled || on == nil {
		attrs = append(attrs, xango.A("disabled", "disabled"))
	} else {
		attrs = append(attrs, xango.On("click", func(xango.Event) { on() }))
	}
	return xango.El("button", attrs, xango.Text(label))
}

// pendingError is the line under a component saying what went wrong.
func pendingError(p *Pending) []xango.Node {
	if err := p.Err(); err != nil {
		return []xango.Node{xango.El("p", []xango.Attr{xango.A("class", "gj-doc-error"), xango.A("role", "alert")}, xango.Text(err.Error()))}
	}
	return nil
}

func docMeta(d Doc) string {
	var parts []string
	if d.Format != "" {
		parts = append(parts, d.Format)
	}
	parts = append(parts, byteSize(d.Size))
	when := d.Updated
	if !d.Trashed.IsZero() {
		when = d.Trashed
	}
	if !when.IsZero() {
		parts = append(parts, when.Format("2006-01-02 15:04"))
	}
	return strings.Join(parts, " · ")
}

// --- DocList -------------------------------------------------------------

// DocListProps configures DocList.
type DocListProps struct {
	// Docs are the documents to list (the ones not in the trash). Current is the
	// ID of the one being edited, marked in the list.
	Docs    []Doc
	Current string
	// The operations a document row offers; a nil one is left out.
	Open      DocAction
	Duplicate DocAction
	Trash     DocsAction // moves to the trash: see Trash for getting them back
	// Delete removes a document for good, with no trash in between.
	Delete DocAction
	// Tag edits a document's tags.
	Tag func(ctx context.Context, id string, tags []string) error
	// TagFilter is the tag the list is narrowed to ("" for all), set by
	// OnTagFilter.
	TagFilter   string
	OnTagFilter func(tag string)
	// Title heads the list; Refresh, if set, adds a Refresh button.
	Title   string
	Refresh func()
	Pending *Pending
}

// DocList lists documents, each with the operations the host plugs in: Open,
// Duplicate, Trash, Delete and a tag editor, and above them a filter by tag.
func DocList(p DocListProps) xango.Node {
	busy := p.Pending.Busy()
	title := p.Title
	if title == "" {
		title = tr("docs.savedHere", "Saved here")
	}
	head := []xango.Node{xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(title))}
	if p.Refresh != nil {
		head = append(head, docButton("gj-doclist-refresh", tr("docs.refresh", "Refresh"), busy, p.Refresh))
	}
	children := []xango.Node{xango.El("div", []xango.Attr{xango.A("class", "gj-doc-head")}, head...)}

	var tags []string
	for _, d := range p.Docs {
		tags = append(tags, d.Tags...)
	}
	if bar := TagFilter(TagFilterProps{Tags: tags, Selected: p.TagFilter, OnSelect: p.OnTagFilter}); len(tags) > 0 {
		children = append(children, bar)
	}

	var rows []xango.Node
	for _, d := range p.Docs {
		if p.TagFilter != "" && !hasTag(d.Tags, p.TagFilter) {
			continue
		}
		class := "gj-doclist-item"
		if d.ID == p.Current {
			class += " gj-doclist-current"
		}
		row := []xango.Node{
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name")}, xango.Text(d.Name)),
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(docMeta(d))),
		}
		if p.Open != nil {
			row = append(row, docButton("gj-doclist-open", tr("docs.open", "Open"), busy, actOne(p.Pending, p.Open, d.ID)))
		}
		if p.Duplicate != nil {
			row = append(row, docButton("gj-doclist-duplicate", tr("docs.duplicate", "Duplicate"), busy, actOne(p.Pending, p.Duplicate, d.ID)))
		}
		if p.Trash != nil {
			row = append(row, docButton("gj-doclist-trash", tr("docs.trash", "Trash"), busy, act(p.Pending, p.Trash, d.ID)))
		}
		if p.Delete != nil {
			row = append(row, docButton("gj-doclist-delete", tr("docs.delete", "Delete"), busy, actOne(p.Pending, p.Delete, d.ID)))
		}
		if p.Tag != nil {
			id := d.ID
			row = append(row, xango.El("input", []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-doclist-tags"),
				xango.A("placeholder", tr("docs.tagsPlaceholder", "tags, separated by commas")),
				xango.A("aria-label", trf("docs.tagsOf", "Tags of %s", d.Name)), xango.A("value", strings.Join(d.Tags, ", ")),
				xango.On("change", func(ev xango.Event) {
					tags := splitTags(xango.EventValue[string](ev))
					p.Pending.Run(func(ctx context.Context) error { return p.Tag(ctx, id, tags) })
				})}))
		}
		rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", class), xango.A("data-id", d.ID)}, row...))
	}
	if len(rows) == 0 {
		children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-empty")}, xango.Text(tr("docs.empty", "Nothing saved yet."))))
	} else {
		children = append(children, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, rows...))
	}
	children = append(children, pendingError(p.Pending)...)
	return xango.El("div", []xango.Attr{xango.A("class", "gj-doclist")}, children...)
}

// --- TagFilter -----------------------------------------------------------

// TagFilterProps configures TagFilter.
type TagFilterProps struct {
	// Tags are the tags in use (repeats and case differences are folded).
	Tags []string
	// Selected is the tag the list is narrowed to, "" for All.
	Selected string
	OnSelect func(tag string)
}

// TagFilter is a row of chips, All and one for each tag, that picks which tag a
// list is narrowed to.
func TagFilter(p TagFilterProps) xango.Node {
	seen := map[string]bool{}
	var uniq []string
	for _, t := range p.Tags {
		if k := strings.ToLower(t); !seen[k] {
			seen[k] = true
			uniq = append(uniq, t)
		}
	}
	sortFold(uniq)
	chip := func(label, tag string) xango.Node {
		on := strings.EqualFold(p.Selected, tag)
		class := "gj-tagchip"
		if on {
			class += " gj-tagchip-on"
		}
		attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", class), xango.A("aria-pressed", boolString(on))}
		if p.OnSelect != nil {
			attrs = append(attrs, xango.On("click", func(xango.Event) { p.OnSelect(tag) }))
		}
		return xango.El("button", attrs, xango.Text(label))
	}
	chips := []xango.Node{chip(tr("docs.all", "All"), "")}
	for _, t := range uniq {
		chips = append(chips, chip(t, t))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-tagbar"), xango.A("role", "group"), xango.A("aria-label", tr("docs.filterByTag", "Filter by tag"))}, chips...)
}

// --- Trash ---------------------------------------------------------------

// TrashProps configures Trash.
type TrashProps struct {
	// Docs are the documents in the trash.
	Docs []Doc
	// Restore puts documents back; Delete removes them for good. Either may be
	// nil.
	Restore DocsAction
	Delete  DocsAction
	// Selected are the IDs ticked in the trash; with OnSelect set, each row gets a
	// checkbox and the buttons act on the ticked rows, else on the row itself.
	Selected []string
	OnSelect func(ids []string)
	// Children are what goes with the trash: usually the list of documents that
	// can be trashed. They are drawn above it.
	Children []xango.Node
	Pending  *Pending
}

// TrashBackend is what a trash needs from a store; Use fills Restore and Delete
// from one.
type TrashBackend interface {
	Restore(ctx context.Context, ids ...string) error
	// Purge deletes documents in the trash for good.
	Purge(ctx context.Context, ids ...string) error
}

// Use makes p's Restore and Delete the backend's.
func (p TrashProps) Use(b TrashBackend) TrashProps {
	p.Restore = func(ctx context.Context, ids []string) error { return b.Restore(ctx, ids...) }
	p.Delete = func(ctx context.Context, ids []string) error { return b.Purge(ctx, ids...) }
	return p
}

// Trash shows the documents in the trash, each to Restore or Delete forever, and
// Empty trash to delete them all; Children (the live list) come first. With
// nothing in the trash only the Children are drawn.
func Trash(p TrashProps) xango.Node {
	children := append([]xango.Node{}, p.Children...)
	if len(p.Docs) > 0 {
		busy := p.Pending.Busy()
		selected := map[string]bool{}
		for _, id := range p.Selected {
			selected[id] = true
		}
		var allIDs []string
		var rows []xango.Node
		for _, d := range p.Docs {
			allIDs = append(allIDs, d.ID)
			row := []xango.Node{}
			if p.OnSelect != nil {
				id := d.ID
				cb := []xango.Attr{xango.A("type", "checkbox"), xango.A("class", "gj-trash-select"), xango.A("aria-label", trf("docs.select", "Select %s", d.Name))}
				if selected[id] {
					cb = append(cb, xango.A("checked", "checked"))
				}
				cb = append(cb, xango.On("change", func(xango.Event) { p.OnSelect(toggled(p.Selected, id)) }))
				row = append(row, xango.El("input", cb))
			}
			row = append(row,
				xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name")}, xango.Text(d.Name)),
				xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(docMeta(d))))
			if p.OnSelect == nil {
				row = append(row,
					docButton("gj-trash-restore", tr("docs.restore", "Restore"), busy, act(p.Pending, p.Restore, d.ID)),
					docButton("gj-trash-delete", tr("docs.deleteForever", "Delete forever"), busy, act(p.Pending, p.Delete, d.ID)))
			}
			rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "gj-trash-item"), xango.A("data-id", d.ID)}, row...))
		}
		head := []xango.Node{xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(tr("docs.trashTitle", "Trash")))}
		if p.OnSelect != nil && len(p.Selected) > 0 {
			head = append(head,
				docButton("gj-trash-restore-selected", trf("docs.restoreN", "Restore %d", len(p.Selected)), busy, act(p.Pending, p.Restore, p.Selected...)),
				docButton("gj-trash-delete-selected", trf("docs.deleteN", "Delete %d forever", len(p.Selected)), busy, act(p.Pending, p.Delete, p.Selected...)))
		}
		head = append(head, docButton("gj-trash-empty", tr("docs.emptyTrash", "Empty trash"), busy, act(p.Pending, p.Delete, allIDs...)))
		bin := []xango.Node{xango.El("div", []xango.Attr{xango.A("class", "gj-doc-head")}, head...),
			xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, rows...)}
		bin = append(bin, pendingError(p.Pending)...)
		children = append(children, xango.El("div", []xango.Attr{xango.A("class", "gj-trash-bin")}, bin...))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-trash")}, children...)
}

// --- Versions ------------------------------------------------------------

// VersionsProps configures Versions.
type VersionsProps struct {
	// Name is the document the versions are of, for the heading.
	Name     string
	Versions []DocVersion
	// Save snapshots the document being edited; Open puts a snapshot in the
	// editor; Delete removes one. Each may be nil.
	Save    func(ctx context.Context) error
	Open    DocAction
	Delete  DocAction
	Pending *Pending
}

// Versions lists a document's snapshots, newest first, to open or delete, with a
// button to save a new one.
func Versions(p VersionsProps) xango.Node {
	busy := p.Pending.Busy()
	head := []xango.Node{xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(trf("docs.versionsOf", "Versions of %s", p.Name)))}
	if p.Save != nil {
		head = append(head, docButton("gj-versions-save", tr("docs.saveVersion", "Save version"), busy,
			func() { p.Pending.Run(p.Save) }))
	}
	children := []xango.Node{xango.El("div", []xango.Attr{xango.A("class", "gj-doc-head")}, head...)}
	var rows []xango.Node
	for _, v := range p.Versions {
		row := []xango.Node{
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name")}, xango.Text(v.Time.Format("2006-01-02 15:04:05"))),
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(byteSize(v.Size)))}
		if p.Open != nil {
			row = append(row, docButton("gj-versions-open", tr("docs.open", "Open"), busy, actOne(p.Pending, p.Open, v.Key)))
		}
		if p.Delete != nil {
			row = append(row, docButton("gj-versions-delete", tr("docs.delete", "Delete"), busy, actOne(p.Pending, p.Delete, v.Key)))
		}
		rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "gj-versions-item"), xango.A("data-key", v.Key)}, row...))
	}
	if len(rows) == 0 {
		children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-empty")}, xango.Text(tr("docs.noVersions", "No versions yet."))))
	} else {
		children = append(children, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, rows...))
	}
	children = append(children, pendingError(p.Pending)...)
	return xango.El("div", []xango.Attr{xango.A("class", "gj-versions")}, children...)
}

// toggled is ids with id added, or removed if it is there.
func toggled(ids []string, id string) []string {
	out := make([]string, 0, len(ids)+1)
	found := false
	for _, x := range ids {
		if x == id {
			found = true
			continue
		}
		out = append(out, x)
	}
	if !found {
		out = append(out, id)
	}
	return out
}

func sortFold(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && strings.ToLower(s[j]) < strings.ToLower(s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
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

func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
