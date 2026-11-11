package docfile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
)

func richElements() Document {
	return Document{Title: "Elements", Content: []Item{
		{Chars: &Chars{Text: "Before\n"}},
		{Equation: &EquationItem{Formula: "x^2 + y^2 = z^2", Alt: "x squared plus y squared equals z squared"}},
		{Image: &ImageItem{URL: "data:image/png;base64,AAAA", Width: 40, Height: 30, Alt: "Flow chart", LongDesc: "Three boxes: input, process, output.", Legend: "Figure 1: the flow", Credit: "Flow by Ana, CC BY 4.0, via Openverse", SourceURL: "https://example.org/a1"}},
		{Para: &Para{Style: "Code", CodeLang: "go"}},
		{Chars: &Chars{Text: "x := 1\n"}},
		{Chars: &Chars{Text: "Let "}},
		{Equation: &EquationItem{Formula: "x_1", Alt: "x sub 1", Inline: true}},
		{Chars: &Chars{Text: " be given.\n"}},
	}}
}

func TestElementsSurviveEveryEncoding(t *testing.T) {
	d := richElements()
	for _, c := range Formats() {
		b, err := c.Marshal(d)
		if err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
		got, err := c.Unmarshal(b)
		if err != nil || !reflect.DeepEqual(got, d) {
			t.Errorf("%s: read back %+v, %v", c.Name(), got, err)
		}
	}
}

func TestElementsSurviveTheEditorsFlow(t *testing.T) {
	d := richElements()
	f := &canvas.TextFlow{}
	d.Apply(f)
	var eq *canvas.EquationObject
	var code canvas.ParaFormat
	for _, it := range f.Items {
		switch v := it.(type) {
		case canvas.Inline:
			if e, ok := v.Object.(*canvas.EquationObject); ok {
				if eq == nil {
					eq = e
				}
				if v.Block == e.Inline {
					t.Error("a block equation has a line of its own and an inline one is in its line")
				}
			}
		case canvas.ParaFormat:
			code = v
		}
	}
	if eq == nil || eq.Formula != "x^2 + y^2 = z^2" || eq.Alt != "x squared plus y squared equals z squared" {
		t.Fatalf("the equation in the flow: %+v", eq)
	}
	if code.Style != "Code" || code.CodeLang != "go" {
		t.Errorf("the code paragraph in the flow: %+v", code)
	}
	back := FromFlow("Elements", f)
	if !reflect.DeepEqual(back.Content, d.Content) {
		t.Errorf("flow back to a document:\n got %+v\nwant %+v", back.Content, d.Content)
	}
	eq.Alt = "Pythagoras"
	if got := FromFlow("Elements", f).Content[1].Equation; got == nil || got.Alt != "Pythagoras" {
		t.Errorf("an edited alternative is not saved: %+v", got)
	}
}

func TestEquationWordsAndText(t *testing.T) {
	if got := (EquationItem{Formula: " x^2 ", Alt: "  x squared "}).Words(); got != "x squared" {
		t.Errorf("Words = %q", got)
	}
	if got := (EquationItem{Formula: " x^2 "}).Words(); got != "x^2" {
		t.Errorf("with no alternative the formula stands in: %q", got)
	}
	d := Document{Content: []Item{{Chars: &Chars{Text: "see"}}, {Equation: &EquationItem{Formula: "a+b", Alt: "a plus b"}}, {Chars: &Chars{Text: "after\n"}}}}
	got := d.EquationsAsText()
	want := []Item{{Chars: &Chars{Text: "see"}}, {Chars: &Chars{Text: "\n"}}, {Chars: &Chars{Text: "a plus b\n"}}, {Chars: &Chars{Text: "after\n"}}}
	if !reflect.DeepEqual(got.Content, want) {
		t.Errorf("EquationsAsText = %+v", got.Content)
	}
	if len(d.Content) != 3 || d.Content[1].Equation == nil {
		t.Error("the document itself was changed")
	}
	plain := Document{Content: []Item{{Chars: &Chars{Text: "x\n"}}}}
	if !reflect.DeepEqual(plain.EquationsAsText(), plain) {
		t.Error("a document with no equation is returned as it is")
	}
}

func TestAnInlineEquationStaysInItsLineAsText(t *testing.T) {
	d := richElements().EquationsAsText()
	var text string
	for _, it := range d.Content {
		if it.Chars != nil {
			text += it.Chars.Text
		}
	}
	if !strings.Contains(text, "Let x sub 1 be given.\n") {
		t.Fatalf("the inline words stay in the sentence: %q", text)
	}
	back := ToItems(richElements().Content)
	var inline, block int
	for _, it := range back {
		if in, ok := it.(canvas.Inline); ok {
			if eq, ok := in.Object.(*canvas.EquationObject); ok {
				if eq.Inline && !in.Block {
					inline++
				}
				if !eq.Inline && in.Block {
					block++
				}
			}
		}
	}
	if inline != 1 || block != 1 {
		t.Fatalf("one inline and one block equation: %d %d", inline, block)
	}
}

func TestFormFieldRoundTripsThroughTheFlow(t *testing.T) {
	d := Document{Title: "Form", Content: []Item{
		{Chars: &Chars{Text: "Name "}},
		{Form: &FormFieldItem{Kind: "email", Name: "mail", Label: "E-mail", Placeholder: "you@example.com", Pattern: `.+@.+`, Required: true,
			Options: []string{"a", "b"}, Value: "ana@example.com", Locked: true}},
		{Chars: &Chars{Text: "\n"}},
	}}
	f := &canvas.TextFlow{}
	d.Apply(f)
	var got *canvas.FormFieldObject
	for _, it := range f.Items {
		if in, ok := it.(canvas.Inline); ok {
			got, _ = in.Object.(*canvas.FormFieldObject)
		}
	}
	if got == nil || got.Kind != canvas.FormEmail || got.Value != "ana@example.com" || !got.Locked || !got.Required || len(got.Options) != 2 {
		t.Fatalf("flow: %+v", got)
	}
	got.Value = "bo@example.com"
	back := FromFlow("Form", f).Content
	var item *FormFieldItem
	for _, it := range back {
		if it.Form != nil {
			item = it.Form
		}
	}
	if item == nil || item.Value != "bo@example.com" || item.Label != "E-mail" || !item.Locked || item.Pattern != ".+@.+" {
		t.Fatalf("back: %+v", item)
	}
}

func TestTableCellFieldRoundTrips(t *testing.T) {
	d := Document{Title: "T", Content: []Item{
		{Table: &TableItem{Rows: []TableRow{{Cells: []string{"Item", "Qty"}}, {Cells: []string{"Pens", "Qty: 4"}}},
			Fields: []TableCellField{{Row: 1, Col: 1, Field: &FormFieldItem{Kind: "number", Label: "Qty", Value: "4"}}}}},
		{Chars: &Chars{Text: "\n"}},
	}}
	for _, c := range Formats() {
		b, err := c.Marshal(d)
		if err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
		got, err := c.Unmarshal(b)
		if err != nil || !reflect.DeepEqual(got, d) {
			t.Errorf("%s: read back %+v, %v", c.Name(), got, err)
		}
	}
	f := &canvas.TextFlow{}
	d.Apply(f)
	ffs := f.FormFields()
	if len(ffs) != 1 || ffs[0].Label != "Qty" || ffs[0].Value != "4" {
		t.Fatalf("fields: %+v", ffs)
	}
	ffs[0].Value = "5"
	f.Tables()[0].View.Table.SyncFields()
	back := FromFlow("T", f).Content
	var tab *TableItem
	for _, it := range back {
		if it.Table != nil {
			tab = it.Table
		}
	}
	if tab == nil || len(tab.Fields) != 1 || tab.Fields[0].Field.Value != "5" || tab.Rows[1].Cells[1] != "Qty: 5" {
		t.Fatalf("back: %+v", tab)
	}
	if n := f.ClearFormFields(); n != 1 || f.Tables()[0].View.Table.Cell(1, 1) != "Qty: " {
		t.Fatalf("cleared %d, cell %q", n, f.Tables()[0].View.Table.Cell(1, 1))
	}
}

func TestTableSizesRoundTrip(t *testing.T) {
	d := Document{Title: "T", Content: []Item{
		{Table: &TableItem{Rows: []TableRow{{Cells: []string{"a", "b"}}, {Cells: []string{"c", "d"}}}, ColWidths: []float64{0, 140}, RowHeights: []float64{0, 48}}},
		{Chars: &Chars{Text: "\n"}},
	}}
	for _, c := range Formats() {
		b, err := c.Marshal(d)
		if err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
		got, err := c.Unmarshal(b)
		if err != nil || !reflect.DeepEqual(got, d) {
			t.Errorf("%s: read back %+v, %v", c.Name(), got, err)
		}
	}
	f := &canvas.TextFlow{}
	d.Apply(f)
	tb := f.Tables()[0].View.Table
	if tb.ColumnWidth(1) != 140 || tb.RowHeightSet(1) != 48 || tb.ColumnWidth(0) != 0 {
		t.Fatalf("sizes %v %v", tb.ColWidths, tb.RowHeights)
	}
	back := FromFlow("T", f).Content
	if got := back[0].Table; got == nil || got.ColWidths[1] != 140 || got.RowHeights[1] != 48 {
		t.Fatalf("back %+v", got)
	}
}
