//go:build js && wasm

package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docsearch"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/xango"
)

type searchAll struct {
	cache    indexCache
	read     int
	loaded   bool
	synced   bool
	query    string
	repl     string
	opts     canvas.FindOptions
	hits     []docsearch.Hit
	keyToID  map[string]string
	searched bool
	plan     *docsearch.Plan
	applied  []replacedDoc   // what the last replacement wrote, for Undo
	picked   map[string]bool // the documents the person left ticked in the preview
	message  string
	errText  string
	view     docstore.Adapter // the documents' own keys in the current place
	target   string
}

func (u *ui) adapterFor(target string) docstore.Adapter {
	for _, t := range u.saver.Saver.TargetList() {
		if t.Name == target {
			return t.Adapter
		}
	}
	return nil
}

func (u *ui) searchKeyFor(target string) ([]byte, sealedVault) {
	var secret []byte
	if e := u.enc; e != nil && target == encTarget && (e.broken != nil || e.store.Enabled()) {
		if e.broken != nil {
			return nil, nil
		}
		s, ok := e.store.SearchSecret()
		if !ok {
			return nil, nil
		}
		secret = s
	} else {
		s, err := deviceSecret()
		if err != nil {
			return nil, nil
		}
		secret = s
	}
	key, err := searchKey(secret, target)
	if err != nil {
		return nil, nil
	}
	return key, idbVault{}
}

func (u *ui) syncSearchAll(ctx context.Context) error {
	sa := &u.sa
	target, _ := u.saver.Where()
	base := u.adapterFor(target)
	if base == nil {
		return errors.New(T("search.noPlace", "There is no place to search."))
	}
	entries, err := u.saver.Saver.List(ctx, target)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(entries))
	sa.keyToID = map[string]string{}
	for _, e := range entries {
		keys = append(keys, e.Key)
		sa.keyToID[e.Key] = e.ID
	}
	sa.view = docsearch.Subset(base, keys)
	key, vault := u.searchKeyFor(target)
	res, loaded, err := sa.cache.refresh(ctx, vault, target, key, sa.view)
	if err != nil {
		return err
	}
	sa.target, sa.loaded, sa.read, sa.synced = target, loaded, res.Added+res.Updated, true
	if len(res.Skipped) > 0 {
		sa.message = T("search.skipped", "%d documents could not be read here (is the store locked?) and were left out.", len(res.Skipped))
	}
	return nil
}

func (u *ui) runSearchAll(renderUI func()) {
	sa := &u.sa
	sa.errText, sa.message, sa.plan = "", "", nil // a finished replacement stays undoable while the person searches on
	if sa.query == "" {
		return
	}
	u.saver.Pending.Run(func(ctx context.Context) error {
		if err := u.syncSearchAll(ctx); err != nil {
			sa.errText = err.Error()
			return nil
		}
		sa.hits, sa.searched = sa.cache.ix.Search(sa.query, 50), true
		if len(sa.hits) == 0 {
			u.announce(T("a11y.noMatches", "No matches"))
		} else {
			u.announce(T("search.docMatches", "%d documents match", len(sa.hits)))
		}
		return nil
	})
}

func (u *ui) previewReplaceAll() {
	sa := &u.sa
	sa.errText, sa.message, sa.applied = "", "", nil
	u.saver.Pending.Run(func(ctx context.Context) error {
		if err := u.syncSearchAll(ctx); err != nil {
			sa.errText = err.Error()
			return nil
		}
		sa.opts = canvas.FindOptions{Query: sa.query, MatchCase: u.find.opts.MatchCase, WholeWord: u.find.opts.WholeWord, Regex: u.find.opts.Regex}
		plan, err := docsearch.PlanReplace(ctx, sa.view, sa.opts, sa.repl)
		if err != nil {
			sa.errText, sa.plan = err.Error(), nil
			return nil
		}
		open := u.saver.ID()
		kept := plan.Changes[:0]
		for _, c := range plan.Changes {
			if sa.keyToID[c.ID] != open {
				kept = append(kept, c)
			}
		}
		plan.Changes = kept
		sa.plan, sa.picked = plan, map[string]bool{}
		for _, c := range plan.Changes {
			sa.picked[c.ID] = true
		}
		if len(plan.Changes) == 0 {
			sa.message = T("search.nothingToReplace", "Nothing to replace in the other documents.")
		}
		return nil
	})
}

func (u *ui) applyReplaceAll() {
	sa := &u.sa
	if sa.plan == nil {
		return
	}
	var only []string
	for _, c := range sa.plan.Changes {
		if sa.picked[c.ID] {
			only = append(only, c.ID)
		}
	}
	if len(only) == 0 {
		return
	}
	plan, keyToID := sa.plan, sa.keyToID
	target, _ := u.saver.Where()
	u.saver.Pending.Run(func(ctx context.Context) error {
		done, conflicts, err := applyReplace(ctx, u.saver.Saver, target, u.saver.Author,
			T("search.versionName", "Before replace in all documents"), plan, keyToID, only)
		if err != nil {
			sa.errText = err.Error()
			if len(done) > 0 { // some were written before the failure: keep them undoable
				sa.applied, sa.plan = done, nil
			}
			return nil
		}
		sa.applied, sa.plan = done, nil
		sa.message = T("search.replaced", "Replaced in %d documents. Undo puts the old text back where a document was not saved since.", len(done))
		if len(conflicts) > 0 {
			sa.message += " " + T("search.replaceSkipped", "%d were saved since the preview and were left as they are.", len(conflicts))
		}
		u.announce(sa.message)
		sa.hits, sa.searched = nil, false
		return nil
	})
}

func (u *ui) undoReplaceAll() {
	sa := &u.sa
	if len(sa.applied) == 0 {
		return
	}
	done := sa.applied
	target, _ := u.saver.Where()
	u.saver.Pending.Run(func(ctx context.Context) error {
		restored, kept, err := undoReplace(ctx, u.saver.Saver, target, done)
		if err != nil {
			sa.errText = err.Error()
			return nil
		}
		sa.applied = nil
		sa.message = T("search.undone", "Put back in %d documents.", restored)
		if kept > 0 {
			sa.message += " " + T("search.undoKept", "%d were saved again since and were left as they are.", kept)
		}
		u.announce(sa.message)
		return nil
	})
}

func (u *ui) searchAllPanel(renderUI func()) xango.Node {
	sa := &u.sa
	busy := u.saver.Pending.Busy()
	field := func(id, label, value, ph string, on func(string)) xango.Node {
		return xango.El("input", []xango.Attr{xango.A("type", "text"), xango.A("id", id), xango.A("value", value), xango.A("aria-label", label),
			xango.A("placeholder", ph), xango.A("autocomplete", "off"), xango.A("style", "width:100%;box-sizing:border-box;margin:2px 0"),
			xango.On("input", func(ev xango.Event) { on(xango.EventValue[string](ev)) })})
	}
	nodes := []xango.Node{
		xango.El("h3", []xango.Attr{xango.A("style", "font-size:14px;margin:12px 0 4px")}, xango.Text(T("search.title", "Search all documents"))),
		field("sa-query", T("search.query", "Search all documents"), sa.query, T("search.queryHint", "Words to find in every document"), func(v string) { sa.query = v; renderUI() }),
		xango.El("div", []xango.Attr{xango.A("style", "display:flex;gap:6px;flex-wrap:wrap")},
			btn("sa-search", T("search.go", "Search"), func() { u.runSearchAll(renderUI) }, busy || sa.query == "")),
	}
	if sa.errText != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "sa-error"), xango.A("role", "alert"), xango.A("style", "color:#b91c1c")}, xango.Text(sa.errText)))
	}
	if sa.message != "" {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "sa-message"), xango.A("role", "status")}, xango.Text(sa.message)))
	}
	if sa.synced {
		nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "sa-index"), xango.A("style", "font-size:12px;margin:2px 0"),
			xango.A("data-indexed", strconv.Itoa(sa.cache.ix.Len())), xango.A("data-read", strconv.Itoa(sa.read)), xango.A("data-kept", strconv.FormatBool(sa.loaded))},
			xango.Text(T("search.indexStatus", "%d documents are indexed on this device; %d were read this time.", sa.cache.ix.Len(), sa.read))))
	}
	if sa.searched {
		if len(sa.hits) == 0 {
			nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "sa-none")}, xango.Text(T("a11y.noMatches", "No matches"))))
		}
		var items []xango.Node
		for _, h := range sa.hits {
			h := h
			id := sa.keyToID[h.ID]
			items = append(items, xango.El("li", []xango.Attr{xango.A("class", "sa-hit"), xango.A("data-id", id)},
				xango.El("strong", nil, xango.Text(h.Title)), xango.Text(" "),
				xango.El("span", []xango.Attr{xango.A("class", "sa-snippet")}, xango.Text(h.Snippet)), xango.Text(" "),
				btn("sa-open-"+id, T("search.open", "Open"), func() { u.saver.Pending.Run(func(ctx context.Context) error { return u.saver.Open(ctx, id) }) }, busy)))
		}
		if len(items) > 0 {
			nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("id", "sa-hits"), xango.A("style", "padding-left:18px")}, items...))
		}
	}
	nodes = append(nodes,
		xango.El("h3", []xango.Attr{xango.A("style", "font-size:14px;margin:12px 0 4px")}, xango.Text(T("search.replaceTitle", "Replace in the other documents"))),
		xango.El("p", []xango.Attr{xango.A("style", "font-size:12px;margin:2px 0")}, xango.Text(T("search.replaceNote", "Uses the search above (and the options of the bar above). You see what would change first; the open document is left out: use Replace all above for it."))),
		field("sa-repl", T("search.replacement", "Replace with"), sa.repl, T("search.replacement", "Replace with"), func(v string) { sa.repl = v }),
		btn("sa-preview", T("search.preview", "Preview"), func() { u.previewReplaceAll() }, busy || sa.query == ""),
	)
	if sa.plan != nil && len(sa.plan.Changes) > 0 {
		var rows []xango.Node
		for _, c := range sa.plan.Changes {
			c := c
			rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "sa-change")},
				components.Checkbox(components.CheckboxProps{
					Label:    fmt.Sprintf("%s (%s)", c.Title, strconv.Itoa(c.Count)),
					Checked:  sa.picked[c.ID],
					OnChange: func(on bool) { sa.picked[c.ID] = on; renderUI() },
				}),
				xango.El("div", []xango.Attr{xango.A("class", "sa-before"), xango.A("style", "font-size:12px")}, xango.Text(c.Before)),
				xango.El("div", []xango.Attr{xango.A("class", "sa-after"), xango.A("style", "font-size:12px;font-weight:bold")}, xango.Text(c.After))))
		}
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("id", "sa-changes"), xango.A("style", "list-style:none;padding:0")}, rows...),
			btn("sa-apply", T("search.apply", "Replace in the ticked documents"), func() { u.applyReplaceAll() }, busy))
	}
	if len(sa.applied) > 0 {
		nodes = append(nodes, btn("sa-undo", T("search.undo", "Undo the replacement"), func() { u.undoReplaceAll() }, busy))
	}
	return xango.El("div", []xango.Attr{xango.A("id", "search-all")}, nodes...)
}
