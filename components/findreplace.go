package components

import (
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

// FindReplaceProps configures FindReplace. It is controlled: the caller owns
// the query and the results and feeds them back (canvas.TextFlow.Find and
// ReplaceAll do the work).
type FindReplaceProps struct {
	Query, Replacement          string
	MatchCase, WholeWord, Regex bool
	// Count is how many matches there are, and Current which one is selected
	// (from 1, 0 for none).
	Count, Current int
	// Error is shown instead of the count, for a query that cannot be used (a
	// regular expression that does not compile).
	Error string
	// ReadOnly hides the replace row, for a viewer.
	ReadOnly bool

	OnQuery       func(string)
	OnReplacement func(string)
	// OnOptions is called with the three toggles when any is clicked.
	OnOptions                                        func(matchCase, wholeWord, regex bool)
	OnNext, OnPrev, OnReplace, OnReplaceAll, OnClose func()
}

// FindReplace is the find and replace bar: a query box with the match count
// and previous and next buttons, toggles for case, whole word and regular
// expression, and a replacement box with Replace and Replace all.
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
	status := "No query"
	switch {
	case p.Error != "":
		status = p.Error
	case p.Query == "":
	case p.Count == 0:
		status = "No matches"
	default:
		status = strconv.Itoa(p.Current) + " of " + strconv.Itoa(p.Count)
	}
	statusClass := "gj-find-status"
	if p.Error != "" {
		statusClass += " gj-find-error"
	}
	query := []xango.Attr{xango.A("type", "search"), xango.A("class", "gj-find-input"), xango.A("placeholder", "Find"), xango.A("aria-label", "Find"), xango.A("value", p.Query),
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
			btn("gj-find-prev", "↑", "Previous match", has, p.OnPrev),
			btn("gj-find-next", "↓", "Next match", has, p.OnNext),
			btn("gj-find-close", "×", "Close", true, p.OnClose),
		),
		xango.El("div", []xango.Attr{xango.A("class", "gj-find-row gj-find-options")},
			toggle("gj-find-case", "Aa", "Match case", p.MatchCase, func(m, w, r bool) (bool, bool, bool) { return !m, w, r }),
			toggle("gj-find-word", "ab", "Whole word", p.WholeWord, func(m, w, r bool) (bool, bool, bool) { return m, !w, r }),
			toggle("gj-find-regex", ".*", "Regular expression", p.Regex, func(m, w, r bool) (bool, bool, bool) { return m, w, !r }),
		),
	}
	if !p.ReadOnly {
		repl := []xango.Attr{xango.A("type", "text"), xango.A("class", "gj-find-replacement"), xango.A("placeholder", "Replace with"), xango.A("aria-label", "Replace with"), xango.A("value", p.Replacement),
			xango.On("input", func(e xango.Event) {
				if p.OnReplacement != nil {
					p.OnReplacement(xango.EventValue[string](e))
				}
			})}
		rows = append(rows, xango.El("div", []xango.Attr{xango.A("class", "gj-find-row")},
			xango.El("input", repl),
			btn("gj-find-replace", "Replace", "Replace the current match", has, p.OnReplace),
			btn("gj-find-replace-all", "Replace all", "Replace every match", has, p.OnReplaceAll),
		))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-find"), xango.A("role", "search"), xango.A("aria-label", "Find and replace")}, rows...)
}
