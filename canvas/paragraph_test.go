package canvas

import (
	"strings"
	"testing"
)

// Test flow: 160px wide at x=10; 8px characters, 22px lines.
func paraFlow(items ...Item) (*TextFlow, *Stage) {
	f, p := selFlow(items...)
	return f, NewStage(p, 400, 400, f)
}

func lineOf2(t *testing.T, f *TextFlow, i int) Line {
	t.Helper()
	lines := f.Layout().Lines
	if i >= len(lines) {
		t.Fatalf("only %d lines", len(lines))
	}
	return lines[i]
}

func TestParagraphIndentsAlignmentAndSpacing(t *testing.T) {
	long := "aaaa bbbb cccc dddd eeee ffff" // 29 chars
	f, _ := paraFlow(ParaFormat{IndentLeft: 16, IndentRight: 16, FirstLine: 8}, Chars{Text: long})
	l0, l1 := lineOf2(t, f, 0), lineOf2(t, f, 1)
	if l0.X != 10+16+8 || l1.X != 10+16 {
		t.Errorf("indents: first line at %g, next at %g", l0.X, l1.X)
	}
	if l0.W > 160-16-16-8 || l1.W > 160-32 {
		t.Errorf("lines must fit between the indents: %g, %g", l0.W, l1.W)
	}

	f, _ = paraFlow(ParaFormat{Align: AlignCenter}, Chars{Text: "abcd"}, Chars{Text: "\n"}, ParaFormat{Align: AlignRight}, Chars{Text: "wxyz"})
	if x := lineOf2(t, f, 0).X; x != 10+(160-32)/2 {
		t.Errorf("centred line at %g", x)
	}
	if x := lineOf2(t, f, 1).X; x != 10+160-32 {
		t.Errorf("right-aligned line at %g", x)
	}

	f, _ = paraFlow(ParaFormat{LineSpacing: 2}, Chars{Text: "ab\ncd"})
	if h := lineOf2(t, f, 0).H; h != 44 {
		t.Errorf("double spacing: line height %g", h)
	}

	f, _ = paraFlow(Chars{Text: "one\n"}, ParaFormat{SpaceBefore: 10, SpaceAfter: 6}, Chars{Text: "two\nthree"})
	y0, y1, y2 := lineOf2(t, f, 0).Y, lineOf2(t, f, 1).Y, lineOf2(t, f, 2).Y
	if y1-y0 != 22+10 || y2-y1 != 22+6 {
		t.Errorf("space before/after: gaps %g, %g", y1-y0, y2-y1)
	}

	// A narrow column ignores indents that would leave no room.
	f, _ = paraFlow(ParaFormat{IndentLeft: 140}, Chars{Text: "abcd"})
	if x := lineOf2(t, f, 0).X; x != 10 {
		t.Errorf("an indent that leaves no room is ignored: x=%g", x)
	}
}

func TestListMarkersAndNumbering(t *testing.T) {
	num := ListStyle{Kind: ListOrdered}
	f, _ := paraFlow(
		ParaFormat{List: num}, Chars{Text: "one\n"},
		ParaFormat{List: num}, Chars{Text: "two\n"},
		ParaFormat{List: ListStyle{Kind: ListOrdered, Level: 1}}, Chars{Text: "nested\n"},
		ParaFormat{List: ListStyle{Kind: ListOrdered, Level: 1}}, Chars{Text: "nested 2\n"},
		ParaFormat{List: num}, Chars{Text: "three\n"},
		Chars{Text: "plain\n"},
		ParaFormat{List: ListStyle{Kind: ListOrdered, Start: 5, Number: NumLowerRoman, Marker: "({n})"}}, Chars{Text: "again\n"},
		ParaFormat{List: ListStyle{Kind: ListBullet}}, Chars{Text: "bullet\n"},
		ParaFormat{List: ListStyle{Kind: ListBullet, Level: 1}}, Chars{Text: "deeper"},
	)
	var got []string
	for _, ln := range f.Layout().Lines {
		if ln.Marker != nil {
			got = append(got, ln.Marker.Text)
		} else {
			got = append(got, "-")
		}
	}
	want := []string{"1.", "2.", "1.", "2.", "3.", "-", "(v)", "•", "◦"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("markers = %v, want %v", got, want)
	}
	l0 := lineOf2(t, f, 0)
	if l0.X != 10+ListIndent || l0.Marker.X != l0.X-6-l0.Marker.W {
		t.Errorf("text at %g, marker at %g (w %g)", l0.X, l0.Marker.X, l0.Marker.W)
	}
	if x := lineOf2(t, f, 2).X; x != 10+2*ListIndent {
		t.Errorf("nested item at %g", x)
	}
	// Wrapped lines of an item have no marker and share its indent.
	f, _ = paraFlow(ParaFormat{List: ListStyle{Kind: ListBullet}}, Chars{Text: "aaaa bbbb cccc dddd eeee ffff"})
	if lineOf2(t, f, 0).Marker == nil || lineOf2(t, f, 1).Marker != nil || lineOf2(t, f, 1).X != 10+ListIndent {
		t.Errorf("continuation line: %+v", lineOf2(t, f, 1))
	}
}

func TestMarkerOnEmptyItemAndCheckToggle(t *testing.T) {
	f, st := paraFlow(ParaFormat{List: ListStyle{Kind: ListCheck}}, Chars{Text: "todo\n"}, ParaFormat{List: ListStyle{Kind: ListCheck, Checked: true}})
	lines := f.Layout().Lines
	if len(lines) != 2 || lines[0].Marker.Text != "☐" || lines[1].Marker == nil || lines[1].Marker.Text != "☑" {
		t.Fatalf("an empty last item still shows its marker: %+v", lines)
	}
	f.SetMode(ModeEdit)
	m := lines[0].Marker
	st.MouseDown(m.X+2, lines[0].Y+5)
	st.MouseUp(0, 0)
	if got := f.Layout().Lines[0].Marker.Text; got != "☑" {
		t.Errorf("clicking the box ticks it: %q", got)
	}
	if f.Editing() {
		t.Error("ticking a box does not start editing")
	}
	st.MouseDown(m.X+2, lines[0].Y+5)
	st.MouseUp(0, 0)
	if got := f.Layout().Lines[0].Marker.Text; got != "☐" {
		t.Errorf("and again unticks: %q", got)
	}
	// In view mode the box is just drawn.
	f.SetMode(ModeView)
	st.MouseDown(m.X+2, lines[0].Y+5)
	if f.Layout().Lines[0].Marker.Text != "☐" {
		t.Error("view mode cannot tick")
	}
}

func TestQuoteBarAndDrawing(t *testing.T) {
	f, st := paraFlow(ParaFormat{QuoteBar: "#abc", IndentLeft: 16, List: ListStyle{Kind: ListBullet}}, Chars{Text: "quoted"})
	ln := lineOf2(t, f, 0)
	if ln.QuoteBar != "#abc" || ln.BarX != 10+16-8 {
		t.Errorf("quote bar: %+v", ln)
	}
	p := st.Painter.(*fakePainter)
	st.Draw()
	if !p.has(`fillText "•"`) || !p.has("fillStyle #abc") || !p.has("fillRect 18 ") {
		t.Errorf("marker and bar are drawn: %v", p.calls)
	}
}

func TestApplyParagraphAndEditing(t *testing.T) {
	f, st := editFlow(t, 2, Chars{Text: "ab\ncd\nef"})
	if !st.RunCommand(CmdBulletList) {
		t.Fatal("bullets not applied")
	}
	if got := f.ParagraphAt(0); got.List.Kind != ListBullet {
		t.Fatalf("paragraph 1 = %+v", got)
	}
	if f.ParagraphAt(3).List.Kind != ListNone {
		t.Error("only the caret's paragraph changed")
	}
	if items := f.ContentItems(); len(items) != 2 || items[0] != Item(ParaFormat{List: ListStyle{Kind: ListBullet}}) {
		t.Fatalf("items = %+v", items)
	}
	// Typing at the start of the paragraph stays inside it.
	f.SetCursors([]Cursor{At(0)})
	st.InsertText("X")
	if flowText(f) != "Xab\ncd\nef" || f.ParagraphAt(0).List.Kind != ListBullet {
		t.Fatalf("typing at the start: %q %+v", flowText(f), f.ContentItems())
	}
	// Enter continues the list; Enter on the empty item ends it.
	f.SetCursors([]Cursor{At(3)})
	st.HandleKey("Enter", false)
	if f.ParagraphAt(4).List.Kind != ListBullet {
		t.Fatalf("Enter continues the list: %+v", f.ContentItems())
	}
	st.HandleKey("Enter", false)
	if f.ParagraphAt(4).List.Kind != ListNone || flowText(f) != "Xab\n\ncd\nef" {
		t.Fatalf("Enter on an empty item ends the list: %q %+v", flowText(f), f.ContentItems())
	}
	// Toggling again removes the (now empty) format.
	f.SetCursors([]Cursor{At(0)})
	st.RunCommand(CmdBulletList)
	for _, it := range f.ContentItems() {
		if _, ok := it.(ParaFormat); ok {
			t.Errorf("an empty format is removed: %+v", f.ContentItems())
		}
	}
}

func TestParagraphFormatTravelsWithItsText(t *testing.T) {
	f, st := editFlow(t, 0, ParaFormat{Align: AlignCenter}, Chars{Text: "one\n"}, ParaFormat{List: ListStyle{Kind: ListBullet}}, Chars{Text: "two\nthree"})
	// Deleting the paragraph deletes its format.
	f.SetCursors([]Cursor{{Anchor: 4, Caret: 8}})
	st.HandleKey("Delete", false)
	if flowText(f) != "one\nthree" || f.ParagraphAt(4).List.Kind != ListNone {
		t.Fatalf("deleted: %q %+v", flowText(f), f.ContentItems())
	}
	if f.ParagraphAt(0).Align != AlignCenter {
		t.Error("the first paragraph keeps its format")
	}
	// Moving a line moves its format with it.
	f, st = editFlow(t, 0, Chars{Text: "a\n"}, ParaFormat{List: ListStyle{Kind: ListCheck}}, Chars{Text: "b"})
	f.SetCursors([]Cursor{At(3)})
	st.HandleChord("Alt+ArrowUp")
	if flowText(f) != "b\na" || f.ParagraphAt(0).List.Kind != ListCheck || f.ParagraphAt(2).List.Kind != ListNone {
		t.Fatalf("move line: %q %+v", flowText(f), f.ContentItems())
	}
	// A copy of a line carries it too.
	st.HandleChord("Alt+Shift+ArrowDown")
	if flowText(f) != "b\nb\na" || f.ParagraphAt(0).List.Kind != ListCheck || f.ParagraphAt(2).List.Kind != ListCheck {
		t.Fatalf("copy line: %q %+v", flowText(f), f.ContentItems())
	}
	// Slice includes formats, so copies and line commands keep them.
	var n int
	for _, it := range f.Slice(0, 3) {
		if _, ok := it.(ParaFormat); ok {
			n++
		}
	}
	if n != 2 {
		t.Errorf("Slice carries paragraph formats: %d", n)
	}
	// Delete does not eat a format sitting at the caret (it is not a break).
	f, st = editFlow(t, 0, ParaFormat{Align: AlignRight}, Chars{Text: "xy"})
	st.HandleKey("Delete", false)
	if flowText(f) != "y" {
		t.Errorf("Delete deletes the character: %q", flowText(f))
	}
}

func TestParagraphCommands(t *testing.T) {
	f, st := editFlow(t, 1, Chars{Text: "alpha\nbeta"})
	for _, tc := range []struct {
		chord string
		check func(ParaFormat) bool
	}{
		{"Mod+E", func(p ParaFormat) bool { return p.Align == AlignCenter }},
		{"Mod+R", func(p ParaFormat) bool { return p.Align == AlignRight }},
		{"Mod+L", func(p ParaFormat) bool { return p.Align == AlignLeft }},
		{"Mod+]", func(p ParaFormat) bool { return p.IndentLeft == ListIndent }},
		{"Mod+[", func(p ParaFormat) bool { return p.IndentLeft == 0 }},
		{"Mod+Shift+7", func(p ParaFormat) bool { return p.List.Kind == ListOrdered }},
		{"Mod+Shift+9", func(p ParaFormat) bool { return p.List.Kind == ListCheck }},
		{"Mod+Shift+8", func(p ParaFormat) bool { return p.List.Kind == ListBullet }},
	} {
		if !st.HandleChord(tc.chord) || !tc.check(f.ParagraphAt(0)) {
			t.Errorf("%s: %+v", tc.chord, f.ParagraphAt(0))
		}
	}
	if f.ParagraphAt(6) != (ParaFormat{}) {
		t.Error("the other paragraph is untouched")
	}
	// Indent and outdent on a list item change its level; Tab and Shift+Tab too.
	st.HandleChord("Mod+]")
	if f.ParagraphAt(0).List.Level != 1 {
		t.Errorf("indent nests a list item: %+v", f.ParagraphAt(0))
	}
	st.HandleKey("Tab", false)
	if f.ParagraphAt(0).List.Level != 2 {
		t.Errorf("Tab nests: %+v", f.ParagraphAt(0))
	}
	st.HandleKey("Tab", true)
	st.HandleChord("Mod+[")
	st.HandleChord("Mod+[")
	st.HandleChord("Mod+[")
	if f.ParagraphAt(0).List.Level != 0 {
		t.Errorf("outdent stops at 0: %+v", f.ParagraphAt(0))
	}
	f.SetCursors([]Cursor{At(8)})
	if st.HandleKey("Tab", false) {
		t.Error("Tab outside a list is not ours")
	}

	// Toggles and cycles.
	f.SetCursors([]Cursor{At(1)})
	f.ApplyParagraph(func(p *ParaFormat) { *p = ParaFormat{} })
	for _, tc := range []struct {
		name  Command
		check func(ParaFormat) bool
	}{
		{CmdFirstLineIndent, func(p ParaFormat) bool { return p.FirstLine == ListIndent }},
		{CmdSpaceBefore, func(p ParaFormat) bool { return p.SpaceBefore == ParagraphGap }},
		{CmdSpaceAfter, func(p ParaFormat) bool { return p.SpaceAfter == ParagraphGap }},
		{CmdLineSpacing, func(p ParaFormat) bool { return p.LineSpacing == 1.15 }},
		{CmdQuote, func(p ParaFormat) bool { return p.QuoteBar != "" && p.IndentLeft == 16 }},
		{CmdDropCap, func(p ParaFormat) bool { return p.DropCap == 3 }},
	} {
		if !st.RunCommand(tc.name) || !tc.check(f.ParagraphAt(0)) {
			t.Errorf("%s: %+v", tc.name, f.ParagraphAt(0))
		}
		f.ApplyParagraph(func(p *ParaFormat) { *p = ParaFormat{} })
	}
	f.ApplyParagraph(func(p *ParaFormat) { *p = ParaFormat{} })
	st.RunCommand(CmdLineSpacing)
	st.RunCommand(CmdLineSpacing)
	st.RunCommand(CmdLineSpacing)
	st.RunCommand(CmdLineSpacing)
	if f.ParagraphAt(0).LineSpacing != 0 {
		t.Errorf("line spacing cycles back to single: %+v", f.ParagraphAt(0))
	}
	st.RunCommand(CmdCycleBullet)
	if got := f.ParagraphAt(0).List; got.Kind != ListBullet || got.Marker != "◦" {
		t.Errorf("cycle bullet: %+v", got)
	}
	for range BulletChoices {
		st.RunCommand(CmdCycleBullet)
	}
	if f.ParagraphAt(0).List.Marker != "◦" {
		t.Error("the bullet cycle wraps")
	}
	st.RunCommand(CmdCycleNumberFormat)
	if got := f.ParagraphAt(0).List; got.Kind != ListOrdered || got.Marker != "{n}." || got.Number != NumArabic {
		t.Errorf("cycle number format starts at 1.: %+v", got)
	}
	st.RunCommand(CmdCycleNumberFormat)
	if f.ParagraphAt(0).List.Marker != "{n})" {
		t.Errorf("then 1): %+v", f.ParagraphAt(0).List)
	}

	// A floating text has no paragraphs.
	fst, _ := stageWith(t, "text")
	if fst.HandleChord("Mod+E") || fst.HandleChord("Mod+Shift+8") {
		t.Error("a FloatingText declines paragraph commands")
	}
}

func TestParagraphsAcrossSelectionAndMulti(t *testing.T) {
	f, st := editFlow(t, 0, Chars{Text: "aa\nbb\ncc\ndd"})
	f.SetCursors([]Cursor{{Anchor: 1, Caret: 7}}) // aa..cc
	st.HandleChord("Mod+E")
	if f.ParagraphAt(0).Align != AlignCenter || f.ParagraphAt(3).Align != AlignCenter || f.ParagraphAt(6).Align != AlignCenter || f.ParagraphAt(9).Align != AlignLeft {
		t.Errorf("a selection formats every paragraph it touches: %+v", f.ContentItems())
	}
	f.SetCursors([]Cursor{At(0), At(9)})
	st.HandleChord("Mod+Shift+8")
	if f.ParagraphAt(0).List.Kind != ListBullet || f.ParagraphAt(9).List.Kind != ListBullet || f.ParagraphAt(3).List.Kind != ListNone {
		t.Errorf("each cursor's paragraph: %+v", f.ContentItems())
	}
	// A selection ending at the start of a paragraph leaves it out.
	f.SetCursors([]Cursor{{Anchor: 3, Caret: 6}})
	st.HandleChord("Mod+R")
	if f.ParagraphAt(3).Align != AlignRight || f.ParagraphAt(6).Align == AlignRight {
		t.Errorf("selection ending at a paragraph start: %+v", f.ContentItems())
	}
}

func TestDropCap(t *testing.T) {
	long := "Hello world this is a rather long paragraph that wraps over many lines of text to show the cap"
	f, st := paraFlow(ParaFormat{DropCap: 3}, Chars{Text: long})
	lines := f.Layout().Lines
	if len(lines) < 5 {
		t.Fatalf("only %d lines", len(lines))
	}
	first := lines[0]
	if first.DropCap == nil || first.DropCap.Text != "H" || first.Start != 1 || !strings.HasPrefix(first.Text, "ello") {
		t.Fatalf("the first letter moves into the cap: %+v", first)
	}
	if first.X != 10+14 || lines[1].X != 10+14 || lines[2].X != 10+14 || lines[3].X != 10 {
		t.Errorf("three lines are indented beside the cap, then back: %g %g %g %g", first.X, lines[1].X, lines[2].X, lines[3].X)
	}
	if lines[1].DropCap != nil {
		t.Error("only the first line has the cap")
	}
	if want := first.Y + first.H*2 + (first.Baseline - first.Y); first.DropCap.Baseline != want {
		t.Errorf("the cap's baseline is the third line's: %g, want %g", first.DropCap.Baseline, want)
	}
	if !strings.Contains(strings.Join(func() []string {
		p := st.Painter.(*fakePainter)
		st.Draw()
		return p.calls
	}(), "\n"), `fillText "H"`) {
		t.Error("the cap is drawn")
	}
	// Caret placement: before the cap and after it both work.
	if x, _, _, ok := f.CaretRect(0); !ok || x != first.X {
		t.Errorf("caret before the cap sits at the text start: %g", x)
	}

	// A short paragraph leaves room for the whole cap.
	f, _ = paraFlow(ParaFormat{DropCap: 3}, Chars{Text: "Hi\n"}, ParaFormat{}, Chars{Text: "next"})
	lines = f.Layout().Lines
	if lines[1].Y < lines[0].Y+3*22 {
		t.Errorf("the next paragraph clears the cap: %g vs %g", lines[1].Y, lines[0].Y+3*22)
	}
	// One-letter paragraphs and a leading space get no cap.
	f, _ = paraFlow(ParaFormat{DropCap: 2}, Chars{Text: "X\n"}, ParaFormat{DropCap: 2}, Chars{Text: " y"})
	lines = f.Layout().Lines
	if lines[0].DropCap == nil || lines[0].Text != "" || lines[1].DropCap != nil {
		t.Errorf("edge cases: %+v / %+v", lines[0], lines[len(lines)-1])
	}
	// Editing keeps working around the cap.
	f, st = editFlow(t, 0, ParaFormat{DropCap: 3}, Chars{Text: "Hello"})
	f.SetCursors([]Cursor{At(0)})
	st.InsertText(">")
	if flowText(f) != ">Hello" {
		t.Errorf("typing before the cap letter: %q", flowText(f))
	}
}
