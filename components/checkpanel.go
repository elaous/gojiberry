package components

import (
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

type CheckFinding struct {
	Rule, Severity     string
	Message, Fix, Text string
	FixLabel           string
}

// CheckPanelProps configures CheckPanel.
type CheckPanelProps struct {
	Findings   []CheckFinding
	Checked    bool
	OnCheck    func()
	OnGo       func(i int)
	OnFix      func(i int)
	Gate       string
	OnGate     func(mode string)
	Overridden bool
	OnOverride func()
	GateFloor  string
}

func gateRank(g string) int {
	switch g {
	case "warn":
		return 1
	case "block":
		return 2
	}
	return 0
}

func CheckPanel(p CheckPanelProps) xango.Node {
	run := xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("id", "gj-check-run"), xango.A("class", "gj-keybindings-button"),
		xango.On("click", func(xango.Event) {
			if p.OnCheck != nil {
				p.OnCheck()
			}
		})}, xango.Text(tr("check.run", "Check this document")))
	var summary string
	switch {
	case !p.Checked:
		summary = tr("check.notRun", "Not checked yet.")
	case len(p.Findings) == 0:
		summary = tr("check.none", "Nothing found. A check finds what a tool can; it does not replace reading the document with a screen reader.")
	default:
		errs := 0
		for _, f := range p.Findings {
			if f.Severity == "error" {
				errs++
			}
		}
		summary = trf("check.found", "%d findings: %d errors, %d warnings.", len(p.Findings), errs, len(p.Findings)-errs)
	}
	var items []xango.Node
	for i, f := range p.Findings {
		i := i
		sev := tr("check.sev."+f.Severity, f.Severity)
		kids := []xango.Node{
			xango.El("strong", []xango.Attr{xango.A("class", "gj-check-severity")}, xango.Text(sev+": ")),
			xango.El("span", []xango.Attr{xango.A("class", "gj-check-message")}, xango.Text(f.Message)),
			xango.El("div", []xango.Attr{xango.A("class", "gj-doc-meta gj-check-fix")}, xango.Text(f.Fix)),
		}
		if p.OnGo != nil {
			kids = append(kids, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-keybindings-button gj-check-go"), xango.A("data-finding", strconv.Itoa(i)),
				xango.A("aria-label", trf("check.goLabel", "Go to finding %d: %s", i+1, f.Message)),
				xango.On("click", func(xango.Event) { p.OnGo(i) })}, xango.Text(tr("check.go", "Go to it"))))
		}
		if p.OnFix != nil && f.FixLabel != "" {
			kids = append(kids, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-keybindings-button gj-check-fix-button"), xango.A("data-finding", strconv.Itoa(i)),
				xango.On("click", func(xango.Event) { p.OnFix(i) })}, xango.Text(f.FixLabel)))
		}
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-check-item gj-check-"+f.Rule)}, kids...))
	}
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(tr("check.title", "Accessibility check"))),
		run,
		xango.El("p", []xango.Attr{xango.A("id", "gj-check-summary"), xango.A("role", "status")}, xango.Text(summary)),
	}
	if p.OnGate != nil {
		var modes []xango.Node
		floorLabel := ""
		for _, m := range []struct{ value, label string }{
			{"", tr("check.gate.allow", "Export without checking")},
			{"warn", tr("check.gate.warn", "Warn about errors")},
			{"block", tr("check.gate.block", "Block export with errors")},
		} {
			value := m.value
			attrs := []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-keybindings-button gj-check-gate"), xango.A("data-gate", value),
				xango.A("aria-pressed", boolAttr(p.Gate == value))}
			if value == p.GateFloor {
				floorLabel = m.label
			}
			if gateRank(value) < gateRank(p.GateFloor) {
				attrs = append(attrs, xango.A("disabled", "disabled")) // below what the operator allows
			} else {
				attrs = append(attrs, xango.On("click", func(xango.Event) { p.OnGate(value) }))
			}
			modes = append(modes, xango.El("button", attrs, xango.Text(m.label)))
		}
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("role", "group"), xango.A("aria-label", tr("check.gate", "Before exporting")), xango.A("class", "gj-check-gates"), xango.A("style", "display:flex;gap:6px;flex-wrap:wrap;margin:8px 0")}, modes...))
		if gateRank(p.GateFloor) > 0 {
			note := trf("check.gate.floor", "Set by the administrator of this site: at least \"%s\". It cannot be lowered here.", floorLabel)
			if p.GateFloor == "block" {
				note = trf("check.gate.floorBlock", "Set by the administrator of this site: \"%s\". An export with errors cannot be allowed anyway.", floorLabel)
			}
			nodes = append(nodes, xango.El("p", []xango.Attr{xango.A("id", "gj-check-floor"), xango.A("class", "gj-doc-meta")}, xango.Text(note)))
		}
		if p.Gate == "block" && p.OnOverride != nil && p.GateFloor != "block" {
			label := tr("check.override", "Allow the next export anyway")
			if p.Overridden {
				label = tr("check.overridden", "The next export is allowed")
			}
			nodes = append(nodes, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("id", "gj-check-override"), xango.A("class", "gj-keybindings-button"), xango.A("aria-pressed", boolAttr(p.Overridden)),
				xango.On("click", func(xango.Event) { p.OnOverride() })}, xango.Text(label)))
		}
	}
	if len(items) > 0 {
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list gj-check-list")}, items...))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-check")}, nodes...)
}
