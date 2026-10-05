package components

import (
	"math"
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

// Paper is a paper size in points (1/72 inch), portrait.
type Paper struct {
	Name, Label   string
	Width, Height float64
}

// Papers are the paper sizes offered, A4 first.
var Papers = []Paper{
	{"a4", "A4", 595, 842},
	{"a3", "A3", 842, 1191},
	{"a5", "A5", 420, 595},
	{"b5", "B5", 499, 709},
	{"letter", "Letter", 612, 792},
	{"legal", "Legal", 612, 1008},
}

// PageSetup is how the sheets of a document are laid out: the paper, its
// orientation and the margins in points, in CSS order (top, right, bottom, left).
type PageSetup struct {
	Paper     string // a Papers name; "" or unknown is A4
	Landscape bool
	Margins   [4]float64
}

// DefaultPageSetup is A4, portrait, with one inch all round.
func DefaultPageSetup() PageSetup { return PageSetup{Paper: "a4", Margins: [4]float64{72, 72, 72, 72}} }

// Margin presets: a name, its label and the margins.
var marginPresets = []struct {
	Name, Label string
	Margins     [4]float64
}{
	{"normal", "Normal", [4]float64{72, 72, 72, 72}},
	{"narrow", "Narrow", [4]float64{36, 36, 36, 36}},
	{"wide", "Wide", [4]float64{72, 144, 72, 144}},
}

// paper finds the paper of the setup, A4 when unknown.
func (p PageSetup) paper() Paper {
	for _, pp := range Papers {
		if pp.Name == p.Paper {
			return pp
		}
	}
	return Papers[0]
}

// Size is the sheet's width and height in points, turned by the orientation.
func (p PageSetup) Size() (w, h float64) {
	pp := p.paper()
	if p.Landscape {
		return pp.Height, pp.Width
	}
	return pp.Width, pp.Height
}

// Normalized fixes what cannot be laid out: the paper is a known one, a margin
// is not negative, and the margins leave at least a quarter of each side for text.
func (p PageSetup) Normalized() PageSetup {
	p.Paper = p.paper().Name
	w, h := p.Size()
	for i, m := range p.Margins {
		if math.IsNaN(m) || math.IsInf(m, 0) {
			m = 0 // "NaN" and "Inf" parse as numbers
		}
		p.Margins[i] = math.Max(0, math.Round(m))
	}
	fit := func(a, b *float64, size float64) {
		if limit := size * 3 / 4; *a+*b > limit {
			f := limit / (*a + *b)
			*a, *b = math.Floor(*a*f), math.Floor(*b*f)
		}
	}
	fit(&p.Margins[3], &p.Margins[1], w)
	fit(&p.Margins[0], &p.Margins[2], h)
	return p
}

// MarginList is the margins as a TextFlow's Margins prop wants them.
func (p PageSetup) MarginList() []float64 { return p.Margins[:] }

// Label is the setup in a few words for a status bar: "A4 / portrait".
func (p PageSetup) Label() string {
	o := tr("page.portrait", "portrait")
	if p.Landscape {
		o = tr("page.landscape", "landscape")
	}
	return p.paper().Label + " / " + o
}

// PageSetupProps configures PageSetupPanel.
type PageSetupProps struct {
	Setup PageSetup
	// OnChange receives the new setup (already Normalized) when something is changed.
	OnChange func(PageSetup)
}

// PageSetupPanel edits a PageSetup: paper, orientation, margin presets and each
// margin. It is controlled: it shows Setup and reports changes through OnChange.
func PageSetupPanel(props PageSetupProps) xango.Node {
	set := props.Setup.Normalized()
	change := func(f func(*PageSetup)) {
		if props.OnChange != nil {
			n := set
			f(&n)
			props.OnChange(n.Normalized())
		}
	}
	sel := func(class, label string, opts []xango.Node, on func(string)) xango.Node {
		return xango.El("label", []xango.Attr{xango.A("class", "gj-pagesetup-field")},
			xango.El("span", nil, xango.Text(label)),
			xango.El("select", []xango.Attr{xango.A("class", class), xango.On("change", func(e xango.Event) { on(xango.EventValue[string](e)) })}, opts...))
	}
	opt := func(value, label string, on bool) xango.Node {
		a := []xango.Attr{xango.A("value", value)}
		if on {
			a = append(a, xango.A("selected", "selected"))
		}
		return xango.El("option", a, xango.Text(label))
	}
	var papers []xango.Node
	for _, p := range Papers {
		w, h := p.Width, p.Height
		papers = append(papers, opt(p.Name, p.Label+" ("+mm(w)+" × "+mm(h)+" mm)", p.Name == set.Paper))
	}
	orient := []xango.Node{
		opt("portrait", tr("page.portrait", "portrait"), !set.Landscape),
		opt("landscape", tr("page.landscape", "landscape"), set.Landscape),
	}
	presets := []xango.Node{opt("-", tr("page.custom", "Custom"), true)}
	for _, m := range marginPresets {
		presets = append(presets, opt(m.Name, tr("page.margins."+m.Name, m.Label), m.Margins == set.Margins))
	}
	kids := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-pagesetup-title")}, xango.Text(tr("page.setup", "Page setup"))),
		sel("gj-pagesetup-paper", tr("page.paper", "Paper"), papers, func(v string) { change(func(p *PageSetup) { p.Paper = v }) }),
		sel("gj-pagesetup-orientation", tr("page.orientation", "Orientation"), orient, func(v string) { change(func(p *PageSetup) { p.Landscape = v == "landscape" }) }),
		sel("gj-pagesetup-preset", tr("page.marginPreset", "Margins"), presets, func(v string) {
			for _, m := range marginPresets {
				if m.Name == v {
					change(func(p *PageSetup) { p.Margins = m.Margins })
				}
			}
		}),
	}
	for i, side := range []struct{ id, label string }{{"top", "Top"}, {"right", "Right"}, {"bottom", "Bottom"}, {"left", "Left"}} {
		i := i
		kids = append(kids, xango.El("label", []xango.Attr{xango.A("class", "gj-pagesetup-field")},
			xango.El("span", nil, xango.Text(tr("page.margin."+side.id, side.label)+" (pt)")),
			xango.El("input", []xango.Attr{xango.A("type", "number"), xango.A("min", "0"), xango.A("class", "gj-pagesetup-margin"), xango.A("data-side", side.id),
				xango.A("value", strconv.FormatFloat(set.Margins[i], 'f', -1, 64)),
				xango.On("change", func(e xango.Event) {
					v, err := strconv.ParseFloat(xango.EventValue[string](e), 64)
					if err != nil {
						return // not a number: leave the margin as it was
					}
					change(func(p *PageSetup) { p.Margins[i] = v })
				})})))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-pagesetup")}, kids...)
}

// mm is a size in points as millimetres.
func mm(pt float64) string { return strconv.Itoa(int(math.Round(pt * 25.4 / 72))) }
