package components

import (
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
)

func TestProposalSentences(t *testing.T) {
	for _, tc := range []struct {
		p    canvas.Proposal
		want string
	}{
		{canvas.Proposal{Author: "Ana", Deleted: "brown", Inserted: "red"}, "Ana replaced 'brown' with 'red'"},
		{canvas.Proposal{Author: "Ana", Inserted: "X"}, "Ana added 'X'"},
		{canvas.Proposal{Author: "Ana", Deleted: "two\nlines"}, "Ana removed 'two lines'"},
		{canvas.Proposal{Inserted: "Y"}, "Someone added 'Y'"},
		{canvas.Proposal{Author: "Bo", Inserted: strings.Repeat("é", 80)}, "Bo added '" + strings.Repeat("é", 50) + "…'"},
	} {
		if got := ProposalSentence(tc.p); got != tc.want {
			t.Errorf("%q, want %q", got, tc.want)
		}
	}
}

func TestProposalPanelMarkup(t *testing.T) {
	when := time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC)
	var accepted, rejected string
	allAccepted := false
	html := render(t, ProposalPanel(ProposalPanelProps{
		Suggesting: true, OnMode: func(bool) {},
		Proposals: []canvas.Proposal{{ID: "p1", Author: "Ana", Time: when, Deleted: "brown", Inserted: "red"}, {ID: "p2", Author: "Bo", Time: when, Inserted: "X"}},
		OnAccept:  func(id string) { accepted = id }, OnReject: func(id string) { rejected = id }, OnAcceptAll: func() { allAccepted = true }, OnRejectAll: func() {},
	}))
	for _, want := range []string{"2 suggested changes", "Ana replaced &#39;brown&#39; with &#39;red&#39;", "Bo added &#39;X&#39;", `aria-label="Accept: Ana replaced &#39;brown&#39; with &#39;red&#39;"`,
		`aria-label="Reject: Bo added &#39;X&#39;"`, "Accept all", "Reject all", `checked="checked"`, "2026-03-04 05:06", `data-id="p2"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q in\n%s", want, html)
		}
	}
	_, _, _ = accepted, rejected, allAccepted
	empty := render(t, ProposalPanel(ProposalPanelProps{}))
	if !strings.Contains(empty, "No suggested changes.") || strings.Contains(empty, "<ul") {
		t.Errorf("%s", empty)
	}
	one := render(t, ProposalPanel(ProposalPanelProps{Proposals: []canvas.Proposal{{ID: "p", Inserted: "a"}}}))
	if !strings.Contains(one, "1 suggested change<") || strings.Contains(one, "accept-all") {
		t.Errorf("singular and no bulk buttons without handlers: %s", one)
	}
}
