package components

import "github.com/elaous/gojiberry/xango"

// TransferProps configures Transfer.
type TransferProps struct {
	Imports, Exports []SaverChoice
	Disabled         bool
	OnImport         func(format string)
	OnExport         func(format string)
}

func Transfer(props TransferProps) xango.Node {
	group := func(title, kind string, items []SaverChoice, on func(string)) xango.Node {
		var buttons []xango.Node
		for _, c := range items {
			attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-transfer-button"), xango.A("data-format", c.Name), xango.A("data-kind", kind)}
			if props.Disabled || on == nil {
				attrs = append(attrs, xango.A("disabled", "disabled"))
			} else {
				name := c.Name
				attrs = append(attrs, xango.On("click", func(xango.Event) { on(name) }))
			}
			buttons = append(buttons, xango.El("button", attrs, xango.Text(c.Label)))
		}
		return xango.El("div", []xango.Attr{xango.A("class", "gj-transfer-group"), xango.A("role", "group"), xango.A("aria-label", title)},
			append([]xango.Node{xango.El("h4", []xango.Attr{xango.A("class", "gj-transfer-title")}, xango.Text(title))}, buttons...)...)
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-transfer")},
		group("Import", "import", props.Imports, props.OnImport),
		group("Export", "export", props.Exports, props.OnExport))
}
