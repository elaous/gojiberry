package docfile

import (
	"reflect"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
)

// A saved document holds an equation with its text alternative, a diagram's long description and the language
// of a code block, in every encoding, and the editor's flow gives them back.
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
