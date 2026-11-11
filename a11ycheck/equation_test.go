package a11ycheck

import (
	"slices"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

func TestEquationFindingsRequireAWordedAlternative(t *testing.T) {
	for _, c := range []struct {
		name string
		e    docfile.EquationItem
		want []string
	}{
		{"no alternative", docfile.EquationItem{Formula: "x^2"}, []string{"equation-alt:error"}},
		{"a blank alternative", docfile.EquationItem{Formula: "x^2", Alt: "  \n"}, []string{"equation-alt:error"}},
		{"the formula typed again", docfile.EquationItem{Formula: "x^2", Alt: " x^2 "}, []string{"equation-alt:warning"}},
		{"words", docfile.EquationItem{Formula: "x^2", Alt: "x squared"}, nil},
	} {
		if got := rulesOf(EquationFindings(c.e, 0, 0)); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	for _, f := range EquationFindings(docfile.EquationItem{Formula: "x^2"}, 0, 0) {
		if f.Message == "" || f.Fix == "" || f.Action != ActionDescribe {
			t.Errorf("a finding must say what is wrong and how to fix it: %+v", f)
		}
	}
}

func TestCheckFindsAnEquationWithoutAnAlternativeWhereItIs(t *testing.T) {
	doc := docfile.Document{Content: []docfile.Item{
		{Chars: &docfile.Chars{Text: "Intro\n"}},
		{Equation: &docfile.EquationItem{Formula: "e=mc^2"}},
		{Chars: &docfile.Chars{Text: "Between\n"}},
		{Equation: &docfile.EquationItem{Formula: "a+b", Alt: "a plus b"}},
		{Image: &docfile.ImageItem{URL: "x.png"}},
	}}
	var eq []Finding
	var image Finding
	for _, f := range Check(doc, Options{}) {
		switch f.Rule {
		case RuleEquationAlt:
			eq = append(eq, f)
		case RuleImageAlt:
			image = f
		}
	}
	if len(eq) != 1 {
		t.Fatalf("equation findings: %+v", eq)
	}
	if eq[0].Offset != 6 || eq[0].Item != 1 || eq[0].Severity != Error {
		t.Errorf("finding = %+v", eq[0])
	}
	if image.Offset != 6+1+8+1 {
		t.Errorf("the picture is at %d: an equation counts as one rune of the text stream", image.Offset)
	}
	offsets := itemOffsets(doc)
	if offsets[3] != 6+1+8 || offsets[4] != 6+1+8+1 {
		t.Errorf("item offsets = %v", offsets)
	}
}
