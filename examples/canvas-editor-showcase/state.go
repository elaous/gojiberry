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
	Setup                          components.PageSetup
	NoAutoClose                    bool
	TextScale                      float64
	Theme                          *canvas.FlowTheme
	PeerPalette                    []string
	StrongCaret                    bool
	Typewriter                     bool
	TypewriterX                    bool
	RunHeader, RunFooter, DocTitle string
	FirstDiffers                   bool
	FirstHeader, FirstFooter       string
	EvenDiffers                    bool
	EvenHeader, EvenFooter         string
	OutlineCollapsed               []int
	Spread                         bool
	// canvas owns the text after edits, until Source changes
	Source      string
	Items       []canvas.Item
	Image       canvas.ImageSource
	Note        string
	Styles      []canvas.StyleDef
	Role        canvas.Role
	Comments    *canvas.CommentSet
	Author      string
	Footnotes   *canvas.FootnoteSet
	Live        *collab.CollabProps
	FindFeature canvas.FindFeature
	// CompleteFeature is the command that opens the word suggestions.
	CompleteFeature canvas.CompleteFeature
	// Review is the suggestion-mode commands (turn it on, accept, reject).
	Review canvas.Review
	// Hyphenate breaks long words at their hyphenation points.
	Hyphenate bool
}

func (s State) Features() []canvas.Feature {
	return append(canvas.DefaultFeatures(), s.FindFeature, s.CompleteFeature, s.Review, canvas.Columns{})
}

func (s State) Transform() canvas.Transform {
	if len(s.Items) > 0 {
		return nil
	}
	return canvas.Markdown{}
}

func (s State) Hyphenator() canvas.Hyphenator {
	if s.Hyphenate {
		return hyphen.English()
	}
	return nil
}

func (s State) AutoClose() map[rune]rune {
	if s.NoAutoClose {
		return nil
	}
	return canvas.DefaultAutoClose()
}

func (s State) PageWidth() float64  { w, _ := s.Setup.Size(); return w }
func (s State) PageHeight() float64 { _, h := s.Setup.Size(); return h }
