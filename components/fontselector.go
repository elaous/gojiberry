package components

import (
	"strconv"

	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/gojiberry/gox"
)

// FontSelectorProps configures FontSelector.
type FontSelectorProps struct {
	// Fonts is the catalog to choose from (see the fonts package:
	// fonts.Multi of web-safe, local and online repositories).
	Fonts []fonts.Font
	// Selected is the chosen family ("" for none).
	Selected string
	// Query is the text in the search box; the list shows the fonts
	// whose family matches it (best matches first).
	Query string
	// Source, if set, limits the list to one repository's fonts.
	Source string
	// Recent are families used lately, listed first while Query is empty.
	Recent []string
	// PreviewText is the sample shown in each font ("The quick brown fox"
	// by default).
	PreviewText string
	Placeholder string
	// Limit caps how many fonts are rendered at once (default 100): a
	// catalog of thousands would bury the page. A note says how many more
	// there are.
	Limit    int
	Open     bool
	Disabled bool

	OnToggleOpen func(open bool)
	OnQuery      func(query string)
	OnSource     func(source string) // "" selects all
	OnSelect     func(font fonts.Font)
}

// FontSelector renders a closed control showing the chosen family in its
// own typeface and, when Open, a panel with a search box, source filter
// chips and a list of fonts each previewed in itself. It is controlled:
// the caller owns Open, Query and Source (see the root README's
// controlled-component notes) and loads the fonts it lists (for fonts
// with URLs, add fonts.FontFaceCSS to the page).
//
// Like Dropdown it implements the ARIA listbox pattern's attributes
// only; it has no keyboard navigation of its own.
func FontSelector(props FontSelectorProps) gox.Node {
	label := props.Placeholder
	if label == "" {
		label = "Choose a font"
	}
	labelAttrs := []gox.Attr{gox.A("class", "gj-fontselector-label")}
	if props.Selected != "" {
		label = props.Selected
		if f, ok := fonts.Find(props.Fonts, props.Selected); ok {
			labelAttrs = append(labelAttrs, gox.A("style", "font-family:"+fonts.CSSFamily(f)))
		} else {
			labelAttrs = append(labelAttrs, gox.A("style", "font-family:"+fonts.CSSFamily(fonts.Font{Family: props.Selected})))
		}
	}
	controlAttrs := []gox.Attr{
		gox.A("class", "gj-fontselector-control"),
		gox.A("type", "button"),
		gox.A("aria-haspopup", "listbox"),
		gox.A("aria-expanded", boolAttr(props.Open)),
	}
	if props.Disabled {
		controlAttrs = append(controlAttrs, gox.A("disabled", "disabled"))
	} else if props.OnToggleOpen != nil {
		open := props.Open
		controlAttrs = append(controlAttrs, gox.On("click", func(gox.Event) { props.OnToggleOpen(!open) }))
	}
	children := []gox.Node{gox.El("button", controlAttrs,
		gox.El("span", labelAttrs, gox.Text(label)),
		gox.El("span", []gox.Attr{gox.A("class", "gj-fontselector-caret")}, gox.Text("▾")),
	)}
	if props.Open && !props.Disabled {
		children = append(children, fontSelectorPanel(props))
	}
	return gox.El("div", []gox.Attr{gox.A("class", "gj-fontselector")}, children...)
}

func fontSelectorPanel(props FontSelectorProps) gox.Node {
	limit := props.Limit
	if limit <= 0 {
		limit = 100
	}
	preview := props.PreviewText
	if preview == "" {
		preview = "The quick brown fox"
	}

	searchAttrs := []gox.Attr{
		gox.A("type", "search"),
		gox.A("class", "gj-fontselector-search"),
		gox.A("value", props.Query),
		gox.A("placeholder", "Search fonts"),
		gox.A("aria-label", "Search fonts"),
	}
	if props.OnQuery != nil {
		searchAttrs = append(searchAttrs, gox.On("input", func(e gox.Event) { props.OnQuery(gox.EventValue[string](e)) }))
	}
	panel := []gox.Node{gox.El("input", searchAttrs)}

	// Source filter chips, when there is more than one source.
	if sources := fonts.Sources(props.Fonts); len(sources) > 1 {
		chip := func(label, value string) gox.Node {
			attrs := []gox.Attr{
				gox.A("type", "button"),
				gox.A("class", "gj-fontselector-chip"),
				gox.A("aria-pressed", boolAttr(props.Source == value)),
				gox.A("data-source", value),
			}
			if props.OnSource != nil {
				attrs = append(attrs, gox.On("click", func(gox.Event) { props.OnSource(value) }))
			}
			return gox.El("button", attrs, gox.Text(label))
		}
		chips := []gox.Node{chip("All", "")}
		for _, s := range sources {
			chips = append(chips, chip(s, s))
		}
		panel = append(panel, gox.El("div", []gox.Attr{gox.A("class", "gj-fontselector-sources")}, chips...))
	}

	matches := fonts.FromSource(fonts.Search(props.Fonts, props.Query), props.Source)
	option := func(f fonts.Font) gox.Node {
		selected := f.Family == props.Selected
		class := "gj-fontselector-option"
		if selected {
			class += " gj-fontselector-option-selected"
		}
		attrs := []gox.Attr{
			gox.A("class", class),
			gox.A("role", "option"),
			gox.A("aria-selected", boolAttr(selected)),
			gox.A("data-family", f.Family),
		}
		if props.OnSelect != nil {
			font := f
			attrs = append(attrs, gox.On("click", func(gox.Event) { props.OnSelect(font) }))
		}
		return gox.El("div", attrs,
			gox.El("span", []gox.Attr{gox.A("class", "gj-fontselector-name")}, gox.Text(f.Family)),
			gox.El("span", []gox.Attr{gox.A("class", "gj-fontselector-preview"), gox.A("style", "font-family:"+fonts.CSSFamily(f))}, gox.Text(preview)),
			gox.El("span", []gox.Attr{gox.A("class", "gj-fontselector-source")}, gox.Text(f.Source)),
		)
	}
	heading := func(text string) gox.Node {
		return gox.El("div", []gox.Attr{gox.A("class", "gj-fontselector-group"), gox.A("role", "presentation")}, gox.Text(text))
	}

	var items []gox.Node
	shown := 0
	if props.Query == "" && len(props.Recent) > 0 {
		var recent []gox.Node
		for _, fam := range props.Recent {
			if f, ok := fonts.Find(matches, fam); ok {
				recent = append(recent, option(f))
			}
		}
		if len(recent) > 0 {
			items = append(items, heading("Recent"))
			items = append(items, recent...)
		}
	}
	for _, f := range matches {
		if shown == limit {
			break
		}
		items = append(items, option(f))
		shown++
	}
	if len(matches) > shown {
		items = append(items, gox.El("div", []gox.Attr{gox.A("class", "gj-fontselector-more")},
			gox.Text(strconv.Itoa(len(matches)-shown)+" more — keep typing to narrow the list")))
	}
	if len(matches) == 0 {
		items = append(items, gox.El("div", []gox.Attr{gox.A("class", "gj-fontselector-empty")}, gox.Text("No fonts match")))
	}
	panel = append(panel, gox.El("div", []gox.Attr{gox.A("class", "gj-fontselector-list"), gox.A("role", "listbox")}, items...))
	return gox.El("div", []gox.Attr{gox.A("class", "gj-fontselector-panel")}, panel...)
}
