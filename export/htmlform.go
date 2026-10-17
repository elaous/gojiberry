package export

import (
	"strings"

	"github.com/elaous/gojiberry/docfile"
)

// htmlFormControl writes a form field as the HTML control for its kind, inside a label, with what was written in it as
// its value. Nothing submits it: a reader can fill it in and print or copy the page.
func htmlFormControl(f docfile.FormFieldItem) string {
	label := escapeHTMLText(strings.TrimSpace(f.Label))
	name := escapeHTMLAttr(f.Name)
	attrs := ""
	if f.Name != "" {
		attrs += ` name="` + name + `"`
	}
	if f.Required {
		attrs += " required"
	}
	if f.Placeholder != "" && f.Kind != "checkbox" && f.Kind != "select" {
		attrs += ` placeholder="` + escapeHTMLAttr(f.Placeholder) + `"`
	}
	if f.Pattern != "" && f.Kind != "checkbox" && f.Kind != "select" && f.Kind != "textarea" {
		attrs += ` pattern="` + escapeHTMLAttr(f.Pattern) + `"`
	}
	var control string
	switch f.Kind {
	case "textarea":
		control = "<textarea" + attrs + ">" + escapeHTMLText(f.Value) + "</textarea>"
	case "checkbox":
		if f.Value == "true" {
			attrs += " checked"
		}
		control = `<input type="checkbox"` + attrs + ">"
	case "select":
		var opts strings.Builder
		for _, o := range f.Options {
			sel := ""
			if o == f.Value {
				sel = " selected"
			}
			opts.WriteString(`<option value="` + escapeHTMLAttr(o) + `"` + sel + ">" + escapeHTMLText(o) + "</option>")
		}
		control = "<select" + attrs + ">" + opts.String() + "</select>"
	default:
		typ := map[string]string{"email": "email", "number": "number", "phone": "tel", "url": "url", "date": "date"}[f.Kind]
		if typ == "" {
			typ = "text"
		}
		control = `<input type="` + typ + `"` + attrs + ` value="` + escapeHTMLAttr(f.Value) + `">`
	}
	if f.Kind == "checkbox" {
		return "<label>" + control + " " + label + "</label>"
	}
	return "<label>" + label + " " + control + "</label>"
}
