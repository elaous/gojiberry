package docfile

import (
	"reflect"
	"testing"

	"github.com/elaous/gojiberry/canvas"
)

// A saved document holds an equation with its text alternative, a diagram's long description and the language
// of a code block, in every encoding, and the editor's flow gives them back.
func richElements() Document {
	return Document{Title: "Elements", Content: []Item{
		{Chars: &Chars{Text: "Before\n"}},
		{Equation: &EquationItem{Formula: "x^2 + y^2 = z^2", Alt: "x squared plus y squared equals z squared"}},
		{Image: &ImageItem{URL: "data:image/png;base64,AAAA", Width: 40, Height: 30, Alt: "Flow chart", LongDesc: "Three boxes: input, process, output."}},
		{Para: &Para{Style: "Code", CodeLang: "go"}},
		{Chars: &Chars{Text: "x := 1\n"}},
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
				eq = e
			}
			if !v.Block && reflect.TypeOf(v.Object) == reflect.TypeOf(&canvas.EquationObject{}) {
				t.Error("an equation is a block object: it has a line of its own")
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
	// editing the equation in the flow is saved
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
