package canvas

import (
	"slices"
)

// Obstacle is something text must flow around (see Image). Exclusion
// reports, for the horizontal band y0..y1 (canvas coordinates), the
// x-range text must stay out of; ok is false if the band is clear.
type Obstacle interface {
	Exclusion(y0, y1 float64) (x0, x1 float64, ok bool)
}

// FlowStyle is the chrome TextFlow draws in edit mode, and its page.
type FlowStyle struct {
	PageBG          string
	PageBorder      string
	GuideColor      string // line down each column gap
	GripColor       string
	GripActiveColor string
	GripW, GripH    float64 // size of the draggable grip above each gap
	SelectColor     string  // tint over selected text
	// FindColor tints the matches of a search, FindCurrentColor the one that is
	// current (see TextFlow.SetSearch).
	FindColor, FindCurrentColor string
}

// DefaultFlowStyle is a light look matching DefaultTableStyle.
func DefaultFlowStyle() FlowStyle {
	return FlowStyle{
		PageBG:           "#ffffff",
		PageBorder:       "#d4d6da",
		GuideColor:       "#b9a9ff",
		GripColor:        "#7b818b",
		GripActiveColor:  "#7c5cff",
		GripW:            16,
		GripH:            16,
		SelectColor:      "rgba(124, 92, 255, 0.28)",
		FindColor:        "rgba(255, 213, 0, 0.45)",
		FindCurrentColor: "rgba(255, 140, 0, 0.6)",
	}
}

// Run is a stretch of a Line in one font and color.
type Run struct {
	Text       string
	X, W       float64 // offset from the line's X, and measured width
	Font       string  // CSS font
	Color      string
	Start, End int // rune offsets into the text stream

	// Decorations. DY is the baseline shift of a superscript (negative)
	// or subscript (positive); Size is the size the run is drawn at.
	Underline, Strike bool
	Background        string
	DY, Size          float64
	// Link is the URL the run links to, if any.
	Link string
	// Comment is the ID of the comment on the run, if any.
	Comment string
	// Level is the bidirectional embedding level: odd for right-to-left text, which is drawn in the browser as
	// a string of its own and measured from the right edge of the run.
	Level uint8
}

// Line is one laid-out line of text. Start/End are rune offsets into
// the text stream (all Chars items concatenated; End exclusive, trailing
// spaces excluded). A blank paragraph is a Line with no text.
type Line struct {
	Text       string
	X, Y, W, H float64 // left edge, top of the line box, measured text width, line height
	Baseline   float64 // y to draw the runs' text at
	Page, Col  int
	Start, End int
	ColX, ColW float64 // the column the line is in
	Runs       []Run
	Objects    []PlacedObject // inline objects on this line
	// Marker is the list marker, on the first line of a list item.
	Marker *LineMarker
	// Hyphen is true when the line ends inside a word that a Hyphenator broke:
	// a "-" of width HyphenW is drawn after the last run (and counted in W).
	Hyphen  bool
	HyphenW float64
	// QuoteBar is the color of a block quote's bar (drawn at BarX).
	QuoteBar string
	BarX     float64
	// DropCap is the large initial of a paragraph, on its first line.
	DropCap *DropCapMark
	// RTL is true when the paragraph's direction is right-to-left: the line is aligned to the right by default
	// and its marker is on the right. Bidi is true when the line has text in more than one direction, so its
	// runs are not in the order of the text: positions are found through the runs (lineCaretX, lineRects).
	RTL, Bidi bool
}

// DropCapMark is a paragraph's large first letter. The line's Start is
// just after it: the letter is not part of the line's Text or Runs.
type DropCapMark struct {
	Text        string
	X, Baseline float64
	Font, Color string
}

// BreakMark records a break the layout honoured or made: a
// BreakNatural when text overflowed Page's last column, or the user's
// BreakPage/BreakColumn. Page and Col are where the break happened;
// Offset is the text-stream offset the flow resumed at.
type BreakMark struct {
	Kind      BreakKind
	Page, Col int
	Offset    int
}

// Band is a stretch of a page's text area using one column layout: the
// first of a page, or one started by a ColumnConfig.
type Band struct {
	Page        int
	Top, Bottom float64   // vertical extent actually used
	Xs, Widths  []float64 // each column's left edge and width
	Gap         float64
	Avail       float64 // the text area's width
	Cfg         int     // index of the ColumnConfig in Items, or -1 for the base Columns/Gap
}

// Layout is the result of flowing text into pages.
type Layout struct {
	Lines  []Line
	Breaks []BreakMark
	Bands  []Band
	// Overflow is true if the text didn't all fit in the pages; End is
	// the text-stream offset where layout stopped (the stream's length
	// when it all fit).
	Overflow bool
	End      int
	// Pages is how many pages the text reached (always at least 1).
	Pages int
}

// TextFlow lays out a content stream into pages, word-wrapping it using
// the Style's font metrics, in columns, around Obstacles such as a
// wrapped Image, and across any number of pages.
//
// The content is Items (or just Text, for a single unformatted run):
// Chars (text with a Format), ColumnConfig (the column widths and gap
// from here on -- they persist until the next one) and Break (a page or
// column break). Pages is the sheets to flow into; with no Pages the
// flow fills the single page rectangle (X, Y, Width, Height). The flow
// is continuous: when a page's last column is full the text carries on
// at the top of the next page (a natural break); a user BreakPage or
// BreakColumn forces it to move on early; if it runs out of pages
// Layout.Overflow is set.
//
// The base column layout is Columns (widths, default one full-width
// column) and Gap. In edit mode a grip above each column gap can be
// dragged with the mouse to resize the two neighbouring columns (their
// combined width is preserved); it edits whichever ColumnConfig (or the
// base) that band uses. View mode just draws the text.
type TextFlow struct {
	Text  string
	Items []Item
	Style TextStyle
	// TextScale multiplies the size and line height of every run (1 or 0 leave them as they are): a person's
	// text-size setting applied to the whole text, whatever sizes its runs have. The layout is worked out again
	// when it changes.
	TextScale float64
	// Ghost is a suggestion drawn faintly at the primary caret while editing: the rest of the word being typed
	// ("" none). It is a picture only, in the font and size of the text it continues. At the end of a line it
	// continues the line; in the middle of one it is a small tag under the caret, so that it never hides the
	// words after it. Putting it in is the host's job (see the showcase's Tab).
	Ghost string
	// preedit is the text an input method is composing, drawn at the caret (see Composer).
	preedit string
	// reading is the sentence and the word being read aloud (readmarks.go).
	reading readMarks
	// Vertical lays the text out in vertical lines (see vertical.go): top to bottom, the lines following one
	// another from right to left.
	Vertical bool
	vpage    int  // the page being drawn, in a vertical flow
	vdrawing bool // a vertical flow is drawing one page
	// LinkColor is the colour of linked text whose format names none ("" the default, LinkColor). A document
	// theme sets it (see FlowTheme).
	LinkColor string
	// StrongCaret draws the caret thicker, dark with a light edge, and the selection in a stronger tint: for
	// people who lose sight of the thin default caret (the high-contrast setting turns it on).
	StrongCaret bool
	// PeerPalette, when set, gives the remote cursors and selections the palette's colours instead of the ones
	// their owners chose (see ColourBlindPalette).
	PeerPalette []string

	// Transform, if set, turns Source into the content (Items and Text
	// are then ignored): see Transform and Markdown. The result is
	// cached until Source changes; call InvalidateTransform after
	// changing the transform's own settings.
	Source    string
	Transform Transform

	// Pages, if set, supplies the page rectangles (see PageSet); X, Y,
	// Width and Height are then ignored.
	Pages         *PageSet
	X, Y          float64
	Width, Height float64

	// Styles are the named styles Format.Style and ParaFormat.Style refer
	// to (DefaultStyles is a starting set).
	Styles []StyleDef

	// AutoClose maps each opening character to its closer (nil: none;
	// DefaultAutoClose is the code-editor set). Typing an opener inserts
	// both, typing the closer in front of one steps over it, and an opener
	// typed over a selection wraps it -- at every cursor.
	AutoClose map[rune]rune
	// Complete turns on the word-completion popup (for the primary
	// cursor); so does setting Completer or Vocabulary.
	Complete bool
	// Completer suggests completions for the word before the caret (nil
	// means the words already in the text and in Vocabulary).
	Completer  Completer
	Vocabulary []string

	Columns []float64
	Gap     float64
	// MinColumnWidth stops a drag from collapsing a column (default 40).
	MinColumnWidth float64

	ChromeStyle FlowStyle
	Painter     Painter
	// Transparent skips drawing the single-page background (for a flow
	// inside another component).
	Transparent bool

	// OnChange is called when the columns are resized by dragging, and
	// after every edit to the content.
	OnChange func(*TextFlow)
	// OnLink is called to open a link (see Format.Link) that was clicked.
	OnLink func(url string)
	// OnHoverLink is called with the URL of the link the pointer moves
	// onto, and with "" when it leaves.
	OnHoverLink func(url string)
	// Comments holds the comments Format.Comment refers to; the text they
	// are on is highlighted and, hovered, shows a tip with the summary.
	Comments *CommentSet
	// RemoteCursors are other people's cursors, drawn with a name flag.
	RemoteCursors []RemoteCursor
	// ObjectBusy marks inline objects somebody is changing (see Busy); the host keeps it up to date.
	ObjectBusy map[InlineObject]Busy
	// UndoHook, if set, handles Undo and Redo (redo says which) instead of
	// the flow's own snapshot history: the collab bridge uses it to undo only
	// your own changes while others edit too.
	UndoHook func(redo bool) bool
	// Footnotes holds the notes FootnoteRef marks in the text refer to;
	// FootnoteBand is the most a page gives its notes (zero: 40 % of the page's text area); a page gives them
	// what they need up to that, and the rest of a long note goes on at the foot of the next page.
	Footnotes    *FootnoteSet
	FootnoteBand float64
	fnBands      []float64  // the room each page keeps for its notes, from the last layout
	fnLines      [][]fnLine // the lines of notes each page shows
	// OnFootnote is called with the ID of a note whose text on the page was pressed; the host opens its editor (see
	// components.NoteEditor). Nil: pressing a note does nothing.
	OnFootnote func(id string)
	// Author is who new comments are by (a name or e-mail address).
	Author string
	// OnHoverComment is called with the ID of the comment the pointer
	// moves onto ("" when it leaves).
	OnHoverComment func(id string)
	// Suggesting makes typing, pasting and deleting propose changes instead of making them: new text
	// is marked as a suggested insertion and deleted text is kept, struck through, as a suggested
	// deletion, by Author, until someone accepts or rejects it (see Suggestions).
	Suggesting bool

	derived    []Item
	derivedSrc string
	derivedOK  bool
	// objDrag is the inline object being dragged (a table's grip), with where its top-left corner is.
	objDrag            DragObject
	objDragX, objDragY float64

	sel     flowSel
	formats *FormatClipboard

	// editing state (see flowedit.go)
	editing  bool     // the caret is in the text (edit mode, after a click)
	caretOff bool     // blink: the caret is hidden right now
	extra    []Cursor // further cursors; sel is the primary one
	pending  Format   // format for the next typed text when nothing is selected
	edited   bool     // Items hold the content now (see materialize)
	hist     snapshotHistory[flowSnap]
	histKind string // the kind of the change being made, for merging typing runs

	obstacles []Obstacle
	mode      Mode
	hover     grip
	drag      *flowDrag
	data      *flowData // from the latest Layout, for caret queries

	stageLink     func(url string)
	hoverLink     string
	hoverComment  string
	listeners     []func(*TextFlow)
	revealAt      int
	revealPending bool
	lastLay       Layout        // the layout of the latest Draw, for objects that show the document's own structure
	search        searchState   // the matches a search highlights (see SetSearch)
	squiggles     squiggleState // wavy underlines (see SetSquiggles)
	// Hyphenator, if set, breaks words that do not fit at their hyphenation points,
	// drawing a hyphen at the end of the line. See package hyphen.
	Hyphenator     Hyphenator
	hoverCRect     [4]float64
	sugg           *Menu // completion popup
	suggFrom       int   // where the word being completed starts
	viewport       [4]float64
	hoverRect      [4]float64
	hoverX, hoverY float64
}

// NewTextFlow returns a flow of text in a single width x height page at
// (x, y), with DefaultTextStyle, one column, in ModeView.
func NewTextFlow(text string, x, y, width, height float64) *TextFlow {
	return &TextFlow{
		Text: text, Style: DefaultTextStyle(), X: x, Y: y, Width: width, Height: height,
		ChromeStyle: DefaultFlowStyle(), hover: noGrip,
	}
}

// SetPainter implements PainterSetter.
func (f *TextFlow) SetPainter(p ImagePainter) { f.Painter = p }

// Mode returns the current mode.
func (f *TextFlow) Mode() Mode { return f.mode }

// SetMode switches mode; leaving edit mode cancels any drag. Returns
// true if a redraw is needed.
func (f *TextFlow) SetMode(m Mode) bool {
	if m == f.mode {
		return false
	}
	f.mode = m
	if m == ModeView {
		f.drag, f.hover = nil, noGrip
		f.stopEditing()
	}
	return true
}

// AddObstacle makes text flow around o (no-op if already added).
func (f *TextFlow) AddObstacle(o Obstacle) {
	if !slices.Contains(f.obstacles, o) {
		f.obstacles = append(f.obstacles, o)
	}
}

// RemoveObstacle stops text flowing around o.
func (f *TextFlow) RemoveObstacle(o Obstacle) {
	f.obstacles = slices.DeleteFunc(f.obstacles, func(x Obstacle) bool { return x == o })
}

// SetColumnCount makes the base layout n equal columns (minimum 1)
// separated by Gap, sized for the single page (X, Y, Width, Height).
func (f *TextFlow) SetColumnCount(n int) {
	n = max(n, 1)
	w := (f.Width - f.Gap*float64(n-1)) / float64(n)
	f.Columns = make([]float64, n)
	for i := range f.Columns {
		f.Columns[i] = w
	}
}

func (f *TextFlow) minColumn() float64 {
	if f.MinColumnWidth > 0 {
		return f.MinColumnWidth
	}
	return 40
}

func (f *TextFlow) items() []Item {
	if f.Transform != nil && !f.edited {
		if !f.derivedOK || f.derivedSrc != f.Source {
			f.derived, f.derivedSrc, f.derivedOK = f.Transform.Transform(f.Source), f.Source, true
		}
		return f.derived
	}
	if len(f.Items) > 0 {
		return f.Items
	}
	return []Item{Chars{Text: f.Text}}
}

// InvalidateTransform makes the next layout run the Transform again
// (it otherwise reruns only when Source changes).
func (f *TextFlow) InvalidateTransform() { f.derivedOK = false }

func (f *TextFlow) pageCount() int {
	if f.Pages != nil {
		return len(f.Pages.Pages)
	}
	return 1
}

// pageRect is the rectangle text flows into on a page, in the plane the text is laid out in: the page's own
// rectangle, or for vertical text its turned image (vertical.go).
func (f *TextFlow) pageRect(page int) (x, y, w, h float64) {
	px, py, pw, ph := f.physRect(page)
	if !f.Vertical {
		return px, py, pw, ph
	}
	vx, vy := f.virtualOrigin(page)
	_ = py
	return vx, vy, ph, pw
}

// config returns column config idx (-1: the base Columns and Gap).
func (f *TextFlow) config(idx int) ColumnConfig {
	if idx >= 0 {
		if c, ok := f.items()[idx].(ColumnConfig); ok {
			return c
		}
	}
	return ColumnConfig{Widths: f.Columns, Gap: f.Gap}
}

// setConfigWidths writes widths back to config idx (-1: base).
func (f *TextFlow) setConfigWidths(idx int, widths []float64) {
	if idx >= 0 {
		items := f.items()
		if c, ok := items[idx].(ColumnConfig); ok {
			c.Widths = widths
			items[idx] = c
			return
		}
	}
	f.Columns = widths
}

// The colours of the strong caret and selection (TextFlow.StrongCaret).
const (
	StrongCaretColor  = "#000000"
	StrongCaretEdge   = "#ffffff"
	StrongSelectColor = "rgba(0, 80, 200, 0.45)"
)
