package docsearch

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

// The words of an equation are searchable.
func TestFromDocfileIndexesTheWordsOfAnEquation(t *testing.T) {
	d := docfile.Document{Title: "Maths", Content: []docfile.Item{
		{Chars: &docfile.Chars{Text: "Intro\n"}},
		{Equation: &docfile.EquationItem{Formula: "a^2+b^2=c^2", Alt: "Pythagoras theorem"}},
	}}
	data, err := docfile.JSON.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	_, text, err := FromDocfile(data)
	if err != nil || !strings.Contains(text, "Pythagoras theorem") {
		t.Errorf("text = %q, %v", text, err)
	}
}
