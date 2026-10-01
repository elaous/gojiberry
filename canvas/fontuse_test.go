package canvas

import (
	"reflect"
	"testing"
)

func TestTextFlowUsedFonts(t *testing.T) {
	f, p := selFlow(
		Chars{Text: "plain "},
		Chars{Text: "bold", Format: Format{FontFamily: `"Lato", sans-serif`, FontWeight: 700}},
		Chars{Text: "ital", Format: Format{FontFamily: "Lato", FontStyle: StyleItalic}},
		Chars{Text: "serif", Format: Format{FontFamily: "serif"}},
		Chars{Text: "", Format: Format{FontFamily: "Unused"}},
		ParaFormat{Style: "Fancy"},
		Chars{Text: "x", Format: Format{Style: "Mono"}},
	)
	f.Style.FontFamily = "Base Font"
	f.Styles = []StyleDef{
		{Name: "Fancy", Paragraph: true, Format: Format{FontFamily: "Playfair Display"}},
		{Name: "Mono", Format: Format{FontFamily: "Fira Code", FontWeight: 500}},
	}
	_ = p
	want := []FontUse{
		{"Base Font", 400, false}, {"Fira Code", 500, false},
		{"Lato", 400, true}, {"Lato", 700, false}, {"Playfair Display", 400, false},
	}
	if got := f.UsedFonts(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v\nwant %v", got, want)
	}
}

func TestFloatingTextUsedFonts(t *testing.T) {
	ft := NewFloatingText("", 0, 0, 100, 40)
	ft.Style.FontFamily = "serif"
	ft.runes = []fRune{{'a', Format{}}, {'b', Format{FontFamily: "Lora", FontWeight: 600}}}
	if got := ft.UsedFonts(); !reflect.DeepEqual(got, []FontUse{{"Lora", 600, false}}) {
		t.Fatal(got)
	}
}

func TestStageReportsFontsOnce(t *testing.T) {
	f, p := selFlow(Chars{Text: "hi", Format: Format{FontFamily: "Lato"}})
	sv := NewScrollView(0, 0, 100, 100)
	sv.Add(f, 100, 100)
	st := NewStage(p, 100, 100, sv)
	var got []FontUse
	st.OnFontUse = func(u FontUse) { got = append(got, u) }
	st.Draw()
	st.Draw()
	if len(got) != 1 || got[0].Family != "Lato" {
		t.Fatalf("reported %v", got)
	}
	st.ForgetFonts()
	st.Draw()
	if len(got) != 2 {
		t.Fatalf("after Forget: %v", got)
	}
	st.OnFontUse = nil
	st.Draw() // no panic with no hook
}
