package components

import (
	"fmt"

	"github.com/elaous/gojiberry/xango"
)

// CapacityProps configures Capacity.
type CapacityProps struct {
	Used, Total        int64
	Label              string
	ShowCounts         bool
	Documents, Trashed int
}

const CapacityWarnAt = 80

const CapacityFullAt = 95

func CapacityAdvice(pct float64) string {
	if pct >= CapacityFullAt {
		return tr("capacity.full", "This store is full or nearly: a save may be refused, and then the document is not saved. Right now, download your documents (Download as, or a folder as a ZIP), then make room by deleting old versions and emptying the trash, or save somewhere else.")
	}
	return tr("capacity.nearlyFull", "This store is filling up. So that no work is lost: download your documents now (Download as, or a folder as a ZIP), delete old versions and empty the trash, or save to another place before it is full.")
}

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
		if pct >= CapacityWarnAt {
			role := "status"
			if pct >= CapacityFullAt {
				role = "alert"
			}
			children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-capacity-warning"), xango.A("role", role)}, xango.Text(CapacityAdvice(pct))))
		}
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
