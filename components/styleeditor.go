package components

import (
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/gojiberry/xango"
)

// StyleEditorProps configures StyleEditor.
type StyleEditorProps struct {
	Format canvas.Format
	Base   canvas.TextStyle
	// OnChange receives the edited format after every change.
	OnChange func(canvas.Format)

	// FontPicker, if set, adds a FontSelector for the family.
	FontPicker *FontSelectorProps

	PreviewText string
	Disabled    bool
}

var styleWeights = []int{100, 200, 300, 400, 500, 600, 700, 800, 900}

func StyleEditor(props StyleEditorProps) xango.Node {
	resolved := props.Format.Resolve(props.Base)
	if resolved.FontSize <= 0 {
		resolved.FontSize = 16 // what the canvas draws unset sizes at
	}
	change := func(edit func(*canvas.Format)) func(xango.Event) {
		return func(xango.Event) {
			if props.OnChange == nil {
				return
			}
			f := props.Format
			edit(&f)
			props.OnChange(f)
		}
	}
	withValue := func(edit func(f *canvas.Format, v string)) func(xango.Event) {
		return func(e xango.Event) {
			if props.OnChange == nil {
				return
			}
			f := props.Format
			edit(&f, xango.EventValue[string](e))
			props.OnChange(f)
		}
	}
	disabled := func(attrs []xango.Attr) []xango.Attr {
		if props.Disabled {
			attrs = append(attrs, xango.A("disabled", "disabled"))
		}
		return attrs
	}

	toggle := func(action, label, title string, on bool, edit func(*canvas.Format)) xango.Node {
		class := "gj-styleeditor-toggle"
		if on {
			class += " gj-styleeditor-toggle-on"
		}
		attrs := disabled([]xango.Attr{
			xango.A("type", "button"), xango.A("class", class), xango.A("title", title), xango.A("aria-label", title),
			xango.A("aria-pressed", boolAttr(on)), xango.A("data-action", action),
			xango.On("click", change(edit)),
		})
		return xango.El("button", attrs, xango.Text(label))
	}

	var rows []xango.Node

	var fontRow []xango.Node
	if props.FontPicker != nil {
		picker := *props.FontPicker
		picker.Selected = primaryFamily(props.Format.FontFamily)
		picker.Disabled = picker.Disabled || props.Disabled
		userSelect := picker.OnSelect
		picker.OnSelect = func(f fonts.Font) {
			if userSelect != nil {
				userSelect(f)
			}
			if props.OnChange != nil {
				next := props.Format
				next.FontFamily = fonts.CSSFamily(f)
				props.OnChange(next)
			}
		}
		fontRow = append(fontRow, FontSelector(picker))
	}
	fontRow = append(fontRow,
		xango.El("input", disabled([]xango.Attr{
			xango.A("type", "number"), xango.A("class", "gj-styleeditor-size"), xango.A("min", "1"), xango.A("step", "1"),
			xango.A("aria-label", "Font size"), xango.A("data-field", "size"),
			xango.A("value", strconv.FormatFloat(roundTo(resolved.FontSize, 2), 'f', -1, 64)),
			xango.On("input", withValue(func(f *canvas.Format, v string) {
				if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && n > 0 {
					f.FontSize = n / maxFloat(f.Scale, 1)
				} else if strings.TrimSpace(v) == "" {
					f.FontSize = 0
				}
			})),
		})),
	)
	weightOpts := []xango.Node{xango.El("option", []xango.Attr{xango.A("value", "")}, xango.Text("Weight: inherit"))}
	for _, w := range styleWeights {
		attrs := []xango.Attr{xango.A("value", strconv.Itoa(w))}
		if props.Format.FontWeight == w {
			attrs = append(attrs, xango.A("selected", "selected"))
		}
		weightOpts = append(weightOpts, xango.El("option", attrs, xango.Text(strconv.Itoa(w))))
	}
	fontRow = append(fontRow, xango.El("select", disabled([]xango.Attr{
		xango.A("class", "gj-styleeditor-weight"), xango.A("aria-label", "Font weight"), xango.A("data-field", "weight"),
		xango.On("change", withValue(func(f *canvas.Format, v string) {
			f.FontWeight, _ = strconv.Atoi(v)
		})),
	}), weightOpts...))
	rows = append(rows, xango.El("div", []xango.Attr{xango.A("class", "gj-styleeditor-row")}, fontRow...))

	rows = append(rows, xango.El("div", []xango.Attr{xango.A("class", "gj-styleeditor-row gj-styleeditor-toggles"), xango.A("role", "group")},
		toggle("bold", "B", "Bold", resolved.FontWeight >= 600, func(f *canvas.Format) {
			if resolved.FontWeight >= 600 {
				f.FontWeight = 400
			} else {
				f.FontWeight = 700
			}
		}),
		toggle("italic", "I", "Italic", resolved.FontStyle == canvas.StyleItalic, func(f *canvas.Format) {
			if resolved.FontStyle == canvas.StyleItalic {
				f.FontStyle = canvas.StyleNormal
			} else {
				f.FontStyle = canvas.StyleItalic
			}
		}),
		toggle("underline", "U", "Underline", resolved.Underline, func(f *canvas.Format) { f.Underline = !resolved.Underline }),
		toggle("strike", "S", "Strikethrough", resolved.Strike, func(f *canvas.Format) { f.Strike = !resolved.Strike }),
		toggle("superscript", "x²", "Superscript", resolved.Superscript, func(f *canvas.Format) {
			f.Superscript, f.Subscript = !resolved.Superscript, false
		}),
		toggle("subscript", "x₂", "Subscript", resolved.Subscript, func(f *canvas.Format) {
			f.Subscript, f.Superscript = !resolved.Subscript, false
		}),
	))

	colorField := func(field, label, value string, set func(*canvas.Format, string)) xango.Node {
		hex := value
		if !isHexColor(hex) {
			hex = map[string]string{"color": "#000000", "background": "#ffff00"}[field]
		}
		return xango.El("label", []xango.Attr{xango.A("class", "gj-styleeditor-color")},
			xango.El("span", nil, xango.Text(label)),
			xango.El("input", disabled([]xango.Attr{
				xango.A("type", "color"), xango.A("value", hex), xango.A("data-field", field),
				xango.On("input", withValue(func(f *canvas.Format, v string) { set(f, v) })),
			})),
			xango.El("button", disabled([]xango.Attr{
				xango.A("type", "button"), xango.A("class", "gj-styleeditor-clear"), xango.A("data-clear", field),
				xango.A("aria-label", "Clear "+strings.ToLower(label)),
				xango.On("click", change(func(f *canvas.Format) { set(f, "") })),
			}), xango.Text("×")),
		)
	}
	rows = append(rows, xango.El("div", []xango.Attr{xango.A("class", "gj-styleeditor-row")},
		colorField("color", "Text color", props.Format.Color, func(f *canvas.Format, v string) { f.Color = v }),
		colorField("background", "Highlight", props.Format.Background, func(f *canvas.Format, v string) { f.Background = v }),
	))

	text := props.PreviewText
	if text == "" {
		text = "The quick brown fox jumps over the lazy dog"
	}
	rows = append(rows, xango.El("div", []xango.Attr{
		xango.A("class", "gj-styleeditor-preview"), xango.A("data-preview", "true"), xango.A("style", resolved.CSS()),
	}, xango.Text(text)))

	return xango.El("div", []xango.Attr{xango.A("class", "gj-styleeditor")}, rows...)
}

func primaryFamily(css string) string {
	first, _, _ := strings.Cut(css, ",")
	first = strings.TrimSpace(first)
	if len(first) >= 2 && (first[0] == '"' || first[0] == '\'') && first[len(first)-1] == first[0] {
		first = first[1 : len(first)-1]
	}
	return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(first)
}

func isHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

func roundTo(v float64, places int) float64 {
	p := 1.0
	for range places {
		p *= 10
	}
	return float64(int(v*p+0.5)) / p
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
