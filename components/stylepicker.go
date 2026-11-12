package components

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/xango"
)

// StylePickerProps configures StylePicker.
type StylePickerProps struct {
	Styles  []canvas.StyleDef
	Base    canvas.TextStyle
	Current string
	OnPick  func(name string, paragraph bool)
}

func StylePicker(props StylePickerProps) xango.Node {
	base := props.Base
	if base.FontSize <= 0 {
		base.FontSize = 16
	}
	if base.FontFamily == "" {
		base.FontFamily = "sans-serif"
	}
	entry := ""
	for _, s := range props.Styles {
		if s.Name == props.Current {
			entry = s.Name
		}
	}
	if entry == "" {
		for _, para := range []bool{true, false} {
			for _, s := range props.Styles {
				if entry == "" && s.Paragraph == para {
					entry = s.Name
				}
			}
		}
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
			if s.Name == entry {
				attrs = append(attrs, xango.A("tabindex", "0"))
			} else {
				attrs = append(attrs, xango.A("tabindex", "-1"))
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
	return xango.El("div", []xango.Attr{xango.A("class", "gj-stylepicker"), gridKeys(".gj-stylepicker", ".gj-stylepicker-item")},
		section("Paragraph styles", true), section("Character styles", false))
}
