// Package goxcanvas draws gox trees on a canvas.Stage: the fourth
// renderer after HTML (gox.Render), the DOM (domrt) and Gio (goxgio).
//
// The elements here are constructors with the shape every gox component
// has -- Name(NameProps) gox.Node -- so .gox files use them as tags:
//
//	import canvas "github.com/elaous/gojiberry/canvas/goxcanvas"
//
//	<canvas.Canvas width={800} height={600}>
//		<canvas.TextFlow source={doc} pageCount={3} margins={[]float64{60, 50}}
//			pages={func(page gox.Node, n int) gox.Node {
//				return <>
//					<canvas.Header text="Quarterly report" />
//					{page}
//					<canvas.Footer><canvas.PageNumber format="Page {n}" /></canvas.Footer>
//				</>
//			}} />
//	</canvas.Canvas>
//
// Each constructor returns a gox.Host node carrying its typed props.
// A Root turns the tree into live canvas components and, on later
// renders, updates the same components in place, so selection, caret,
// scroll position and open menus survive a re-render. See Root.
package goxcanvas

import (
	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/gox"
)

// Host tags.
const (
	TagCanvas     = "canvas.Canvas"
	TagTextFlow   = "canvas.TextFlow"
	TagPage       = "canvas.Page"
	TagHeader     = "canvas.Header"
	TagFooter     = "canvas.Footer"
	TagPageNumber = "canvas.PageNumber"
)

// CanvasProps is the root element: the stage's size and mode.
type CanvasProps struct {
	Width, Height float64
	// Mode is ModeView (default) or ModeEdit for every component below.
	Mode canvas.Mode
	// Features replaces the editing features registered on the stage
	// (nil keeps canvas.DefaultFeatures): list only what the document
	// should have, plus your own.
	Features []canvas.Feature
	Children []gox.Node
}

// Canvas is the root of a tree.
func Canvas(p CanvasProps) gox.Node { return gox.Host(TagCanvas, p, p.Children...) }

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

	Columns []float64
	Gap     float64

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
	Pages func(page gox.Node, index int) gox.Node

	// OnChange is called when columns are resized by dragging.
	OnChange func(*canvas.TextFlow)
}

// TextFlow is a text flow across pages.
func TextFlow(p TextFlowProps) gox.Node { return gox.Host(TagTextFlow, p).WithKey(p.Key) }

// PageProps describes the sheet slot passed to TextFlowProps.Pages.
type PageProps struct {
	Index, Count int
}

// Page is the slot for a sheet; Pages receives one already built.
func Page(p PageProps) gox.Node { return gox.Host(TagPage, p) }

// HeaderProps is a page header. Text may also come from text children.
type HeaderProps struct {
	Text   string
	Style  *canvas.TextStyle
	Align  canvas.Align
	Offset float64
	// None removes the header from this page on.
	None     bool
	Children []gox.Node
}

// Header is a page header.
func Header(p HeaderProps) gox.Node { return gox.Host(TagHeader, p, p.Children...) }

// FooterProps is a page footer: text (or text children) and, usually, a
// PageNumber child.
type FooterProps struct {
	Text     string
	Style    *canvas.TextStyle
	Align    canvas.Align
	Offset   float64
	None     bool
	Children []gox.Node
}

// Footer is a page footer.
func Footer(p FooterProps) gox.Node { return gox.Host(TagFooter, p, p.Children...) }

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
func PageNumber(p PageNumberProps) gox.Node { return gox.Host(TagPageNumber, p) }
