package canvas

import "testing"

func TestTableFromMarkdownKeepsFormulasAndAlignment(t *testing.T) {
	tb, ok := TableFromMarkdown("| Item | Qty | Price | Total |\n| :--- | --: | :-: | ---: |\n| Pens | 3 | 1.5 | =B2*C2 |\n| a \\| b | 2 | 4 | \\=B3*C3 |\n\nafter")
	if !ok || tb.Rows() != 3 || tb.Cols() != 4 {
		t.Fatalf("%v %+v", ok, tb)
	}
	if tb.Cell(1, 3) != "=B2*C2" || tb.Cell(2, 0) != "a | b" || tb.Cell(2, 3) != `\=B3*C3` {
		t.Fatalf("%q", tb.Cells)
	}
	v := tb.Values()
	if v[1][3] != "4.5" || v[2][3] != "=B3*C3" {
		t.Fatalf("%q", v)
	}
	if tb.StyleAt(1, 1).Align != AlignRight || tb.StyleAt(1, 2).Align != AlignCenter || tb.StyleAt(1, 0).Align != AlignLeft {
		t.Fatal("alignment from the separator row")
	}
	for _, bad := range []string{"", "plain text", "| a | b |\n| c | d |", "| a | b |\n| -- | x |"} {
		if _, ok := TableFromMarkdown(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestMarkdownExportKeepsTheFormulaSoItWorksWhenReadBack(t *testing.T) {
	tb := NewTableFrom([][]string{{"Qty", "Total"}, {"3", "=A2*2"}})
	md := MarkdownFromItems([]Item{Inline{Object: &TableObject{View: NewTableView(tb, nil)}, Block: true}})
	back, ok := TableFromMarkdown(md)
	if !ok || back.Cell(1, 1) != "=A2*2" || back.Values()[1][1] != "6" {
		t.Fatalf("%q\n%v", md, ok)
	}
}

func TestShowFormulasSwitchesWhatIsDrawn(t *testing.T) {
	p := &fakePainter{}
	v := NewTableView(NewTableFrom([][]string{{"2", "=A1*5"}}), p)
	v.Draw()
	if !p.has(`fillText "10"`) || p.has(`fillText "=A1*5"`) {
		t.Fatal("values by default")
	}
	v.ShowFormulas = true
	p.calls = nil
	v.Draw()
	if !p.has(`fillText "=A1*5"`) || p.has(`fillText "10"`) {
		t.Fatal("the formula when asked")
	}
}
