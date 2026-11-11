//go:build !(js && wasm)

package main

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/elaous/gojiberry/e2etest"
)

func TestEndToEnd(t *testing.T) {
	d := e2etest.NewSite(t).Open(t, "", e2etest.Options{Width: 900, Height: 900})
	preview := func(prop string) string {
		return d.Str(`getComputedStyle(document.querySelector('[data-preview]'))[` + e2etest.J(prop) + `]`)
	}
	options := func() int { return d.Count(".gj-fontselector-option") }
	ok := d.OK

	s := d.State()
	ok(s.Str("format.FontFamily") == `"Georgia", serif` && s.Int("fonts") >= 19, fmt.Sprint("starts with Georgia and a merged catalog of ", s.Get("fonts"), " fonts"))
	ok(regexp.MustCompile("Georgia").MatchString(preview("fontFamily")), "the preview uses the family")
	ok(preview("fontSize") == "18px", "unset size shows the base size")

	d.Click(".gj-fontselector-control")
	ok(d.State().Bool("open") && d.Visible(".gj-fontselector-panel"), "clicking the control opens the panel")
	all := options()
	d.Fill(".gj-fontselector-search", "fira")
	ok(d.State().Str("query") == "fira" && options() == 1, "searching narrows the list to Fira Code")
	d.Fill(".gj-fontselector-search", "")
	d.Click(`.gj-fontselector-chip[data-source="Project fonts"]`)
	ok(options() == 2, "the source chip limits the list to the two project fonts")
	d.Click(`.gj-fontselector-chip[data-source=""]`)
	ok(options() == all, "All brings the list back")
	d.Click(`[data-family="Playfair Display"]`)
	s = d.State()
	ok(s.Str("format.FontFamily") == `"Playfair Display", serif` && !s.Bool("open"), "choosing a font sets the format and closes the panel: "+s.Str("format.FontFamily"))
	ok(d.Is("s.recent[0] === 'Playfair Display'"), "it is remembered as recent")
	ok(d.Text(".gj-fontselector-label") == "Playfair Display", "the control shows the choice")

	d.Click(".gj-fontselector-control")
	ok(d.Text(".gj-fontselector-group") == "Recent", "a Recent group appears")
	ok(d.Attr(".gj-fontselector-option", "data-family") == "Playfair Display", "recent fonts come first")
	d.Click(".gj-fontselector-control")

	d.Click(`[data-action="bold"]`)
	d.Click(`[data-action="italic"]`)
	d.Click(`[data-action="underline"]`)
	ok(d.Is("s.format.FontWeight === 700 && s.format.FontStyle === 'italic' && s.format.Underline"), "bold, italic and underline set the format")
	ok(preview("fontWeight") == "700" && preview("fontStyle") == "italic" && regexp.MustCompile("underline").MatchString(preview("textDecorationLine")), "the preview shows them")
	ok(d.Attr(`[data-action="bold"]`, "aria-pressed") == "true", "toggles show as pressed")

	d.Click(`[data-action="superscript"]`)
	d.Click(`[data-action="subscript"]`)
	ok(d.Is("s.format.Subscript && !s.format.Superscript") && preview("verticalAlign") == "sub", "superscript and subscript exclude each other")
	d.Click(`[data-action="subscript"]`)

	d.Fill(`[data-field="size"]`, "30")
	ok(d.State().Int("format.FontSize") == 30 && preview("fontSize") == "30px", "size edits the format and preview")
	d.Select(`[data-field="weight"]`, "300")
	ok(d.State().Int("format.FontWeight") == 300, "weight select")
	d.Fill(`[data-field="color"]`, "#aa0000")
	ok(d.State().Str("format.Color") == "#aa0000" && preview("color") == "rgb(170, 0, 0)", "text color")
	d.Click(`[data-clear="color"]`)
	ok(d.State().Str("format.Color") == "", "clear color returns to inheriting")
	d.Fill(`[data-field="background"]`, "#00ff00")
	ok(d.State().Str("format.Background") == "#00ff00", "highlight")

	d.Click("#load-device-fonts")
	d.WaitForTimeout("a device-fonts outcome", `JSON.parse(document.getElementById('state').textContent).status !== ''`, 20e9)
	status := d.State().Str("status")
	ok(regexp.MustCompile(`Loaded device fonts|Could not list device fonts`).MatchString(status), "loading device fonts reports: "+status)
	d.NoErrors()
}
