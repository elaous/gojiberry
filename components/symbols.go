package components

import (
	"strconv"
	"strings"

	"github.com/elaous/xango"
)

// Symbol is a character in a SymbolPicker.
type Symbol struct {
	Char     string
	Name     string
	Category string
}

func DefaultSymbols() []Symbol { return append([]Symbol(nil), defaultSymbols...) }

// SymbolCategories lists the categories of symbols, in first-seen order.
func SymbolCategories(symbols []Symbol) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range symbols {
		if !seen[s.Category] {
			seen[s.Category] = true
			out = append(out, s.Category)
		}
	}
	return out
}

func FilterSymbols(symbols []Symbol, query, category string) []Symbol {
	words := strings.Fields(strings.ToLower(query))
	var out []Symbol
	for _, s := range symbols {
		if category != "" && s.Category != category {
			continue
		}
		hay := strings.ToLower(s.Name + " " + s.Category + " " + s.Char)
		ok := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, s)
		}
	}
	return out
}

// SymbolPickerProps configures SymbolPicker.
type SymbolPickerProps struct {
	// Symbols is the set to choose from (DefaultSymbols if nil).
	Symbols []Symbol
	// Query and Category filter the grid; the caller owns them.
	Query, Category string
	Recent          []string
	// Columns is the grid width in characters (default 10).
	Columns int
	// OnPick is called with the chosen character.
	OnPick     func(char string)
	OnQuery    func(query string)
	OnCategory func(category string) // "" for all
}

func SymbolPicker(props SymbolPickerProps) xango.Node {
	symbols := props.Symbols
	if symbols == nil {
		symbols = defaultSymbols
	}
	cols := props.Columns
	if cols <= 0 {
		cols = 10
	}
	nsym := 0
	btn := func(s Symbol) xango.Node {
		attrs := []xango.Attr{
			xango.A("class", "gj-symbol"), xango.A("type", "button"), xango.A("title", s.Name), xango.A("data-char", s.Char),
			rovingTab(nsym, 0),
		}
		nsym++
		if props.OnPick != nil {
			char := s.Char
			attrs = append(attrs, xango.On("click", func(xango.Event) { props.OnPick(char) }))
		}
		return xango.El("button", attrs, xango.Text(s.Char))
	}

	search := []xango.Attr{
		xango.A("class", "gj-symbolpicker-search"), xango.A("type", "search"), xango.A("placeholder", "Search symbols"), xango.A("value", props.Query),
	}
	if props.OnQuery != nil {
		search = append(search, xango.On("input", func(e xango.Event) { props.OnQuery(xango.EventValue[string](e)) }))
	}
	nchip := 0
	chip := func(label, cat string) xango.Node {
		class := "gj-symbolpicker-chip"
		on := props.Category == cat
		if on {
			class += " gj-symbolpicker-chip-on"
		}
		tab := "-1"
		if on {
			tab = "0"
		}
		nchip++
		attrs := []xango.Attr{xango.A("class", class), xango.A("type", "button"), xango.A("data-category", cat), xango.A("tabindex", tab)}
		if props.OnCategory != nil {
			attrs = append(attrs, xango.On("click", func(xango.Event) { props.OnCategory(cat) }))
		}
		return xango.El("button", attrs, xango.Text(label))
	}
	chips := []xango.Node{chip("All", "")}
	for _, c := range SymbolCategories(symbols) {
		chips = append(chips, chip(c, c))
	}

	var body []xango.Node
	if props.Query == "" && props.Category == "" && len(props.Recent) > 0 {
		var recent []xango.Node
		for _, ch := range props.Recent {
			recent = append(recent, btn(Symbol{Char: ch, Name: "recently used"}))
		}
		body = append(body, xango.El("div", []xango.Attr{xango.A("class", "gj-symbolpicker-recent")}, recent...))
	}
	var grid []xango.Node
	for _, s := range FilterSymbols(symbols, props.Query, props.Category) {
		grid = append(grid, btn(s))
	}
	if len(grid) == 0 {
		body = append(body, xango.El("p", []xango.Attr{xango.A("class", "gj-symbolpicker-empty")}, xango.Text("No symbols match.")))
	} else {
		body = append(body, xango.El("div", []xango.Attr{
			xango.A("class", "gj-symbolpicker-grid"),
			xango.A("style", "grid-template-columns: repeat("+itoa(cols)+", 1fr);"),
		}, grid...))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-symbolpicker")},
		xango.El("input", search),
		xango.El("div", []xango.Attr{xango.A("class", "gj-symbolpicker-chips"), xango.A("role", "toolbar"), xango.A("aria-label", tr("symbols.categories", "Symbol categories")),
			gridKeys(".gj-symbolpicker-chips", ".gj-symbolpicker-chip")}, chips...),
		xango.El("div", []xango.Attr{xango.A("class", "gj-symbolpicker-body"), gridKeys(".gj-symbolpicker-body", ".gj-symbol")}, body...),
	)
}

func itoa(n int) string { return strconv.Itoa(n) }
