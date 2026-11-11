package components

import (
	"context"
	"fmt"
	"github.com/elaous/gojiberry/xango"
	"strings"
	"sync"
	"time"
)

// Doc is a document as the components list it.
type Doc struct {
	ID, Name                 string
	Format                   string // the file format's label ("JSON")
	Size                     int
	Updated                  time.Time
	Trashed                  time.Time
	Tags                     []string
	Confidential, NameHidden bool
}

type DocFolder struct {
	ID, Name, Parent string
	Confidential     bool
	NameHidden       bool
}

func (d Doc) shown() string {
	if d.NameHidden {
		return tr("docs.confidentialName", "Confidential document")
	}
	return d.Name
}

func (f DocFolder) shown() string {
	if f.NameHidden {
		return tr("docs.confidentialFolderName", "Confidential folder")
	}
	return f.Name
}

// DocVersion is a snapshot of a document.
type DocVersion struct {
	Key    string // what the version operations take
	Time   time.Time
	Size   int
	Name   string
	Pinned bool
	Author string
}

type DocsAction func(ctx context.Context, ids []string) error

type DocAction func(ctx context.Context, id string) error

type Pending struct {
	mu     sync.Mutex
	busy   int
	err    error
	change func()
	// Inline runs operations on the calling goroutine (for tests).
	Inline bool
}

func NewPending(onChange func()) *Pending { return &Pending{change: onChange} }

func (p *Pending) Run(f func(ctx context.Context) error) {
	if p == nil {
		go func() { _ = f(context.Background()) }()
		return
	}
	p.mu.Lock()
	if p.busy == 0 {
		p.err = nil
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

var docIcons = map[string]string{
	"gj-doclist-open": "description", "gj-doclist-duplicate": "content_copy", "gj-doclist-trash": "delete",
	"gj-doclist-delete": "delete_forever", "gj-doclist-refresh": "refresh", "gj-doclist-check": "fact_check", "gj-doclist-download": "download",
	"gj-doclist-folder-open": "folder_open", "gj-doclist-folder-delete": "delete_forever",
	"gj-doclist-protect": "lock", "gj-doclist-reveal": "key", "gj-doclist-folder-protect": "lock", "gj-doclist-folder-reveal": "key",
	"gj-doclist-folder-download": "download", "gj-doclist-folder-pack": "folder", "gj-doclist-import-pack": "upload_file", "gj-doclist-newfolder": "create_new_folder",
	"gj-migrate-pick": "upload_file", "gj-migrate-start": "download",
}

func pendingError(p *Pending) []xango.Node {
	if err := p.Err(); err != nil {
		return []xango.Node{ErrorView("", err)}
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
		parts = append(parts, FormatTime(when, false))
	}
	return strings.Join(parts, " · ")
}

// DocCheck is the operation behind DocListProps.Check.
type DocCheck func(ctx context.Context) error

// DocListProps configures DocList.
type DocListProps struct {
	Docs      []Doc
	Current   string
	Open      DocAction
	Duplicate DocAction
	Trash     DocsAction // moves to the trash: see Trash for getting them back
	Delete    DocAction
	// Tag edits a document's tags.
	Tag         func(ctx context.Context, id string, tags []string) error
	TagFilter   string
	OnTagFilter func(tag string)
	Title       string
	Refresh     func()
	Check       DocCheck
	Pending     *Pending
	Confirm     func(message string, do func())
	ConfirmAs   func(label, message string, do func())

	Folders, Path, AllFolders []DocFolder
	OpenFolder                DocAction
	NewFolder                 func(ctx context.Context, name string) error
	FolderDraft               string
	OnFolderDraft             func(name string)
	RenameFolder              func(ctx context.Context, id, name string) error
	DeleteFolder              DocAction
	Move                      func(ctx context.Context, ids []string, folder string) error

	Link, FolderLink func(id string) string
	CopyLink         func(url string)
	OpenDownload     *OpenDownload
	DownloadPack     func(ctx context.Context, id string) error
	ImportPack       func()
	Download         func(ctx context.Context, id, format string) error
	DownloadFolder   DocAction
	DownloadFormats  []DocChoice

	SetProtection       func(ctx context.Context, id string, confidential bool) error
	SetFolderProtection func(ctx context.Context, id string, confidential bool) error
	Reveal              DocAction
	RevealFolder        DocAction
}

// DocChoice is a name and the label shown for it.
type DocChoice struct{ Name, Label, Group string }

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

// TagFilterProps configures TagFilter.
type TagFilterProps struct {
	Tags     []string
	Selected string
	OnSelect func(tag string)
}

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

func folderRows(p DocListProps, busy bool) []xango.Node {
	var rows []xango.Node
	for _, f := range p.Folders {
		id := f.ID
		nameNodes := []xango.Node{xango.Text("📁 " + f.shown())}
		if f.Confidential {
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
						return
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
		here = p.Path[len(p.Path)-1].ID
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

func confidentialClass(c bool) string {
	if c {
		return " gj-doc-confidential"
	}
	return ""
}

func docNameNodes(d Doc) []xango.Node {
	nodes := []xango.Node{xango.Text(d.shown())}
	if d.Confidential {
		nodes = append(nodes, xango.Text(" "), Icon(IconProps{Glyph: "🔒", Size: 14, Label: tr("docs.docConfidential", "Confidential document")}))
	}
	return nodes
}

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
