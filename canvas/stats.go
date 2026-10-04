package canvas

import (
	"strings"
	"time"
	"unicode"
)

// TextStats counts a piece of text the way a word processor's status bar
// does.
type TextStats struct {
	Words int
	// Characters counts every visible character, spaces included;
	// CharactersNoSpaces leaves the spaces out. Paragraph and line breaks,
	// and inline objects, are not characters.
	Characters         int
	CharactersNoSpaces int
	// Paragraphs counts the non-empty paragraphs; Sentences the runs of
	// words ended by . ! or ? (or by the end of the text).
	Paragraphs int
	Sentences  int
}

// wordsPerMinute is the reading speed ReadingTime assumes.
const wordsPerMinute = 238

// ReadingTime is how long the text takes to read at an average speed.
func (s TextStats) ReadingTime() time.Duration {
	return time.Duration(float64(s.Words) / wordsPerMinute * float64(time.Minute))
}

// isBreak reports whether r separates words without being a character:
// paragraph and line breaks, a column break and an inline object.
func isBreak(r rune) bool {
	return r == '\n' || r == ' ' || r == ' ' || r == objectRune
}

// CountText counts s.
func CountText(s string) TextStats {
	var st TextStats
	inWord, inPara, sentenceOpen := false, false, false
	for _, r := range s {
		if isBreak(r) {
			if r == '\n' {
				inPara = false
			}
			if r == '\n' && sentenceOpen {
				st.Sentences++
				sentenceOpen = false
			}
			inWord = false
			continue
		}
		st.Characters++
		if unicode.IsSpace(r) {
			inWord = false
			continue
		}
		st.CharactersNoSpaces++
		if !inWord {
			st.Words++
			inWord = true
		}
		if !inPara {
			st.Paragraphs++
			inPara = true
		}
		if strings.ContainsRune(".!?…", r) {
			if sentenceOpen {
				st.Sentences++
				sentenceOpen = false
			}
		} else if !strings.ContainsRune(`"')]”’`, r) {
			sentenceOpen = true
		}
	}
	if sentenceOpen {
		st.Sentences++
	}
	return st
}

// FlowStats is what Stats reports about a text flow.
type FlowStats struct {
	// Text counts the whole text; Selection the selected part (zero when
	// nothing is selected, see HasSelection).
	Text, Selection TextStats
	HasSelection    bool
	// Pages is how many pages the text reached in the latest layout drawn
	// (0 before the first); it settles one redraw after a change.
	Pages int
}

// Stats counts the flow's text and, if there is one, its selection.
func (f *TextFlow) Stats() FlowStats {
	st := FlowStats{Text: CountText(f.EditText()), Pages: f.lastLay.Pages}
	if f.HasSelection() {
		st.HasSelection = true
		st.Selection = CountText(f.SelectedText())
	}
	return st
}
