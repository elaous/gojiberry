package canvas

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// fixedProposals makes the ids and times of new proposals predictable (p1, p2, ...).
func fixedProposals(t *testing.T) {
	t.Helper()
	oldID, oldClock := proposalID, proposalClock
	n := 0
	proposalID = func() string { n++; return fmt.Sprintf("p%d", n) }
	proposalClock = func() time.Time { return time.Unix(1_700_000_000, 0) }
	t.Cleanup(func() { proposalID, proposalClock = oldID, oldClock })
}

func TestProposalMarkRoundTrip(t *testing.T) {
	m := ProposalMark{Kind: ProposalDelete, ID: "ab12", Author: "ana@example.com: the \"best\"", Time: time.Unix(1_700_000_000, 0)}
	got, ok := ParseProposalMark(m.String())
	if !ok || got.Kind != ProposalDelete || got.ID != "ab12" || got.Author != m.Author || !got.Time.Equal(m.Time) {
		t.Fatalf("%q -> %+v %v", m.String(), got, ok)
	}
	for _, bad := range []string{"", "x", "i:id:1", "z:id:1:a", "i::1:a", "i:id:notanumber:a", "i:id:1:%zz"} {
		if _, ok := ParseProposalMark(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
	first := ProposalColor("ana")
	for range 3 {
		if got := ProposalColor("ana"); got != first || got == "" {
			t.Error("a person always has the same color")
		}
	}
}

// text of the flow as the person sees it: struck text in [..], added text in {..}.
func marked(f *TextFlow) string {
	var sb strings.Builder
	for _, it := range f.Slice(0, len([]rune(f.EditText()))) {
		c, ok := it.(Chars)
		if !ok {
			continue
		}
		m, has := ParseProposalMark(c.Format.Proposal)
		switch {
		case !has:
			sb.WriteString(c.Text)
		case m.Kind == ProposalDelete:
			sb.WriteString("[" + c.Text + "]")
		default:
			sb.WriteString("{" + c.Text + "}")
		}
	}
	return sb.String()
}

func TestTypingAndDeletingInSuggestionMode(t *testing.T) {
	fixedProposals(t)
	f, st := editFlow(t, 5, Chars{Text: "hello world"})
	f.Author = "ana"
	f.Suggesting = true
	st.InsertText("!")
	st.InsertText("?") // continues the same proposal
	if marked(f) != "hello{!?} world" {
		t.Fatalf("typed: %q", marked(f))
	}
	if ps := f.Proposals(); len(ps) != 1 || ps[0].Inserted != "!?" || ps[0].Deleted != "" || ps[0].Author != "ana" {
		t.Fatalf("one proposal: %+v", ps)
	}
	// backspace over the proposed text removes it for real: it was never part of the document
	st.HandleKey("Backspace", false)
	if marked(f) != "hello{!} world" || f.Cursors()[0] != At(6) {
		t.Fatalf("backspace over a proposal: %q %v", marked(f), f.Cursors())
	}
	st.HandleKey("Backspace", false)
	st.HandleKey("Backspace", false)
	if marked(f) != "hell[o] world" || flowText(f) != "hello world" {
		t.Fatalf("backspace over real text only strikes it: %q / %q", marked(f), flowText(f))
	}
	if f.Cursors()[0] != At(4) {
		t.Fatalf("the caret goes before what was struck: %v", f.Cursors())
	}
	// the Delete key strikes forward and the caret moves past it
	st.HandleKey("Delete", false)
	if marked(f) != "hell[o] world" {
		t.Fatalf("delete of already struck text changes nothing: %q", marked(f))
	}
	st.HandleKey("ArrowRight", false)
	st.HandleKey("Delete", false)
	// (the Delete over struck text moved the caret past it; the arrow moved it past the space)
	if marked(f) != "hell[o] [w]orld" {
		t.Fatalf("delete forward: %q", marked(f))
	}
}

func TestReplacingASelectionIsOneProposal(t *testing.T) {
	fixedProposals(t)
	f, st := editFlow(t, 0, Chars{Text: "the brown fox"})
	f.Author = "bo"
	f.Suggesting = true
	f.SetSelection(4, 9)
	st.InsertText("red")
	if marked(f) != "the [brown]{red} fox" {
		t.Fatalf("%q", marked(f))
	}
	ps := f.Proposals()
	if len(ps) != 1 || ps[0].Deleted != "brown" || ps[0].Inserted != "red" || ps[0].From != 4 || ps[0].To != 12 {
		t.Fatalf("%+v", ps)
	}
	if f.Cursors()[0] != At(12) {
		t.Fatalf("the caret after the new text: %v", f.Cursors())
	}
}

func TestAcceptAndRejectAProposal(t *testing.T) {
	for _, accept := range []bool{true, false} {
		fixedProposals(t)
		f, st := editFlow(t, 0, Chars{Text: "the brown fox"})
		f.Author = "bo"
		f.Suggesting = true
		f.SetSelection(4, 9)
		st.InsertText("red")
		f.Suggesting = false
		id := f.Proposals()[0].ID
		var ok bool
		if accept {
			ok = f.AcceptProposal(id)
		} else {
			ok = f.RejectProposal(id)
		}
		want := "the brown fox"
		if accept {
			want = "the red fox"
		}
		if !ok || flowText(f) != want || marked(f) != want || len(f.Proposals()) != 0 {
			t.Fatalf("accept=%v: %q / %q (%v)", accept, flowText(f), marked(f), ok)
		}
		if f.AcceptProposal(id) || f.RejectProposal("nope") {
			t.Error("settling a proposal twice, or one that is not there, does nothing")
		}
		// one undo brings the proposal back
		if !f.Undo() || len(f.Proposals()) != 1 {
			t.Fatalf("accept=%v: undo: %d proposals", accept, len(f.Proposals()))
		}
	}
}

func TestAcceptAndRejectAll(t *testing.T) {
	fixedProposals(t)
	f, st := editFlow(t, 0, Chars{Text: "one two three"})
	f.Author = "cy"
	f.Suggesting = true
	f.SetSelection(0, 3)
	st.InsertText("1")
	f.SetSelection(f.Cursors()[0].Caret+1, f.Cursors()[0].Caret+4) // " two" -> struck
	st.HandleKey("Delete", false)
	if len(f.Proposals()) != 2 {
		t.Fatalf("%q %+v", marked(f), f.Proposals())
	}
	f.Suggesting = false
	if n := f.RejectAllProposals(); n != 2 || flowText(f) != "one two three" {
		t.Fatalf("reject all: %d %q", n, flowText(f))
	}
	f.Suggesting = true
	f.SetSelection(0, 3)
	st.InsertText("1")
	f.Suggesting = false
	if n := f.AcceptAllProposals(); n != 1 || flowText(f) != "1 two three" {
		t.Fatalf("accept all: %d %q", n, flowText(f))
	}
	if f.AcceptAllProposals() != 0 {
		t.Error("nothing left to accept")
	}
}

func TestTypingOutsideSuggestionModeDoesNotJoinAProposal(t *testing.T) {
	fixedProposals(t)
	f, st := editFlow(t, 3, Chars{Text: "abcdef"})
	f.Author = "di"
	f.Suggesting = true
	st.InsertText("X")
	f.Suggesting = false
	st.InsertText("Y") // typed next to the proposal, but not part of it
	if marked(f) != "abc{X}Ydef" {
		t.Fatalf("%q", marked(f))
	}
	// formatting applied to a proposal keeps it a proposal, and bold text carries no mark of its own
	f.SetSelection(3, 5)
	f.ApplyFormat(func(fm *Format) { fm.FontWeight = 700 })
	if len(f.Proposals()) != 1 {
		t.Fatalf("a bold proposal is still a proposal: %+v", f.Proposals())
	}
}

func TestProposalsAreDrawnInTheirAuthorsColor(t *testing.T) {
	fixedProposals(t)
	f, st := editFlow(t, 0, Chars{Text: "ab"})
	f.Author = "ed"
	f.Suggesting = true
	st.InsertText("X")
	f.SetSelection(1, 2)
	st.HandleKey("Delete", false)
	d := f.flatten()
	var added, struck TextStyle
	for i, r := range d.runes {
		switch r {
		case 'X':
			added = d.styles[d.rs[i]]
		case 'a':
			struck = d.styles[d.rs[i]]
		}
	}
	if !added.Underline || added.Color != ProposalColor("ed") {
		t.Errorf("an added word is underlined in the author's color: %+v", added)
	}
	if !struck.Strike || struck.Color != ProposalColor("ed") {
		t.Errorf("a removed word is struck through in the author's color: %+v", struck)
	}
}

func TestReviewCommands(t *testing.T) {
	fixedProposals(t)
	f, st := editFlow(t, 0, Chars{Text: "the brown fox runs"})
	f.Author = "fay"
	var modes []bool
	var settled []string
	st.Features = NewRegistry()
	Review{OnMode: func(on bool) { modes = append(modes, on) },
		OnSettle: func(accepted bool, p Proposal) { settled = append(settled, fmt.Sprint(accepted, ":", p.Inserted)) }}.Install(st.Features)
	if !st.HandleChord("Mod+Alt+S") || !f.Suggesting || !st.HandleChord("Mod+Alt+S") || f.Suggesting {
		t.Fatalf("the same key turns the mode on and off: %v", modes)
	}
	if fmt.Sprint(modes) != "[true false]" {
		t.Errorf("the host is told: %v", modes)
	}
	st.HandleChord("Mod+Alt+S")
	f.SetSelection(4, 9)
	st.InsertText("red")
	end := len([]rune(f.EditText()))
	f.SetSelection(end, end)
	st.InsertText("X") // a second proposal at the end of the text
	f.Suggesting = false
	if len(f.Proposals()) != 2 {
		t.Fatalf("%q", marked(f))
	}
	f.SetSelection(0, 0) // before both: the next one is the first
	if !st.HandleChord("Mod+Alt+Y") || flowText(f) != "the red fox runsX" {
		t.Fatalf("accept the next: %q / %q", flowText(f), marked(f))
	}
	f.SetSelection(0, 0)
	if !st.HandleChord("Mod+Alt+D") || len(f.Proposals()) != 0 {
		t.Fatalf("reject the last one: %q", marked(f))
	}
	if fmt.Sprint(settled) != "[true:red false:X]" {
		t.Errorf("the host is told: %v", settled)
	}
	if st.HandleChord("Mod+Alt+Y") {
		t.Error("with no proposal there is nothing to accept")
	}
}

// Accepting a completion while suggesting is a proposal too: one proposal holding the whole word.
func TestCompletionWhileSuggestingIsAProposal(t *testing.T) {
	fixedProposals(t)
	f, _ := editFlow(t, 0, Chars{Text: "alpha alps\n"})
	f.Author = "gil"
	f.Complete = true
	f.SetCursors([]Cursor{At(11)})
	f.Suggesting = true
	typeFlow(f, "al")
	if len(f.Suggestions()) == 0 {
		t.Fatal("completions are offered")
	}
	f.HandleKey("Tab", false)
	// the typed letters were themselves proposed additions, which the completion replaces for real
	if flowText(f) != "alpha alps\nalpha" {
		t.Fatalf("%q / %q", marked(f), flowText(f))
	}
	if ps := f.Proposals(); len(ps) != 1 || ps[0].Inserted != "alpha" || ps[0].Author != "gil" {
		t.Fatalf("one proposal, the whole word: %+v (%q)", ps, marked(f))
	}
	f.Suggesting = false
	f.RejectAllProposals()
	if flowText(f) != "alpha alps\n" {
		t.Fatalf("rejecting leaves the text as it was: %q", flowText(f))
	}
}
