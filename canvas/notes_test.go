package canvas

import (
	"reflect"
	"strconv"
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
	f.Layout() // the layout settles the room the notes take
	_, _, _, h1 := f.pageRect(0)
	if want := footnotePad + footnoteLineH; h0-h1 != want {
		t.Fatalf("a one-line note takes one line and its padding from the bottom of the page: %v -> %v", h0, h1)
	}
	p := st.Painter.(*fakePainter)
	p.calls = nil
	f.Draw()
	if !p.has(`fillText "1 Bold note text"`) {
		t.Fatalf("the note is drawn with its number, Markdown stripped:\n%v", p.calls)
	}
	if !p.has(`fillText "1" `) {
		t.Fatal("the mark is drawn in the text")
	}
	// A long note takes more room, is not cut, and the cap holds it.
	long := strings.Repeat("long note words ", 40)
	f.Footnotes.Add(Footnote{ID: f.Footnotes.List()[0].ID, Text: long})
	p.calls = nil
	f.Draw()
	f.Layout()
	for _, c := range p.calls {
		if strings.HasPrefix(c, "fillText") && strings.Contains(c, "…") {
			t.Fatalf("a note is never cut with an ellipsis: %s", c)
		}
	}
	_, _, _, h3 := f.pageRect(0)
	if h3 >= h1 {
		t.Fatalf("a longer note takes more room: %v then %v", h1, h3)
	}
	if h3 < 0.2*h0-1 {
		t.Fatalf("the last page gives its notes at most four fifths of itself: %v of %v", h3, h0)
	}
	f.FootnoteBand = 0
	_, _, _, h4 := NewTextFlow("", 0, 0, 100, 100).pageRect(0)
	if h4 != 100 {
		t.Fatal("no notes, no band")
	}
}

// A page's notes sit in a band that shrinks the text above.
func TestFootnotesPushTheTextAndContinueOnTheNextPage(t *testing.T) {
	f := pagedFlow(Chars{Text: "aaaa\nbbbb\ncccc\ndddd\neeee"})
	f.Footnotes = NewFootnoteSet()
	before := linesOnPage(f.Layout(), 0)
	if len(before) != 3 {
		t.Fatalf("three lines fit: %q", before)
	}
	// A mark on line one, with a three-line note.
	f.Items = []Item{Chars{Text: "aa"}, Inline{Object: &FootnoteRef{ID: "x"}}, Chars{Text: "aa\nbbbb\ncccc\ndddd\neeee"}}
	f.Footnotes.Add(Footnote{ID: "x", Text: "one two three four five six seven eight nine ten eleven twelve thirteen fourteen"})
	lay := f.Layout()
	if got := len(linesOnPage(lay, 0)); got >= 3 {
		t.Fatalf("the note pushes text off the page: %d lines still on it", got)
	}
	if len(f.fnLines[0]) == 0 {
		t.Fatal("the page holds the start of its note")
	}
	if f.fnBands[0] <= 0 || f.fnBands[0] > footnoteCapShare*80+footnoteLineH {
		t.Fatalf("band %v", f.fnBands[0])
	}
	// No line is cut.
	for pg, ls := range f.fnLines {
		for _, l := range ls {
			if strings.Contains(l.text, "…") {
				t.Fatalf("page %d: %q", pg, l)
			}
		}
	}
	// All words of a note appear in order across pages.
	var all []string
	for _, ls := range f.fnLines {
		for _, l := range ls {
			all = append(all, l.text)
		}
	}
	joined := strings.Join(all, " ")
	for _, w := range []string{"1", "one", "seven", "fourteen"} {
		if !strings.Contains(joined, w) {
			t.Fatalf("%q is missing from the notes shown: %q", w, joined)
		}
	}
}

func TestFootnotesAreLimitedInSize(t *testing.T) {
	s := NewFootnoteSet()
	n := s.Add(Footnote{Text: strings.Repeat("é", MaxFootnoteRunes+50)})
	if got := len([]rune(n.Text)); got != MaxFootnoteRunes {
		t.Fatalf("kept %d characters, want %d", got, MaxFootnoteRunes)
	}
	exact := s.Add(Footnote{Text: strings.Repeat("a", MaxFootnoteRunes)})
	if len([]rune(exact.Text)) != MaxFootnoteRunes {
		t.Fatal("exactly the limit is kept whole")
	}
	short := s.Add(Footnote{Text: "short"})
	if short.Text != "short" {
		t.Fatal("a short note is untouched")
	}
	// A note at the limit is shown in full across pages.
	f := pagedFlow(Chars{Text: "x"}, Inline{Object: &FootnoteRef{ID: "big"}}, Chars{Text: "y\nbbbb\ncccc\ndddd"})
	f.Footnotes = NewFootnoteSet()
	f.Footnotes.Add(Footnote{ID: "big", Text: strings.Repeat("word ", 400)})
	f.Layout()
	total := 0
	for _, ls := range f.fnLines {
		total += len(ls)
	}
	if total == 0 || len(f.fnLines[0]) < 2 {
		t.Fatalf("lines shown: %v", f.fnLines)
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
	want := []IndexEntry{{Term: "alpha", Pages: []int{1}, Start: 5}, {Term: "Alpha entry", Pages: []int{1}, Start: 17}, {Term: "Beta", Pages: []int{1}, Start: 0}}
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

func tocFlow(t *testing.T) (*TextFlow, *TOCObject, *fakePainter) {
	t.Helper()
	f, st := editFlow(t, 0,
		Chars{Text: "Intro", Format: Format{Scale: 2, FontWeight: 700}}, Chars{Text: "\nbody text\n"},
		Chars{Text: "Part two", Format: Format{Scale: 1.6, FontWeight: 700}}, Chars{Text: "\nmore\n"})
	f.SetCursors([]Cursor{At(0)})
	if !runCmd(f, st, CmdTOCInsert, "") {
		t.Fatal("insert")
	}
	return f, f.items()[0].(Inline).Object.(*TOCObject), st.Painter.(*fakePainter)
}

// A table of contents has leader and depth options.
func TestTableOfContentsStyleAndDepth(t *testing.T) {
	f, obj, p := tocFlow(t)
	f.lastLay = Layout{Lines: []Line{{Start: 0, End: 40, Page: 0}}}
	p.calls = nil
	f.Draw()
	if !p.has(`fillText "." `) {
		t.Errorf("dotted by default:\n%v", p.calls)
	}
	obj.Style = TOCPlain
	p.calls = nil
	f.Draw()
	if p.has(`fillText "." `) || !p.has(`fillText "Intro"`) {
		t.Errorf("plain has no leader:\n%v", p.calls)
	}
	obj.Depth = 1
	if got := obj.Entries(); len(got) != 1 || got[0].Text != "Intro" {
		t.Errorf("depth 1 lists the top level only: %+v", got)
	}
	obj.Depth = 0
	if len(obj.Entries()) != 2 {
		t.Error("depth 0 lists them all")
	}
}

// A click on a table-of-contents row goes to its heading.
func TestClickOnATableOfContentsGoesToTheHeading(t *testing.T) {
	f, obj, _ := tocFlow(t)
	f.Draw()
	var ox, oy float64
	found := false
	for _, ln := range f.Layout().Lines {
		for _, po := range ln.Objects {
			if po.Object == obj {
				ox, oy, found = po.X, po.Y, true
			}
		}
	}
	if !found {
		t.Fatal("the table was not laid out")
	}
	entries := obj.Entries()
	if _, ok := obj.EntryAt(ox+5, oy+5, ox, oy); ok {
		t.Error("the title is not a link")
	}
	for i, e := range entries {
		if off, ok := obj.EntryAt(ox+5, oy+tocLineH*float64(i+1)+5, ox, oy); !ok || off != e.Start {
			t.Errorf("row %d: %d, %v (want %d)", i, off, ok, e.Start)
		}
	}
	if !f.MouseDown(ox+5, oy+tocLineH*2+5) {
		t.Fatal("the click was not taken")
	}
	if got := f.Cursors()[0].Caret; got != entries[1].Start {
		t.Errorf("the caret is at %d, want %d", got, entries[1].Start)
	}
	if _, _, _, ok := f.TakeReveal(); !ok {
		t.Error("the view is asked to scroll to the heading")
	}
}

// However long the notes, the layout settles and every page fits.
func TestFootnoteLayoutAlwaysSettles(t *testing.T) {
	for marks := 1; marks <= 6; marks++ {
		for _, words := range []int{1, 8, 40, 200} {
			items := []Item{}
			f := pagedFlow()
			f.Footnotes = NewFootnoteSet()
			for i := range marks {
				id := "n" + strconv.Itoa(i)
				items = append(items, Chars{Text: "line text\n"}, Inline{Object: &FootnoteRef{ID: id}})
				f.Footnotes.Add(Footnote{ID: id, Text: strings.Repeat("w ", words)})
			}
			f.Items = items
			lay := f.Layout()
			marked := map[int]bool{}
			for _, ln := range lay.Lines {
				if len(ln.Objects) > 0 {
					marked[ln.Page] = true
				}
			}
			for pg := range marked {
				if pg < len(f.fnLines) && len(f.fnLines[pg]) == 0 {
					t.Errorf("%d marks, %d words: page %d has a mark and shows no note", marks, words, pg)
				}
			}
		}
	}
}

func TestFootnoteCommandsUseTheNoteEditorWhenThereIsOne(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "text with a note"})
	f.SetCursors([]Cursor{At(6)})
	var asked []string
	var finish func(string, bool)
	st.EditNote = func(title, initial string, max int, done func(string, bool)) {
		asked = append(asked, title+"|"+initial+"|"+strconv.Itoa(max))
		finish = done
	}
	if !st.RunCommand(CmdFootnoteAdd) || len(asked) != 1 || asked[0] != "Footnote||"+strconv.Itoa(MaxFootnoteRunes) {
		t.Fatalf("asked %v", asked)
	}
	if f.Footnotes.Len() != 0 {
		t.Fatal("nothing is added until the dialog is answered")
	}
	finish("a note", true)
	if f.Footnotes.Len() != 1 || len(f.FootnoteList()) != 1 || f.FootnoteList()[0].Text != "a note" {
		t.Fatalf("after answering: %+v", f.FootnoteList())
	}
	// Cancelled: nothing changes.
	f.SetCursors([]Cursor{At(2)})
	st.RunCommand(CmdFootnoteAdd)
	finish("", false)
	if f.Footnotes.Len() != 1 {
		t.Fatal("cancel adds nothing")
	}
	// Edit starts from the note's own text.
	f.SetCursors([]Cursor{At(f.FootnoteList()[0].Offset + 1)})
	st.RunCommand(CmdFootnoteEdit)
	if len(asked) < 3 || !strings.Contains(asked[len(asked)-1], "|a note|") {
		t.Fatalf("edit asked %v", asked)
	}
	finish("changed", true)
	if f.FootnoteList()[0].Text != "changed" {
		t.Fatal("edit answered")
	}
}

// A click on a row of the index goes to the first mark of the term; the title goes nowhere.
func TestClickOnAnIndexRowGoesToTheFirstMark(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "alpha beta alpha gamma\n"})
	f.SetCursors([]Cursor{{Anchor: 11, Caret: 16}}) // the second "alpha"
	if !runCmd(f, st, CmdIndexMark, "alpha") {
		t.Fatal("mark")
	}
	f.SetCursors([]Cursor{{Anchor: 0, Caret: 5}}) // the first
	runCmd(f, st, CmdIndexMark, "alpha")
	f.SetCursors([]Cursor{{Anchor: 17, Caret: 22}}) // gamma
	runCmd(f, st, CmdIndexMark, "gamma")
	f.SetCursors([]Cursor{At(f.Layout().End)})
	if !st.RunCommand(CmdIndexInsert) {
		t.Fatal("insert the index")
	}
	var obj *IndexObject
	var ox, oy float64
	for _, ln := range f.Layout().Lines {
		for _, po := range ln.Objects {
			if o, ok := po.Object.(*IndexObject); ok {
				obj, ox, oy = o, po.X, po.Y
			}
		}
	}
	if obj == nil {
		t.Fatal("the index was not laid out")
	}
	es := obj.Entries()
	if len(es) != 2 || es[0].Term != "alpha" || es[0].Start != 0 || es[1].Term != "gamma" || es[1].Start != 17 {
		t.Fatalf("entries %+v", es)
	}
	if _, ok := obj.EntryAt(ox+5, oy+5, ox, oy); ok {
		t.Error("the title is not a link")
	}
	if !f.MouseDown(ox+5, oy+tocLineH*2+5) {
		t.Fatal("the click was not taken")
	}
	if got := f.Cursors()[0].Caret; got != 17 {
		t.Errorf("the second row goes to gamma: caret %d", got)
	}
}

// Pressing the text of a note on the page asks the host to edit it.
func TestPressingANoteOnThePageEditsIt(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "text with a note"})
	f.SetCursors([]Cursor{At(6)})
	runCmd(f, st, CmdFootnoteAdd, "the note")
	f.Draw()
	var got []string
	f.OnFootnote = func(id string) { got = append(got, id) }
	px, py, pw, ph := f.physRect(0)
	y := py + ph + 4 + 4 + footnoteLineH/2
	if !f.MouseDown(px+5, y) || len(got) != 1 || got[0] != f.FootnoteList()[0].ID {
		t.Fatalf("pressed the note: %v", got)
	}
	if f.MouseDown(px+pw+50, y); len(got) != 1 {
		t.Fatal("outside the page is not the note")
	}
	f.OnFootnote = nil
	f.MouseDown(px+5, y) // nothing is called, the press is a plain one
}
