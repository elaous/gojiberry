package main

import (
	"github.com/elaous/gojiberry/canvas"
	collab "github.com/elaous/gojiberry/collab/xango"
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/hyphen"
)

// State is everything the canvas tree depends on.
type State struct {
	Mode  canvas.Mode
	Pages int
	// Setup is the paper, orientation and margins of every sheet.
	Setup components.PageSetup
	// NoAutoClose turns off the closing bracket or quote that typing an opener adds (a preference).
	NoAutoClose bool
	// RunHeader and RunFooter are the running header and footer of the print layout (fields {page}, {pages}
	// and {title}, a tab between the left, centre and right parts); empty is the sample's own header and
	// page number. DocTitle fills {title}.
	RunHeader, RunFooter, DocTitle string
	// Spread shows the sheets two to a row, as facing pages.
	Spread bool
	// Source is the document's starting Markdown; once the text is edited
	// on the canvas the canvas owns it, until Source changes.
	Source string
	// Items, when set (a document that was opened), are the content
	// instead of Source.
	Items  []canvas.Item
	Image  canvas.ImageSource
	Note   string
	Styles []canvas.StyleDef
	// Role is what the user may do; a viewer is held in view mode.
	Role canvas.Role
	// Comments are the document's comments; Author is who adds new ones.
	Comments *canvas.CommentSet
	Author   string
	// Footnotes are the notes the footnote marks in the text refer to.
	Footnotes *canvas.FootnoteSet
	// Live, when set (the page was opened on /d/{doc}), makes the text
	// flow a shared document.
	Live *collab.CollabProps
	// FindFeature is the find and replace commands, with the app's own bar
	// behind them.
	FindFeature canvas.FindFeature
	// CompleteFeature is the command that opens the word suggestions.
	CompleteFeature canvas.CompleteFeature
	// Review is the suggestion-mode commands (turn it on, accept, reject).
	Review canvas.Review
	// Hyphenate breaks long words at their hyphenation points.
	Hyphenate bool
}

// Features is what the canvas registers: the defaults, find and replace and
// columns.
func (s State) Features() []canvas.Feature {
	return append(canvas.DefaultFeatures(), s.FindFeature, s.CompleteFeature, s.Review, canvas.Columns{})
}

// Transform is the Markdown transform for Source, or none when the
// content is Items.
func (s State) Transform() canvas.Transform {
	if len(s.Items) > 0 {
		return nil
	}
	return canvas.Markdown{}
}

// Hyphenator is what the text flow breaks words with: English patterns when
// Hyphenate is on, nothing otherwise.
func (s State) Hyphenator() canvas.Hyphenator {
	if s.Hyphenate {
		return hyphen.English()
	}
	return nil
}

// AutoClose is what typing an opening bracket or quote adds: its closer, unless the person turned that off.
func (s State) AutoClose() map[rune]rune {
	if s.NoAutoClose {
		return nil
	}
	return canvas.DefaultAutoClose()
}

// PageWidth and PageHeight are the sheet's size in points, from Setup.
func (s State) PageWidth() float64  { w, _ := s.Setup.Size(); return w }
func (s State) PageHeight() float64 { _, h := s.Setup.Size(); return h }
