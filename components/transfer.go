package components

import "github.com/elaous/gojiberry/gox"

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
func Transfer(props TransferProps) gox.Node {
	group := func(title, kind string, items []SaverChoice, on func(string)) gox.Node {
		var buttons []gox.Node
		for _, c := range items {
			attrs := []gox.Attr{gox.A("type", "button"), gox.A("class", "gj-transfer-button"), gox.A("data-format", c.Name), gox.A("data-kind", kind)}
			if props.Disabled || on == nil {
				attrs = append(attrs, gox.A("disabled", "disabled"))
			} else {
				name := c.Name
				attrs = append(attrs, gox.On("click", func(gox.Event) { on(name) }))
			}
			buttons = append(buttons, gox.El("button", attrs, gox.Text(c.Label)))
		}
		return gox.El("div", []gox.Attr{gox.A("class", "gj-transfer-group"), gox.A("role", "group"), gox.A("aria-label", title)},
			append([]gox.Node{gox.El("h4", []gox.Attr{gox.A("class", "gj-transfer-title")}, gox.Text(title))}, buttons...)...)
	}
	return gox.El("div", []gox.Attr{gox.A("class", "gj-transfer")},
		group("Import", "import", props.Imports, props.OnImport),
		group("Export", "export", props.Exports, props.OnExport))
}
