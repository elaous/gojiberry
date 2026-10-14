package components

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/tokens"
)

func TestThemeEditorSaysWhatIsWrongAsItIsTyped(t *testing.T) {
	good := render(t, ThemeEditor(ThemeEditorProps{Set: tokens.Default()}))
	mustContain(t, good, "gj-theme-ok", `id="gj-theme-text"`, `id="gj-theme-typeScale"`, "16, 18, 20, 24, 30, 36")
	if strings.Contains(good, "aria-invalid") {
		t.Error("a valid theme has no invalid field")
	}
	bad := tokens.Default()
	bad.Colors = map[string]string{}
	for k, v := range tokens.Default().Colors {
		bad.Colors[k] = v
	}
	bad.Colors[tokens.Text] = "#bbbbbb"
	bad.TypeScale = []float64{12, 14}
	out := render(t, ThemeEditor(ThemeEditorProps{Set: bad, OnReset: func() {}}))
	mustContain(t, out, `aria-invalid="true"`, `aria-describedby="gj-theme-problems"`, "2 things to fix", "contrast of", "body size", "Back to the default theme")
	// a colour field cannot write more CSS than a colour
	bad.Colors[tokens.Link] = "red;background:url(x)"
	if strings.Contains(render(t, ThemeEditor(ThemeEditorProps{Set: bad})), "color:red;background:url(x)") {
		t.Error("a colour field wrote CSS into the sample")
	}
}

func TestParseNumberList(t *testing.T) {
	got := parseNumberList("16, 18 20;x 24.5")
	if len(got) != 4 || got[3] != 24.5 || numberList(got) != "16, 18, 20, 24.5" {
		t.Errorf("parse = %v / %q", got, numberList(got))
	}
}
