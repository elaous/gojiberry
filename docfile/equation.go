package docfile

import "strings"

// Words is the text that stands for the equation.
func (e EquationItem) Words() string {
	if a := strings.TrimSpace(e.Alt); a != "" {
		return a
	}
	return strings.TrimSpace(e.Formula)
}

// EquationsAsText is d with each equation replaced by its words.
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
			// The equation's paragraph starts a line.
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

// FormFieldsAsText is d with each form field replaced by its text.
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
		if it.Form == nil {
			out.Content = append(out.Content, it)
			continue
		}
		if w := it.Form.Words(); w != "" {
			out.Content = append(out.Content, Item{Chars: &Chars{Text: w}})
		}
	}
	return out
}
