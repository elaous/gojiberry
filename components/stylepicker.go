package components

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

// StylePickerProps configures StylePicker.
type StylePickerProps struct {
	// Styles are the named styles to list (canvas.DefaultStyles, or the
	// flow's own).
	Styles []canvas.StyleDef
	// Base is the text style the previews are drawn over (default 16px
	// sans-serif).
	Base canvas.TextStyle
	// Current is the style at the caret, highlighted ("" for none).
	Current string
	// OnPick is called with the chosen style and whether it is a
	// paragraph style.
	OnPick func(name string, paragraph bool)
}

// StylePicker renders a gallery of named styles, each previewed in its
// own formatting -- paragraph styles first, then character styles -- the
// way a word processor's style list does. It is controlled: the caller
// applies the pick (see canvas.Styles for the commands, or
// TextFlow.ApplyParagraph / ApplyFormat with a Style).
func StylePicker(props StylePickerProps) xango.Node {
	base := props.Base
	if base.FontSize <= 0 {
		base.FontSize = 16
	}
	if base.FontFamily == "" {
		base.FontFamily = "sans-serif"
	}
	section := func(title string, paragraph bool) xango.Node {
		var items []xango.Node
		for _, s := range props.Styles {
			if s.Paragraph != paragraph {
				continue
			}
			s := s
			class := "gj-stylepicker-item"
			if s.Name == props.Current {
				class += " gj-stylepicker-item-on"
			}
			ts := s.Format.Resolve(base)
			ts.LineHeight = 0
			attrs := []xango.Attr{
				xango.A("class", class), xango.A("type", "button"), xango.A("data-style", s.Name),
				xango.A("style", ts.CSS()),
			}
			if props.OnPick != nil {
				attrs = append(attrs, xango.On("click", func(xango.Event) { props.OnPick(s.Name, s.Paragraph) }))
			}
			items = append(items, xango.El("button", attrs, xango.Text(s.Name)))
		}
		if len(items) == 0 {
			return xango.Frag()
		}
		return xango.El("div", []xango.Attr{xango.A("class", "gj-stylepicker-section")},
			xango.El("h4", []xango.Attr{xango.A("class", "gj-stylepicker-title")}, xango.Text(title)),
			xango.El("div", []xango.Attr{xango.A("class", "gj-stylepicker-list")}, items...))
	}
	return xango.El("div", []xango.Attr{xango.A("class", "gj-stylepicker")},
		section("Paragraph styles", true), section("Character styles", false))
}
