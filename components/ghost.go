package components

import (
	"strings"
	"unicode/utf8"
)

// Ghost text: the single best completion of the word being typed, drawn faintly after the caret.
//
// The rules, all in GhostState so that they are tested without a browser: the suggestion is only a
// picture (aria-hidden) until Tab accepts it; no other key inserts it; Escape dismisses it until the
// next letter is typed; it is announced once, politely, when it first appears or changes, and the
// announcement says that Tab accepts it. It is off unless the person chose it (a preference).

// GhostAction is what a key did to the ghost text.
type GhostAction int

const (
	GhostNone    GhostAction = iota // the key is not for the ghost text; let it through
	GhostAccept                     // insert Suffix at the caret
	GhostDismiss                    // hide it
)

// GhostState is the suggestion being shown, if any.
type GhostState struct {
	// Word is the whole suggested word, Suffix the part the person has not typed yet.
	Word, Suffix string
	// Prefix is what had been typed when the suggestion was made.
	Prefix string
	// Caret is the caret offset (in runes) the suggestion belongs to; accepting at another caret is refused.
	Caret int
	// dismissed holds the prefix that Escape was pressed for: the same word is not offered again until
	// the prefix changes.
	dismissed string
	// announced is the word that was last announced.
	announced string
}

// Visible says whether there is a suggestion to show.
func (g *GhostState) Visible() bool { return g.Suffix != "" }

// Show offers word for prefix at caret; it returns whether it is shown. A word that does not
// continue the prefix, that is no longer than it, or that was dismissed for this prefix is not shown.
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

// Typed forgets a dismissal once the prefix is not the one it was made for.
func (g *GhostState) Typed(prefix string) {
	if g.dismissed != "" && g.dismissed != prefix {
		g.dismissed = ""
	}
	if prefix == "" {
		g.announced = ""
	}
}

// Key applies a key (as KeyboardEvent.key, with no modifier held) and says what it did: only Tab
// accepts and only Escape dismisses; every other key, Enter and the arrows included, leaves the
// suggestion unaccepted.
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

// Accept returns the text to insert at the caret, "" when there is none or the caret has moved.
func (g *GhostState) Accept(caret int) string {
	if !g.Visible() || caret != g.Caret {
		return ""
	}
	s := g.Suffix
	g.Clear()
	g.announced = ""
	return s
}

// Announcement is the polite message for the suggestion now shown, "" when it was announced already
// (typing another letter of the same word says nothing again) or nothing is shown.
func (g *GhostState) Announcement() string {
	if !g.Visible() || g.announced == g.Word {
		return ""
	}
	g.announced = g.Word
	return trf("ghost.offer", "Suggestion: %s. Press Tab to accept.", g.Word)
}

// Width is the number of characters of the suggestion, for sizing.
func (g *GhostState) Width() int { return utf8.RuneCountInString(g.Suffix) }
