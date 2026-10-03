package components

import (
	"strconv"
	"strings"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/fonts"
	"github.com/elaous/gojiberry/gox"
)

// StyleEditorProps configures StyleEditor.
type StyleEditorProps struct {
	// Format is the formatting being edited: the overrides applied over
	// Base (zero fields inherit from it).
	Format canvas.Format
	// Base is the style the format is applied over; it gives the values
	// shown for fields the format leaves unset.
	Base canvas.TextStyle
	// OnChange receives the edited format after every change.
	OnChange func(canvas.Format)

	// FontPicker, if set, adds a FontSelector for the family. The editor
	// supplies Selected (from Format) and wraps OnSelect so a choice also
	// updates the format; everything else -- Fonts, Open, Query, Source,
	// the callbacks that keep them -- is yours, as it is for a plain
	// FontSelector.
	FontPicker *FontSelectorProps

	// PreviewText is the sample shown in the resulting style
	// ("The quick brown fox jumps over the lazy dog" by default).
	PreviewText string
	Disabled    bool
}

var styleWeights = []int{100, 200, 300, 400, 500, 600, 700, 800, 900}

// StyleEditor renders controls for a canvas.Format -- font, size,
// weight, bold, italic, underline, strikethrough, superscript, subscript,
// text and highlight colors -- and a live preview of the result. It is
// controlled: it shows props.Format and reports edits through OnChange.
// Superscript and subscript exclude each other; "clear" buttons (and an
// empty size) put a field back to inheriting from Base.
func StyleEditor(props StyleEditorProps) gox.Node {
	resolved := props.Format.Resolve(props.Base)
	if resolved.FontSize <= 0 {
		resolved.FontSize = 16 // what the canvas draws unset sizes at
	}
	change := func(edit func(*canvas.Format)) func(gox.Event) {
		return func(gox.Event) {
			if props.OnChange == nil {
				return
			}
			f := props.Format
			edit(&f)
			props.OnChange(f)
		}
	}
	withValue := func(edit func(f *canvas.Format, v string)) func(gox.Event) {
		return func(e gox.Event) {
			if props.OnChange == nil {
				return
			}
			f := props.Format
			edit(&f, gox.EventValue[string](e))
			props.OnChange(f)
		}
	}
	disabled := func(attrs []gox.Attr) []gox.Attr {
		if props.Disabled {
			attrs = append(attrs, gox.A("disabled", "disabled"))
		}
		return attrs
	}

	toggle := func(action, label, title string, on bool, edit func(*canvas.Format)) gox.Node {
		class := "gj-styleeditor-toggle"
		if on {
			class += " gj-styleeditor-toggle-on"
		}
		attrs := disabled([]gox.Attr{
			gox.A("type", "button"), gox.A("class", class), gox.A("title", title), gox.A("aria-label", title),
			gox.A("aria-pressed", boolAttr(on)), gox.A("data-action", action),
			gox.On("click", change(edit)),
		})
		return gox.El("button", attrs, gox.Text(label))
	}

	var rows []gox.Node

	// Row 1: font family, size, weight.
	var fontRow []gox.Node
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
		gox.El("input", disabled([]gox.Attr{
			gox.A("type", "number"), gox.A("class", "gj-styleeditor-size"), gox.A("min", "1"), gox.A("step", "1"),
			gox.A("aria-label", "Font size"), gox.A("data-field", "size"),
			gox.A("value", strconv.FormatFloat(roundTo(resolved.FontSize, 2), 'f', -1, 64)),
			gox.On("input", withValue(func(f *canvas.Format, v string) {
				if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && n > 0 {
					f.FontSize = n / maxFloat(f.Scale, 1)
				} else if strings.TrimSpace(v) == "" {
					f.FontSize = 0
				}
			})),
		})),
	)
	weightOpts := []gox.Node{gox.El("option", []gox.Attr{gox.A("value", "")}, gox.Text("Weight: inherit"))}
	for _, w := range styleWeights {
		attrs := []gox.Attr{gox.A("value", strconv.Itoa(w))}
		if props.Format.FontWeight == w {
			attrs = append(attrs, gox.A("selected", "selected"))
		}
		weightOpts = append(weightOpts, gox.El("option", attrs, gox.Text(strconv.Itoa(w))))
	}
	fontRow = append(fontRow, gox.El("select", disabled([]gox.Attr{
		gox.A("class", "gj-styleeditor-weight"), gox.A("aria-label", "Font weight"), gox.A("data-field", "weight"),
		gox.On("change", withValue(func(f *canvas.Format, v string) {
			f.FontWeight, _ = strconv.Atoi(v)
		})),
	}), weightOpts...))
	rows = append(rows, gox.El("div", []gox.Attr{gox.A("class", "gj-styleeditor-row")}, fontRow...))

	// Row 2: toggles.
	rows = append(rows, gox.El("div", []gox.Attr{gox.A("class", "gj-styleeditor-row gj-styleeditor-toggles"), gox.A("role", "group")},
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

	// Row 3: colors.
	colorField := func(field, label, value string, set func(*canvas.Format, string)) gox.Node {
		hex := value
		if !isHexColor(hex) {
			hex = map[string]string{"color": "#000000", "background": "#ffff00"}[field]
		}
		return gox.El("label", []gox.Attr{gox.A("class", "gj-styleeditor-color")},
			gox.El("span", nil, gox.Text(label)),
			gox.El("input", disabled([]gox.Attr{
				gox.A("type", "color"), gox.A("value", hex), gox.A("data-field", field),
				gox.On("input", withValue(func(f *canvas.Format, v string) { set(f, v) })),
			})),
			gox.El("button", disabled([]gox.Attr{
				gox.A("type", "button"), gox.A("class", "gj-styleeditor-clear"), gox.A("data-clear", field),
				gox.A("aria-label", "Clear "+strings.ToLower(label)),
				gox.On("click", change(func(f *canvas.Format) { set(f, "") })),
			}), gox.Text("×")),
		)
	}
	rows = append(rows, gox.El("div", []gox.Attr{gox.A("class", "gj-styleeditor-row")},
		colorField("color", "Text color", props.Format.Color, func(f *canvas.Format, v string) { f.Color = v }),
		colorField("background", "Highlight", props.Format.Background, func(f *canvas.Format, v string) { f.Background = v }),
	))

	// Preview.
	text := props.PreviewText
	if text == "" {
		text = "The quick brown fox jumps over the lazy dog"
	}
	rows = append(rows, gox.El("div", []gox.Attr{
		gox.A("class", "gj-styleeditor-preview"), gox.A("data-preview", "true"), gox.A("style", resolved.CSS()),
	}, gox.Text(text)))

	return gox.El("div", []gox.Attr{gox.A("class", "gj-styleeditor")}, rows...)
}

// primaryFamily extracts the first family from a CSS font-family list
// (`"Open Sans", sans-serif` -> `Open Sans`).
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
