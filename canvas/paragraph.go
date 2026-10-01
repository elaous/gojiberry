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
	// Marker is the bullet character for ListBullet (default: • ◦ ▪ by
	// level), or the number format for ListOrdered, with "{n}" for the
	// number (default "{n}."): "{n})", "({n})", "{n}:".
	Marker string
	// Number is how an ordered list counts: 1, i, I, a, A.
	Number PageNumberStyle
	// Start restarts an ordered list at this number (0 continues, or
	// starts at 1).
	Start int
	// Checked is the state of a ListCheck item.
	Checked bool
}

// ParaFormat is the formatting of one paragraph (the text up to a '\n').
// It is an item of the content stream like ColumnConfig, and applies to
// the paragraph that contains the offset it sits at: put it at the start
// of the paragraph, in front of its first character. Several in one
// paragraph (after two were joined) -- the first wins.
type ParaFormat struct {
	Align Align
	// IndentLeft and IndentRight inset the paragraph's lines from the
	// column; FirstLine moves the first line further in (or, negative,
	// out: a hanging indent).
	IndentLeft, IndentRight, FirstLine float64
	// SpaceBefore and SpaceAfter are the gaps above and below the
	// paragraph, in px (a gap at the top of a column is kept).
	SpaceBefore, SpaceAfter float64
	// LineSpacing multiplies the line height; zero means 1.
	LineSpacing float64
	List        ListStyle
	// QuoteBar draws a vertical bar of this color to the left of the
	// paragraph (a block quote).
	QuoteBar string
	// DropCap makes the first letter this many lines tall, with the
	// text wrapping around it; zero for none.
	DropCap int
}

func (ParaFormat) flowItem() {}

// IsZero reports whether the format changes nothing.
func (p ParaFormat) IsZero() bool { return p == ParaFormat{} }

// ListIndent is how far each list level (and the first, for its marker)
// is indented.
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

// LineMarker is the bullet, number or check box in front of a list
// item's first line.
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

// next returns the number for an item of list l, updating the counters:
// an ordered item continues its level's count (nested levels restart), a
// bullet or check item leaves the outer counts alone, and a paragraph
// that is not a list item resets everything.
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

// ListPrefix is the marker as plain text with a trailing space: for
// writing a list as Markdown or flattening it into a text box.
func ListPrefix(l ListStyle, number int) string {
	if l.Kind == ListNone {
		return ""
	}
	return strings.Repeat("  ", l.Level) + l.markerText(max(number, 1)) + " "
}
