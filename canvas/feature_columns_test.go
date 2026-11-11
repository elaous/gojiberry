package canvas

import "testing"

func columnsFlow(text string) (*TextFlow, *Stage) {
	f := NewTextFlow(text, 0, 0, 400, 400)
	p := &fakePainter{}
	f.SetPainter(p)
	st := NewStage(p, 400, 400, f)
	st.Features = NewRegistry(Columns{})
	st.SetMode(ModeEdit)
	f.Layout()
	f.MouseDown(5, 5) // the caret goes into the text
	return f, st
}

func TestColumnCommandsStartARegionAtTheParagraph(t *testing.T) {
	f, st := columnsFlow("first paragraph\nsecond paragraph here\nthird")
	f.SetCursors([]Cursor{At(20)}) // in the second paragraph (starts at 16)
	if !st.RunCommand(CmdColumns2) {
		t.Fatal("the command did not run")
	}
	if got := f.ColumnsAt(16); got.Count != 2 || got.Gap != DefaultColumnGap {
		t.Errorf("columns at the paragraph: %+v", got)
	}
	if got := f.ColumnsAt(0); got.Count != 0 {
		t.Errorf("the text above keeps its own layout: %+v", got)
	}
	lay := f.Layout()
	if len(lay.Bands) < 2 {
		t.Fatalf("bands %+v", lay.Bands)
	}
	if n := len(lay.Bands[len(lay.Bands)-1].Xs); n != 2 {
		t.Errorf("the second region has %d columns", n)
	}
	st.RunCommand(CmdColumns3)
	st.RunCommand(CmdColumns4)
	n := 0
	for _, it := range f.ContentItems() {
		if _, ok := it.(ColumnConfig); ok {
			n++
		}
	}
	if n != 1 || f.ColumnsAt(16).Count != 4 {
		t.Errorf("%d configs, columns now %+v", n, f.ColumnsAt(16))
	}
	if f.EditText() != "first paragraph\nsecond paragraph here\nthird" {
		t.Errorf("the text changed: %q", f.EditText())
	}
}

func TestWideNarrowAndGap(t *testing.T) {
	f, st := columnsFlow("one paragraph of text")
	st.RunCommand(CmdColumns21)
	lay := f.Layout()
	b := lay.Bands[len(lay.Bands)-1]
	if len(b.Widths) != 2 || b.Widths[0] < 1.9*b.Widths[1] || b.Widths[0] > 2.1*b.Widths[1] {
		t.Errorf("2+1 widths %v", b.Widths)
	}
	st.RunCommand(CmdColumns12)
	b = f.Layout().Bands[len(f.Layout().Bands)-1]
	if b.Widths[1] < 1.9*b.Widths[0] {
		t.Errorf("1+2 widths %v", b.Widths)
	}
	st.RunCommand(CmdGapWide)
	if g := f.ColumnsAt(0).Gap; g != 48 || len(f.ColumnsAt(0).Widths) != 2 {
		t.Errorf("a wider gap keeps the widths: %+v", f.ColumnsAt(0))
	}
	st.RunCommand(CmdColumns1)
	if c := f.ColumnsAt(0); c.Count != 1 || len(c.Widths) != 0 {
		t.Errorf("one column: %+v", c)
	}
}

func TestUndoRemovesTheColumns(t *testing.T) {
	f, st := columnsFlow("some text")
	st.RunCommand(CmdColumns3)
	if f.ColumnsAt(0).Count != 3 {
		t.Fatal("not set")
	}
	f.Undo()
	if f.ColumnsAt(0).Count != 0 {
		t.Error("undo should take the columns away")
	}
}

func TestResetColumnWidthsMakesThemEqualAgain(t *testing.T) {
	f, st := columnsFlow("first paragraph\nsecond paragraph here")
	f.SetCursors([]Cursor{At(20)})
	st.RunCommand(CmdColumns21) // wide then narrow
	st.RunCommand(CmdGapWide)
	if got := f.ColumnsAt(16); len(got.Widths) != 2 || got.Gap != 48 {
		t.Fatalf("before: %+v", got)
	}
	if !st.RunCommand(CmdColumnsReset) {
		t.Fatal("the command did not run")
	}
	if got := f.ColumnsAt(16); len(got.Widths) != 0 || got.Count != 2 || got.Gap != 48 {
		t.Fatalf("after: equal columns, same count and space: %+v", got)
	}
	st.RunCommand(CmdColumns1)
	st.RunCommand(CmdColumnsReset)
	if got := f.ColumnsAt(16); got.Count > 1 {
		t.Fatalf("one column stays one: %+v", got)
	}
}
