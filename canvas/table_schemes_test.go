package canvas

import "testing"

func TestADressedTableStaysDressedWhenRowsAndColumnsChange(t *testing.T) {
	s, _ := SchemeByName("Blue header, banded")
	tb := NewTableFrom([][]string{{"h", "h"}, {"1", "2"}, {"3", "4"}, {"5", "6"}})
	tb.ApplyScheme(s)
	tb.InsertRow(2)
	if tb.SchemeOf() != s.Name {
		t.Fatal("the new row is banded with the rest")
	}
	tb.AddRow()
	tb.DeleteRow(1)
	tb.DuplicateRow(1)
	tb.MoveRow(2, 1)
	tb.SortByColumn(0, true)
	tb.InsertColumn(0)
	tb.DeleteColumn(1)
	if tb.SchemeOf() != s.Name {
		t.Fatalf("still dressed in %q after changes: %q", s.Name, tb.SchemeOf())
	}
	if tb.StyleAt(2, 1).Background != s.BandBG {
		t.Fatal("bands run on every second row")
	}
	// a table dressed by hand is left as the change makes it
	hand := NewTableFrom([][]string{{"h"}, {"1"}, {"2"}})
	hand.SetCellStyle(1, 0, CellStyle{Background: "#ffcc00"})
	hand.InsertRow(1)
	if hand.StyleAt(2, 0).Background != "#ffcc00" || hand.SchemeOf() != "" {
		t.Fatal("hand formatting moved with its cell and was not replaced by a style")
	}
}

func TestStylesThePersonDefinesJoinTheBuiltInOnes(t *testing.T) {
	SetCustomTableSchemes(nil)
	defer SetCustomTableSchemes(nil)
	mine := TableScheme{Name: "Sunrise", HeaderBG: "#c2410c", HeaderColor: "#fff", HeaderBold: true, BandBG: "#ffedd5"}
	if err := AddTableScheme(mine); err != nil {
		t.Fatal(err)
	}
	if s, ok := SchemeByName("Sunrise"); !ok || s.BandBG != "#ffedd5" {
		t.Fatal("found by name")
	}
	tb := NewTableFrom([][]string{{"h"}, {"1"}, {"2"}, {"3"}})
	s, _ := SchemeByName("Sunrise")
	tb.ApplyScheme(s)
	if tb.SchemeOf() != "Sunrise" {
		t.Fatalf("recognised: %q", tb.SchemeOf())
	}
	tb.AddRow()
	if tb.SchemeOf() != "Sunrise" {
		t.Fatal("follows the rows too")
	}
	for _, bad := range []TableScheme{{Name: ""}, {Name: "Plain"}, {Name: "x", HeaderBG: "blue"}, {Name: "bold header"}, {Name: "y", BandBG: "#12"}} {
		if AddTableScheme(bad) == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := AddTableScheme(TableScheme{Name: "SUNRISE", HeaderBG: "#000"}); err != nil || len(CustomTableSchemes()) != 1 || CustomTableSchemes()[0].HeaderBG != "#000" {
		t.Fatal("the same name replaces")
	}
	RemoveTableScheme("sunrise") // removal is by the exact name, as the picker offers it
	if len(CustomTableSchemes()) != 1 {
		t.Fatal("removal is by the exact name")
	}
	RemoveTableScheme("SUNRISE")
	if len(CustomTableSchemes()) != 0 {
		t.Fatal("removed")
	}
	for i := 0; i < MaxCustomSchemes+3; i++ {
		_ = AddTableScheme(TableScheme{Name: "s" + itoa(i)})
	}
	if len(CustomTableSchemes()) != MaxCustomSchemes {
		t.Fatalf("at most %d: %d", MaxCustomSchemes, len(CustomTableSchemes()))
	}
}
