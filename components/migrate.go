package components

import (
	"strings"

	"github.com/elaous/gojiberry/xango"
)

type MigrateItem struct {
	ID, Name string
	Folder   bool
	Selected bool
	Meta     string
}

// MigrateFailure is a document that did not come over.
type MigrateFailure struct{ Name, Err string }

// MigrateProps configures Migrate.
type MigrateProps struct {
	Sources  []DocChoice
	Source   string
	OnSource func(name string)

	Formats  []DocChoice
	Format   string
	OnFormat func(name string)

	Token     string
	OnToken   func(token string)
	OnConnect func()
	Connected bool

	Path         []MigrateItem
	Items        []MigrateItem
	OnOpenFolder func(id string)
	OnToggle     func(id string)
	OnSelectAll  func()
	OnStart      func()

	OnAgain    func(on bool)
	Again      bool
	OnHistory  func(on bool)
	History    bool
	HistoryMax int

	OnPickFiles func()

	// Busy is set while something runs.
	Busy              bool
	Done, Total       int
	Current           string
	Reported          bool
	Imported          int
	Skipped, Versions int
	Failed            []MigrateFailure
	Truncated         bool
	Partial           bool
	Error             string
	ConfirmedLabel    string // optional heading override
}

func Migrate(p MigrateProps) xango.Node {
	title := p.ConfirmedLabel
	if title == "" {
		title = tr("migrate.title", "Migrate")
	}
	kids := []xango.Node{xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(title))}

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

	if len(p.Formats) > 0 && p.OnFormat != nil {
		var opts []xango.Node
		for _, f := range p.Formats {
			opts = append(opts, migrateOption(f.Name, f.Label, f.Name == p.Format))
		}
		attrs := []xango.Attr{xango.A("class", "gj-migrate-format"), xango.On("change", func(e xango.Event) { p.OnFormat(xango.EventValue[string](e)) })}
		if p.Busy {
			attrs = append(attrs, xango.A("disabled", "disabled"))
		}
		kids = append(kids, xango.El("label", []xango.Attr{xango.A("class", "gj-migrate-formatrow")},
			xango.El("span", nil, xango.Text(tr("migrate.convertTo", "Convert to"))),
			xango.El("select", attrs, opts...)))
	}

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
		if p.OnAgain != nil {
			kids = append(kids, Checkbox(CheckboxProps{Label: tr("migrate.again", "Bring again what an earlier run already brought"), Checked: p.Again, Disabled: p.Busy, OnChange: p.OnAgain}))
		}
		if p.OnHistory != nil {
			kids = append(kids, Checkbox(CheckboxProps{Label: trf("migrate.history", "Also bring the version history (the last %d versions of each)", p.HistoryMax), Checked: p.History, Disabled: p.Busy, OnChange: p.OnHistory}))
		}
		if len(actions) > 0 {
			kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-migrate-actions")}, actions...))
		}
	}

	if p.OnPickFiles != nil {
		kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-migrate-files")},
			docButton("gj-migrate-pick", tr("migrate.pickFiles", "Import Word files…"), p.Busy, p.OnPickFiles)))
	}

	if p.Busy && p.Total > 0 {
		kids = append(kids, xango.El("progress", []xango.Attr{xango.A("class", "gj-migrate-progress"), xango.A("max", itoa(p.Total)), xango.A("value", itoa(p.Done)),
			xango.A("aria-label", tr("migrate.progress", "Migration progress"))}),
			xango.El("p", []xango.Attr{xango.A("class", "gj-migrate-current"), xango.A("role", "status")},
				xango.Text(trf("migrate.working", "%d of %d: %s", p.Done, p.Total, p.Current))))
	}
	if p.Reported && !p.Busy {
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-migrate-report"), xango.A("role", "status")},
			xango.Text(trf("migrate.imported", "%d documents migrated", p.Imported))))
		if p.Skipped > 0 {
			kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-migrate-skipped"), xango.A("role", "status")},
				xango.Text(trf("migrate.skipped", "%d already brought by an earlier run and not changed since, so left as they are", p.Skipped))))
		}
		if p.Versions > 0 {
			kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-migrate-versions"), xango.A("role", "status")},
				xango.Text(trf("migrate.versions", "%d earlier versions came with their documents", p.Versions))))
		}
		if p.Partial {
			kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-doc-error")}, xango.Text(tr("migrate.partial", "The migration did not finish, so the pack is partial and was not downloaded."))))
		}
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
