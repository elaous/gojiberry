package canvas

import (
	"slices"
)

// Obstacle is something text must flow around, such as an image.
type Obstacle interface {
	Exclusion(y0, y1 float64) (x0, x1 float64, ok bool)
}

// FlowStyle is the chrome TextFlow draws in edit mode.
type FlowStyle struct {
	PageBG          string
	PageBorder      string
	GuideColor      string // line down each column gap
	GripColor       string
	GripActiveColor string
	GripW, GripH    float64 // size of the draggable grip above each gap
	SelectColor     string  // tint over selected text
	// FindColor tints search matches; FindCurrentColor tints the current one.
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

	// Decorations: DY is the superscript or subscript baseline shift.
	Underline, Strike bool
	Background        string
	DY, Size          float64
	// Link is the URL the run links to, if any.
	Link string
	// Comment is the ID of the comment on the run, if any.
	Comment string
	// Level is the bidi embedding level: odd for right-to-left.
	Level uint8
}

// Line is one laid-out line of text.
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
	// Hyphen is true when the line ends inside a hyphenated word.
	Hyphen  bool
	HyphenW float64
	// QuoteBar is a block quote's bar colour.
	QuoteBar string
	BarX     float64
	// DropCap is the large initial of a paragraph, on its first line.
	DropCap *DropCapMark
	// RTL is true when the paragraph is right-to-left.
	RTL, Bidi bool
}

// DropCapMark is a paragraph's large first letter.
type DropCapMark struct {
	Text        string
	X, Baseline float64
	Font, Color string
}

// BreakMark records a break the layout honoured or made.
type BreakMark struct {
	Kind      BreakKind
	Page, Col int
	Offset    int
}

// Band is a stretch of a page's text area with one column layout.
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
	// Overflow is true if the text did not fit in the pages.
	Overflow bool
	End      int
	// Pages is how many pages the text reached, at least 1.
	Pages int
}

// TextFlow lays out a content stream into pages, word-wrapping it.
type TextFlow struct {
	Text  string
	Items []Item
	Style TextStyle
	// TextScale multiplies the size and line height of every run.
	TextScale float64
	// Ghost is a suggestion drawn faintly at the primary caret.
	Ghost string
	// preedit is the text an input method is composing.
	preedit string
	// reading is the sentence and word being read aloud.
	reading readMarks
	// Vertical lays text out in vertical lines.
	Vertical bool
	vpage    int  // the page being drawn, in a vertical flow
	vdrawing bool // a vertical flow is drawing one page
	// LinkColor is the colour of linked text with no colour of its own.
	LinkColor string
	// StrongCaret draws the caret thicker, with a light edge.
	StrongCaret bool
	// PeerPalette, when set, colours remote cursors and selections.
	PeerPalette []string

	// Transform, if set, turns Source into the content items.
	Source    string
	Transform Transform

	// Pages, if set, supplies the page rectangles.
	Pages         *PageSet
	X, Y          float64
	Width, Height float64

	// Styles are the named styles that formats refer to.
	Styles []StyleDef

	// AutoClose maps each opening character to its closer.
	AutoClose map[rune]rune
	// Complete turns on the word-completion popup at the caret.
	Complete bool
	// Completer suggests completions for the word before the caret.
	Completer  Completer
	Vocabulary []string

	Columns []float64
	Gap     float64
	// MinColumnWidth stops a drag from collapsing a column.
	MinColumnWidth float64

	ChromeStyle FlowStyle
	Painter     Painter
	// Transparent skips drawing the single-page background.
	Transparent bool

	// OnChange runs when a drag resizes the columns.
	OnChange func(*TextFlow)
	// OnLink opens a link clicked in the text.
	OnLink func(url string)
	// OnHoverLink receives the URL under the pointer.
	OnHoverLink func(url string)
	// Comments holds the comments that Format.Comment refers to.
	Comments *CommentSet
	// RemoteCursors are other people's cursors, drawn with names.
	RemoteCursors []RemoteCursor
	// TypedRanges are stretches of text others have just typed.
	TypedRanges []TypedRange
	// ObjectBusy marks inline objects somebody is changing.
	ObjectBusy map[InlineObject]Busy
	// OnTableSelect runs when a table grip is pressed.
	OnTableSelect func(*TableObject, Selection)
	// UndoHook, if set, handles Undo and Redo.
	UndoHook func(redo bool) bool
	// Footnotes holds the notes that FootnoteRef marks refer to.
	Footnotes    *FootnoteSet
	FootnoteBand float64
	fnBands      []float64  // room each page keeps for notes.
	fnLines      [][]fnLine // the lines of notes each page shows
	// OnFootnote receives the ID of a note clicked in the text.
	OnFootnote func(id string)
	// Author is who new comments are by.
	Author string
	// OnHoverComment receives the ID of the hovered comment.
	OnHoverComment func(id string)
	// Suggesting makes typing, pasting and deleting propose changes.
	Suggesting bool

	derived    []Item
	derivedSrc string
	derivedOK  bool
	// objDrag is the inline object being dragged.
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
	// Hyphenator, if set, breaks words that do not fit at hyphens.
	Hyphenator     Hyphenator
	hoverCRect     [4]float64
	sugg           *Menu // completion popup
	suggFrom       int   // where the word being completed starts
	viewport       [4]float64
	hoverRect      [4]float64
	hoverX, hoverY float64
}

// NewTextFlow returns a flow in a single width x height page.
func NewTextFlow(text string, x, y, width, height float64) *TextFlow {
	return &TextFlow{
		Text: text, Style: DefaultTextStyle(), X: x, Y: y, Width: width, Height: height,
		ChromeStyle: DefaultFlowStyle(), hover: noGrip,
	}
}

func (f *TextFlow) SetPainter(p ImagePainter) { f.Painter = p }

// Mode returns the current mode.
func (f *TextFlow) Mode() Mode { return f.mode }

// SetMode switches mode; leaving edit mode cancels any drag.
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

// AddObstacle makes text flow around o.
func (f *TextFlow) AddObstacle(o Obstacle) {
	if !slices.Contains(f.obstacles, o) {
		f.obstacles = append(f.obstacles, o)
	}
}

// RemoveObstacle stops text flowing around o.
func (f *TextFlow) RemoveObstacle(o Obstacle) {
	f.obstacles = slices.DeleteFunc(f.obstacles, func(x Obstacle) bool { return x == o })
}

// SetColumnCount makes the base layout n equal columns, minimum 1.
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

// InvalidateTransform makes the next layout run Transform again.
func (f *TextFlow) InvalidateTransform() { f.derivedOK = false }

func (f *TextFlow) pageCount() int {
	if f.Pages != nil {
		return len(f.Pages.Pages)
	}
	return 1
}

// pageRect is the rectangle text flows into on a page.
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

// Colours of the strong caret and selection.
const (
	StrongCaretColor  = "#000000"
	StrongCaretEdge   = "#ffffff"
	StrongSelectColor = "rgba(0, 80, 200, 0.45)"
)
