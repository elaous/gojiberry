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
	// a viewer's canvas is held in view mode whatever Mode says
	Role     canvas.Role
	Features []canvas.Feature
	Children []xango.Node
}

// Canvas is the root of a tree.
func Canvas(p CanvasProps) xango.Node { return xango.Host(TagCanvas, p, p.Children...) }

type TextFlowProps struct {
	Key string

	X, Y, Width float64

	Source    string
	Transform canvas.Transform
	Items     []canvas.Item
	Text      string
	Style     *canvas.TextStyle

	Styles      []canvas.StyleDef
	AutoClose   map[rune]rune
	TextScale   float64
	StrongCaret bool
	// PeerPalette recolours the remote cursors (see canvas.TextFlow.PeerPalette).
	PeerPalette    []string
	Theme          *canvas.FlowTheme
	Comments       *canvas.CommentSet
	Footnotes      *canvas.FootnoteSet
	FootnoteBand   float64
	Author         string
	OnHoverComment func(id string)
	OnFootnote     func(id string)
	Complete       bool
	Completer      canvas.Completer
	Vocabulary     []string

	Columns []float64
	Gap     float64

	Spread, SpreadBook bool
	Gutter             float64
	OnTurn             func(page int)

	PageCount             int
	PageWidth, PageHeight float64
	Margins               []float64

	Pages func(page xango.Node, index int) xango.Node

	OnChange    func(*canvas.TextFlow)
	OnLink      func(url string)
	OnHoverLink func(url string)

	Hyphenator canvas.Hyphenator
}

// TextFlow is a text flow across pages.
func TextFlow(p TextFlowProps) xango.Node { return xango.Host(TagTextFlow, p).WithKey(p.Key) }

// PageProps describes the sheet slot passed to TextFlowProps.Pages.
type PageProps struct {
	Index, Count int
}

func Page(p PageProps) xango.Node { return xango.Host(TagPage, p) }

// HeaderProps is a page header.
type HeaderProps struct {
	Text   string
	Style  *canvas.TextStyle
	Align  canvas.Align
	Offset float64
	Parts  [3]string
	// None removes the header from this page on.
	None     bool
	Children []xango.Node
}

// Header is a page header.
func Header(p HeaderProps) xango.Node { return xango.Host(TagHeader, p, p.Children...) }

type FooterProps struct {
	Text     string
	Style    *canvas.TextStyle
	Align    canvas.Align
	Offset   float64
	Parts    [3]string
	None     bool
	Children []xango.Node
}

// Footer is a page footer.
func Footer(p FooterProps) xango.Node { return xango.Host(TagFooter, p, p.Children...) }

// PageNumberProps is the page number. Format uses "{n}" and "{total}".
type PageNumberProps struct {
	Format    string
	Style     canvas.PageNumberStyle
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
	Source        canvas.ImageSource
	X, Y          float64
	Width, Height float64
	Anchor        *canvas.Anchor
	Wrap          bool
	Inline        bool
	// Opacity is 0..1; zero means opaque.
	Opacity    float64
	Clip       canvas.Insets
	OnChange   func(*canvas.Image)
	Alt        string
	Decorative bool
}

// Image is a floating or inline picture.
func Image(p ImageProps) xango.Node { return xango.Host(TagImage, p).WithKey(p.Key) }

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

type TableProps struct {
	Key        string
	Cells      [][]string
	Merges     []canvas.Merge
	Styles     map[canvas.CellPos]canvas.CellStyle
	X, Y       float64
	Anchor     *canvas.Anchor
	Wrap       bool
	OnChange   func(*canvas.Table)
	RenderCell func(p canvas.Painter, row, col int, text string, x, y, w, h float64) bool
}

// Table is an editable table.
func Table(p TableProps) xango.Node { return xango.Host(TagTable, p).WithKey(p.Key) }

// ScrollViewProps is a clipping, scrolling viewport over its children.
type ScrollViewProps struct {
	Key                         string
	X, Y, Width, Height         float64
	ContentWidth, ContentHeight float64
	LockH, LockV                bool
	Typewriter                  bool
	TypewriterAt                float64
	TypewriterX                 bool
	Children                    []xango.Node
}

// ScrollView is a scroll container.
func ScrollView(p ScrollViewProps) xango.Node {
	return xango.Host(TagScrollView, p, p.Children...).WithKey(p.Key)
}

type WatermarkProps struct {
	Text     string
	Style    *canvas.TextStyle
	Color    string
	Image    canvas.ImageSource
	Scale    float64
	Opacity  float64
	Angle    float64
	Straight bool
	None     bool
}

// Watermark is a page watermark.
func Watermark(p WatermarkProps) xango.Node { return xango.Host(TagWatermark, p) }

// BackgroundProps is a picture filling a sheet.
type BackgroundProps struct {
	Image   canvas.ImageSource
	Opacity float64
	Fit     canvas.BackgroundFit
	None    bool
}

// Background is a page background picture.
func Background(p BackgroundProps) xango.Node { return xango.Host(TagBackground, p) }
