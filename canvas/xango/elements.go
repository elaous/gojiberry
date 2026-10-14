// Package xango draws xango trees on a canvas.Stage (import path canvas/xango): the fourth
// renderer after HTML (xango.Render), the DOM (domrt) and Gio (xango/gio).
//
// The elements here are constructors with the shape every xango component
// has -- Name(NameProps) xango.Node -- so .xg files use them as tags:
//
//	import canvas "github.com/elaous/gojiberry/canvas/xango"
//
//	<canvas.Canvas width={800} height={600}>
//		<canvas.TextFlow source={doc} pageCount={3} margins={[]float64{60, 50}}
//			pages={func(page xango.Node, n int) xango.Node {
//				return <>
//					<canvas.Header text="Quarterly report" />
//					{page}
//					<canvas.Footer><canvas.PageNumber format="Page {n}" /></canvas.Footer>
//				</>
//			}} />
//	</canvas.Canvas>
//
// Each constructor returns a xango.Host node carrying its typed props.
// A Root turns the tree into live canvas components and, on later
// renders, updates the same components in place, so selection, caret,
// scroll position and open menus survive a re-render. See Root.
package xango

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/xango"
)

// Host tags.
const (
	TagCanvas       = "canvas.Canvas"
	TagTextFlow     = "canvas.TextFlow"
	TagPage         = "canvas.Page"
	TagHeader       = "canvas.Header"
	TagFooter       = "canvas.Footer"
	TagPageNumber   = "canvas.PageNumber"
	TagWatermark    = "canvas.Watermark"
	TagBackground   = "canvas.Background"
	TagImage        = "canvas.Image"
	TagFloatingText = "canvas.FloatingText"
	TagTable        = "canvas.Table"
	TagScrollView   = "canvas.ScrollView"
)

// CanvasProps is the root element: the stage's size and mode.
type CanvasProps struct {
	Width, Height float64
	// Mode is ModeView (default) or ModeEdit for every component below.
	Mode canvas.Mode
	// Role is what the user may do (the zero value is the owner); a
	// viewer's canvas is held in view mode whatever Mode says.
	Role canvas.Role
	// Features replaces the editing features registered on the stage
	// (nil keeps canvas.DefaultFeatures): list only what the document
	// should have, plus your own.
	Features []canvas.Feature
	Children []xango.Node
}

// Canvas is the root of a tree.
func Canvas(p CanvasProps) xango.Node { return xango.Host(TagCanvas, p, p.Children...) }

// TextFlowProps is a text flow running across a set of pages: it is a
// canvas.PageSet (the sheets, headers, footers and page numbers) and the
// canvas.TextFlow that fills them.
type TextFlowProps struct {
	Key string

	// X, Y and Width place the pages in the canvas; with a Width the
	// sheets are centred in it.
	X, Y, Width float64

	// Content: Source with a Transform (Markdown, say), or Items, or Text.
	Source    string
	Transform canvas.Transform
	Items     []canvas.Item
	Text      string
	Style     *canvas.TextStyle

	// Styles are the named styles Format.Style and ParaFormat.Style refer
	// to (canvas.DefaultStyles is a starting set).
	Styles []canvas.StyleDef
	// AutoClose maps openers to closers (canvas.DefaultAutoClose is the
	// code-editor set); Complete, Completer or Vocabulary turn on the
	// word-completion popup.
	AutoClose map[rune]rune
	// TextScale multiplies the size of all the text (see canvas.TextFlow.TextScale); 0 or 1 is as it is.
	TextScale float64
	// StrongCaret draws a thicker caret and a stronger selection (see canvas.TextFlow.StrongCaret).
	StrongCaret bool
	// PeerPalette recolours the remote cursors (see canvas.TextFlow.PeerPalette).
	PeerPalette []string
	// Comments are the comments Format.Comment refers to (share the set
	// with the DOM side to list them); Author is who new ones are by.
	Comments *canvas.CommentSet
	// Footnotes holds the notes the footnote marks in the text refer to;
	// FootnoteBand is the room kept at the bottom of each page for them.
	Footnotes      *canvas.FootnoteSet
	FootnoteBand   float64
	Author         string
	OnHoverComment func(id string)
	Complete       bool
	Completer      canvas.Completer
	Vocabulary     []string

	Columns []float64
	Gap     float64

	// Spread lays the sheets out two to a row as facing pages; SpreadBook
	// leaves the first one alone on the right; Gutter is the space
	// between facing sheets.
	Spread, SpreadBook bool
	Gutter             float64

	// PageCount is how many sheets there are (default 1); PageWidth and
	// PageHeight their size (default 595 x 842, A4 in points).
	PageCount             int
	PageWidth, PageHeight float64
	// Margins is CSS-style: one value for all sides, two for
	// (top and bottom, left and right), or four for (top, right, bottom,
	// left). Default 50 on every side.
	Margins []float64

	// Pages is called once per sheet with a slot node for the sheet
	// itself and its index, and returns the sheet's furniture around the
	// slot: a Header, a Footer and a PageNumber (directly or in the
	// Footer). Whatever one sheet returns equal to the one before it
	// *inherits* instead of setting its own, as in canvas.Page, so
	// editing a header on the canvas keeps the same "this page and the
	// following ones" meaning. An element left out where the previous
	// sheet had one removes it from here on; set None to say so
	// explicitly.
	Pages func(page xango.Node, index int) xango.Node

	// OnChange is called when columns are resized by dragging and after
	// every edit to the content.
	OnChange func(*canvas.TextFlow)
	// OnLink opens a clicked link (the Stage opens it in a new browser
	// tab if this is nil); OnHoverLink hears the URL under the pointer,
	// "" when it leaves -- for a link preview in the DOM.
	OnLink      func(url string)
	OnHoverLink func(url string)

	// Hyphenator, if set, breaks words that do not fit at their hyphenation
	// points (hyphen.English() is one).
	Hyphenator canvas.Hyphenator
}

// TextFlow is a text flow across pages.
func TextFlow(p TextFlowProps) xango.Node { return xango.Host(TagTextFlow, p).WithKey(p.Key) }

// PageProps describes the sheet slot passed to TextFlowProps.Pages.
type PageProps struct {
	Index, Count int
}

// Page is the slot for a sheet; Pages receives one already built.
func Page(p PageProps) xango.Node { return xango.Host(TagPage, p) }

// HeaderProps is a page header. Text may also come from text children.
type HeaderProps struct {
	Text   string
	Style  *canvas.TextStyle
	Align  canvas.Align
	Offset float64
	// Parts are the left, centre and right parts of one line; when any is set they are used instead of
	// Text.
	Parts [3]string
	// None removes the header from this page on.
	None     bool
	Children []xango.Node
}

// Header is a page header.
func Header(p HeaderProps) xango.Node { return xango.Host(TagHeader, p, p.Children...) }

// FooterProps is a page footer: text (or text children) and, usually, a
// PageNumber child.
type FooterProps struct {
	Text   string
	Style  *canvas.TextStyle
	Align  canvas.Align
	Offset float64
	// Parts are the left, centre and right parts of one line; when any is set they are used instead of
	// Text.
	Parts    [3]string
	None     bool
	Children []xango.Node
}

// Footer is a page footer.
func Footer(p FooterProps) xango.Node { return xango.Host(TagFooter, p, p.Children...) }

// PageNumberProps is the page number. Format uses "{n}" and "{total}".
type PageNumberProps struct {
	Format string
	Style  canvas.PageNumberStyle
	// Start is the number this page gets when it begins (or restarts)
	// numbering; zero continues the count, or, when numbering begins
	// here, uses the sheet's position (page 2 prints 2).
	Start     int
	Align     canvas.Align
	TextStyle *canvas.TextStyle
	// None switches numbering off from this page on.
	None bool
}

// PageNumber is a page number.
func PageNumber(p PageNumberProps) xango.Node { return xango.Host(TagPageNumber, p) }

// ImageProps is a picture the text can wrap around.
type ImageProps struct {
	Key string
	// Source is the bitmap (see canvas.LoadImage in the browser).
	Source canvas.ImageSource
	X, Y   float64
	// Width and Height scale the picture; zero keeps the natural size.
	Width, Height float64
	// Anchor pins the picture to a page (X, Y are then relative to its
	// sheet); the page set is that of the TextFlow before it.
	Anchor *canvas.Anchor
	Wrap   bool
	// Inline makes it a character-like object instead of a floating one.
	Inline bool
	// Opacity is 0..1; zero means opaque.
	Opacity  float64
	Clip     canvas.Insets
	OnChange func(*canvas.Image)
	// Alt describes the picture for a person who cannot see it; Decorative says it needs no
	// description (see canvas.Image).
	Alt        string
	Decorative bool
}

// Image is a floating or inline picture.
func Image(p ImageProps) xango.Node { return xango.Host(TagImage, p).WithKey(p.Key) }

// FloatingTextProps is an editable text box that text can wrap around.
type FloatingTextProps struct {
	Key  string
	Text string
	X, Y float64
	// Width and Height default to 190 x 96.
	Width, Height float64
	Anchor        *canvas.Anchor
	Wrap          bool
	Background    string
	// Opacity is 0..1 for the whole box; zero means opaque.
	Opacity float64
	// Vocabulary adds words the completion may suggest.
	Vocabulary []string
	OnChange   func(*canvas.FloatingText)
}

// FloatingText is an editable, floating text box.
func FloatingText(p FloatingTextProps) xango.Node {
	return xango.Host(TagFloatingText, p).WithKey(p.Key)
}

// TableProps is an editable table that can float with text wrapped
// around it.
type TableProps struct {
	Key string
	// Cells are the rows of cells; changing them replaces the table.
	Cells [][]string
	// Merges and Styles are the table's merged cells and per-cell formatting;
	// like Cells, changing them replaces what the table has.
	Merges   []canvas.Merge
	Styles   map[canvas.CellPos]canvas.CellStyle
	X, Y     float64
	Anchor   *canvas.Anchor
	Wrap     bool
	OnChange func(*canvas.Table)
}

// Table is an editable table.
func Table(p TableProps) xango.Node { return xango.Host(TagTable, p).WithKey(p.Key) }

// ScrollViewProps is a clipping, scrolling viewport over its children.
type ScrollViewProps struct {
	Key                 string
	X, Y, Width, Height float64
	// ContentWidth and ContentHeight are the scrollable extent; zero
	// takes it from the children (a TextFlow's pages, a table).
	ContentWidth, ContentHeight float64
	LockH, LockV                bool
	Children                    []xango.Node
}

// ScrollView is a scroll container.
func ScrollView(p ScrollViewProps) xango.Node {
	return xango.Host(TagScrollView, p, p.Children...).WithKey(p.Key)
}

// WatermarkProps is text or a picture drawn faintly across a sheet's
// middle, behind the text. Returned from a Pages template it is inherited
// by later sheets like a Header; None removes it from this sheet on.
type WatermarkProps struct {
	Text    string
	Style   *canvas.TextStyle
	Color   string
	Image   canvas.ImageSource
	Scale   float64
	Opacity float64
	// Angle is in degrees clockwise (text defaults to -45); Straight
	// makes text level.
	Angle    float64
	Straight bool
	None     bool
}

// Watermark is a page watermark.
func Watermark(p WatermarkProps) xango.Node { return xango.Host(TagWatermark, p) }

// BackgroundProps is a picture filling a sheet. Inherited like a Header.
type BackgroundProps struct {
	Image   canvas.ImageSource
	Opacity float64
	Fit     canvas.BackgroundFit
	None    bool
}

// Background is a page background picture.
func Background(p BackgroundProps) xango.Node { return xango.Host(TagBackground, p) }
