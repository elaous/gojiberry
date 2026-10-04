package components

import (
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

// SpellItem is one misspelled word in SpellPanel.
type SpellItem struct {
	Word string
	// Count is how many times it occurs.
	Count       int
	Suggestions []string
}

// SpellPanelProps configures SpellPanel. It is controlled: the caller checks the
// text (package spell) and feeds the result back.
type SpellPanelProps struct {
	// Enabled turns the check on; Language names the dictionary in use
	// ("English"), and Detected the language the text seems to be in ("" when
	// unknown). A Detected different from Language is flagged.
	Enabled            bool
	Language, Detected string
	Items              []SpellItem
	// ReadOnly hides the buttons that change the text (a viewer's).
	ReadOnly bool

	OnToggle func(enabled bool)
	// OnPick replaces every occurrence of word with the suggestion.
	OnPick func(word, suggestion string)
	// OnIgnore leaves the word alone for now; OnAdd adds it to the user's own
	// dictionary; OnGoto selects its first occurrence.
	OnIgnore, OnAdd, OnGoto func(word string)
}

// SpellPanel lists the misspelled words with their suggestions, and an
// "Ignore" and "Add to dictionary" for each.
func SpellPanel(p SpellPanelProps) xango.Node {
	box := []xango.Attr{xango.A("type", "checkbox"), xango.A("class", "gj-spell-enable"), xango.A("aria-label", "Check spelling"),
		xango.On("change", func(xango.Event) {
			if p.OnToggle != nil {
				p.OnToggle(!p.Enabled)
			}
		})}
	if p.Enabled {
		box = append(box, xango.A("checked", "checked"))
	}
	toggle := xango.El("label", []xango.Attr{xango.A("class", "gj-spell-toggle")}, xango.El("input", box), xango.Text(" Check spelling"))
	kids := []xango.Node{toggle}
	if p.Enabled {
		info := "Dictionary: " + p.Language
		if p.Detected != "" && p.Detected != p.Language {
			info += " · the text looks like " + p.Detected
		}
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-spell-info")}, xango.Text(info)))
		if len(p.Items) == 0 {
			kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-spell-none"), xango.A("role", "status")}, xango.Text("No misspelled words")))
		}
	}
	btn := func(class, label string, f func(string), word string) xango.Node {
		return xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", class),
			xango.On("click", func(xango.Event) {
				if f != nil {
					f(word)
				}
			})}, xango.Text(label))
	}
	for _, it := range p.Items {
		head := xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-spell-word"), xango.A("title", "Go to the word"),
			xango.On("click", func(xango.Event) {
				if p.OnGoto != nil {
					p.OnGoto(it.Word)
				}
			})}, xango.Text(it.Word))
		row := []xango.Node{head}
		if it.Count > 1 {
			row = append(row, xango.El("span", []xango.Attr{xango.A("class", "gj-spell-count")}, xango.Text("×"+strconv.Itoa(it.Count))))
		}
		var sugg []xango.Node
		if !p.ReadOnly {
			for _, s := range it.Suggestions {
				sugg = append(sugg, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-spell-suggestion"),
					xango.On("click", func(xango.Event) {
						if p.OnPick != nil {
							p.OnPick(it.Word, s)
						}
					})}, xango.Text(s)))
			}
		}
		if len(it.Suggestions) == 0 {
			sugg = append(sugg, xango.El("span", []xango.Attr{xango.A("class", "gj-spell-nosuggestion")}, xango.Text("No suggestions")))
		}
		actions := []xango.Node{btn("gj-spell-ignore", "Ignore", p.OnIgnore, it.Word)}
		if !p.ReadOnly {
			actions = append(actions, btn("gj-spell-add", "Add to dictionary", p.OnAdd, it.Word))
		}
		kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-spell-item"), xango.A("data-word", it.Word)},
			xango.El("div", []xango.Attr{xango.A("class", "gj-spell-head")}, row...),
			xango.El("div", []xango.Attr{xango.A("class", "gj-spell-suggestions")}, sugg...),
			xango.El("div", []xango.Attr{xango.A("class", "gj-spell-actions")}, actions...)))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-spell"), xango.A("aria-label", "Spelling")}, kids...)
}
