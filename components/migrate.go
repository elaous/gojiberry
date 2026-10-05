package components

import (
	"strings"

	"github.com/elaous/gojiberry/xango"
)

// MigrateItem is a folder or a document at the platform being migrated from.
type MigrateItem struct {
	ID, Name string
	Folder   bool
	Selected bool
	// Meta is the line under the name (when it changed, how big).
	Meta string
}

// MigrateFailure is a document that did not come over.
type MigrateFailure struct{ Name, Err string }

// MigrateProps configures Migrate. Every operation is a plain callback: a nil one
// leaves its control out, and the host decides how it runs (migrate.Session runs
// them in the background and sets Busy).
type MigrateProps struct {
	// Sources are the platforms to migrate from; Source is the one chosen ("" for
	// none yet).
	Sources  []DocChoice
	Source   string
	OnSource func(name string)

	// Token is the access token for the platform, typed into a box that reports to
	// OnToken; OnConnect signs in with it and lists the top of the platform.
	// Connected says that has been done.
	Token     string
	OnToken   func(token string)
	OnConnect func()
	Connected bool

	// Path are the folders from the top down to the one listed; Items what is in
	// it. OnOpenFolder goes into one ("" is the top, the breadcrumb's first item);
	// OnToggle selects or unselects an item; OnSelectAll selects them all;
	// OnStart migrates the selection (or everything listed when none is selected).
	Path         []MigrateItem
	Items        []MigrateItem
	OnOpenFolder func(id string)
	OnToggle     func(id string)
	OnSelectAll  func()
	OnStart      func()

	// OnPickFiles opens the chooser for Word files on this computer, which are
	// migrated at once.
	OnPickFiles func()

	// Busy is set while something runs. Done and Total, Current describe the file
	// being migrated; Imported and Failed the outcome of the last run (Reported
	// says there was one).
	Busy           bool
	Done, Total    int
	Current        string
	Reported       bool
	Imported       int
	Failed         []MigrateFailure
	Truncated      bool
	Error          string
	ConfirmedLabel string // optional heading override
}

// Migrate is the panel for moving documents in from another platform (Google
// Docs, Microsoft Word on OneDrive) or from Word files on this computer: choose
// the platform, sign in with a token, browse its folders, tick what to bring and
// start; progress and a report of what could not be migrated are shown.
func Migrate(p MigrateProps) xango.Node {
	title := p.ConfirmedLabel
	if title == "" {
		title = tr("migrate.title", "Migrate")
	}
	kids := []xango.Node{xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(title))}

	// The platform.
	if len(p.Sources) > 0 && p.OnSource != nil {
		opts := []xango.Node{migrateOption("-", tr("migrate.choose", "Choose a platform…"), p.Source == "")}
		for _, s := range p.Sources {
			opts = append(opts, migrateOption(s.Name, s.Label, s.Name == p.Source))
		}
		attrs := []xango.Attr{xango.A("class", "gj-migrate-source"), xango.A("aria-label", tr("migrate.platform", "Platform")),
			xango.On("change", func(e xango.Event) {
				if v := xango.EventValue[string](e); v != "-" {
					p.OnSource(v)
				}
			})}
		if p.Busy {
			attrs = append(attrs, xango.A("disabled", "disabled"))
		}
		kids = append(kids, xango.El("select", attrs, opts...))
	}
	if p.Source != "" && p.OnToken != nil {
		kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-migrate-signin")},
			xango.El("input", []xango.Attr{xango.A("type", "password"), xango.A("class", "gj-migrate-token"), xango.A("autocomplete", "off"),
				xango.A("placeholder", tr("migrate.tokenPlaceholder", "Access token")), xango.A("aria-label", tr("migrate.token", "Access token")),
				xango.A("value", p.Token),
				xango.On("input", func(e xango.Event) { p.OnToken(xango.EventValue[string](e)) })}),
			docButton("gj-migrate-connect", tr("migrate.connect", "Connect"), p.Busy || strings.TrimSpace(p.Token) == "", p.OnConnect)))
	}

	// Browsing.
	if p.Connected {
		crumbs := []BreadcrumbItem{{Label: tr("migrate.top", "All files")}}
		for _, f := range p.Path {
			crumbs = append(crumbs, BreadcrumbItem{Label: f.Name})
		}
		for i := range crumbs {
			if i < len(crumbs)-1 && p.OnOpenFolder != nil {
				id := ""
				if i > 0 {
					id = p.Path[i-1].ID
				}
				crumbs[i].Href = "#" + id
				crumbs[i].OnClick = func(e xango.Event) {
					e.PreventDefault()
					p.OnOpenFolder(id)
				}
			}
		}
		kids = append(kids, Breadcrumbs(BreadcrumbsProps{Items: crumbs}))
		var rows []xango.Node
		for _, it := range p.Items {
			it := it
			row := []xango.Node{}
			if p.OnToggle != nil {
				attrs := []xango.Attr{xango.A("type", "checkbox"), xango.A("class", "gj-migrate-check"),
					xango.A("aria-label", trf("migrate.select", "Select %s", it.Name)), xango.On("change", func(xango.Event) { p.OnToggle(it.ID) })}
				if it.Selected {
					attrs = append(attrs, xango.A("checked", "checked"))
				}
				if p.Busy {
					attrs = append(attrs, xango.A("disabled", "disabled"))
				}
				row = append(row, xango.El("input", attrs))
			}
			name := it.Name
			if it.Folder {
				name = "📁 " + name
			}
			row = append(row, xango.El("span", []xango.Attr{xango.A("class", "gj-doc-name")}, xango.Text(name)))
			if it.Meta != "" {
				row = append(row, xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(it.Meta)))
			}
			if it.Folder && p.OnOpenFolder != nil {
				row = append(row, docButton("gj-migrate-open", tr("docs.openFolder", "Open"), p.Busy, func() { p.OnOpenFolder(it.ID) }))
			}
			class := "gj-migrate-item"
			if it.Folder {
				class += " gj-migrate-folder"
			}
			rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", class), xango.A("data-id", it.ID)}, row...))
		}
		if len(rows) == 0 {
			kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-empty")}, xango.Text(tr("migrate.empty", "No documents here."))))
		} else {
			kids = append(kids, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list gj-migrate-list")}, rows...))
		}
		actions := []xango.Node{}
		if p.OnSelectAll != nil {
			actions = append(actions, docButton("gj-migrate-all", tr("migrate.selectAll", "Select all"), p.Busy || len(p.Items) == 0, p.OnSelectAll))
		}
		if p.OnStart != nil {
			actions = append(actions, docButton("gj-migrate-start", tr("migrate.start", "Migrate"), p.Busy || len(p.Items) == 0, p.OnStart))
		}
		if len(actions) > 0 {
			kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-migrate-actions")}, actions...))
		}
	}

	// Word files on this computer.
	if p.OnPickFiles != nil {
		kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-migrate-files")},
			docButton("gj-migrate-pick", tr("migrate.pickFiles", "Import Word files…"), p.Busy, p.OnPickFiles)))
	}

	// Progress and the report.
	if p.Busy && p.Total > 0 {
		kids = append(kids, xango.El("progress", []xango.Attr{xango.A("class", "gj-migrate-progress"), xango.A("max", itoa(p.Total)), xango.A("value", itoa(p.Done)),
			xango.A("aria-label", tr("migrate.progress", "Migration progress"))}),
			xango.El("p", []xango.Attr{xango.A("class", "gj-migrate-current"), xango.A("role", "status")},
				xango.Text(trf("migrate.working", "%d of %d: %s", p.Done, p.Total, p.Current))))
	}
	if p.Reported && !p.Busy {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-migrate-report"), xango.A("role", "status")},
			xango.Text(trf("migrate.imported", "%d documents migrated", p.Imported))))
		if p.Truncated {
			kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-error")}, xango.Text(tr("migrate.truncated", "Too many files: only the first ones were migrated."))))
		}
		if len(p.Failed) > 0 {
			var fails []xango.Node
			for _, f := range p.Failed {
				fails = append(fails, xango.El("li", []xango.Attr{xango.A("class", "gj-migrate-failure")}, xango.Text(f.Name+": "+f.Err)))
			}
			kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-error")}, xango.Text(trf("migrate.failed", "%d could not be migrated:", len(p.Failed)))),
				xango.El("ul", []xango.Attr{xango.A("class", "gj-migrate-failures")}, fails...))
		}
	}
	if p.Error != "" {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-error"), xango.A("role", "alert")}, xango.Text(p.Error)))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-migrate")}, kids...)
}

func migrateOption(value, label string, on bool) xango.Node {
	a := []xango.Attr{xango.A("value", value)}
	if on {
		a = append(a, xango.A("selected", "selected"))
	}
	return xango.El("option", a, xango.Text(label))
}
