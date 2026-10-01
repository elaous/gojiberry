package canvas

import (
	"reflect"
	"testing"
)

func TestOutlineFromStylesAndMarkdown(t *testing.T) {
	f, _ := selFlow(
		Chars{Text: "Intro text\n"},
		ParaFormat{Style: "Heading 2"}, Chars{Text: "By style\n"},
		Chars{Text: "Big", Format: Format{Scale: 2, FontWeight: 700}}, Chars{Text: " and bold\n", Format: Format{Scale: 2, FontWeight: 700}},
		Chars{Text: "Medium\n", Format: Format{Scale: 1.6, FontWeight: 700}},
		Chars{Text: "Bold but small\n", Format: Format{FontWeight: 700}},
		Chars{Text: "Big but not bold\n", Format: Format{Scale: 2}},
		ParaFormat{Style: "Title"}, Chars{Text: "A title\n"},
		ParaFormat{Style: "Heading 3"}, Chars{Text: "\n"}, // an empty heading is skipped
		Chars{Text: "small", Format: Format{Scale: 1.3, FontWeight: 700}},
	)
	f.Styles = DefaultStyles()
	var got []string
	for _, e := range f.Outline() {
		got = append(got, string(rune('0'+e.Level))+":"+e.Text)
	}
	want := []string{"2:By style", "1:Big and bold", "2:Medium", "1:A title", "3:small"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	o := f.Outline()
	if o[0].Style != "Heading 2" || o[1].Style != "" || o[0].Start != 11 {
		t.Fatalf("%+v", o[:2])
	}
	if i := f.OutlineIndexAt(0); i != -1 {
		t.Fatalf("before the first heading: %d", i)
	}
	if i := f.OutlineIndexAt(o[1].Start + 2); i != 1 {
		t.Fatalf("inside the second: %d", i)
	}
	if i := f.OutlineIndexAt(10000); i != len(o)-1 {
		t.Fatalf("after the last: %d", i)
	}
}

func TestBookmarkCommandsAndLinks(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "alpha beta gamma"})
	f.SetCursors([]Cursor{{Anchor: 6, Caret: 10}})
	if !runCmd(f, st, CmdBookmarkAdd, "middle") {
		t.Fatal("add")
	}
	if got := f.Bookmarks(); !reflect.DeepEqual(got, []Bookmark{{Name: "middle", Start: 6, End: 10}}) {
		t.Fatalf("%+v", got)
	}
	// With nothing selected the character at the caret is named; a name in
	// use moves.
	f.SetCursors([]Cursor{At(0)})
	if !runCmd(f, st, CmdBookmarkAdd, "middle") {
		t.Fatal("move")
	}
	if got := f.Bookmarks(); !reflect.DeepEqual(got, []Bookmark{{Name: "middle", Start: 0, End: 1}}) {
		t.Fatalf("%+v", got)
	}
	f.SetCursors([]Cursor{At(16)}) // at the end: the character before
	runCmd(f, st, CmdBookmarkAdd, "end")
	if got := f.Bookmarks(); len(got) != 2 || got[1].Name != "end" || got[1].Start != 15 {
		t.Fatalf("%+v", got)
	}
	// Ordering follows the text; the bookmark follows the text through edits.
	f.SetCursors([]Cursor{At(0)})
	f.InsertText(">>")
	if got := f.Bookmarks(); got[0].Start != 0 || got[0].End != 3 || got[1].Start != 17 {
		t.Fatalf("the bookmark follows its text (and grows with text typed at its start): %+v", got)
	}
	// Go to.
	f.SetCursors([]Cursor{At(5)})
	if !runCmd(f, st, CmdBookmarkGoto, "end") || f.Cursors()[0] != At(17) {
		t.Fatalf("goto: %v", f.Cursors())
	}
	if runCmd(f, st, CmdBookmarkGoto, "nope") || runCmd(f, st, CmdBookmarkGoto, "") {
		t.Fatal("no such bookmark")
	}
	// Remove at the caret.
	f.SetCursors([]Cursor{At(17)})
	if !runCmd(f, st, CmdBookmarkRemove, "x") || len(f.Bookmarks()) != 1 {
		t.Fatalf("%+v", f.Bookmarks())
	}
	f.SetCursors([]Cursor{At(8)})
	if runCmd(f, st, CmdBookmarkRemove, "x") {
		t.Fatal("none there")
	}
	f.SetCursors([]Cursor{At(1)})
	st.Prompt = nil
	if st.RunCommand(CmdBookmarkAdd) || st.RunCommand(CmdBookmarkGoto) {
		t.Fatal("needs a prompt")
	}
	empty, st2 := editFlow(t, 0, Chars{Text: ""})
	if runCmd(empty, st2, CmdBookmarkAdd, "x") {
		t.Fatal("nothing to name")
	}
}

func TestLinkToBookmarkAndReveal(t *testing.T) {
	long := ""
	for i := 0; i < 40; i++ {
		long += "line\n"
	}
	f, p := selFlow(
		Chars{Text: "go to "}, Chars{Text: "target", Format: Format{Link: "#there"}}, Chars{Text: "\n" + long},
		Chars{Text: "here", Format: Format{Bookmark: "there"}},
	)
	f.X, f.Y, f.Width, f.Height = 0, 0, 200, 1500
	sv := NewScrollView(0, 0, 200, 100)
	sv.Add(f, 200, 1500)
	NewStage(p, 200, 100, sv)
	opened := ""
	f.OnLink = func(u string) { opened = u }
	if sv.MouseDown(8*8, 10) {
		_ = 0
	}
	if opened != "" || sv.ScrollY == 0 {
		t.Fatalf("a #link jumps to its bookmark instead of opening: %q, scroll %v", opened, sv.ScrollY)
	}
	top := sv.ScrollY
	if _, y, _, _ := f.CaretRect(f.Bookmarks()[0].Start); y-scrollMargin != top {
		t.Fatalf("the bookmark is at the top of the view: %v vs %v", y, top)
	}
	// A link to a missing bookmark is opened as a link.
	f.Items[1] = Chars{Text: "target", Format: Format{Link: "#missing"}}
	sv.ScrollTo(0, 0)
	sv.MouseDown(8*8, 10)
	if opened != "#missing" {
		t.Fatalf("opened %q", opened)
	}
	// GotoOffset from outside an event, then RevealPending.
	sv.ScrollTo(0, 0)
	f.GotoOffset(f.Bookmarks()[0].Start)
	if !sv.RevealPending() || sv.RevealPending() {
		t.Fatal("reveal once")
	}
	if _, _, _, ok := f.TakeReveal(); ok {
		t.Fatal("taken")
	}
}

func TestRemoveBookmark(t *testing.T) {
	f, _ := selFlow(Chars{Text: "ab"}, Chars{Text: "cd", Format: Format{Bookmark: "x"}}, Chars{Text: "ef", Format: Format{Bookmark: "y"}})
	if !f.RemoveBookmark("x") || f.RemoveBookmark("x") {
		t.Fatal("removed once")
	}
	if got := f.Bookmarks(); len(got) != 1 || got[0].Name != "y" {
		t.Fatalf("%+v", got)
	}
}
