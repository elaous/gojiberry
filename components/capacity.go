package components

import (
	"fmt"

	"github.com/elaous/gojiberry/xango"
)

// CapacityProps configures Capacity.
type CapacityProps struct {
	// Used and Total are bytes; Total 0 means the allocated space is not known,
	// and only Used is shown. Label names what is measured ("Storage").
	Used, Total int64
	Label       string
	// ShowCounts adds how many documents there are (and how many of them are in the
	// trash) to the meter.
	ShowCounts         bool
	Documents, Trashed int
}

// Capacity shows how full a store is: a meter with the percentage, how much is
// used of how much was allocated, and how much is left. A meter above 90% is
// marked, and one over 100% too.
func Capacity(p CapacityProps) xango.Node {
	label := p.Label
	if label == "" {
		label = tr("capacity.storage", "Storage")
	}
	var children []xango.Node
	head := xango.El("div", []xango.Attr{xango.A("class", "gj-capacity-head")},
		xango.El("span", []xango.Attr{xango.A("class", "gj-capacity-label")}, xango.Text(label)))
	if p.Total > 0 {
		pct := float64(p.Used) / float64(p.Total) * 100
		class := "gj-capacity"
		switch {
		case pct > 100:
			class += " gj-capacity-over"
		case pct > 90:
			class += " gj-capacity-high"
		}
		left := max(p.Total-p.Used, 0)
		bar := min(pct, 100)
		children = append(children, head,
			xango.El("div", []xango.Attr{xango.A("class", "gj-capacity-meter"), xango.A("role", "meter"), xango.A("aria-label", label),
				xango.A("aria-valuemin", "0"), xango.A("aria-valuemax", "100"), xango.A("aria-valuenow", fmt.Sprintf("%.0f", pct)),
				xango.A("aria-valuetext", trf("capacity.valuetext", "%.0f%% used", pct))},
				xango.El("div", []xango.Attr{xango.A("class", "gj-capacity-bar"), xango.A("style", fmt.Sprintf("width:%.1f%%", bar))})),
			xango.El("div", []xango.Attr{xango.A("class", "gj-capacity-text")},
				xango.El("span", []xango.Attr{xango.A("class", "gj-capacity-percent")}, xango.Text(fmt.Sprintf("%.0f%%", pct))),
				xango.El("span", []xango.Attr{xango.A("class", "gj-capacity-used")}, xango.Text(trf("capacity.used", "%s of %s used", byteSize64(p.Used), byteSize64(p.Total)))),
				xango.El("span", []xango.Attr{xango.A("class", "gj-capacity-left")}, xango.Text(trf("capacity.left", "%s left", byteSize64(left))))))
		return withCounts(xango.El("div", []xango.Attr{xango.A("class", class)}, children...), p)
	}
	children = append(children, head,
		xango.El("div", []xango.Attr{xango.A("class", "gj-capacity-text")},
			xango.El("span", []xango.Attr{xango.A("class", "gj-capacity-used")}, xango.Text(trf("capacity.usedOnly", "%s used", byteSize64(p.Used)))),
			xango.El("span", []xango.Attr{xango.A("class", "gj-capacity-unknown")}, xango.Text(tr("capacity.unknown", "allocated space not known")))))
	return withCounts(xango.El("div", []xango.Attr{xango.A("class", "gj-capacity")}, children...), p)
}

func withCounts(n xango.Node, p CapacityProps) xango.Node {
	if !p.ShowCounts {
		return n
	}
	text := trf("capacity.documents", "%d documents", p.Documents)
	if p.Trashed > 0 {
		text += " · " + trf("capacity.trashed", "%d in the trash", p.Trashed)
	}
	n.Children = append(n.Children, xango.El("div", []xango.Attr{xango.A("class", "gj-capacity-counts")}, xango.Text(text)))
	return n
}

func byteSize64(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
