package main

import (
	cv "github.com/elaous/gojiberry/canvas"
	canvas "github.com/elaous/gojiberry/canvas/xango"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/xango"
)

// runningHeader is the header of sheet n: the print layout's header with its fields filled in for that page
// (the same text the exports print), or the sample's own header when the layout has none.
func runningHeader(state State, n int) xango.Node {
	if state.FirstDiffers && n == 0 {
		return canvas.Header(canvas.HeaderProps{Parts: export.RunningParts(state.FirstHeader, 1, state.Pages, state.DocTitle)})
	}
	if state.RunHeader == "" {
		return canvas.Header(canvas.HeaderProps{Text: "Gojiberry editor"})
	}
	return canvas.Header(canvas.HeaderProps{Parts: export.RunningParts(state.RunHeader, n+1, state.Pages, state.DocTitle)})
}

// runningFooter is the footer of sheet n, likewise; without a footer in the layout the sheet has the sample's
// "Page n of total".
func runningFooter(state State, n int) xango.Node {
	if state.FirstDiffers && n == 0 {
		return canvas.Footer(canvas.FooterProps{Parts: export.RunningParts(state.FirstFooter, 1, state.Pages, state.DocTitle)})
	}
	if state.RunFooter == "" {
		return canvas.Footer(canvas.FooterProps{Children: []xango.Node{canvas.PageNumber(canvas.PageNumberProps{Format: "Page {n} of {total}", Align: cv.AlignRight})}})
	}
	return canvas.Footer(canvas.FooterProps{Parts: export.RunningParts(state.RunFooter, n+1, state.Pages, state.DocTitle)})
}
