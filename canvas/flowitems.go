package canvas

import "unicode"

type Format struct {
	FontFamily string
	FontSize   float64
	// Scale multiplies the font size and line height.
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

	// Case changes letter display: upper, lower, title or small caps.
	Case CaseKind
	// Style names a character style whose formatting this text gets.
	Style string
	// Link makes the text a link to this URL.
	Link     string
	Comment  string
	Bookmark string
	Index    string
	Lang     string
	// Proposal marks text that is part of a suggested change.
	Proposal string
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

const LinkColor = "#1a5fb4"

// IsZero reports whether f changes nothing.
func (f Format) IsZero() bool { return f == Format{} }

// Resolve applies f over base.
func (f Format) Resolve(base TextStyle) TextStyle { return f.resolve(base) }

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

type Item interface{ flowItem() }

type Chars struct {
	Text   string
	Format Format
}

// ColumnConfig sets the column layout from this point on.
type ColumnConfig struct {
	Widths []float64
	// Count, with no Widths, makes that many equal columns.
	Count int
	Gap   float64
}

// BreakKind says what kind of break a Break is.
type BreakKind int

const (
	// BreakNatural is a layout-made break at a full page.
	BreakNatural BreakKind = iota
	// BreakPage is a user-set page break.
	BreakPage
	// BreakColumn is a user-set column break.
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

// Transform turns source text, such as Markdown, into content items.
type Transform interface {
	Transform(src string) []Item
}

type TransformFunc func(src string) []Item

func (f TransformFunc) Transform(src string) []Item { return f(src) }
