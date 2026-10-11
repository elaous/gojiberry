package read

import (
	"strings"
	"unicode"

	"github.com/elaous/gojiberry/components"
)

// Package read decides what to say when a document is read aloud. Chunks turns the document, as the blocks a screen reader is given, into the
// pieces a speech engine is handed one after the other: a sentence at a time (so the one being
// spoken can be shown and announced, and pause and skip work at sentence borders), each with the
// language its text is in. The order and the wording are those of the accessible copy, so what is
// read aloud is what a screen reader would read. Speaking itself is the browser's (the Web Speech
// API); this is the part that decides what to say.

// Chunk is one piece to speak.
type Chunk struct {
	Text string
	// Lang is the BCP 47 tag of the text, "" for the page's own.
	Lang string
	// Block is the index of the block the piece is from (-1 for text that is not from the blocks).
	Block int
}

// MaxChunk is the longest a piece is (in runes): engines cut off a long utterance.
const MaxChunk = 200

const sentenceMarks = ".!?…。！？"
const closers = `"')]”’»`

// Chunks is the blocks as pieces to speak, in order. docLang is the document's language, used
// for text that has none of its own.
func Chunks(blocks []components.A11yBlock, docLang string) []Chunk {
	var out []Chunk
	for i, b := range blocks {
		if b.Kind == components.A11yTableRow {
			out = appendSentences(out, strings.Join(nonEmpty(b.Cells), ", "), docLang, i)
			continue
		}
		runs := b.Runs
		if len(runs) == 0 {
			runs = []components.A11yRun{{Text: b.Text}}
		}
		start := len(out)
		for _, r := range runs {
			lang := r.Lang
			if lang == "" {
				lang = docLang
			}
			out = appendSentences(out, r.Text, lang, i)
		}
		// A heading, a list item and a code line have no full stop of their own: the engine
		// would run them into the next block without a pause.
		if len(out) > start && b.Kind != components.A11yParagraph && !endsSentence(out[len(out)-1].Text) {
			out[len(out)-1].Text += "."
		}
	}
	return out
}

// OfText is a text (a selection) as pieces to speak, all in lang.
func OfText(text, lang string) []Chunk {
	return appendSentences(nil, text, lang, -1)
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}

func endsSentence(s string) bool {
	rs := []rune(strings.TrimRightFunc(s, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(closers, r) }))
	return len(rs) == 0 || strings.ContainsRune(sentenceMarks, rs[len(rs)-1])
}

// appendSentences splits text into sentences (after . ! ? followed by a space or the end) and those
// longer than MaxChunk at word borders, and appends them.
func appendSentences(out []Chunk, text, lang string, block int) []Chunk {
	rs := []rune(strings.Join(strings.Fields(text), " "))
	start := 0
	flush := func(end int) {
		piece := strings.TrimSpace(string(rs[start:end]))
		start = end
		for _, p := range splitLong(piece, MaxChunk) {
			out = append(out, Chunk{Text: p, Lang: lang, Block: block})
		}
	}
	for i, r := range rs {
		if !strings.ContainsRune(sentenceMarks, r) {
			continue
		}
		// "3.5" and "e.g." stay whole: a sentence ends at a mark followed by a space (or the end).
		j := i + 1
		for j < len(rs) && strings.ContainsRune(closers, rs[j]) {
			j++
		}
		if j == len(rs) || rs[j] == ' ' || strings.ContainsRune("。！？", r) {
			flush(j)
		}
	}
	if start < len(rs) {
		flush(len(rs))
	}
	return out
}

// splitLong cuts s at the last space before max runes, repeatedly.
func splitLong(s string, max int) []string {
	rs := []rune(s)
	var out []string
	for len(rs) > max {
		cut := max
		for cut > 0 && rs[cut] != ' ' {
			cut--
		}
		if cut == 0 {
			cut = max
		}
		out = append(out, strings.TrimSpace(string(rs[:cut])))
		rs = []rune(strings.TrimSpace(string(rs[cut:])))
	}
	if len(rs) > 0 {
		out = append(out, string(rs))
	}
	return out
}
