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

// SpellPanelProps configures SpellPanel.
type SpellPanelProps struct {
	Enabled            bool
	Language, Detected string
	Items              []SpellItem
	// ReadOnly hides the buttons that change the text (a viewer's).
	ReadOnly bool

	OnToggle func(enabled bool)
	// OnPick replaces every occurrence of word with the suggestion.
	OnPick                  func(word, suggestion string)
	OnIgnore, OnAdd, OnGoto func(word string)

	Dictionaries     []DocChoice
	Dictionary       string
	OnDictionary     func(name string)
	DictionaryNote   string
	DictionaryFailed bool
}

func SpellPanel(p SpellPanelProps) xango.Node {
	box := []xango.Attr{xango.A("type", "checkbox"), xango.A("class", "gj-spell-enable"), xango.A("aria-label", tr("spell.check", "Check spelling")),
		xango.On("change", func(xango.Event) {
			if p.OnToggle != nil {
				p.OnToggle(!p.Enabled)
			}
		})}
	if p.Enabled {
		box = append(box, xango.A("checked", "checked"))
	}
	toggle := xango.El("label", []xango.Attr{xango.A("class", "gj-spell-toggle")}, xango.El("input", box), xango.Text(" "+tr("spell.check", "Check spelling")))
	kids := []xango.Node{toggle}
	if p.Enabled {
		info := trf("spell.dictionary", "Dictionary: %s", p.Language)
		if p.Detected != "" && p.Detected != p.Language {
			info += trf("spell.looksLike", " · the text looks like %s", p.Detected)
		}
		kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-spell-info")}, xango.Text(info)))
		if len(p.Dictionaries) > 1 && p.OnDictionary != nil {
			var opts []xango.Node
			for _, d := range p.Dictionaries {
				a := []xango.Attr{xango.A("value", d.Name)}
				if d.Name == p.Dictionary {
					a = append(a, xango.A("selected", "selected"))
				}
				opts = append(opts, xango.El("option", a, xango.Text(d.Label)))
			}
			kids = append(kids, xango.El("label", []xango.Attr{xango.A("class", "gj-spell-dictrow")}, xango.Text(tr("spell.chooseDictionary", "Use the dictionary")+" "),
				xango.El("select", []xango.Attr{xango.A("class", "gj-spell-dict"), xango.On("change", func(e xango.Event) { p.OnDictionary(xango.EventValue[string](e)) })}, opts...)))
		}
		if p.DictionaryNote != "" {
			attrs := []xango.Attr{xango.A("class", "gj-spell-dictnote"), xango.A("role", "status")}
			if p.DictionaryFailed {
				attrs = []xango.Attr{xango.A("class", "gj-spell-dictnote gj-doc-error"), xango.A("role", "alert")}
			}
			kids = append(kids, xango.El("p", attrs, xango.Text(p.DictionaryNote)))
		}
		if len(p.Items) == 0 {
			kids = append(kids, xango.El("p", []xango.Attr{xango.A("class", "gj-spell-none"), xango.A("role", "status")}, xango.Text(tr("spell.none", "No misspelled words"))))
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
		head := xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-spell-word"), xango.A("title", tr("spell.goto", "Go to the word")),
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
			sugg = append(sugg, xango.El("span", []xango.Attr{xango.A("class", "gj-spell-nosuggestion")}, xango.Text(tr("spell.noSuggestions", "No suggestions"))))
		}
		actions := []xango.Node{btn("gj-spell-ignore", tr("spell.ignore", "Ignore"), p.OnIgnore, it.Word)}
		if !p.ReadOnly {
			actions = append(actions, btn("gj-spell-add", tr("spell.add", "Add to dictionary"), p.OnAdd, it.Word))
		}
		kids = append(kids, xango.El("div", []xango.Attr{xango.A("class", "gj-spell-item"), xango.A("data-word", it.Word)},
			xango.El("div", []xango.Attr{xango.A("class", "gj-spell-head")}, row...),
			xango.El("div", []xango.Attr{xango.A("class", "gj-spell-suggestions")}, sugg...),
			xango.El("div", []xango.Attr{xango.A("class", "gj-spell-actions")}, actions...)))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-spell"), xango.A("aria-label", tr("spell.region", "Spelling"))}, kids...)
}
