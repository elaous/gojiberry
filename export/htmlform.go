package export

import (
	"strings"

	"github.com/elaous/gojiberry/docfile"
)

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

func htmlFormWrap(snap Snapshot, body string) string {
	if snap.FormAction == "" || !strings.Contains(body, "<label>") || !hasFormField(snap) {
		return body
	}
	button := snap.FormButton
	if strings.TrimSpace(button) == "" {
		button = "Send"
	}
	attrs := ` method="post"`
	if strings.HasPrefix(snap.FormAction, "mailto:") {
		attrs += ` enctype="text/plain"`
	}
	return `<form action="` + escapeHTMLAttr(snap.FormAction) + `"` + attrs + ">\n" + body +
		`<p><button type="submit">` + escapeHTMLText(button) + "</button></p>\n</form>\n"
}

func hasFormField(snap Snapshot) bool {
	for _, b := range snap.Blocks {
		if b.TableField != nil {
			return true
		}
		for _, sg := range b.Segments {
			if sg.Format.Field != nil {
				return true
			}
		}
	}
	return false
}
