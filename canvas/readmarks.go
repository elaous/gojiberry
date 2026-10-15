package canvas

// The marks of reading aloud: the sentence being spoken and, inside it, the word. A host that speaks the document
// (the showcase's Read aloud menu) tells the flow where, in stream offsets, and the flow draws the marks under the
// text, the sentence faint and the word strong, so that a person who follows the text with their eyes sees where the
// voice is. They are only a picture: the caret and the selection are not moved.

// The colours of the marks.
const (
	ReadSentenceColor = "rgba(124, 92, 255, 0.20)"
	ReadWordColor     = "rgba(255, 190, 0, 0.60)"
)

// readMarks is the range of the sentence and of the word being read, [from, to) each; from < 0 for none.
type readMarks struct {
	sentFrom, sentTo, wordFrom, wordTo int
}

// SetReadMarks marks the sentence [sentFrom, sentTo) and the word [wordFrom, wordTo) of it (pass a negative
// from for none) and draws them on the next Draw.
func (f *TextFlow) SetReadMarks(sentFrom, sentTo, wordFrom, wordTo int) {
	f.reading = readMarks{sentFrom, sentTo, wordFrom, wordTo}
}

// ClearReadMarks removes the marks.
func (f *TextFlow) ClearReadMarks() { f.reading = readMarks{sentFrom: -1, wordFrom: -1} }

// ReadMarks returns the marks: the sentence and the word, each as [from, to), or from < 0 when there is none.
func (f *TextFlow) ReadMarks() (sentFrom, sentTo, wordFrom, wordTo int) {
	r := f.reading
	if r.sentFrom == 0 && r.sentTo == 0 && r.wordFrom == 0 && r.wordTo == 0 { // never set
		return -1, 0, -1, 0
	}
	return r.sentFrom, r.sentTo, r.wordFrom, r.wordTo
}

// Reveal asks the scroll view around the flow to bring offset o into view, without moving the caret or the
// selection (GotoOffset moves the caret).
func (f *TextFlow) Reveal(o int) { f.revealAt, f.revealPending = o, true }

func (f *TextFlow) drawReadMarks(lay Layout) {
	sf, st, wf, wt := f.ReadMarks()
	if sf < 0 || f.Painter == nil {
		return
	}
	p := f.Painter
	fill := func(from, to int, colour string) {
		if from < 0 || to <= from {
			return
		}
		p.SetFillStyle(colour)
		for _, ln := range lay.Lines {
			s, e := max(from, ln.Start), min(to, ln.End)
			for _, r := range f.lineRects(ln, s, e) {
				p.FillRect(r[0], ln.Y, r[1]-r[0], ln.H)
			}
		}
	}
	fill(sf, st, ReadSentenceColor)
	fill(wf, wt, ReadWordColor)
}
