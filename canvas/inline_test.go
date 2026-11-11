package canvas

import (
	"reflect"
	"testing"
)

type fakeObj struct {
	w, h         float64
	gotMaxW      float64
	gotMaxH      float64
	measured     int
	drawX, drawY float64
	drawn        int
}

func (o *fakeObj) Measure(_ Painter, maxW, maxH float64) (float64, float64) {
	o.measured++
	o.gotMaxW, o.gotMaxH = maxW, maxH
	s := min(1, maxW/o.w, maxH/o.h)
	return o.w * s, o.h * s
}

func (o *fakeObj) DrawAt(_ Painter, x, y float64) { o.drawX, o.drawY, o.drawn = x, y, o.drawn+1 }

func TestDecorationsAndScriptsInRuns(t *testing.T) {
	f, p := newFlow("", 400, 200)
	f.Items = []Item{
		Chars{Text: "a"},
		Chars{Text: "U", Format: Format{Underline: true}},
		Chars{Text: "S", Format: Format{Strike: true}},
		Chars{Text: "H", Format: Format{Background: "#ff0"}},
		Chars{Text: "2", Format: Format{Superscript: true}},
		Chars{Text: "3", Format: Format{Subscript: true}},
	}
	ln := f.Layout().Lines[0]
	if len(ln.Runs) != 6 {
		t.Fatalf("runs = %+v", ln.Runs)
	}
	r := ln.Runs
	if !r[1].Underline || !r[2].Strike || r[3].Background != "#ff0" {
		t.Fatalf("decorations lost: %+v", r)
	}
	if r[4].Font != "normal 400 11.2px sans-serif" || r[4].DY != -5.6 || r[4].Size != 11.2 {
		t.Fatalf("superscript run = %+v", r[4])
	}
	if r[5].DY != 2.4000000000000004 && r[5].DY != 2.4 {
		t.Fatalf("subscript DY = %g", r[5].DY)
	}
	if ln.H != 22 {
		t.Fatalf("scripts must not change the line height: %g", ln.H)
	}
	f.Draw()
	for _, want := range []string{
		"fillStyle #ff0", // highlight
		"fillRect 18 ",   // underline of "U" at x = 10+8, 1px
	} {
		if !p.has(want) {
			t.Errorf("missing %q in %v", want, p.calls)
		}
	}
	if !p.has("fillText \"2\" ") {
		t.Fatal("script text drawn")
	}
}

func TestScaleFormatScalesFontAndLeading(t *testing.T) {
	f, _ := newFlow("", 400, 200)
	f.Items = []Item{Chars{Text: "big", Format: Format{Scale: 2}}}
	ln := f.Layout().Lines[0]
	if ln.Runs[0].Font != "normal 400 32px sans-serif" || ln.H != 44 {
		t.Fatalf("run=%+v h=%g", ln.Runs[0], ln.H)
	}
}

func TestInlineObjectSitsInTheLine(t *testing.T) {
	f, _ := newFlow("", 400, 300)
	obj := &fakeObj{w: 40, h: 60}
	f.Items = []Item{Chars{Text: "ab "}, Inline{Object: obj}, Chars{Text: " cd"}}
	l := f.Layout()
	if len(l.Lines) != 1 {
		t.Fatalf("lines = %+v", l.Lines)
	}
	ln := l.Lines[0]
	if ln.Text != "ab ￼ cd" || len(ln.Objects) != 1 || len(ln.Runs) != 2 {
		t.Fatalf("line = %+v", ln)
	}
	if ln.H != 60+0.25*16 {
		t.Fatalf("line height = %g", ln.H)
	}
	o := ln.Objects[0]
	if o.X != 10+24 || o.W != 40 || o.H != 60 || o.Offset != 3 {
		t.Fatalf("object = %+v", o)
	}
	if o.Y+o.H != ln.Baseline {
		t.Fatalf("object bottom %g vs baseline %g", o.Y+o.H, ln.Baseline)
	}
	if ln.Runs[1].X != 24+40 {
		t.Fatalf("text after the object starts after it: %g", ln.Runs[1].X)
	}
}

func TestInlineObjectIsOneUnbreakableWord(t *testing.T) {
	f, _ := newFlow("", 100, 300) // 100px wide
	f.Items = []Item{Chars{Text: "aaaa "}, Inline{Object: &fakeObj{w: 80, h: 10}}, Chars{Text: " b"}}
	l := f.Layout()
	if len(l.Lines) != 2 || l.Lines[0].Text != "aaaa" || len(l.Lines[1].Objects) != 1 {
		t.Fatalf("an 80px object doesn't fit after 'aaaa ' in 100px: %+v", l.Lines)
	}
}

func TestInlineObjectIsFittedToTheColumnAndPage(t *testing.T) {
	f, _ := newFlow("", 100, 80) // text area 100 x 80
	obj := &fakeObj{w: 400, h: 200}
	f.Items = []Item{Inline{Object: obj}}
	l := f.Layout()
	if obj.gotMaxW != 100 || obj.gotMaxH != 80 {
		t.Fatalf("offered %gx%g, want the text area 100x80", obj.gotMaxW, obj.gotMaxH)
	}
	o := l.Lines[0].Objects[0]
	if o.W != 100 || o.H != 50 { // 400x200 scaled by min(100/400, 80/200)
		t.Fatalf("object = %gx%g", o.W, o.H)
	}
	f.Columns = []float64{60, 30}
	f.Gap = 10
	f.Layout()
	if obj.gotMaxW != 60 {
		t.Fatalf("column width offered = %g", obj.gotMaxW)
	}
}

func TestBlockObjectGetsItsOwnLine(t *testing.T) {
	f, p := newFlow("", 300, 300)
	obj := &fakeObj{w: 100, h: 40}
	f.Items = []Item{Chars{Text: "before "}, Inline{Object: obj, Block: true}, Chars{Text: " after"}}
	l := f.Layout()
	var texts []string
	for _, ln := range l.Lines {
		texts = append(texts, ln.Text)
	}
	if !reflect.DeepEqual(texts, []string{"before", "￼", " after"}) && !reflect.DeepEqual(texts, []string{"before", "￼", "after"}) {
		t.Fatalf("lines = %q", texts)
	}
	blk := l.Lines[1]
	if blk.H != 40 || blk.Objects[0].Y != blk.Y || blk.Objects[0].X != 10 {
		t.Fatalf("block line = %+v", blk)
	}
	if l.Lines[2].Y != blk.Y+40 {
		t.Fatal("text resumes below the block")
	}
	f.Draw()
	if obj.drawn != 1 || obj.drawX != 10 || obj.drawY != blk.Y {
		t.Fatalf("drawn %d times at %g,%g", obj.drawn, obj.drawX, obj.drawY)
	}
	_ = p
}

func TestBlockObjectMovesToNextPageWhenItDoesNotFit(t *testing.T) {
	f := pagedFlow(Chars{Text: "a\nb"}, Inline{Object: &fakeObj{w: 50, h: 40}, Block: true})
	l := f.Layout()
	var obj Line
	for _, ln := range l.Lines {
		if len(ln.Objects) > 0 {
			obj = ln
		}
	}
	if obj.Page != 1 || obj.Y != 158 {
		t.Fatalf("a 40px block doesn't fit in the 36px left on page 0: %+v", obj)
	}
}

func TestInlineObjectCaretOffsets(t *testing.T) {
	f, _ := newFlow("", 400, 300)
	f.Items = []Item{Chars{Text: "ab"}, Inline{Object: &fakeObj{w: 40, h: 10}}, Chars{Text: "cd"}}
	x, _, _, ok := f.CaretRect(3) // after the object
	if !ok || x != 10+16+40 {
		t.Fatalf("caret after object x = %g", x)
	}
}

func TestRuleObject(t *testing.T) {
	f, p := newFlow("", 200, 100)
	f.Items = []Item{Chars{Text: "x\n"}, Inline{Object: &Rule{Color: "#f00", Thickness: 2}, Block: true}, Chars{Text: "y"}}
	l := f.Layout()
	if l.Lines[1].H != 14 || l.Lines[1].Objects[0].W != 200 {
		t.Fatalf("rule line = %+v", l.Lines[1])
	}
	f.Draw()
	if !p.has("fillStyle #f00") || !p.has("fillRect 10 "+"") {
		t.Fatalf("rule not drawn: %v", p.calls)
	}
}

func TestTableObjectMeasureAndClip(t *testing.T) {
	tbl := NewTableObject(NewTableFrom([][]string{{"a", "b"}, {"c", "d"}}))
	p := &fakePainter{}
	if w, h := tbl.Measure(p, 500, 500); w != 200 || h != 90 {
		t.Fatalf("natural size = %gx%g", w, h)
	}
	if w, h := tbl.Measure(p, 100, 50); w != 100 || h != 50 {
		t.Fatalf("clipped size = %gx%g", w, h)
	}
	tbl.DrawAt(p, 20, 30)
	if !p.has("clipRect 20 30 100 50") || !p.has("save") || !p.has("restore") {
		t.Fatalf("table must draw clipped: %v", p.calls)
	}
	if !p.has(`fillText "a" 50 `) {
		t.Fatalf("cells drawn at the given origin: %v", p.calls)
	}
	if p.has(`fillText "+"`) {
		t.Fatal("an inline table is read-only: no + bars")
	}
}

type countingTransform struct{ calls int }

func (c *countingTransform) Transform(src string) []Item {
	c.calls++
	return []Item{Chars{Text: "<" + src + ">"}}
}

func TestTransformIsCachedUntilSourceChanges(t *testing.T) {
	f, _ := newFlow("ignored", 400, 100)
	tr := &countingTransform{}
	f.Transform, f.Source = tr, "one"
	f.Layout()
	f.Layout()
	f.Draw()
	if tr.calls != 1 || f.Layout().Lines[0].Text != "<one>" {
		t.Fatalf("calls = %d, text %q", tr.calls, f.Layout().Lines[0].Text)
	}
	f.Source = "two"
	if f.Layout().Lines[0].Text != "<two>" || tr.calls != 2 {
		t.Fatalf("calls = %d", tr.calls)
	}
	f.InvalidateTransform()
	f.Layout()
	if tr.calls != 3 {
		t.Fatalf("calls = %d", tr.calls)
	}
	f.Transform = nil
	if f.Layout().Lines[0].Text != "ignored" {
		t.Fatal("Text is used again without a Transform")
	}
}

func TestTransformFunc(t *testing.T) {
	f, _ := newFlow("", 400, 100)
	f.Transform = TransformFunc(func(s string) []Item { return []Item{Chars{Text: s + "!"}} })
	f.Source = "hi"
	if f.Layout().Lines[0].Text != "hi!" {
		t.Fatal("TransformFunc")
	}
}

func TestMarkdownFlowEndToEnd(t *testing.T) {
	f, p := newFlow("", 600, 600)
	f.Transform = Markdown{}
	f.Source = "# Title\n\nSome **bold**, x^2^ and ~~old~~ text.\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\n- item"
	l := f.Layout()
	if l.Overflow {
		t.Fatal("fits")
	}
	if l.Lines[0].Text != "Title" || l.Lines[0].H != 44 { // scale 2 on a 22px leading
		t.Fatalf("heading = %+v", l.Lines[0])
	}
	var sawTable bool
	for _, ln := range l.Lines {
		for _, o := range ln.Objects {
			if _, ok := o.Object.(*TableObject); ok {
				sawTable = true
				if ln.H < 70 {
					t.Fatalf("table line height %g", ln.H)
				}
			}
		}
	}
	if !sawTable {
		t.Fatalf("no table object laid out: %+v", l.Lines)
	}
	f.Draw()
	for _, want := range []string{"font normal 700 32px sans-serif", "font normal 700 16px sans-serif", `fillText "2"`, `fillText "•"`, `fillText "item"`} {
		if !p.has(want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestDragEditsTransformedColumnConfig(t *testing.T) {
	f := pagedFlow()
	f.Transform = Markdown{}
	f.Source = "\\columns 2 gap=20\nhello"
	f.SetMode(ModeEdit)
	lay := f.Layout()
	if len(lay.Bands) != 1 || len(lay.Bands[0].Xs) != 2 || lay.Bands[0].Widths[0] != 80 {
		t.Fatalf("bands = %+v", lay.Bands)
	}
	g := f.gripRect(lay.Bands[0], 0)
	f.MouseDown(g[0]+g[2]/2, g[1]+2)
	f.MouseMove(g[0]+g[2]/2+20, g[1]+2)
	if w := f.Layout().Bands[0].Widths; !reflect.DeepEqual(w, []float64{100, 60}) {
		t.Fatalf("widths = %v: a drag on a transformed config must persist", w)
	}
	f.MouseUp(0, 0)
	if w := f.Layout().Bands[0].Widths; w[0] != 100 {
		t.Fatalf("widths after release = %v", w)
	}
}

func TestColumnConfigCountMakesEqualColumns(t *testing.T) {
	f := pagedFlow(ColumnConfig{Count: 3, Gap: 15}, Chars{Text: "x"})
	b := f.Layout().Bands[0]
	if len(b.Widths) != 3 || b.Widths[0] != 50 || b.Xs[1] != 34+50+15 { // (180-30)/3
		t.Fatalf("band = %+v", b)
	}
	f = pagedFlow(ColumnConfig{Count: 2, Gap: 500}, Chars{Text: "x"})
	if b := f.Layout().Bands[0]; len(b.Widths) != 1 {
		t.Fatalf("gaps leave no room: one column: %+v", b)
	}
}
