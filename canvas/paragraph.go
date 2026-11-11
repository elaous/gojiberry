package canvas

import "strings"

// ListKind says what kind of list a paragraph is an item of.
type ListKind int

const (
	ListNone    ListKind = iota
	ListBullet           // • item
	ListOrdered          // 1. item
	ListCheck            // ☐ item (click the box to tick it)
)

// ListStyle makes a paragraph a list item.
type ListStyle struct {
	Kind ListKind
	// Level is the nesting depth, from 0.
	Level int
	// Marker is the ListBullet character; the default cycles by level.
	Marker string
	// Number is how an ordered list counts: 1, i, I, a, A.
	Number PageNumberStyle
	// Start restarts an ordered list at this number; zero continues.
	Start int
	// Checked is the state of a ListCheck item.
	Checked bool
}

// ParaFormat is the formatting of one paragraph.
type ParaFormat struct {
	Align Align
	// IndentLeft and IndentRight inset the paragraph's lines.
	IndentLeft, IndentRight, FirstLine float64
	// SpaceBefore and SpaceAfter are gaps above and below the paragraph.
	SpaceBefore, SpaceAfter float64
	// LineSpacing multiplies the line height; zero means 1.
	LineSpacing float64
	List        ListStyle
	// QuoteBar draws a bar of this colour left of the paragraph.
	QuoteBar string
	// Style names a paragraph style whose formatting this paragraph gets.
	Style string
	// DropCap makes the first letter this many lines tall.
	DropCap int
	// CodeLang is the language of a code block.
	CodeLang string
}

func (ParaFormat) flowItem() {}

// IsZero reports whether the format changes nothing.
func (p ParaFormat) IsZero() bool { return p == ParaFormat{} }

// ListIndent is how far each list level indents.
const ListIndent = 24.0

// DefaultBullets are the bullet characters by nesting level.
var DefaultBullets = []string{"•", "◦", "▪"}

// BulletChoices are the characters list.cycle-bullet goes through.
var BulletChoices = []string{"•", "◦", "▪", "–", "★", "➤"}

// NumberFormats are the formats list.cycle-number-format goes through.
var NumberFormats = []struct {
	Style  PageNumberStyle
	Marker string
}{
	{NumArabic, "{n}."}, {NumArabic, "{n})"}, {NumArabic, "({n})"},
	{NumLowerAlpha, "{n}."}, {NumLowerRoman, "{n}."}, {NumUpperRoman, "{n}."}, {NumUpperAlpha, "{n}."},
}

// LineMarker is a list item's bullet, number or check box.
type LineMarker struct {
	Text string
	// X is where it is drawn (left edge), in the line's coordinates.
	X float64
	// Font and Color are the text's.
	Font, Color string
	W           float64
	// Check marks a check box; Checked is its state.
	Check, Checked bool
}

// leftIndent is how far in the paragraph's lines start, list included.
func (p ParaFormat) leftIndent() float64 {
	in := p.IndentLeft
	if p.List.Kind != ListNone {
		in += float64(p.List.Level+1) * ListIndent
	}
	return in
}

func (l ListStyle) bullet() string {
	if l.Marker != "" {
		return l.Marker
	}
	return DefaultBullets[l.Level%len(DefaultBullets)]
}

// markerText writes the marker of a list item with the given number.
func (l ListStyle) markerText(n int) string {
	switch l.Kind {
	case ListBullet:
		return l.bullet()
	case ListCheck:
		if l.Checked {
			return "☑"
		}
		return "☐"
	case ListOrdered:
		format := l.Marker
		if format == "" || !strings.Contains(format, "{n}") {
			format = "{n}."
		}
		return strings.ReplaceAll(format, "{n}", formatPageNumber(n, l.Number))
	}
	return ""
}

// listNumbers tracks the numbers of consecutive ordered list items.
type listNumbers struct{ counters []int }

// next returns the number for an item of list l, updating the counters.
func (n *listNumbers) next(l ListStyle) int {
	if l.Kind == ListNone {
		n.counters = n.counters[:0]
		return 0
	}
	lv := l.Level
	if len(n.counters) > lv+1 {
		n.counters = n.counters[:lv+1]
	}
	if l.Kind != ListOrdered {
		if len(n.counters) > lv {
			n.counters = n.counters[:lv]
		}
		return 0
	}
	for len(n.counters) <= lv {
		n.counters = append(n.counters, 0)
	}
	if l.Start > 0 && n.counters[lv] == 0 {
		n.counters[lv] = l.Start - 1
	}
	n.counters[lv]++
	return n.counters[lv]
}

// ListPrefix is the marker as plain text with a trailing space.
func ListPrefix(l ListStyle, number int) string {
	if l.Kind == ListNone {
		return ""
	}
	return strings.Repeat("  ", l.Level) + l.markerText(max(number, 1)) + " "
}
