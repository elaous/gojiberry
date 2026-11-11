package docfile

import (
	"math"
	"testing"
)

func TestPageLayoutRoundTripsThroughMeta(t *testing.T) {
	var d Document
	want := PageLayout{Header: "{title}\t{page}", Footer: "Page {page} of {pages}", TOC: true, TOCTitle: "Inhalt", Width: 595, Height: 842, Margins: [4]float64{72, 54, 72, 54}}
	d.SetPageLayout(want)
	if got := d.PageLayout(); got != want {
		t.Fatalf("layout = %+v, want %+v", got, want)
	}
	d.SetPageLayout(PageLayout{})
	if len(d.Meta) != 0 {
		t.Fatalf("an empty layout should leave no Meta, got %+v", d.Meta)
	}
	if !d.PageLayout().IsZero() {
		t.Fatal("an empty layout should be zero")
	}
}

func TestPageLayoutIgnoresWhatCannotBeLaidOut(t *testing.T) {
	cases := map[string]PageLayout{
		"tiny sheet":       {Width: 10, Height: 842},
		"nan sheet":        {Width: math.NaN(), Height: 842},
		"negative margin":  {Width: 595, Height: 842, Margins: [4]float64{-1, 10, 10, 10}},
		"margins eat page": {Width: 595, Height: 842, Margins: [4]float64{10, 300, 10, 300}},
	}
	for name, l := range cases {
		n := l.Normalized()
		if name == "negative margin" || name == "margins eat page" {
			if n.Margins != ([4]float64{}) || n.Width != 595 {
				t.Errorf("%s: want margins dropped and the sheet kept, got %+v", name, n)
			}
			continue
		}
		if n.Width != 0 || n.Height != 0 {
			t.Errorf("%s: sheet should be dropped, got %+v", name, n)
		}
	}
	if n := (PageLayout{Header: "a\x00b\nc\td"}).Normalized(); n.Header != "a b c\td" {
		t.Errorf("control characters other than tab should become spaces, got %q", n.Header)
	}
	var d Document
	d.Set(PageSizeKey, "wide x tall")
	d.Set(PageMarginsKey, "1,2,3")
	if !d.PageLayout().IsZero() {
		t.Errorf("garbage in Meta should read as no layout, got %+v", d.PageLayout())
	}
}
