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
	// FixLabel, if set, is the text of a button that does the fix for the person ("Make it level 2");
	// "" shows no button, because nothing is safe to do for them.
	FixLabel string
}

// CheckPanelProps configures CheckPanel.
type CheckPanelProps struct {
	Findings []CheckFinding
	// Checked says a check has run (an empty list then means "nothing found", not "not run").
	Checked bool
	// OnCheck runs the check; OnGo moves to finding i in the document.
	OnCheck func()
	OnGo    func(i int)
	// OnFix does the fix of finding i; the host checks again afterwards.
	OnFix func(i int)
	// Gate is what exporting does about errors: "" (nothing), "warn" or "block"; OnGate sets it. Gate is
	// shown only when OnGate is set. Overridden says the next export is allowed anyway, and OnOverride
	// allows it (shown while Gate is "block").
	Gate       string
	OnGate     func(mode string)
	Overridden bool
	OnOverride func()
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
		for _, m := range []struct{ value, label string }{
			{"", tr("check.gate.allow", "Export without checking")},
			{"warn", tr("check.gate.warn", "Warn about errors")},
			{"block", tr("check.gate.block", "Block export with errors")},
		} {
			value := m.value
			modes = append(modes, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-keybindings-button gj-check-gate"), xango.A("data-gate", value),
				xango.A("aria-pressed", boolAttr(p.Gate == value)), xango.On("click", func(xango.Event) { p.OnGate(value) })}, xango.Text(m.label)))
		}
		nodes = append(nodes, xango.El("div", []xango.Attr{xango.A("role", "group"), xango.A("aria-label", tr("check.gate", "Before exporting")), xango.A("class", "gj-check-gates"), xango.A("style", "display:flex;gap:6px;flex-wrap:wrap;margin:8px 0")}, modes...))
		if p.Gate == "block" && p.OnOverride != nil {
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
