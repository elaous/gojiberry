package canvas

import (
	"reflect"
	"strings"
	"testing"
)

func TestFootnoteCommandsAndNumbering(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "hello world today"})
	f.SetCursors([]Cursor{At(11)})
	if !runCmd(f, st, CmdFootnoteAdd, "second in text") {
		t.Fatal("add")
	}
	f.SetCursors([]Cursor{At(5)})
	if !runCmd(f, st, CmdFootnoteAdd, "first in text") {
		t.Fatal("add before")
	}
	list := f.FootnoteList()
	if len(list) != 2 || list[0].Text != "first in text" || list[0].N != 1 || list[1].Text != "second in text" || list[1].N != 2 {
		t.Fatalf("numbered in text order, not creation order: %+v", list)
	}
	if flowText(f) != "hello# world# today" || f.Cursors()[0] != At(6) {
		t.Fatalf("%q %v", flowText(f), f.Cursors())
	}
	// Edit the note next to the caret.
	if !runCmd(f, st, CmdFootnoteEdit, "FIRST") || f.FootnoteList()[0].Text != "FIRST" {
		t.Fatalf("edit: %+v", f.FootnoteList())
	}
	f.SetCursors([]Cursor{At(0)})
	if runCmd(f, st, CmdFootnoteEdit, "x") || runCmd(f, st, CmdFootnoteDelete, "x") {
		t.Fatal("no mark next to the caret")
	}
	// Delete removes mark and note; deleting only the mark (Backspace) hides the note.
	f.SetCursors([]Cursor{At(6)})
	if !runCmd(f, st, CmdFootnoteDelete, "x") || f.Footnotes.Len() != 1 || flowText(f) != "hello world# today" {
		t.Fatalf("delete: %q %d", flowText(f), f.Footnotes.Len())
	}
	f.SetCursors([]Cursor{At(12)})
	f.HandleKey("Backspace", false)
	if len(f.FootnoteList()) != 0 || f.Footnotes.Len() != 1 {
		t.Fatalf("an orphan note is not listed: %+v", f.FootnoteList())
	}
	st.Prompt = nil
	if st.RunCommand(CmdFootnoteAdd) {
		t.Fatal("needs a prompt")
	}
	if runCmd(f, st, CmdFootnoteAdd, "  ") {
		t.Fatal("empty note")
	}
}

func TestFootnoteSet(t *testing.T) {
	s := NewFootnoteSet(Footnote{Text: "a"}, Footnote{ID: "x", Text: "b"})
	if c := s.Add(Footnote{Text: "c"}); c.ID != "n2" || s.Len() != 3 {
		t.Fatal(c.ID)
	}
	s.Add(Footnote{ID: "x", Text: "b2"})
	if n, ok := s.Get("x"); !ok || n.Text != "b2" || s.Len() != 3 {
		t.Fatal(n)
	}
	if !s.Delete("x") || s.Delete("x") || s.Len() != 2 || len(s.List()) != 2 {
		t.Fatal("delete")
	}
	var nilSet *FootnoteSet
	if _, ok := nilSet.Get("a"); ok || nilSet.Len() != 0 || nilSet.List() != nil || nilSet.Delete("a") {
		t.Fatal("nil set")
	}
}

func TestFootnotesReserveABandAndDrawPerPage(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "text with a note"})
	_, _, _, h0 := f.pageRect(0)
	f.SetCursors([]Cursor{At(6)})
	runCmd(f, st, CmdFootnoteAdd, "**Bold** note text")
	_, _, _, h1 := f.pageRect(0)
	if h0-h1 != DefaultFootnoteBand {
		t.Fatalf("the band is kept at the bottom of the page: %v -> %v", h0, h1)
	}
	f.FootnoteBand = 40
	if _, _, _, h2 := f.pageRect(0); h0-h2 != 40 {
		t.Fatalf("custom band: %v", h0-h2)
	}
	f.FootnoteBand = 0
	p := st.Painter.(*fakePainter)
	p.calls = nil
	f.Draw()
	if !p.has(`fillText "1 Bold note text"`) {
		t.Fatalf("the note is drawn with its number, Markdown stripped:\n%v", p.calls)
	}
	if !p.has(`fillText "1" `) {
		t.Fatal("the mark is drawn in the text")
	}
	// A very long note is cut to the band.
	f.Footnotes.Add(Footnote{ID: f.Footnotes.List()[0].ID, Text: strings.Repeat("long note words ", 60)})
	p.calls = nil
	f.Draw()
	cut := false
	for _, c := range p.calls {
		if strings.HasPrefix(c, "fillText") && strings.Contains(c, "…") {
			cut = true
		}
	}
	if !cut {
		t.Fatal("a note too long for the band ends with an ellipsis")
	}
	_, _, _, h3 := NewTextFlow("", 0, 0, 100, 100).pageRect(0)
	if h3 != 100 {
		t.Fatal("no notes, no band")
	}
}

func TestTableOfContents(t *testing.T) {
	f, st := editFlow(t, 0,
		Chars{Text: "Intro", Format: Format{Scale: 2, FontWeight: 700}}, Chars{Text: "\nbody text\n"},
		Chars{Text: "Part two", Format: Format{Scale: 1.6, FontWeight: 700}}, Chars{Text: "\nmore\n"})
	f.SetCursors([]Cursor{At(0)})
	if !runCmd(f, st, CmdTOCInsert, "") {
		t.Fatal("insert")
	}
	toc, ok := f.items()[0].(Inline)
	if !ok {
		t.Fatalf("%T", f.items()[0])
	}
	obj := toc.Object.(*TOCObject)
	p := st.Painter.(*fakePainter)
	p.calls = nil
	f.Draw()
	var got []string
	for _, e := range obj.Entries() {
		got = append(got, e.Text)
	}
	if !reflect.DeepEqual(got, []string{"Intro", "Part two"}) {
		t.Fatalf("%v", got)
	}
	for _, want := range []string{`fillText "Contents"`, `fillText "Intro"`, `fillText "Part two"`, `fillText "1" `} {
		if !p.has(want) {
			t.Errorf("TOC lacks %s:\n%v", want, p.calls)
		}
	}
	// It follows the text: a new heading appears in the table.
	_, h1 := obj.Measure(p, 300, 500)
	f.Items = append(f.Items, Chars{Text: "Third", Format: Format{Scale: 2, FontWeight: 700}})
	if _, h2 := obj.Measure(p, 300, 500); h2 <= h1 {
		t.Fatalf("the table grows with the headings: %v -> %v", h1, h2)
	}
	empty := &TOCObject{Title: "Mine"}
	if _, h := empty.Measure(p, 100, 100); h <= 0 || len(empty.Entries()) != 0 {
		t.Fatal("an unbound table is empty")
	}
	p.calls = nil
	empty.DrawAt(p, 0, 0)
	if !p.has(`fillText "Mine"`) || !p.has(`fillText "No headings yet."`) {
		t.Fatal(p.calls)
	}
}

func TestPageOfOffset(t *testing.T) {
	f := NewTextFlow("", 0, 0, 100, 100)
	f.lastLay = Layout{Lines: []Line{{Start: 0, End: 5, Page: 0}, {Start: 6, End: 10, Page: 2}}}
	if f.pageOfOffset(3) != 1 || f.pageOfOffset(8) != 3 || f.pageOfOffset(99) != 0 {
		t.Fatal("pages")
	}
}

func TestAlphabeticalIndex(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "beta alpha gamma Alpha"})
	mark := func(from, to int, term string) bool {
		f.SetCursors([]Cursor{{Anchor: from, Caret: to}})
		return runCmd(f, st, CmdIndexMark, term)
	}
	if !mark(5, 10, "alpha") || !mark(17, 22, "Alpha entry") || !mark(0, 4, "Beta") {
		t.Fatal("mark")
	}
	f.lastLay = Layout{Lines: []Line{{Start: 0, End: 22, Page: 0}}}
	got := f.IndexEntries()
	want := []IndexEntry{{Term: "alpha", Pages: []int{1}}, {Term: "Alpha entry", Pages: []int{1}}, {Term: "Beta", Pages: []int{1}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("alphabetical, ignoring case:\n got %+v\nwant %+v", got, want)
	}
	// With no term typed the selected text is the default.
	f.SetCursors([]Cursor{{Anchor: 11, Caret: 16}})
	st.Prompt = func(_, def string) (string, bool) { return def, true }
	if !st.RunCommand(CmdIndexMark) || f.IndexEntries()[len(f.IndexEntries())-1].Term != "gamma" && f.IndexEntries()[2].Term != "gamma" {
		t.Fatalf("%+v", f.IndexEntries())
	}
	f.SetCursors([]Cursor{At(7)})
	if !runCmd(f, st, CmdIndexUnmark, "x") {
		t.Fatal("unmark")
	}
	for _, e := range f.IndexEntries() {
		if e.Term == "alpha" {
			t.Fatal("the term is gone")
		}
	}
	f.SetCursors([]Cursor{At(3)})
	f.SetCursors([]Cursor{At(5)})
	runCmd(f, st, CmdIndexInsert, "")
	var obj *IndexObject
	for _, it := range f.items() {
		if in, ok := it.(Inline); ok {
			obj, _ = in.Object.(*IndexObject)
		}
	}
	if obj == nil {
		t.Fatal("index inserted")
	}
	p := st.Painter.(*fakePainter)
	p.calls = nil
	f.Draw()
	if !p.has(`fillText "Index"`) || !p.has(`fillText "Beta"`) {
		t.Fatalf("index drawn:\n%v", p.calls)
	}
	if mark(3, 3, "x") {
		t.Fatal("nothing selected")
	}
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 2}})
	if runCmd(f, st, CmdIndexMark, "  ") {
		t.Fatal("blank term")
	}
	e := &IndexObject{}
	if _, h := e.Measure(p, 100, 100); h <= 0 {
		t.Fatal("measure")
	}
	p.calls = nil
	e.DrawAt(p, 0, 0)
	if !p.has(`fillText "No index entries yet."`) {
		t.Fatal(p.calls)
	}
}
