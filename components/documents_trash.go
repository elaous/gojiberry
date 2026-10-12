package components

import (
	"context"
	"github.com/elaous/gojiberry/docdiff"
	"github.com/elaous/gojiberry/xango"
)

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
	// Confirm, if set, is asked before documents are deleted for good (see
	// Asker.Ask); nil deletes at once.
	Confirm func(message string, do func())
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
				cb := []xango.Attr{xango.A("type", "checkbox"), xango.A("class", "gj-trash-select"), xango.A("aria-label", trf("docs.select", "Select %s", d.shown()))}
				if selected[id] {
					cb = append(cb, xango.A("checked", "checked"))
				}
				cb = append(cb, xango.On("change", func(xango.Event) { p.OnSelect(toggled(p.Selected, id)) }))
				row = append(row, xango.El("input", cb))
			}
			row = append(row,
				xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name"+confidentialClass(d.Confidential))}, docNameNodes(d)...),
				xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(docMeta(d))))
			if p.OnSelect == nil {
				row = append(row,
					docButton("gj-trash-restore", tr("docs.restore", "Restore"), busy, act(p.Pending, p.Restore, d.ID)),
					docButton("gj-trash-delete", tr("docs.deleteForever", "Delete forever"), busy,
						confirmed(p.Confirm, trf("docs.confirmForever", "Delete %s forever? This cannot be undone.", d.shown()), act(p.Pending, p.Delete, d.ID))))
			}
			rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "gj-trash-item"), xango.A("data-id", d.ID)}, row...))
		}
		head := []xango.Node{xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(tr("docs.trashTitle", "Trash")))}
		if p.OnSelect != nil && len(p.Selected) > 0 {
			head = append(head,
				docButton("gj-trash-restore-selected", trf("docs.restoreN", "Restore %d", len(p.Selected)), busy, act(p.Pending, p.Restore, p.Selected...)),
				docButton("gj-trash-delete-selected", trf("docs.deleteN", "Delete %d forever", len(p.Selected)), busy,
					confirmed(p.Confirm, trf("docs.confirmForeverN", "Delete %d documents forever? This cannot be undone.", len(p.Selected)), act(p.Pending, p.Delete, p.Selected...))))
		}
		head = append(head, docButton("gj-trash-empty", tr("docs.emptyTrash", "Empty trash"), busy,
			confirmed(p.Confirm, trf("docs.confirmEmpty", "Delete all %d documents in the trash forever? This cannot be undone.", len(allIDs)), act(p.Pending, p.Delete, allIDs...))))
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
	// Compare shows what changed between a snapshot and the document being edited; Diff is that
	// comparison (nil before one was asked for) and DiffKey the snapshot it is for.
	Compare DocAction
	Diff    []docdiff.Change
	DiffKey string
	// Rename gives a snapshot a name and Pin keeps it (or lets it go); each may be nil.
	Rename func(ctx context.Context, key, name string) error
	Pin    func(ctx context.Context, key string, pinned bool) error
	// Restore goes back to a snapshot, keeping the current text as a version first; may be nil.
	Restore DocAction
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
		when := v.Time.Format("2006-01-02 15:04:05")
		title := when
		if v.Name != "" {
			title = v.Name + " (" + when + ")"
		}
		row := []xango.Node{
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name")}, xango.Text(when)),
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(byteSize(v.Size)))}
		if v.Author != "" {
			row = append(row, xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta gj-versions-author")}, xango.Text(trf("docs.versionBy", "by %s", v.Author))))
		}
		if p.Rename != nil {
			key := v.Key
			row = append(row, xango.El("input", []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-versions-name"), xango.A("maxlength", "120"),
				xango.A("placeholder", tr("docs.versionNamePlaceholder", "name this version")),
				xango.A("aria-label", trf("docs.versionNameOf", "Name of the version of %s", when)), xango.A("value", v.Name),
				xango.On("change", func(ev xango.Event) {
					name := xango.EventValue[string](ev)
					p.Pending.Run(func(ctx context.Context) error { return p.Rename(ctx, key, name) })
				})}))
		} else if v.Name != "" {
			row = append(row, xango.El("span", []xango.Attr{xango.A("class", "gj-versions-name-text")}, xango.Text(v.Name)))
		}
		if p.Pin != nil {
			key, pinned := v.Key, v.Pinned
			cb := []xango.Attr{xango.A("type", "checkbox"), xango.A("class", "gj-versions-pin"),
				xango.A("aria-label", trf("docs.versionKeep", "Keep the version of %s", title))}
			if pinned {
				cb = append(cb, xango.A("checked", "checked"))
			}
			cb = append(cb, xango.On("change", func(xango.Event) {
				p.Pending.Run(func(ctx context.Context) error { return p.Pin(ctx, key, !pinned) })
			}))
			row = append(row, xango.El("label", []xango.Attr{xango.A("class", "gj-versions-keep")}, xango.El("input", cb), xango.Text(tr("docs.versionKeepLabel", "Keep"))))
		}
		if p.Compare != nil {
			row = append(row, docButtonAs("gj-versions-compare", tr("docs.compare", "Compare"), trf("docs.compareWith", "Compare %s with the current document", v.Time.Format("2006-01-02 15:04:05")), busy, actOne(p.Pending, p.Compare, v.Key)))
		}
		if p.Restore != nil {
			row = append(row, docButtonAs("gj-versions-restore", tr("docs.restore", "Restore"), trf("docs.restoreVersion", "Restore the version of %s (what you have now is kept as a version first)", when), busy, actOne(p.Pending, p.Restore, v.Key)))
		}
		if p.Open != nil {
			row = append(row, docButton("gj-versions-open", tr("docs.open", "Open"), busy, actOne(p.Pending, p.Open, v.Key)))
		}
		if p.Delete != nil {
			// a kept version is not deleted until it is let go
			row = append(row, docButton("gj-versions-delete", tr("docs.delete", "Delete"), busy || v.Pinned, actOne(p.Pending, p.Delete, v.Key)))
		}
		rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "gj-versions-item"), xango.A("data-key", v.Key)}, row...))
	}
	if len(rows) == 0 {
		children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-empty")}, xango.Text(tr("docs.noVersions", "No versions yet."))))
	} else {
		children = append(children, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list")}, rows...))
	}
	if p.DiffKey != "" {
		children = append(children, xango.El("h5", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(tr("docs.compareHeading", "Changes since this version"))),
			VersionDiff(VersionDiffProps{Changes: p.Diff}))
	}
	children = append(children, pendingError(p.Pending)...)
	return xango.El("div", []xango.Attr{xango.A("class", "gj-versions")}, children...)
}
