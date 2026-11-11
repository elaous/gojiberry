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

type PageSetup struct {
	Paper                     string
	Landscape                 bool
	Margins                   [4]float64
	CustomWidth, CustomHeight float64
}

const CustomPaper = "custom"

// DefaultPageSetup is A4, portrait, with one inch all round.
func DefaultPageSetup() PageSetup { return PageSetup{Paper: "a4", Margins: [4]float64{72, 72, 72, 72}} }

var marginPresets = []struct {
	Name, Label string
	Margins     [4]float64
}{
	{"normal", "Normal", [4]float64{72, 72, 72, 72}},
	{"narrow", "Narrow", [4]float64{36, 36, 36, 36}},
	{"wide", "Wide", [4]float64{72, 144, 72, 144}},
}

func (p PageSetup) paper() Paper {
	if p.Paper == CustomPaper {
		ok := func(v float64) bool { return !math.IsNaN(v) && v >= 100 && v <= 5000 }
		if ok(p.CustomWidth) && ok(p.CustomHeight) {
			return Paper{CustomPaper, "Custom", math.Round(p.CustomWidth), math.Round(p.CustomHeight)}
		}
	}
	for _, pp := range Papers {
		if pp.Name == p.Paper {
			return pp
		}
	}
	return Papers[0]
}

func (p PageSetup) Size() (w, h float64) {
	pp := p.paper()
	if p.Landscape {
		return pp.Height, pp.Width
	}
	return pp.Width, pp.Height
}

func PageSetupOf(w, h float64, margins [4]float64) (PageSetup, bool) {
	near := func(a, b float64) bool { return math.Abs(a-b) <= 2 }
	for _, pp := range Papers {
		switch {
		case near(w, pp.Width) && near(h, pp.Height):
			return PageSetup{Paper: pp.Name, Margins: margins}.Normalized(), true
		case near(w, pp.Height) && near(h, pp.Width):
			return PageSetup{Paper: pp.Name, Landscape: true, Margins: margins}.Normalized(), true
		}
	}
	if w >= 100 && w <= 5000 && h >= 100 && h <= 5000 {
		ps := PageSetup{Paper: CustomPaper, CustomWidth: math.Min(w, h), CustomHeight: math.Max(w, h), Landscape: w > h, Margins: margins}.Normalized()
		return ps, ps.Paper == CustomPaper
	}
	return PageSetup{}, false
}

func (p PageSetup) Normalized() PageSetup {
	p.Paper = p.paper().Name
	if p.Paper != CustomPaper {
		p.CustomWidth, p.CustomHeight = 0, 0
	} else {
		p.CustomWidth, p.CustomHeight = math.Round(p.CustomWidth), math.Round(p.CustomHeight)
	}
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

func (p PageSetup) MarginList() []float64 { return p.Margins[:] }

func (p PageSetup) Label() string {
	o := tr("page.portrait", "portrait")
	if p.Landscape {
		o = tr("page.landscape", "landscape")
	}
	return p.paper().Label + " / " + o
}

// PageSetupProps configures PageSetupPanel.
type PageSetupProps struct {
	Setup    PageSetup
	OnChange func(PageSetup)
}

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
	papers = append(papers, opt(CustomPaper, tr("page.paperCustom", "Custom size"), set.Paper == CustomPaper))
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
		sel("gj-pagesetup-paper", tr("page.paper", "Paper"), papers, func(v string) {
			change(func(p *PageSetup) {
				if v == CustomPaper && p.Paper != CustomPaper {
					p.CustomWidth, p.CustomHeight = p.paper().Width, p.paper().Height
				}
				p.Paper = v
			})
		}),
		customSize(set, change),
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

func mm(pt float64) string { return strconv.Itoa(int(math.Round(pt * 25.4 / 72))) }

func customSize(set PageSetup, change func(func(*PageSetup))) xango.Node {
	// one element always, so the panel keeps its order as fields come and go
	box := func(children ...xango.Node) xango.Node {
		return xango.El("div", []xango.Attr{xango.A("class", "gj-pagesetup-custom")}, children...)
	}
	if set.Paper != CustomPaper {
		return box()
	}
	field := func(class, label string, pt float64, set func(*PageSetup, float64)) xango.Node {
		return xango.El("label", []xango.Attr{xango.A("class", "gj-pagesetup-field")},
			xango.El("span", nil, xango.Text(label)),
			xango.El("input", []xango.Attr{xango.A("type", "number"), xango.A("min", "35"), xango.A("class", class),
				xango.A("value", mm(pt)),
				xango.On("change", func(e xango.Event) {
					v, err := strconv.ParseFloat(xango.EventValue[string](e), 64)
					if err != nil {
						return
					}
					change(func(p *PageSetup) { set(p, v*72/25.4) })
				})}))
	}
	return box(
		field("gj-pagesetup-width", tr("page.customWidth", "Width (mm)"), set.CustomWidth, func(p *PageSetup, v float64) { p.CustomWidth = v }),
		field("gj-pagesetup-height", tr("page.customHeight", "Height (mm)"), set.CustomHeight, func(p *PageSetup, v float64) { p.CustomHeight = v }),
	)
}
