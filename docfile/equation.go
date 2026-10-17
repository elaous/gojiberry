package docfile

import "strings"

// Words is the text that stands for the equation: its alternative, else the formula itself, so that no
// format shows an equation as nothing.
func (e EquationItem) Words() string {
	if a := strings.TrimSpace(e.Alt); a != "" {
		return a
	}
	return strings.TrimSpace(e.Formula)
}

// EquationsAsText is d with each equation replaced by a paragraph of the text that stands for it (Words), for
// the formats that cannot typeset a formula (plain text, Markdown): a reader of the file gets the words.
func (d Document) EquationsAsText() Document {
	has := false
	for _, it := range d.Content {
		if it.Equation != nil {
			has = true
			break
		}
	}
	if !has {
		return d
	}
	out := d
	out.Content = make([]Item, 0, len(d.Content))
	for _, it := range d.Content {
		if it.Equation == nil {
			out.Content = append(out.Content, it)
			continue
		}
		if it.Equation.Inline { // stays in its line, as the words
			if w := it.Equation.Words(); w != "" {
				out.Content = append(out.Content, Item{Chars: &Chars{Text: w}})
			}
			continue
		}
		if n := len(out.Content); n > 0 {
			// the equation's paragraph starts a line of its own
			if last := out.Content[n-1].Chars; last != nil && last.Text != "" && !strings.HasSuffix(last.Text, "\n") {
				out.Content = append(out.Content, Item{Chars: &Chars{Text: "\n"}})
			}
		}
		if w := it.Equation.Words(); w != "" {
			out.Content = append(out.Content, Item{Chars: &Chars{Text: w + "\n"}})
		}
	}
	return out
}

// FormFieldsAsText is d with each form field replaced by the text that stands for it (Words), for the formats that
// write the text of a document item by item (plain text, Markdown): a reader of the file gets the label and what was
// written in the field.
func (d Document) FormFieldsAsText() Document {
	has := false
	for _, it := range d.Content {
		if it.Form != nil {
			has = true
			break
		}
	}
	if !has {
		return d
	}
	out := d
	out.Content = make([]Item, 0, len(d.Content))
	for _, it := range d.Content {
		switch {
		case it.Form == nil:
			out.Content = append(out.Content, it)
		default:
			if w := it.Form.Words(); w != "" {
				out.Content = append(out.Content, Item{Chars: &Chars{Text: w}})
			}
		}
	}
	return out
}
