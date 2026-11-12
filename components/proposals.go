package components

import (
	"unicode/utf8"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/xango"
)

// ProposalPanelProps configures ProposalPanel.
type ProposalPanelProps struct {
	// ID is the list's id; "proposals" when empty.
	ID                       string
	Proposals                []canvas.Proposal
	Suggesting               bool
	OnMode                   func(on bool)
	OnAccept, OnReject       func(id string)
	OnAcceptAll, OnRejectAll func()
}

func ProposalPanel(p ProposalPanelProps) xango.Node {
	id := p.ID
	if id == "" {
		id = "proposals"
	}
	children := []xango.Node{
		Checkbox(CheckboxProps{Label: tr("proposals.mode", "Suggest changes (edits are proposals until accepted)"), Checked: p.Suggesting, OnChange: p.OnMode}),
	}
	if len(p.Proposals) == 0 {
		children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-proposals-none"), xango.A("role", "status")}, xango.Text(tr("proposals.none", "No suggested changes."))))
		return xango.El("div", []xango.Attr{xango.A("class", "gj-proposals"), xango.A("id", id)}, children...)
	}
	children = append(children, xango.El("p", []xango.Attr{xango.A("class", "gj-proposals-count"), xango.A("role", "status")},
		xango.Text(trn("proposals.count", "%d suggested change", "%d suggested changes", len(p.Proposals)))))
	var bulk []xango.Node
	if p.OnAcceptAll != nil {
		bulk = append(bulk, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-proposals-accept-all"), xango.On("click", func(xango.Event) { p.OnAcceptAll() })}, xango.Text(tr("proposals.acceptAll", "Accept all"))))
	}
	if p.OnRejectAll != nil {
		bulk = append(bulk, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-proposals-reject-all"), xango.On("click", func(xango.Event) { p.OnRejectAll() })}, xango.Text(tr("proposals.rejectAll", "Reject all"))))
	}
	if len(bulk) > 0 {
		children = append(children, xango.El("div", []xango.Attr{xango.A("class", "gj-proposals-bulk")}, bulk...))
	}
	var rows []xango.Node
	for _, pr := range p.Proposals {
		pr := pr
		sentence := ProposalSentence(pr)
		row := []xango.Node{
			xango.El("span", []xango.Attr{xango.A("class", "gj-proposal-text"), xango.A("style", "border-left-color:"+canvas.ProposalColor(pr.Author))}, xango.Text(sentence)),
			xango.El("span", []xango.Attr{xango.A("class", "gj-doc-meta")}, xango.Text(FormatTime(pr.Time, false))),
		}
		if p.OnAccept != nil {
			row = append(row, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-proposal-accept"),
				xango.A("aria-label", trf("proposals.acceptOne", "Accept: %s", sentence)),
				xango.On("click", func(xango.Event) { p.OnAccept(pr.ID) })}, xango.Text(tr("proposals.accept", "Accept"))))
		}
		if p.OnReject != nil {
			row = append(row, xango.El("button", []xango.Attr{xango.A("type", "button"), xango.A("class", "gj-proposal-reject"),
				xango.A("aria-label", trf("proposals.rejectOne", "Reject: %s", sentence)),
				xango.On("click", func(xango.Event) { p.OnReject(pr.ID) })}, xango.Text(tr("proposals.reject", "Reject"))))
		}
		rows = append(rows, xango.El("li", []xango.Attr{xango.A("class", "gj-proposal"), xango.A("data-id", pr.ID)}, row...))
	}
	children = append(children, xango.El("ul", []xango.Attr{xango.A("class", "gj-proposals-list"), xango.A("aria-label", tr("proposals.label", "Suggested changes"))}, rows...))
	return xango.El("div", []xango.Attr{xango.A("class", "gj-proposals"), xango.A("id", id)}, children...)
}

func ProposalSentence(p canvas.Proposal) string {
	who := p.Author
	if who == "" {
		who = tr("proposals.someone", "Someone")
	}
	switch {
	case p.Deleted != "" && p.Inserted != "":
		return trf("proposals.replaced", "%s replaced '%s' with '%s'", who, shorten(p.Deleted), shorten(p.Inserted))
	case p.Deleted != "":
		return trf("proposals.removed", "%s removed '%s'", who, shorten(p.Deleted))
	}
	return trf("proposals.added", "%s added '%s'", who, shorten(p.Inserted))
}

func shorten(s string) string {
	out := make([]rune, 0, 51)
	for _, r := range s {
		if r == '\n' {
			r = ' '
		}
		out = append(out, r)
	}
	if utf8.RuneCountInString(string(out)) > 50 {
		return string(out[:50]) + "…"
	}
	return string(out)
}
