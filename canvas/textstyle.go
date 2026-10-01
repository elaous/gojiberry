package canvas

import "strconv"

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

	// Decorations. Superscript and Subscript draw the text at 70% of
	// FontSize, raised or lowered; Background highlights behind it.
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

// Font returns the CSS font shorthand Context2D.font expects, filling
// in defaults for unset fields.
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

// drawSize is the size text is actually drawn at: FontSize, or 70% of
// it for superscripts and subscripts (default 16 if unset).
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

// baselineShift is how far the text is moved down from the baseline (a
// negative value raises it): superscripts rise, subscripts sink.
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

// Leading returns the line height in px, applying the 1.4 x FontSize
// default.
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
