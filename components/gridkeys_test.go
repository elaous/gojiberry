package components

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
)

func TestSymbolPickerHasOneTabStop(t *testing.T) {
	html := render(t, SymbolPicker(SymbolPickerProps{Category: "Math"}))
	if n := strings.Count(html, `class="gj-symbol" type="button"`); n < 20 {
		t.Fatalf("only %d symbols", n)
	}
	zero := strings.Count(html, `data-char`) // every symbol has data-char
	tab0 := 0
	for _, part := range strings.Split(html, "<button") {
		if strings.Contains(part, `class="gj-symbol"`) && strings.Contains(part, `tabindex="0"`) {
			tab0++
		}
	}
	if zero == 0 || tab0 != 1 {
		t.Errorf("%d symbols, %d of them in the tab order", zero, tab0)
	}
	chips := 0
	for _, part := range strings.Split(html, "<button") {
		if strings.Contains(part, "gj-symbolpicker-chip") && strings.Contains(part, `tabindex="0"`) {
			chips++
			if !strings.Contains(part, `data-category="Math"`) {
				t.Errorf("the chip in the tab order is not the chosen one: %s", part)
			}
		}
	}
	if chips != 1 {
		t.Errorf("%d chips in the tab order", chips)
	}
	if !strings.Contains(html, `role="toolbar"`) {
		t.Error("the chips are a toolbar")
	}
}

func TestStylePickerEntersAtTheCurrentStyle(t *testing.T) {
	styles := canvas.DefaultStyles()
	if len(styles) < 3 {
		t.Skip("few styles")
	}
	cur := styles[len(styles)-1].Name
	html := render(t, StylePicker(StylePickerProps{Styles: styles, Current: cur}))
	tab0 := 0
	for _, part := range strings.Split(html, "<button") {
		if strings.Contains(part, "gj-stylepicker-item") && strings.Contains(part, `tabindex="0"`) {
			tab0++
			if !strings.Contains(part, `data-style="`+cur+`"`) {
				t.Errorf("the way in is not the current style: %s", part)
			}
		}
	}
	if tab0 != 1 {
		t.Errorf("%d styles in the tab order", tab0)
	}
}
