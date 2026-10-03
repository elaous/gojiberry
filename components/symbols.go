package components

import (
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/gox"
)

// Symbol is a character in a SymbolPicker.
type Symbol struct {
	Char     string
	Name     string
	Category string
}

// DefaultSymbols returns the built-in symbols: punctuation, math, Greek,
// arrows, currency, shapes and accented Latin letters.
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

// FilterSymbols returns the symbols in category (all if empty) whose
// name, category or character contains every word of query.
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
	// Recent are characters picked lately, shown first while there is no
	// query.
	Recent []string
	// Columns is the grid width in characters (default 10).
	Columns int
	// OnPick is called with the chosen character.
	OnPick     func(char string)
	OnQuery    func(query string)
	OnCategory func(category string) // "" for all
}

// SymbolPicker renders a searchable grid of symbols to insert -- a search
// box, category chips, the recently used ones and the matching symbols,
// each a button titled with its name. It is controlled (Query, Category);
// the caller inserts the picked character where it belongs (for a canvas
// editor, FloatingText.InsertText or TextFlow.InsertText).
func SymbolPicker(props SymbolPickerProps) gox.Node {
	symbols := props.Symbols
	if symbols == nil {
		symbols = defaultSymbols
	}
	cols := props.Columns
	if cols <= 0 {
		cols = 10
	}
	btn := func(s Symbol) gox.Node {
		attrs := []gox.Attr{
			gox.A("class", "gj-symbol"), gox.A("type", "button"), gox.A("title", s.Name), gox.A("data-char", s.Char),
		}
		if props.OnPick != nil {
			char := s.Char
			attrs = append(attrs, gox.On("click", func(gox.Event) { props.OnPick(char) }))
		}
		return gox.El("button", attrs, gox.Text(s.Char))
	}

	search := []gox.Attr{
		gox.A("class", "gj-symbolpicker-search"), gox.A("type", "search"), gox.A("placeholder", "Search symbols"), gox.A("value", props.Query),
	}
	if props.OnQuery != nil {
		search = append(search, gox.On("input", func(e gox.Event) { props.OnQuery(gox.EventValue[string](e)) }))
	}
	chip := func(label, cat string) gox.Node {
		class := "gj-symbolpicker-chip"
		if props.Category == cat {
			class += " gj-symbolpicker-chip-on"
		}
		attrs := []gox.Attr{gox.A("class", class), gox.A("type", "button"), gox.A("data-category", cat)}
		if props.OnCategory != nil {
			attrs = append(attrs, gox.On("click", func(gox.Event) { props.OnCategory(cat) }))
		}
		return gox.El("button", attrs, gox.Text(label))
	}
	chips := []gox.Node{chip("All", "")}
	for _, c := range SymbolCategories(symbols) {
		chips = append(chips, chip(c, c))
	}

	var body []gox.Node
	if props.Query == "" && props.Category == "" && len(props.Recent) > 0 {
		var recent []gox.Node
		for _, ch := range props.Recent {
			recent = append(recent, btn(Symbol{Char: ch, Name: "recently used"}))
		}
		body = append(body, gox.El("div", []gox.Attr{gox.A("class", "gj-symbolpicker-recent")}, recent...))
	}
	var grid []gox.Node
	for _, s := range FilterSymbols(symbols, props.Query, props.Category) {
		grid = append(grid, btn(s))
	}
	if len(grid) == 0 {
		body = append(body, gox.El("p", []gox.Attr{gox.A("class", "gj-symbolpicker-empty")}, gox.Text("No symbols match.")))
	} else {
		body = append(body, gox.El("div", []gox.Attr{
			gox.A("class", "gj-symbolpicker-grid"),
			gox.A("style", "grid-template-columns: repeat("+itoa(cols)+", 1fr);"),
		}, grid...))
	}
	return gox.El("div", []gox.Attr{gox.A("class", "gj-symbolpicker")},
		gox.El("input", search),
		gox.El("div", []gox.Attr{gox.A("class", "gj-symbolpicker-chips")}, chips...),
		gox.El("div", []gox.Attr{gox.A("class", "gj-symbolpicker-body")}, body...),
	)
}

func itoa(n int) string { return strconv.Itoa(n) }
