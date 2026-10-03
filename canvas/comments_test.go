package canvas

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCommentSet(t *testing.T) {
	s := NewCommentSet(Comment{Author: "a", Text: "one"}, Comment{ID: "x", Text: "two"})
	if s.Len() != 2 || s.List()[0].ID != "c1" {
		t.Fatalf("%+v", s.List())
	}
	if c := s.Add(Comment{Text: "three"}); c.ID != "c2" {
		t.Fatalf("ids skip nothing: %q", c.ID)
	}
	s.Add(Comment{ID: "x", Text: "two!"}) // replaces
	if c, ok := s.Get("x"); !ok || c.Text != "two!" || s.Len() != 3 {
		t.Fatal(c, s.Len())
	}
	if !s.Update("x", func(c *Comment) { c.Resolved, c.ID = true, "hijack" }) {
		t.Fatal("update")
	}
	if c, _ := s.Get("x"); !c.Resolved {
		t.Fatal("ID cannot be changed by Update")
	}
	if s.Update("nope", func(*Comment) {}) || s.Delete("nope") {
		t.Fatal("missing comment")
	}
	if !s.Delete("c1") || s.Len() != 2 {
		t.Fatal("delete")
	}
	var nilSet *CommentSet
	if _, ok := nilSet.Get("a"); ok || nilSet.Len() != 0 || nilSet.List() != nil || nilSet.Delete("a") || nilSet.Update("a", nil) {
		t.Fatal("a nil set is empty")
	}
}

func TestMentions(t *testing.T) {
	text := "Hi @ana, ask ana.b@example.com or @bob_2 (cc: @c-d.). Not a@b, nor x@@y; mail me@x.org."
	var got []string
	for _, m := range Mentions(text) {
		got = append(got, m.Text)
		if text[m.Start:m.End] != m.Text {
			t.Errorf("range of %q", m.Text)
		}
	}
	want := []string{"@ana", "ana.b@example.com", "@bob_2", "@c-d", "me@x.org"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	ms := Mentions("@ana and ana@x.io")
	if ms[0].Kind != MentionUser || ms[0].Name != "ana" || ms[1].Kind != MentionEmail || ms[1].Name != "ana@x.io" {
		t.Fatalf("%+v", ms)
	}
	if len(Mentions("no one here")) != 0 {
		t.Fatal("none")
	}
}

func TestSummaryStripsMarkdown(t *testing.T) {
	md := "## Fix this\n\n**Bold** and _italic_ with `code` and a [link](http://x.test) and ![img](u.png).\n- item one\n1. item two\n> quoted ~~gone~~\n```go\nx := 1\n```"
	got := CommentSummary(md, 0)
	for _, bad := range []string{"#", "**", "_", "`", "](", "http://x.test", "- ", "~~", ">"} {
		if strings.Contains(got, bad) {
			t.Errorf("%q still has %q", got, bad)
		}
	}
	for _, want := range []string{"Fix this", "Bold and italic with code and a link and img.", "item one", "item two", "quoted gone"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	if s := CommentSummary("abcdefghij", 5); s != "abcd…" {
		t.Fatal(s)
	}
	if s := CommentSummary("short", 50); s != "short" {
		t.Fatal(s)
	}
}

func commentFlow(t *testing.T) (*TextFlow, *Stage) {
	f, st := editFlow(t, 0, Chars{Text: "hello brave world"})
	f.Author = "ana"
	return f, st
}

func runCmd(f *TextFlow, st *Stage, cmd Command, answer string) bool {
	st.Prompt = func(string, string) (string, bool) { return answer, answer != "" }
	return st.RunCommand(cmd)
}

func TestCommentFeatureAddEditResolveDelete(t *testing.T) {
	f, st := commentFlow(t)
	f.SetCursors([]Cursor{{Anchor: 6, Caret: 11}}) // "brave"
	if !runCmd(f, st, CmdCommentAdd, "**Why** brave? cc @bob") {
		t.Fatal("add")
	}
	c, ok := f.Comments.Get("c1")
	if !ok || c.Author != "ana" || c.Created.IsZero() || !strings.Contains(c.Text, "@bob") {
		t.Fatalf("%+v", c)
	}
	ids := charComments(f.ContentItems())
	for i, id := range ids {
		if (id == "c1") != (i >= 6 && i < 11) {
			t.Fatalf("comment tags the selection only: %q", ids)
		}
	}
	// The anchor follows the text through edits.
	f.SetCursors([]Cursor{At(0)})
	f.InsertText(">>")
	if ids := charComments(f.ContentItems()); ids[8] != "c1" || ids[7] != "" {
		t.Fatalf("anchor moved with the text: %q", ids)
	}
	f.SetCursors([]Cursor{At(10)})
	if !runCmd(f, st, CmdCommentEdit, "changed") {
		t.Fatal("edit")
	}
	if c, _ := f.Comments.Get("c1"); c.Text != "changed" {
		t.Fatal(c.Text)
	}
	if !runCmd(f, st, CmdCommentResolve, "x") {
		t.Fatal("resolve")
	}
	if c, _ := f.Comments.Get("c1"); !c.Resolved {
		t.Fatal("resolved")
	}
	f.SetCursors([]Cursor{At(0)})
	if runCmd(f, st, CmdCommentEdit, "x") || runCmd(f, st, CmdCommentDelete, "x") {
		t.Fatal("no comment at the caret")
	}
	f.SetCursors([]Cursor{At(10)})
	if !runCmd(f, st, CmdCommentDelete, "x") || f.Comments.Len() != 0 {
		t.Fatal("delete")
	}
	for _, id := range charComments(f.ContentItems()) {
		if id != "" {
			t.Fatal("delete un-marks the text")
		}
	}
	if flowText(f) != ">>hello brave world" {
		t.Fatalf("text untouched: %q", flowText(f))
	}
}

func TestCommentFeatureNeedsSelectionAndPrompt(t *testing.T) {
	f, st := commentFlow(t)
	f.SetCursors([]Cursor{At(3)})
	if runCmd(f, st, CmdCommentAdd, "x") {
		t.Fatal("nothing selected")
	}
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 5}})
	if runCmd(f, st, CmdCommentAdd, "") {
		t.Fatal("cancelled prompt")
	}
	st.Prompt = nil
	if st.RunCommand(CmdCommentAdd) {
		t.Fatal("no prompt")
	}
	if f.Comments.Len() != 0 {
		t.Fatal("nothing added")
	}
}

func TestCommentHoverTipAndHighlight(t *testing.T) {
	f, st := commentFlow(t)
	f.Comments = NewCommentSet(Comment{ID: "k", Author: "bob", Text: "**Check** this wording, it reads oddly to me and might need a rewrite before we ship", Created: time.Now(),
		Replies: []Reply{{Author: "ana", Text: "ok"}}})
	f.Items = []Item{Chars{Text: "hello "}, Chars{Text: "brave", Format: Format{Comment: "k"}}, Chars{Text: " world"}}
	f.ResetContent()
	f.Items = []Item{Chars{Text: "hello "}, Chars{Text: "brave", Format: Format{Comment: "k"}}, Chars{Text: " world"}}
	f.SetMode(ModeView)
	var heard []string
	f.OnHoverComment = func(id string) { heard = append(heard, id) }
	p := st.Painter.(*fakePainter)
	p.calls = nil
	f.Draw()
	if !p.has("fillStyle " + CommentHighlight) {
		t.Fatal("commented text is highlighted")
	}
	if !f.MouseMove(10+8*7, 40) && f.HoveredComment() != "k" {
		t.Fatal("hover finds the comment")
	}
	if f.HoveredComment() != "k" || !reflect.DeepEqual(heard, []string{"k"}) {
		t.Fatalf("%q %v", f.HoveredComment(), heard)
	}
	p.calls = nil
	f.DrawOverlay()
	for _, want := range []string{`fillText "bob"`, `fillText "Check this wording`, `fillText "1 reply"`} {
		if !p.has(want) {
			t.Errorf("tip lacks %s:\n%v", want, p.calls)
		}
	}
	f.MouseMove(10+8*20, 40) // off the comment
	if f.HoveredComment() != "" || heard[len(heard)-1] != "" {
		t.Fatal("leaving clears it")
	}
	f.MouseMove(10+8*7, 40)
	f.MouseLeave()
	if f.HoveredComment() != "" {
		t.Fatal("MouseLeave clears it")
	}
	// Resolved comments are not highlighted but still show a tip.
	f.Comments.Update("k", func(c *Comment) { c.Resolved = true })
	p.calls = nil
	f.Draw()
	if p.has("fillStyle " + CommentHighlight) {
		t.Fatal("resolved comments are not highlighted")
	}
	f.MouseMove(10+8*7, 40)
	p.calls = nil
	f.DrawOverlay()
	if !p.has(`fillText "1 reply · resolved`) {
		t.Fatalf("resolved tip: %v", p.calls)
	}
	// An unknown ID (deleted comment) is just text.
	f.Comments.Delete("k")
	f.MouseMove(10+8*20, 40)
	f.MouseMove(10+8*7, 40)
	if f.HoveredComment() != "" {
		t.Fatal("no comment, no hover")
	}
}

func TestCommentRangeExcerptDeleteSelect(t *testing.T) {
	f, _ := commentFlow(t)
	f.Comments = NewCommentSet(Comment{ID: "k", Text: "t"}, Comment{ID: "z", Text: "u"})
	f.ResetContent()
	f.Items = []Item{Chars{Text: "ab "}, Chars{Text: "cd", Format: Format{Comment: "k"}}, Chars{Text: " ef "}, Chars{Text: "gh", Format: Format{Comment: "k", FontWeight: 700}}}
	if from, to, ok := f.CommentRange("k"); !ok || from != 3 || to != 11 {
		t.Fatalf("range %d..%d %v", from, to, ok)
	}
	if _, _, ok := f.CommentRange("z"); ok {
		t.Fatal("a comment on no text has no range")
	}
	if got := f.CommentExcerpts(); !reflect.DeepEqual(got, map[string]string{"k": "cdgh"}) {
		t.Fatalf("%v", got)
	}
	if !f.SelectComment("k") || f.Cursors()[0] != (Cursor{Anchor: 3, Caret: 11}) {
		t.Fatalf("%v", f.Cursors())
	}
	if f.SelectComment("z") {
		t.Fatal("nothing to select")
	}
	if !f.DeleteComment("k") || f.Comments.Len() != 1 || f.DeleteComment("k") {
		t.Fatal("delete")
	}
	for _, id := range charComments(f.ContentItems()) {
		if id != "" {
			t.Fatal("text un-marked")
		}
	}
	if flowText(f) != "ab cd ef gh" {
		t.Fatalf("%q", flowText(f))
	}
}

func TestCommentOnWholeMarkdownDocument(t *testing.T) {
	f, st := commentFlow(t)
	f.ResetContent()
	f.Items = Markdown{}.Transform("# Imported\n\nfresh **words**")
	f.SetMode(ModeEdit)
	f.MouseDown(10+8, 40)
	f.MouseUp(0, 0)
	f.SelectAll()
	if !runCmd(f, st, CmdCommentAdd, "whole") || f.Comments.Len() != 1 {
		t.Fatalf("comment on a selection with paragraph formats: %v", f.Cursors())
	}
}

func TestCommentChordRunsThroughStage(t *testing.T) {
	f, st := commentFlow(t)
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 5}})
	st.Prompt = func(string, string) (string, bool) { return "hi", true }
	if !st.HandleChord("Mod+Alt+N") || f.Comments.Len() != 1 {
		t.Fatal("Ctrl+Alt+N adds a comment")
	}
	if st.Features.Keymap()["Mod+Alt+N"] != CmdCommentAdd {
		t.Fatal("bound")
	}
}

func TestSetRemoteContentAndSetReplaceAll(t *testing.T) {
	f, _ := editFlow(t, 3, Chars{Text: "hello"})
	changes := 0
	f.OnChange = func(*TextFlow) { changes++ }
	f.InsertText("x")
	if changes != 1 {
		t.Fatal(changes)
	}
	f.SetRemoteContent([]Item{Chars{Text: "remote text"}}, []Cursor{{Anchor: 2, Caret: 4, After: true}, At(99)})
	if changes != 1 || flowText(f) != "remote text" || !f.Editing() {
		t.Fatalf("a remote change is not a local edit: %d %q %v", changes, flowText(f), f.Editing())
	}
	if cs := f.Cursors(); len(cs) != 2 || cs[0] != (Cursor{Anchor: 2, Caret: 4, After: true}) || cs[1] != At(11) {
		t.Fatalf("%v", cs)
	}
	if _, ok := f.hist.back(f.histSnap()); ok {
		t.Fatal("history cleared")
	}
	f.SetRemoteContent([]Item{Chars{Text: "x"}}, nil)
	if f.Cursors()[0].Caret > 1 {
		t.Fatal("cursors clamped")
	}
	// The sets are replaced in place.
	cs := NewCommentSet(Comment{ID: "a"})
	same := cs
	cs.ReplaceAll([]Comment{{ID: "b"}, {ID: "c"}})
	if same.Len() != 2 || cs.List()[0].ID != "b" {
		t.Fatal(cs.List())
	}
	fs := NewFootnoteSet(Footnote{ID: "a"})
	fs.ReplaceAll(nil)
	if fs.Len() != 0 {
		t.Fatal("notes replaced")
	}
	// Commands that change side data touch the target.
	g, st := commentFlow(t)
	g.Comments = NewCommentSet(Comment{ID: "k", Text: "t"})
	g.Items = []Item{Chars{Text: "ab", Format: Format{Comment: "k"}}}
	g.ResetContent()
	g.Items = []Item{Chars{Text: "ab", Format: Format{Comment: "k"}}}
	g.OnChange = nil
	n := 0
	g.OnChange = func(*TextFlow) { n++ }
	g.SetCursors([]Cursor{At(1)})
	runCmd(g, st, CmdCommentResolve, "x")
	runCmd(g, st, CmdCommentEdit, "new")
	if n < 2 {
		t.Fatalf("resolve and edit notify listeners: %d", n)
	}
}

func TestSubscribeSurvivesOnChangeBeingReplaced(t *testing.T) {
	f, _ := editFlow(t, 0, Chars{Text: "ab"})
	heard, own := 0, 0
	f.Subscribe(func(*TextFlow) { heard++ })
	f.OnChange = func(*TextFlow) { own++ }
	f.InsertText("x")
	f.OnChange = nil // a gox tree setting its props does this
	f.InsertText("y")
	if heard != 2 || own != 1 {
		t.Fatalf("subscribers keep hearing: %d, OnChange %d", heard, own)
	}
}

func TestSafeLinkURL(t *testing.T) {
	for u, want := range map[string]bool{
		"https://a.example/x?y=1:2": true, "http://a": true, "mailto:a@b": true, "tel:+1": true,
		"#bookmark": true, "/path/x": true, "rel/path:colon": true, "page.html": true,
		"javascript:alert(1)": false, " JavaScript:alert(1)": false, "data:text/html,x": false, "vbscript:x": false,
	} {
		if got := SafeLinkURL(u); got != want {
			t.Errorf("SafeLinkURL(%q) = %v, want %v", u, got, want)
		}
	}
	var cs *CommentSet
	cs.ReplaceAll(nil) // no panic on a nil set
	var fs *FootnoteSet
	fs.ReplaceAll(nil)
}

func TestIDPrefixKeepsReplicasFromMintingTheSameID(t *testing.T) {
	a, b := NewCommentSet(), NewCommentSet()
	a.IDPrefix, b.IDPrefix = "ana-", "bo-"
	ca, cb := a.Add(Comment{Text: "x"}), b.Add(Comment{Text: "y"})
	if ca.ID == cb.ID || ca.ID != "ana-c1" || cb.ID != "bo-c1" {
		t.Fatalf("ids %q %q", ca.ID, cb.ID)
	}
	fa, fb := NewFootnoteSet(), NewFootnoteSet()
	fa.IDPrefix, fb.IDPrefix = "ana-", "bo-"
	if fa.Add(Footnote{}).ID == fb.Add(Footnote{}).ID {
		t.Fatal("footnote ids collide")
	}
}

func TestStripMarkdownKeepsIdentifiersAndAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"use snake_case and ana_b@example.com": "use snake_case and ana_b@example.com",
		"**bold** and _italic_ and *it*":       "bold and italic and it",
		"2*3 = 6":                              "2*3 = 6",
		"`code` and ~~gone~~":                  "code and gone",
	} {
		if got := StripMarkdown(in); got != want {
			t.Errorf("StripMarkdown(%q) = %q, want %q", in, got, want)
		}
	}
}
