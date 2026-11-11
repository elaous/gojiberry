package components

import (
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

// FindReplaceProps configures FindReplace.
type FindReplaceProps struct {
	Query, Replacement          string
	MatchCase, WholeWord, Regex bool
	Count, Current              int
	Error                       string
	// ReadOnly hides the replace row, for a viewer.
	ReadOnly bool

	OnQuery                                          func(string)
	OnReplacement                                    func(string)
	OnOptions                                        func(matchCase, wholeWord, regex bool)
	OnNext, OnPrev, OnReplace, OnReplaceAll, OnClose func()
}

func FindReplace(p FindReplaceProps) xango.Node {
	call := func(f func()) func(xango.Event) {
		return func(xango.Event) {
			if f != nil {
				f()
			}
		}
	}
	btn := func(class, label, title string, enabled bool, f func()) xango.Node {
		attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", class), xango.A("title", title), xango.A("aria-label", title), xango.On("click", call(f))}
		if !enabled {
			attrs = append(attrs, xango.A("disabled", "disabled"))
		}
		return xango.El("button", attrs, xango.Text(label))
	}
	toggle := func(class, label, title string, on bool, flip func(m, w, r bool) (bool, bool, bool)) xango.Node {
		cls := "gj-find-toggle " + class
		if on {
			cls += " gj-find-on"
		}
		return xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", cls), xango.A("title", title), xango.A("aria-label", title),
			xango.A("aria-pressed", strconv.FormatBool(on)),
			xango.On("click", func(xango.Event) {
				if p.OnOptions != nil {
					p.OnOptions(flip(p.MatchCase, p.WholeWord, p.Regex))
				}
			})}, xango.Text(label))
	}
	status := tr("find.noQuery", "No query")
	switch {
	case p.Error != "":
		status = p.Error
	case p.Query == "":
	case p.Count == 0:
		status = tr("find.noMatches", "No matches")
	default:
		status = trf("find.nOfM", "%d of %d", p.Current, p.Count)
	}
	statusClass := "gj-find-status"
	if p.Error != "" {
		statusClass += " gj-find-error"
	}
	query := []xango.Attr{xango.A("type", "search"), xango.A("class", "gj-find-input"), xango.A("placeholder", tr("find.find", "Find")), xango.A("aria-label", tr("find.find", "Find")), xango.A("value", p.Query),
		xango.On("input", func(e xango.Event) {
			if p.OnQuery != nil {
				p.OnQuery(xango.EventValue[string](e))
			}
		})}
	has := p.Count > 0
	rows := []xango.Node{
		xango.El("div", []xango.Attr{xango.A("class", "gj-find-row")},
			xango.El("input", query),
			xango.El("span", []xango.Attr{xango.A("class", statusClass), xango.A("role", "status"), xango.A("aria-live", "polite")}, xango.Text(status)),
			btn("gj-find-prev", "↑", tr("find.prev", "Previous match"), has, p.OnPrev),
			btn("gj-find-next", "↓", tr("find.next", "Next match"), has, p.OnNext),
			btn("gj-find-close", "×", tr("find.close", "Close"), true, p.OnClose),
		),
		xango.El("div", []xango.Attr{xango.A("class", "gj-find-row gj-find-options")},
			toggle("gj-find-case", "Aa", tr("find.matchCase", "Match case"), p.MatchCase, func(m, w, r bool) (bool, bool, bool) { return !m, w, r }),
			toggle("gj-find-word", "ab", tr("find.wholeWord", "Whole word"), p.WholeWord, func(m, w, r bool) (bool, bool, bool) { return m, !w, r }),
			toggle("gj-find-regex", ".*", tr("find.regex", "Regular expression"), p.Regex, func(m, w, r bool) (bool, bool, bool) { return m, w, !r }),
		),
	}
	if !p.ReadOnly {
		repl := []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-find-replacement"), xango.A("placeholder", tr("find.replaceWith", "Replace with")), xango.A("aria-label", tr("find.replaceWith", "Replace with")), xango.A("value", p.Replacement),
			xango.On("input", func(e xango.Event) {
				if p.OnReplacement != nil {
					p.OnReplacement(xango.EventValue[string](e))
				}
			})}
		rows = append(rows, xango.El("div", []xango.Attr{xango.A("class", "gj-find-row")},
			xango.El("input", repl),
			btn("gj-find-replace", tr("find.replace", "Replace"), tr("find.replaceCurrent", "Replace the current match"), has, p.OnReplace),
			btn("gj-find-replace-all", tr("find.replaceAll", "Replace all"), tr("find.replaceEvery", "Replace every match"), has, p.OnReplaceAll),
		))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-find"), xango.A("role", "search"), xango.A("aria-label", tr("find.region", "Find and replace"))}, rows...)
}
