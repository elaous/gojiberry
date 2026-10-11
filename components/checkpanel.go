package components

import (
	"strconv"

	"github.com/elaous/gojiberry/xango"
)

// CheckFinding is one problem a document check found, as the panel lists it.
type CheckFinding struct {
	// Rule names the check ("image-alt"); Severity is "error" or "warning" and is written as a word,
	// not only shown by colour.
	Rule, Severity string
	// Message says what is wrong, Fix what to do; Text is a few words of what it concerns.
	Message, Fix, Text string
}

// CheckPanelProps configures CheckPanel.
type CheckPanelProps struct {
	Findings []CheckFinding
	// Checked says a check has run (an empty list then means "nothing found", not "not run").
	Checked bool
	// OnCheck runs the check; OnGo moves to finding i in the document.
	OnCheck func()
	OnGo    func(i int)
}

// CheckPanel lists the findings of a document accessibility check, each with its fix and a button
// that goes to it. The summary is a live status, so a reader hears the result of pressing Check.
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
		items = append(items, xango.El("li", []xango.Attr{xango.A("class", "gj-check-item gj-check-"+f.Rule)}, kids...))
	}
	nodes := []xango.Node{
		xango.El("h4", []xango.Attr{xango.A("class", "gj-doc-title")}, xango.Text(tr("check.title", "Accessibility check"))),
		run,
		xango.El("p", []xango.Attr{xango.A("id", "gj-check-summary"), xango.A("role", "status")}, xango.Text(summary)),
	}
	if len(items) > 0 {
		nodes = append(nodes, xango.El("ul", []xango.Attr{xango.A("class", "gj-doc-list gj-check-list")}, items...))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-check")}, nodes...)
}
