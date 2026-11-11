package components

import (
	"strconv"

	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/gojiberry/xango"
)

// FontSelectorProps configures FontSelector.
type FontSelectorProps struct {
	Fonts []fonts.Font
	// Selected is the chosen family ("" for none).
	Selected string
	Query    string
	// Source, if set, limits the list to one repository's fonts.
	Source      string
	Recent      []string
	PreviewText string
	Placeholder string
	Limit       int
	Open        bool
	Disabled    bool

	OnToggleOpen func(open bool)
	OnQuery      func(query string)
	OnSource     func(source string) // "" selects all
	OnSelect     func(font fonts.Font)
}

func FontSelector(props FontSelectorProps) xango.Node {
	label := props.Placeholder
	if label == "" {
		label = "Choose a font"
	}
	labelAttrs := []xango.Attr{xango.A("class", "gj-fontselector-label")}
	if props.Selected != "" {
		label = props.Selected
		if f, ok := fonts.Find(props.Fonts, props.Selected); ok {
			labelAttrs = append(labelAttrs, xango.A("style", "font-family:"+fonts.CSSFamily(f)))
		} else {
			labelAttrs = append(labelAttrs, xango.A("style", "font-family:"+fonts.CSSFamily(fonts.Font{Family: props.Selected})))
		}
	}
	lb := fontListbox
	controlAttrs := []xango.Attr{
		xango.A("class", "gj-fontselector-control"),
		xango.A("type", "button"),
		xango.A("aria-haspopup", "listbox"),
		xango.A("aria-expanded", boolAttr(props.Open)),
	}
	if props.Disabled {
		controlAttrs = append(controlAttrs, xango.A("disabled", "disabled"))
	} else if props.OnToggleOpen != nil {
		open := props.Open
		controlAttrs = append(controlAttrs,
			xango.On("click", func(xango.Event) { props.OnToggleOpen(!open) }),
			lb.controlKey(open, props.OnToggleOpen))
	}
	children := []xango.Node{xango.El("button", controlAttrs,
		xango.El("span", labelAttrs, xango.Text(label)),
		xango.El("span", []xango.Attr{xango.A("class", "gj-fontselector-caret")}, xango.Text("▾")),
	)}
	if props.Open && !props.Disabled {
		children = append(children, fontSelectorPanel(props))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-fontselector")}, children...)
}

var fontListbox = listbox{root: ".gj-fontselector", control: ":scope > .gj-fontselector-control", options: ":scope .gj-fontselector-option", first: ":scope .gj-fontselector-search"}

func fontSelectorPanel(props FontSelectorProps) xango.Node {
	limit := props.Limit
	if limit <= 0 {
		limit = 100
	}
	preview := props.PreviewText
	if preview == "" {
		preview = "The quick brown fox"
	}

	searchAttrs := []xango.Attr{
		xango.A("type", "search"),
		xango.A("class", "gj-fontselector-search"),
		xango.A("value", props.Query),
		xango.A("placeholder", "Search fonts"),
		xango.A("aria-label", "Search fonts"),
		fontListbox.searchKey(props.OnToggleOpen),
	}
	if props.OnQuery != nil {
		searchAttrs = append(searchAttrs, xango.On("input", func(e xango.Event) { props.OnQuery(xango.EventValue[string](e)) }))
	}
	panel := []xango.Node{xango.El("input", searchAttrs)}

	if sources := fonts.Sources(props.Fonts); len(sources) > 1 {
		chip := func(label, value string) xango.Node {
			attrs := []xango.Attr{
				xango.A("type", "button"),
				xango.A("class", "gj-fontselector-chip"),
				xango.A("aria-pressed", boolAttr(props.Source == value)),
				xango.A("data-source", value),
			}
			if props.OnSource != nil {
				attrs = append(attrs, xango.On("click", func(xango.Event) { props.OnSource(value) }))
			}
			return xango.El("button", attrs, xango.Text(label))
		}
		chips := []xango.Node{chip("All", "")}
		for _, s := range sources {
			chips = append(chips, chip(s, s))
		}
		panel = append(panel, xango.El("div", []xango.Attr{xango.A("class", "gj-fontselector-sources")}, chips...))
	}

	matches := fonts.FromSource(fonts.Search(props.Fonts, props.Query), props.Source)
	option := func(f fonts.Font) xango.Node {
		selected := f.Family == props.Selected
		class := "gj-fontselector-option"
		if selected {
			class += " gj-fontselector-option-selected"
		}
		attrs := []xango.Attr{
			xango.A("class", class),
			xango.A("role", "option"),
			xango.A("aria-selected", boolAttr(selected)),
			xango.A("data-family", f.Family),
			xango.A("tabindex", "-1"),
		}
		var choose func()
		if props.OnSelect != nil {
			font := f
			choose = func() { props.OnSelect(font) }
			attrs = append(attrs, xango.On("click", func(xango.Event) { props.OnSelect(font) }))
		}
		attrs = append(attrs, fontListbox.optionKey(choose, true, props.OnToggleOpen))
		return xango.El("div", attrs,
			xango.El("span", []xango.Attr{xango.A("class", "gj-fontselector-name")}, xango.Text(f.Family)),
			xango.El("span", []xango.Attr{xango.A("class", "gj-fontselector-preview"), xango.A("style", "font-family:"+fonts.CSSFamily(f))}, xango.Text(preview)),
			xango.El("span", []xango.Attr{xango.A("class", "gj-fontselector-source")}, xango.Text(f.Source)),
		)
	}
	heading := func(text string) xango.Node {
		return xango.El("div", []xango.Attr{xango.A("class", "gj-fontselector-group"), xango.A("role", "presentation")}, xango.Text(text))
	}

	var items []xango.Node
	shown := 0
	if props.Query == "" && len(props.Recent) > 0 {
		var recent []xango.Node
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
		items = append(items, xango.El("div", []xango.Attr{xango.A("class", "gj-fontselector-more")},
			xango.Text(strconv.Itoa(len(matches)-shown)+" more — keep typing to narrow the list")))
	}
	if len(matches) == 0 {
		items = append(items, xango.El("div", []xango.Attr{xango.A("class", "gj-fontselector-empty")}, xango.Text("No fonts match")))
	}
	panel = append(panel, xango.El("div", []xango.Attr{xango.A("class", "gj-fontselector-list"), xango.A("role", "listbox"), xango.A("tabindex", "0"), xango.A("aria-label", tr("fontselector.list", "Fonts"))}, items...))
	return xango.El("div", []xango.Attr{xango.A("class", "gj-fontselector-panel")}, panel...)
}
