package components

import (
	"strings"
	"unicode/utf8"
)

// GhostAction is what a key did to the ghost text.
type GhostAction int

const (
	GhostNone GhostAction = iota
	// the key is not for the ghost text: let it through
	GhostAccept  // insert Suffix at the caret
	GhostDismiss // hide it
)

// GhostState is the suggestion being shown, if any.
type GhostState struct {
	Word, Suffix string
	Prefix       string
	Caret        int
	dismissed    string
	announced    string
}

// Visible says whether there is a suggestion to show.
func (g *GhostState) Visible() bool { return g.Suffix != "" }

func (g *GhostState) Show(prefix, word string, caret int) bool {
	g.Word, g.Suffix, g.Prefix, g.Caret = "", "", prefix, caret
	if prefix == "" || g.dismissed == prefix {
		return false
	}
	pr, wr := []rune(prefix), []rune(word)
	if len(wr) <= len(pr) || !strings.EqualFold(string(wr[:len(pr)]), prefix) {
		return false
	}
	g.Word, g.Suffix = word, string(wr[len(pr):])
	return true
}

// Clear hides the suggestion (the dismissal is kept).
func (g *GhostState) Clear() { g.Word, g.Suffix = "", "" }

func (g *GhostState) Typed(prefix string) {
	if g.dismissed != "" && g.dismissed != prefix {
		g.dismissed = ""
	}
	if prefix == "" {
		g.announced = ""
	}
}

func (g *GhostState) Key(key string) GhostAction {
	if !g.Visible() {
		return GhostNone
	}
	switch key {
	case "Tab":
		return GhostAccept
	case "Escape":
		g.dismissed = g.Prefix
		g.Clear()
		return GhostDismiss
	}
	return GhostNone
}

func (g *GhostState) Accept(caret int) string {
	if !g.Visible() || caret != g.Caret {
		return ""
	}
	s := g.Suffix
	g.Clear()
	g.announced = ""
	return s
}

func (g *GhostState) Announcement() string {
	if !g.Visible() || g.announced == g.Word {
		return ""
	}
	g.announced = g.Word
	return trf("ghost.offer", "Suggestion: %s. Press Tab to accept.", g.Word)
}

func (g *GhostState) Width() int { return utf8.RuneCountInString(g.Suffix) }
