package canvas

import "unicode"

// Format overrides a TextFlow's base TextStyle for a run of characters.
// Zero fields inherit from the base style.
type Format struct {
	FontFamily string
	FontSize   float64
	// Scale multiplies the resulting font size (and line height), e.g.
	// 2 for a heading twice the base size; zero means 1.
	Scale      float64
	FontWeight int
	FontStyle  string
	Color      string

	// Decorations; see TextStyle.
	Underline   bool
	Strike      bool
	Superscript bool
	Subscript   bool
	Background  string

	// Case changes how the letters are displayed -- upper, lower, title
	// case, small caps -- without changing the text itself.
	Case CaseKind
	// Style names a character style (see StyleDef) whose formatting this
	// text gets, under anything set directly here.
	Style string
	// Link makes the text a link to this URL: drawn as a link (link
	// color, underlined), with the URL shown on hover.
	Link string
	// Comment is the ID of a comment (see CommentSet) on this text.
	Comment string
}

// CaseKind is how letters are displayed; see Format.Case.
type CaseKind int

const (
	CaseNormal    CaseKind = iota
	CaseUpper              // ALL CAPITALS
	CaseLower              // all lower case
	CaseTitle              // Each Word Capitalised
	CaseSmallCaps          // lower-case letters as smaller capitals
)

// LinkColor is the color of linked text unless its Format sets one.
const LinkColor = "#1a5fb4"

// IsZero reports whether f changes nothing.
func (f Format) IsZero() bool { return f == Format{} }

// Resolve applies f over base: the style text with this format is drawn in.
func (f Format) Resolve(base TextStyle) TextStyle { return f.resolve(base) }

// resolve applies f over base. If the size changes and the base has an
// explicit line height, the line height scales with it.
func (f Format) resolve(base TextStyle) TextStyle {
	s := base
	if f.FontFamily != "" {
		s.FontFamily = f.FontFamily
	}
	if f.FontSize > 0 {
		if base.LineHeight > 0 && base.FontSize > 0 {
			s.LineHeight = base.LineHeight * f.FontSize / base.FontSize
		}
		s.FontSize = f.FontSize
	}
	if f.FontWeight != 0 {
		s.FontWeight = f.FontWeight
	}
	if f.Scale > 0 && f.Scale != 1 {
		if s.LineHeight > 0 {
			s.LineHeight *= f.Scale
		}
		if s.FontSize <= 0 {
			s.FontSize = 16
		}
		s.FontSize *= f.Scale
	}
	if f.FontStyle != "" {
		s.FontStyle = f.FontStyle
	}
	if f.Color != "" {
		s.Color = f.Color
	}
	s.Underline = s.Underline || f.Underline
	s.Strike = s.Strike || f.Strike
	s.Superscript = s.Superscript || f.Superscript
	s.Subscript = s.Subscript || f.Subscript
	if f.Background != "" {
		s.Background = f.Background
	}
	if f.Link != "" {
		if f.Color == "" {
			s.Color = LinkColor
		}
		s.Underline = true
	}
	return s
}

// displayRune is how the letter r is shown under case kind c: the rune
// itself or its replacement (always one rune, so offsets stay put), and
// whether it is a small capital. prevLetter says whether the previous
// character was part of a word (title case capitalises word starts).
func displayRune(c CaseKind, r rune, prevLetter bool) (rune, bool) {
	switch c {
	case CaseUpper:
		return unicode.ToUpper(r), false
	case CaseLower:
		return unicode.ToLower(r), false
	case CaseTitle:
		if !prevLetter {
			return unicode.ToTitle(r), false
		}
		return unicode.ToLower(r), false
	case CaseSmallCaps:
		if unicode.IsLower(r) {
			return unicode.ToUpper(r), true
		}
	}
	return r, false
}

// Item is one element of a TextFlow's content stream: Chars,
// ColumnConfig or Break.
type Item interface{ flowItem() }

// Chars is a run of text with one Format. '\n' starts a new paragraph.
type Chars struct {
	Text   string
	Format Format
}

// ColumnConfig sets the column layout from this point on, until the
// next ColumnConfig: Widths are the column widths left to right (empty
// means one full-width column) separated by Gap. A new config starts a
// new band of the page directly below the text so far (below the tallest
// column of the previous band), like a continuous section break. If the
// widths plus gaps are wider than the page's text area they are scaled
// down to fit.
type ColumnConfig struct {
	Widths []float64
	// Count, with no Widths, makes that many equal columns.
	Count int
	Gap   float64
}

// BreakKind says what kind of break a Break is.
type BreakKind int

const (
	// BreakNatural is the break the layout itself makes when text would
	// overflow a page. It appears in Layout.Breaks; as an input item it
	// is ignored.
	BreakNatural BreakKind = iota
	// BreakPage is a user-set page break: the flow stops and continues
	// at the top of the next page.
	BreakPage
	// BreakColumn is a user-set column break: the flow continues at the
	// top of the next column, or -- in the last column of a page's text
	// area -- of the next page (like BreakPage).
	BreakColumn
)

func (k BreakKind) String() string {
	switch k {
	case BreakPage:
		return "page"
	case BreakColumn:
		return "column"
	}
	return "natural"
}

// Break forces the flow to stop and continue elsewhere.
type Break struct{ Kind BreakKind }

func (Chars) flowItem()        {}
func (ColumnConfig) flowItem() {}
func (Break) flowItem()        {}

// Transform turns source text -- Markdown, say -- into a TextFlow's
// content stream: Chars with Formats, Inline objects (tables, images,
// rules), ColumnConfigs and Breaks. It is how patterns in the source
// pick the right text format or component. See TextFlow.Transform and
// Markdown.
type Transform interface {
	Transform(src string) []Item
}

// TransformFunc adapts a function to Transform.
type TransformFunc func(src string) []Item

// Transform implements Transform.
func (f TransformFunc) Transform(src string) []Item { return f(src) }
