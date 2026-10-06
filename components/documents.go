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
	// Confidential marks a document protected end to end: its real name and tags are inside the
	// envelope, so a UI draws a lock. NameHidden says the name shown is the placeholder (the
	// document has not been opened or revealed here, so its real name is not known): the list
	// offers Reveal for it.
	Confidential, NameHidden bool
}

// DocFolder is a folder as the components list it: like a document, an ID and a
// name that need not be unique; Parent is the folder it is in, "" at the top.
//
// Confidential marks a folder whose real name is kept inside the envelope: Name is then the
// placeholder ("Confidential folder") until the host has revealed it, so a UI can draw a lock.
//
// NameHidden says the name is still the placeholder (not revealed here): the list offers Reveal.
type DocFolder struct {
	ID, Name, Parent string
	Confidential     bool
	NameHidden       bool
}

// shown is the name to draw: the real one, or, for a name that is still the placeholder
// (NameHidden), the placeholder in the interface's language. The catalog stores the English
// constant (saver.ConfidentialName) because it is data that packs and other clients read; what
// is drawn is translated here and never stored.
func (d Doc) shown() string {
	if d.NameHidden {
		return tr("docs.confidentialName", "Confidential document")
	}
	return d.Name
}

// shown is Doc.shown for a folder (saver.ConfidentialFolderName).
func (f DocFolder) shown() string {
	if f.NameHidden {
		return tr("docs.confidentialFolderName", "Confidential folder")
	}
	return f.Name
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
	return docButtonAs(class, label, "", disabled, on)
}

// docButtonAs is docButton with an accessible name (aria-label) that says which row it is for,
// when the visible words alone ("Make confidential") would be the same on every row.
func docButtonAs(class, label, accessible string, disabled bool, on func()) xango.Node {
	attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", class)}
	if accessible != "" {
		attrs = append(attrs, xango.A("aria-label", accessible))
	}
	if disabled || on == nil {
		attrs = append(attrs, xango.A("disabled", "disabled"))
	} else {
		attrs = append(attrs, xango.On("click", func(xango.Event) { on() }))
	}
	if icon, ok := docIcons[class]; ok {
		return xango.El("button", attrs, Material(icon), xango.Text(label))
	}
	return xango.El("button", attrs, xango.Text(label))
}

// docIcons are the icons of the document components' buttons, by class.
var docIcons = map[string]string{
	"gj-doclist-open": "description", "gj-doclist-duplicate": "content_copy", "gj-doclist-trash": "delete",
	"gj-doclist-delete": "delete_forever", "gj-doclist-refresh": "refresh", "gj-doclist-check": "fact_check", "gj-doclist-download": "download",
	"gj-doclist-folder-open": "folder_open", "gj-doclist-folder-delete": "delete_forever",
	"gj-doclist-protect": "lock", "gj-doclist-reveal": "key", "gj-doclist-folder-protect": "lock", "gj-doclist-folder-reveal": "key",
	"gj-doclist-folder-download": "download", "gj-doclist-folder-pack": "folder", "gj-doclist-import-pack": "upload_file", "gj-doclist-newfolder": "create_new_folder",
	"gj-migrate-pick": "upload_file", "gj-migrate-start": "download",
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

// DocCheck is the operation behind DocListProps.Check.
type DocCheck func(ctx context.Context) error

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
	// Check reads every document and compares it with its checksum, restoring damaged
	// ones from their previous copy (saver.Session.Check); nil leaves the button out.
	Check   DocCheck
	Pending *Pending
	// Confirm, if set, is asked before Delete (see Asker.Ask); nil deletes at once.
	Confirm func(message string, do func())
	// ConfirmAs, if set, asks the same question with the words of the confirm button (an Asker's
	// AskWith): the protection switches use it so the dialog says "Make confidential", not
	// "Delete". Without it they use Confirm, or act at once.
	ConfirmAs func(label, message string, do func())

	// Folders are the folders inside the one being shown, and Path the folders from
	// the top down to it (empty at the top level), drawn as a breadcrumb.
	// AllFolders, with their Parents, are where a document can be moved to.
	Folders, Path, AllFolders []DocFolder
	// OpenFolder goes into a folder ("" is the top level, the breadcrumb's first
	// item); NewFolder makes one in the folder shown, named by FolderDraft (typed
	// into a box that reports to OnFolderDraft); RenameFolder and DeleteFolder
	// (which asks first) and Move (documents to a folder, "" the top level)
	// complete it. A nil operation leaves its control out.
	OpenFolder    DocAction
	NewFolder     func(ctx context.Context, name string) error
	FolderDraft   string
	OnFolderDraft func(name string)
	RenameFolder  func(ctx context.Context, id, name string) error
	DeleteFolder  DocAction
	Move          func(ctx context.Context, ids []string, folder string) error

	// Link gives the address of a document and FolderLink of a folder; with
	// CopyLink (which receives that address) they add a Link button to each row.
	Link, FolderLink func(id string) string
	CopyLink         func(url string)
	// Download hands a document over in a format picked from DownloadFormats
	// (none: one Download button, "" for the format); DownloadFolder hands over
	// a folder's documents together.
	// OpenDownload, if set, shows the dialog that asks whether to keep the version
	// history in an opendocs download.
	OpenDownload *OpenDownload
	// DownloadPack hands over a folder ("" for everything) as one .opendoc pack, and
	// ImportPack asks for a .opendoc file to bring in. Each shows its button when set.
	DownloadPack    func(ctx context.Context, id string) error
	ImportPack      func()
	Download        func(ctx context.Context, id, format string) error
	DownloadFolder  DocAction
	DownloadFormats []DocChoice

	// SetProtection makes a document confidential (true: its name and tags move inside the
	// encrypted document) or not (false), and SetFolderProtection does the same for a folder's
	// name. Each is offered as "Make confidential" / "Make not confidential" with a question
	// first (Confirm) that says what changes; a nil operation leaves the control out. Reveal and
	// RevealFolder read the real name of a confidential document or folder whose name is still
	// hidden (Doc.NameHidden), which works once the host holds the key.
	SetProtection       func(ctx context.Context, id string, confidential bool) error
	SetFolderProtection func(ctx context.Context, id string, confidential bool) error
	Reveal              DocAction
	RevealFolder        DocAction
}

// DocChoice is a name and the label shown for it.
type DocChoice struct{ Name, Label, Group string }

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
	if p.ImportPack != nil {
		head = append(head, docButton("gj-doclist-import-pack", tr("docs.importPack", "Import .opendoc"), busy, p.ImportPack))
	}
	if p.Check != nil {
		check := p.Check
		head = append(head, docButton("gj-doclist-check", tr("docs.check", "Check documents"), busy, func() { p.Pending.Run(check) }))
	}
	children := []xango.Node{xango.El("div", []xango.Attr{xango.A("class", "gj-doc-head")}, head...)}
	if crumbs, ok := docBreadcrumbs(p); ok {
		children = append(children, crumbs)
	}
	if len(p.Folders) > 0 || p.NewFolder != nil {
		children = append(children, folderRows(p, busy)...)
	}

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
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name"+confidentialClass(d.Confidential))}, docNameNodes(d)...),
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(docMeta(d))),
		}
		if p.Open != nil {
			row = append(row, docButton("gj-doclist-open", tr("docs.open", "Open"), busy, actOne(p.Pending, p.Open, d.ID)))
		}
		if p.Duplicate != nil {
			row = append(row, docButton("gj-doclist-duplicate", tr("docs.duplicate", "Duplicate"), busy, actOne(p.Pending, p.Duplicate, d.ID)))
		}
		row = append(row, protectionButtons(p, d, busy)...)
		row = append(row, linkButton(p, p.Link, d.ID)...)
		row = append(row, downloadControl(p, d, busy)...)
		if p.Trash != nil {
			row = append(row, docButton("gj-doclist-trash", tr("docs.trash", "Trash"), busy, act(p.Pending, p.Trash, d.ID)))
		}
		if p.Delete != nil {
			row = append(row, docButton("gj-doclist-delete", tr("docs.delete", "Delete"), busy,
				confirmed(p.Confirm, trf("docs.confirmDelete", "Delete %s for good? This cannot be undone.", d.shown()), actOne(p.Pending, p.Delete, d.ID))))
		}
		if p.Move != nil && (len(p.AllFolders) > 1 || len(p.AllFolders) == 1 && len(p.Path) == 0 || len(p.Path) > 0) {
			row = append(row, moveSelect(p, d, busy))
		}
		if p.Tag != nil {
			id := d.ID
			row = append(row, xango.El("input", []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-doclist-tags"),
				xango.A("placeholder", tr("docs.tagsPlaceholder", "tags, separated by commas")),
				xango.A("aria-label", trf("docs.tagsOf", "Tags of %s", d.shown())), xango.A("value", strings.Join(d.Tags, ", ")),
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
	if p.OpenDownload != nil {
		children = append(children, OpenDownloadDialog(*p.OpenDownload))
	}
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

// docBreadcrumbs is the trail from the top level to the folder being shown; not
// there when folders are not in use.
func docBreadcrumbs(p DocListProps) (xango.Node, bool) {
	if p.OpenFolder == nil && len(p.Path) == 0 {
		return xango.Node{}, false
	}
	item := func(label, id string, last bool) BreadcrumbItem {
		it := BreadcrumbItem{Label: label}
		if !last && p.OpenFolder != nil {
			it.Href = "#" + id
			it.OnClick = func(e xango.Event) {
				e.PreventDefault()
				p.Pending.Run(func(ctx context.Context) error { return p.OpenFolder(ctx, id) })
			}
		}
		return it
	}
	items := []BreadcrumbItem{item(tr("docs.allDocuments", "Documents"), "", len(p.Path) == 0)}
	for i, f := range p.Path {
		items = append(items, item(f.shown(), f.ID, i == len(p.Path)-1))
	}
	return Breadcrumbs(BreadcrumbsProps{Items: items}), true
}

// folderRows are the folders in the one shown, each to open, rename or delete,
// and the form for a new one.
func folderRows(p DocListProps, busy bool) []xango.Node {
	var rows []xango.Node
	for _, f := range p.Folders {
		id := f.ID
		nameNodes := []xango.Node{xango.Text("📁 " + f.shown())}
		if f.Confidential {
			// The name is the placeholder until revealed: a lock says why.
			nameNodes = append(nameNodes, xango.Text(" "), Icon(IconProps{Glyph: "🔒", Size: 14, Label: tr("docs.folderConfidential", "Confidential folder")}))
		}
		row := []xango.Node{xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name"+confidentialClass(f.Confidential))}, nameNodes...)}
		if p.OpenFolder != nil {
			row = append(row, docButton("gj-doclist-folder-open", tr("docs.openFolder", "Open"), busy, actOne(p.Pending, p.OpenFolder, id)))
		}
		row = append(row, folderProtectionButtons(p, f, busy)...)
		if p.RenameFolder != nil {
			row = append(row, xango.El("input", []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-doclist-folder-name"),
				xango.A("aria-label", trf("docs.renameFolder", "Name of folder %s", f.shown())), xango.A("value", f.shown()),
				xango.On("change", func(ev xango.Event) {
					name := strings.TrimSpace(xango.EventValue[string](ev))
					if name == "" || name == f.Name {
						return // a blank or unchanged name is not a rename; folders are found by ID, so renaming while something else runs is safe
					}
					p.Pending.Run(func(ctx context.Context) error { return p.RenameFolder(ctx, id, name) })
				})}))
		}
		row = append(row, linkButton(p, p.FolderLink, id)...)
		if p.DownloadFolder != nil {
			row = append(row, docButton("gj-doclist-folder-download", tr("docs.download", "Download"), busy, actOne(p.Pending, p.DownloadFolder, id)))
		}
		if p.DownloadPack != nil {
			row = append(row, docButton("gj-doclist-folder-pack", tr("docs.downloadPack", "As .opendoc"), busy, actOne(p.Pending, p.DownloadPack, id)))
		}
		if p.DeleteFolder != nil {
			row = append(row, docButton("gj-doclist-folder-delete", tr("docs.deleteFolder", "Delete folder"), busy,
				confirmed(p.Confirm, trf("docs.confirmDeleteFolder", "Delete the folder %s? It has to be empty.", f.shown()), actOne(p.Pending, p.DeleteFolder, id))))
		}
		rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "gj-doclist-folder"), xango.A("data-id", id)}, row...))
	}
	var out []xango.Node
	if len(rows) > 0 {
		out = append(out, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list gj-doclist-folders")}, rows...))
	}
	if p.NewFolder != nil {
		draft := strings.TrimSpace(p.FolderDraft)
		input := []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-doclist-newfolder-name"), xango.A("value", p.FolderDraft),
			xango.A("placeholder", tr("docs.newFolderPlaceholder", "New folder")), xango.A("aria-label", tr("docs.newFolder", "New folder"))}
		if p.OnFolderDraft != nil {
			input = append(input, xango.On("input", func(e xango.Event) { p.OnFolderDraft(xango.EventValue[string](e)) }))
		}
		out = append(out, xango.El("div", []xango.Attr{xango.A("class", "gj-doc-head gj-doclist-newfolder-form")},
			xango.El("input", input),
			docButton("gj-doclist-newfolder", tr("docs.createFolder", "Create folder"), busy || draft == "", func() {
				p.Pending.Run(func(ctx context.Context) error { return p.NewFolder(ctx, draft) })
			})))
	}
	return out
}

// moveSelect is the "Move to…" menu of one document.
func moveSelect(p DocListProps, d Doc, busy bool) xango.Node {
	byID := map[string]DocFolder{}
	for _, f := range p.AllFolders {
		byID[f.ID] = f
	}
	label := func(f DocFolder) string {
		name := f.shown()
		for n, parent := 0, f.Parent; parent != "" && n < 20; n++ {
			up, ok := byID[parent]
			if !ok {
				break
			}
			name = up.shown() + " / " + name
			parent = up.Parent
		}
		return name
	}
	here := ""
	if len(p.Path) > 0 {
		here = p.Path[len(p.Path)-1].ID // where the document is: not a place to move it to
	}
	opts := []xango.Node{xango.El("option", []xango.Attr{xango.A("value", "-"), xango.A("selected", "selected")}, xango.Text(tr("docs.moveTo", "Move to…")))}
	if here != "" {
		opts = append(opts, xango.El("option", []xango.Attr{xango.A("value", "top")}, xango.Text(tr("docs.topLevel", "Documents (top level)"))))
	}
	for _, f := range p.AllFolders {
		if f.ID != here {
			opts = append(opts, xango.El("option", []xango.Attr{xango.A("value", "folder:"+f.ID)}, xango.Text(label(f))))
		}
	}
	id := d.ID
	attrs := []xango.Attr{xango.A("class", "gj-doclist-move"), xango.A("aria-label", trf("docs.moveOf", "Move %s to a folder", d.shown()))}
	if busy {
		attrs = append(attrs, xango.A("disabled", "disabled"))
	} else {
		attrs = append(attrs, xango.On("change", func(e xango.Event) {
			v := xango.EventValue[string](e)
			folder, ok := strings.CutPrefix(v, "folder:")
			if v == "-" || (!ok && v != "top") {
				return
			}
			p.Pending.Run(func(ctx context.Context) error { return p.Move(ctx, []string{id}, folder) })
		}))
	}
	return xango.El("select", attrs, opts...)
}

// linkButton is the Link button of a row: it copies the address of the item.
func linkButton(p DocListProps, link func(id string) string, id string) []xango.Node {
	if link == nil || p.CopyLink == nil {
		return nil
	}
	url := link(id)
	if url == "" {
		return nil
	}
	return []xango.Node{xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-doclist-link"),
		xango.A("data-url", url), xango.A("title", url), xango.On("click", func(xango.Event) { p.CopyLink(url) })},
		Material("link"), xango.Text(tr("docs.link", "Link")))}
}

// downloadControl is a document's Download: one button, or a "Download as…" menu
// of the formats offered.
func downloadControl(p DocListProps, d Doc, busy bool) []xango.Node {
	if p.Download == nil {
		return nil
	}
	id := d.ID
	if len(p.DownloadFormats) == 0 {
		return []xango.Node{docButton("gj-doclist-download", tr("docs.download", "Download"), busy,
			func() { p.Pending.Run(func(ctx context.Context) error { return p.Download(ctx, id, "") }) })}
	}
	opts := []xango.Node{xango.El("option", []xango.Attr{xango.A("value", "-"), xango.A("selected", "selected")}, xango.Text(tr("docs.downloadAs", "Download as…")))}
	// Choices with a Group are shown under a heading for it, in the order the groups first appear.
	var groups []string
	byGroup := map[string][]xango.Node{}
	for _, f := range p.DownloadFormats {
		if _, ok := byGroup[f.Group]; !ok {
			groups = append(groups, f.Group)
		}
		byGroup[f.Group] = append(byGroup[f.Group], xango.El("option", []xango.Attr{xango.A("value", "format:"+f.Name)}, xango.Text(f.Label)))
	}
	for _, g := range groups {
		if g == "" {
			opts = append(opts, byGroup[g]...)
		} else {
			opts = append(opts, xango.El("optgroup", []xango.Attr{xango.A("label", g)}, byGroup[g]...))
		}
	}
	attrs := []xango.Attr{xango.A("class", "gj-doclist-download"), xango.A("aria-label", trf("docs.downloadOf", "Download %s as", d.shown()))}
	if busy {
		attrs = append(attrs, xango.A("disabled", "disabled"))
	} else {
		attrs = append(attrs, xango.On("change", func(e xango.Event) {
			if format, ok := strings.CutPrefix(xango.EventValue[string](e), "format:"); ok {
				p.Pending.Run(func(ctx context.Context) error { return p.Download(ctx, id, format) })
			}
		}))
	}
	return []xango.Node{xango.El("select", attrs, opts...)}
}

// confidentialClass marks the name of a confidential folder for styling.
func confidentialClass(c bool) string {
	if c {
		return " gj-doc-confidential"
	}
	return ""
}

// docNameNodes is a document's name, and a lock with its label when it is confidential.
func docNameNodes(d Doc) []xango.Node {
	nodes := []xango.Node{xango.Text(d.shown())}
	if d.Confidential {
		nodes = append(nodes, xango.Text(" "), Icon(IconProps{Glyph: "🔒", Size: 14, Label: tr("docs.docConfidential", "Confidential document")}))
	}
	return nodes
}

// docRowLabel names a document for an accessible name that must be unique per row: its shown
// name, and a short ID when another document in the list is shown under the same name (two
// confidential documents are both the placeholder).
func docRowLabel(p DocListProps, d Doc) string {
	n := 0
	for _, o := range p.Docs {
		if o.shown() == d.shown() {
			n++
		}
	}
	if n > 1 {
		return d.shown() + " (" + shortID(d.ID) + ")"
	}
	return d.shown()
}

// folderRowLabel is docRowLabel for a folder.
func folderRowLabel(p DocListProps, f DocFolder) string {
	n := 0
	for _, o := range p.Folders {
		if o.shown() == f.shown() {
			n++
		}
	}
	if n > 1 {
		return f.shown() + " (" + shortID(f.ID) + ")"
	}
	return f.shown()
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// protectionButtons are a document's "Make confidential" / "Make not confidential" and, for one
// whose name is hidden, "Reveal". The switch asks first (when the list has a Confirm) and says
// what changes.
func protectionButtons(p DocListProps, d Doc, busy bool) []xango.Node {
	var out []xango.Node
	if p.Reveal != nil && d.Confidential && d.NameHidden {
		out = append(out, docButtonAs("gj-doclist-reveal", tr("docs.reveal", "Reveal"), trf("docs.revealOf", "Reveal the name of %s", docRowLabel(p, d)), busy, actOne(p.Pending, p.Reveal, d.ID)))
	}
	if p.SetProtection == nil {
		return out
	}
	id, to := d.ID, !d.Confidential
	run := func() { p.Pending.Run(func(ctx context.Context) error { return p.SetProtection(ctx, id, to) }) }
	if to {
		out = append(out, docButtonAs("gj-doclist-protect", tr("docs.makeConfidential", "Make confidential"), trf("docs.makeConfidentialOf", "Make %s confidential", docRowLabel(p, d)), busy,
			confirmedAs(p.ConfirmAs, p.Confirm, tr("docs.makeConfidential", "Make confidential"), trf("docs.confirmMakeConfidential", "Make %s confidential? Its name and tags move inside the encrypted document, where only your keys can read them: the list shows a placeholder until the store is unlocked, and searching for its name stops working. Older copies and versions are encrypted again for your keys only.", d.shown()), run)))
	} else {
		out = append(out, docButtonAs("gj-doclist-protect", tr("docs.makeNotConfidential", "Make not confidential"), trf("docs.makeNotConfidentialOf", "Make %s not confidential", docRowLabel(p, d)), busy,
			confirmedAs(p.ConfirmAs, p.Confirm, tr("docs.makeNotConfidential", "Make not confidential"), trf("docs.confirmMakeNotConfidential", "Make %s not confidential? Its name and tags move back into the list, where anyone who can read the list sees them and search finds them. Older copies and versions are encrypted again at the store's usual level.", d.shown()), run)))
	}
	return out
}

// folderProtectionButtons are protectionButtons for a folder, whose name is what moves.
func folderProtectionButtons(p DocListProps, f DocFolder, busy bool) []xango.Node {
	var out []xango.Node
	if p.RevealFolder != nil && f.Confidential && f.NameHidden {
		out = append(out, docButtonAs("gj-doclist-folder-reveal", tr("docs.reveal", "Reveal"), trf("docs.revealFolderOf", "Reveal the name of folder %s", folderRowLabel(p, f)), busy, actOne(p.Pending, p.RevealFolder, f.ID)))
	}
	if p.SetFolderProtection == nil {
		return out
	}
	id, to := f.ID, !f.Confidential
	run := func() { p.Pending.Run(func(ctx context.Context) error { return p.SetFolderProtection(ctx, id, to) }) }
	if to {
		out = append(out, docButtonAs("gj-doclist-folder-protect", tr("docs.makeFolderConfidential", "Make confidential"), trf("docs.makeFolderConfidentialOf", "Make folder %s confidential", folderRowLabel(p, f)), busy,
			confirmedAs(p.ConfirmAs, p.Confirm, tr("docs.makeFolderConfidential", "Make confidential"), trf("docs.confirmMakeFolderConfidential", "Make the folder %s confidential? Its name moves into an encrypted record, where only your keys can read it: the list shows \"Confidential folder\" until the store is unlocked, and searching for its name stops working. The documents inside keep their own protection.", f.shown()), run)))
	} else {
		out = append(out, docButtonAs("gj-doclist-folder-protect", tr("docs.makeFolderNotConfidential", "Make not confidential"), trf("docs.makeFolderNotConfidentialOf", "Make folder %s not confidential", folderRowLabel(p, f)), busy,
			confirmedAs(p.ConfirmAs, p.Confirm, tr("docs.makeFolderNotConfidential", "Make not confidential"), trf("docs.confirmMakeFolderNotConfidential", "Make the folder %s not confidential? Its name moves back into the list, where anyone who can read the list sees it. The documents inside keep their own protection.", f.shown()), run)))
	}
	return out
}
