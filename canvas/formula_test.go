package canvas

import "testing"

func TestTableFormulas(t *testing.T) {
	cells := [][]string{
		{"Item", "Qty", "Price", "Total"},
		{"Pens", "3", "1.5", "=B2*C2"},
		{"Ink", "2", "4", "=B3*C3"},
		{"", "=SUM(B2:B3)", "", "=SUM(D2:D3)"},
		{"avg", "=AVERAGE(B2:B3)", "=ROUND(D4/3, 2)", "=IF(D4>=12, \"ok\", \"low\")"},
		{"=1/0", "=A1+1", "=E1", "=FOO(1)"},
		{"=Z99", "=(1+2", "=2^3^2", "=-B2%"},
		{"=MAX(B2:C3)", "=MIN(B2, C3, 9)", "=COUNT(A1:D3)", "=ABS(-4.5)"},
	}
	cells[5][2] = "=D6" // D6 is FOO(1); C6 is fine, E1 does not exist
	cells[5][1] = "=A1+1"
	got := Values(cells)
	want := map[[2]int]string{
		{1, 3}: "4.5", {2, 3}: "8", {3, 1}: "5", {3, 3}: "12.5", {4, 1}: "2.5", {4, 2}: "4.17", {4, 3}: "ok",
		{5, 0}: "#DIV/0!", {5, 1}: "#VALUE!", {5, 2}: "#NAME?", {5, 3}: "#NAME?",
		{6, 0}: "#REF!", {6, 1}: "#ERR!", {6, 2}: "512", {6, 3}: "-0.03",
		{7, 0}: "4", {7, 1}: "3", {7, 2}: "6", {7, 3}: "4.5",
		{0, 0}: "Item", {1, 3 - 1}: "1.5",
	}
	for pos, w := range want {
		if g := got[pos[0]][pos[1]]; g != w {
			t.Errorf("%c%d = %q, want %q", 'A'+pos[1], pos[0]+1, g, w)
		}
	}
}

func TestFormulaCycleAndEditing(t *testing.T) {
	tb := NewTableFrom([][]string{{"=B1", "=A1", "=C1+1"}, {"1", "2", ""}})
	if v := tb.Values(); v[0][0] != "#CYCLE!" || v[0][1] != "#CYCLE!" || v[0][2] != "#CYCLE!" && v[0][2] != "#REF!" && v[0][2] != "1" {
		t.Fatalf("%v", v)
	}
	tb = NewTableFrom([][]string{{"2", "=A1*10"}})
	if tb.Display(0, 1) != "20" || tb.Display(0, 0) != "2" || tb.Cell(0, 1) != "=A1*10" {
		t.Fatal("the text stays, the value is shown")
	}
	tb.SetCell(0, 0, "3")
	if tb.Display(0, 1) != "30" {
		t.Fatal("follows the cell")
	}
}

func TestEscapedEqualsIsPlainText(t *testing.T) {
	tb := NewTableFrom([][]string{{`\=A2+1`, "5", `=B1*2`, `=A1`}})
	v := tb.Values()[0]
	if v[0] != "=A2+1" || v[3] != "#VALUE!" {
		t.Fatalf("the backslash is not shown, the text is not computed: %q", v)
	}
	if tb.Display(0, 0) != "=A2+1" || tb.Cell(0, 0) != `\=A2+1` {
		t.Fatal("the text stays as typed")
	}
	if v[2] != "10" {
		t.Fatal(v)
	}
}

func TestColumnNames(t *testing.T) {
	for i, want := range map[int]string{0: "A", 1: "B", 25: "Z", 26: "AA", 27: "AB", 51: "AZ", 52: "BA", 701: "ZZ", 702: "AAA"} {
		if got := ColumnName(i); got != want {
			t.Errorf("%d: %q want %q", i, got, want)
		}
		if r, c, ok := cellName(want + "1"); i < 703 && (!ok || c != i || r != 0) {
			t.Errorf("%s does not read back: %d %d %v", want, r, c, ok)
		}
	}
}
