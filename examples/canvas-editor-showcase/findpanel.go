//go:build js && wasm

package main

import (
	"strconv"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/xango"
)

// findState is the find and replace bar's state.
type findState struct {
	open, replace bool
	opts          canvas.FindOptions
	repl          string
	matches       []canvas.Match
	cur           int
	err           string
	text          string // the text the matches were found in
}

// refreshFind searches again; jump moves to the first match at or after the
// caret and selects it, otherwise the current match stays where it is.
func (u *ui) refreshFind(flow *canvas.TextFlow, scroll func(), jump bool) {
	f := &u.find
	f.err, f.matches = "", nil
	if flow == nil {
		return
	}
	f.text = flow.EditText()
	if f.opts.Query != "" {
		ms, err := flow.Find(f.opts)
		if err != nil {
			f.err = err.Error()
		}
		f.matches = ms
	}
	if jump {
		f.cur = 0
		caret := flow.Cursors()[0].Caret
		for i, m := range f.matches {
			if m.From >= caret {
				f.cur = i
				break
			}
		}
	}
	f.cur = min(f.cur, max(len(f.matches)-1, 0))
	flow.SetSearch(f.matches, f.cur)
	if jump && len(f.matches) > 0 {
		flow.GotoMatch(f.matches[f.cur])
		scroll()
	}
}

// stepFind goes to the next (1) or previous (-1) match.
func (u *ui) stepFind(flow *canvas.TextFlow, scroll func(), dir int) {
	f := &u.find
	if flow == nil || len(f.matches) == 0 {
		return
	}
	f.cur = (f.cur + dir + len(f.matches)) % len(f.matches)
	flow.SetSearch(f.matches, f.cur)
	flow.GotoMatch(f.matches[f.cur])
	scroll()
}

// closeFind removes the highlights and the bar.
func (u *ui) closeFind(flow *canvas.TextFlow) {
	u.find = findState{opts: u.find.opts, repl: u.find.repl}
	if flow != nil {
		flow.ClearSearch()
	}
	if u.tab == "find" {
		u.tab = ""
	}
}

// findPanel is the Find tab.
func findPanel(u *ui, state *State, flow func() *canvas.TextFlow, scroll func(), redraw, renderUI func()) xango.Node {
	f := &u.find
	fl := flow()
	again := func(jump bool) {
		u.refreshFind(fl, scroll, jump)
		redraw()
		renderUI()
	}
	return components.FindReplace(components.FindReplaceProps{
		Query: f.opts.Query, Replacement: f.repl,
		MatchCase: f.opts.MatchCase, WholeWord: f.opts.WholeWord, Regex: f.opts.Regex,
		Count: len(f.matches), Current: min(f.cur+1, len(f.matches)), Error: f.err,
		ReadOnly:      !state.Role.CanEdit() || state.Mode == canvas.ModeView,
		OnQuery:       func(q string) { f.opts.Query = q; again(true) },
		OnReplacement: func(s string) { f.repl = s },
		OnOptions: func(m, w, r bool) {
			f.opts.MatchCase, f.opts.WholeWord, f.opts.Regex = m, w, r
			again(true)
		},
		OnNext: func() { u.stepFind(fl, scroll, 1); redraw(); renderUI() },
		OnPrev: func() { u.stepFind(fl, scroll, -1); redraw(); renderUI() },
		OnReplace: func() {
			if fl == nil || len(f.matches) == 0 {
				return
			}
			if _, err := fl.ReplaceMatches([]canvas.Match{f.matches[f.cur]}, f.opts, f.repl); err == nil {
				again(false)
			}
		},
		OnReplaceAll: func() {
			if fl == nil {
				return
			}
			if n, err := fl.ReplaceAll(f.opts, f.repl); err == nil {
				u.message = "Replaced " + strconv.Itoa(n)
				again(false)
			}
		},
		OnClose: func() { u.closeFind(fl); redraw(); renderUI() },
	})
}
