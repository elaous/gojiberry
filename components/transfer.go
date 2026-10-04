package components

import "github.com/elaous/gojiberry/xango"

// TransferProps configures Transfer.
type TransferProps struct {
	// Imports and Exports are the formats on offer (see package
	// transfer's Registry).
	Imports, Exports []SaverChoice
	Disabled         bool
	// OnImport is called with the format's name when its button is
	// clicked: open a file chooser there (transfer.PickFile) and read the
	// document in. OnExport likewise writes and downloads one
	// (transfer.Download).
	OnImport func(format string)
	OnExport func(format string)
}

// Transfer renders "Import" and "Export" button groups, one button per
// format. It is controlled and does no I/O itself: the callbacks choose
// the file and write it.
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
