package docfile

import (
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/reference"
)

func TestSourcesAreKeptWithTheDocument(t *testing.T) {
	var d Document
	if d.Sources() != nil || d.ReferenceStyle() != reference.APA {
		t.Fatal("none by default")
	}
	src := []reference.Source{{ID: "s1", Kind: reference.Book, Authors: []string{"Silva, Ana"}, Title: "A", Year: "2020"}}
	d.SetSources(reference.MLA, src)
	var back Document
	for _, f := range Formats() { // every format keeps them: they ride in Meta
		b, err := f.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		if back, err = f.Unmarshal(b); err != nil {
			t.Fatal(err)
		}
		if len(back.Sources()) != 1 {
			t.Fatalf("%s lost the sources", f.Name())
		}
	}
	got := back.Sources()
	if len(got) != 1 || got[0].Title != "A" || got[0].Authors[0] != "Silva, Ana" || back.ReferenceStyle() != reference.MLA {
		t.Fatalf("%+v %v", got, back.ReferenceStyle())
	}
	back.SetSources(reference.MLA, nil)
	if back.Sources() != nil {
		t.Fatal("removed")
	}
}

func TestTableStylesAreKeptWithTheDocument(t *testing.T) {
	var d Document
	d.SetTableSchemes([]canvas.TableScheme{{Name: "Sunrise", HeaderBG: "#c2410c", BandBG: "#ffedd5"}, {Name: "", HeaderBG: "#000"}, {Name: "Bad", HeaderBG: "red"}})
	for _, f := range Formats() {
		b, err := f.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		back, err := f.Unmarshal(b)
		if err != nil {
			t.Fatal(err)
		}
		got := back.TableSchemes()
		if len(got) != 1 || got[0].Name != "Sunrise" || got[0].BandBG != "#ffedd5" {
			t.Fatalf("%s: %+v (the invalid ones are left out)", f.Name(), got)
		}
	}
	d.SetTableSchemes(nil)
	if d.TableSchemes() != nil {
		t.Fatal("removed")
	}
}
