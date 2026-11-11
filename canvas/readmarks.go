package canvas

// Marks for reading aloud: the sentence and word being spoken.

// The colours of the marks.
const (
	ReadSentenceColor = "rgba(124, 92, 255, 0.20)"
	ReadWordColor     = "rgba(255, 190, 0, 0.60)"
)

// readMarks holds the sentence and word ranges, each [from, to).
type readMarks struct {
	sentFrom, sentTo, wordFrom, wordTo int
}

// SetReadMarks marks the sentence and word being read.
func (f *TextFlow) SetReadMarks(sentFrom, sentTo, wordFrom, wordTo int) {
	f.reading = readMarks{sentFrom, sentTo, wordFrom, wordTo}
}

// ClearReadMarks removes the marks.
func (f *TextFlow) ClearReadMarks() { f.reading = readMarks{sentFrom: -1, wordFrom: -1} }

// ReadMarks returns the sentence and word ranges, or -1 if unset.
func (f *TextFlow) ReadMarks() (sentFrom, sentTo, wordFrom, wordTo int) {
	r := f.reading
	if r.sentFrom == 0 && r.sentTo == 0 && r.wordFrom == 0 && r.wordTo == 0 { // never set
		return -1, 0, -1, 0
	}
	return r.sentFrom, r.sentTo, r.wordFrom, r.wordTo
}

// Reveal scrolls offset o into view without moving the caret.
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
