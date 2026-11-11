package canvas

import (
	"strconv"
	"strings"
)

// Font styles for TextStyle.FontStyle.
const (
	StyleNormal  = "normal"
	StyleItalic  = "italic"
	StyleOblique = "oblique"
)

// TextStyle holds the text metrics a TextFlow lays out with.
type TextStyle struct {
	FontFamily string  // CSS family list, e.g. `Georgia, "Times New Roman", serif`
	FontSize   float64 // px
	FontWeight int     // CSS weight, 100-900 (400 regular, 700 bold)
	FontStyle  string  // StyleNormal, StyleItalic or StyleOblique
	LineHeight float64 // px between baselines; 0 means 1.4 x FontSize
	Color      string  // CSS color

	// Decorations.
	Underline   bool
	Strike      bool
	Superscript bool
	Subscript   bool
	Background  string // CSS color
}

// DefaultTextStyle is 16px regular sans-serif on a 22px line.
func DefaultTextStyle() TextStyle {
	return TextStyle{FontFamily: "sans-serif", FontSize: 16, FontWeight: 400, FontStyle: StyleNormal, LineHeight: 22, Color: "#2e3338"}
}

func (s TextStyle) Font() string {
	style, weight, size, family := s.FontStyle, s.FontWeight, s.drawSize(), s.FontFamily
	if style == "" {
		style = StyleNormal
	}
	if weight == 0 {
		weight = 400
	}
	if size <= 0 {
		size = 16
	}
	if family == "" {
		family = "sans-serif"
	}
	return style + " " + strconv.Itoa(weight) + " " + strconv.FormatFloat(size, 'f', -1, 64) + "px " + family
}

func (s TextStyle) drawSize() float64 {
	size := s.FontSize
	if size <= 0 {
		size = 16
	}
	if s.Superscript || s.Subscript {
		size *= 0.7
	}
	return size
}

func (s TextStyle) baselineShift() float64 {
	size := s.FontSize
	if size <= 0 {
		size = 16
	}
	switch {
	case s.Superscript:
		return -0.35 * size
	case s.Subscript:
		return 0.15 * size
	}
	return 0
}

func (s TextStyle) Leading() float64 {
	if s.LineHeight > 0 {
		return s.LineHeight
	}
	size := s.FontSize
	if size <= 0 {
		size = 16
	}
	return 1.4 * size
}

func (s TextStyle) CSS() string {
	var b strings.Builder
	decl := func(k, v string) { b.WriteString(k + ":" + v + ";") }
	if s.FontFamily != "" {
		decl("font-family", s.FontFamily)
	}
	decl("font-size", strconv.FormatFloat(s.drawSize(), 'f', -1, 64)+"px")
	if s.FontWeight != 0 {
		decl("font-weight", strconv.Itoa(s.FontWeight))
	}
	if s.FontStyle != "" {
		decl("font-style", s.FontStyle)
	}
	if s.LineHeight > 0 {
		decl("line-height", strconv.FormatFloat(s.LineHeight, 'f', -1, 64)+"px")
	}
	if s.Color != "" {
		decl("color", s.Color)
	}
	if s.Background != "" {
		decl("background-color", s.Background)
	}
	var lines []string
	if s.Underline {
		lines = append(lines, "underline")
	}
	if s.Strike {
		lines = append(lines, "line-through")
	}
	if len(lines) > 0 {
		decl("text-decoration", strings.Join(lines, " "))
	}
	switch {
	case s.Superscript:
		decl("vertical-align", "super")
	case s.Subscript:
		decl("vertical-align", "sub")
	}
	return b.String()
}
