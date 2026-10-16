package canvas

import (
	"strings"
)

// The outline is the document's headings as a list: paragraphs in a
// "Heading 1".."Heading 6" or "Title" style, or text that is big and bold
// the way Markdown headings come out (Scale and weight 700). It is derived
// from the content, so it follows edits; a DOM component (components.Outline)
// shows it and GotoOffset takes the reader there.

// OutlineEntry is one heading.
type OutlineEntry struct {
	// Level is 1 for the biggest heading, then 2 to 6.
	Level int
	// Text is the heading's text.
	Text string
	// Start and End are the stream range of the paragraph.
	Start, End int
	// Style is the paragraph style that made it a heading, "" when it is
	// recognised by its look.
	Style string
}

// styleLevel returns the outline level of a paragraph style name, or 0.
func styleLevel(style string) int {
	switch style {
	case "Title":
		return 1
	case "Heading 1":
		return 1
	case "Heading 2":
		return 2
	case "Heading 3":
		return 3
	case "Heading 4":
		return 4
	case "Heading 5":
		return 5
	case "Heading 6":
		return 6
	}
	return 0
}

// scaleLevel is the level of text that is bold and scaled like a Markdown
// heading, or 0.
func scaleLevel(fm Format) int {
	if fm.FontWeight < 700 {
		return 0
	}
	switch {
	case fm.Scale >= 1.9:
		return 1
	case fm.Scale >= 1.5:
		return 2
	case fm.Scale >= 1.3:
		return 3
	case fm.Scale >= 1.15:
		return 4
	}
	return 0
}

// charFormats lists the format of every character of items (an object
// has none).
func charFormats(items []Item) []Format {
	var out []Format
	for _, it := range items {
		switch v := it.(type) {
		case Chars:
			for range v.Text {
				out = append(out, v.Format)
			}
		case Inline:
			out = append(out, Format{})
		}
	}
	return out
}

// Outline returns the headings in order.
func (f *TextFlow) Outline() []OutlineEntry {
	d := f.flatten()
	fms := charFormats(f.items())
	var out []OutlineEntry
	for ps := 0; ps <= len(d.runes); {
		_, pe := paraBounds(d.runes, ps)
		text := strings.TrimSpace(strings.ReplaceAll(string(d.runes[ps:pe]), string(objectRune), ""))
		if text != "" {
			level, style := styleLevel(d.paraIn(ps, pe).Style), d.paraIn(ps, pe).Style
			if level == 0 && ps < len(fms) {
				level, style = scaleLevel(f.expand(Format{}, fms[ps])), ""
			}
			if level > 0 {
				out = append(out, OutlineEntry{Level: level, Text: text, Start: ps, End: pe, Style: style})
			}
		}
		if pe >= len(d.runes) {
			break
		}
		ps = pe + 1
	}
	return out
}

// OutlineIndexAt returns the index in Outline of the heading the offset
// is under (the last one that starts at or before it), or -1.
func (f *TextFlow) OutlineIndexAt(o int) int {
	idx := -1
	for i, e := range f.Outline() {
		if e.Start <= o {
			idx = i
		}
	}
	return idx
}

// GotoOffset puts the caret at stream offset o and asks the scroll view
// around the flow to bring it to the top of the view (ScrollView does so
// after the event it is handling, or when RevealPending is called). It
// works in view mode too.
func (f *TextFlow) GotoOffset(o int) {
	f.SetCursors([]Cursor{At(o)})
	f.revealAt, f.revealPending = o, true
}

// TakeReveal returns where the offset passed to the last GotoOffset is
// (x, top y and line height in the flow's coordinates) and forgets it.
func (f *TextFlow) TakeReveal() (x, y, h float64, ok bool) {
	if !f.revealPending {
		return 0, 0, 0, false
	}
	f.revealPending = false
	x, y, h, ok = f.CaretRect(f.revealAt)
	return f.physicalCaret(x, y, h, ok, f.revealAt)
}

// Revealer is implemented by components that can ask for a position to be
// scrolled to.
type Revealer interface {
	TakeReveal() (x, y, h float64, ok bool)
}

// flowBinder is implemented by inline objects that show the flow's own
// structure (a table of contents): the flow hands itself over each time
// it reads its content.
type flowBinder interface{ bindFlow(f *TextFlow) }

// SectionRange returns the stream range of the section that heading i of Outline opens: from the heading to the next
// heading of the same level or a higher one (a lower level number), or to the end of the text. Its subsections are
// inside it.
func (f *TextFlow) SectionRange(i int) (from, to int, ok bool) {
	es := f.Outline()
	if i < 0 || i >= len(es) {
		return 0, 0, false
	}
	to = len(f.flatten().runes)
	for _, e := range es[i+1:] {
		if e.Level <= es[i].Level {
			to = e.Start
			break
		}
	}
	return es[i].Start, to, true
}

// MoveSection moves the section that heading i opens (with its subsections) in front of heading before of Outline;
// before == len(Outline) moves it to the end. Moving a section into itself does nothing and reports false. The caret
// goes to the moved heading. It is the editing under the outline panel's move buttons and its drag and drop.
func (f *TextFlow) MoveSection(i, before int) bool {
	es := f.Outline()
	from, to, ok := f.SectionRange(i)
	if !ok || before < 0 || before > len(es) {
		return false
	}
	runes := []rune(f.EditText())
	n := len(runes)
	dest := n
	if before < len(es) {
		dest = es[before].Start
	}
	if dest >= from && dest <= to {
		return false
	}
	moved := f.Slice(from, to)
	delFrom := from
	if to == n && (n == 0 || runes[n-1] != '\n') && from > 0 {
		// The last section has no paragraph end of its own: it takes the one before it along.
		moved = append(moved, Chars{Text: "\n"})
		delFrom = from - 1
	}
	if dest == n && (n == 0 || runes[n-1] != '\n') {
		moved = append([]Item{Chars{Text: "\n"}}, moved...)
	}
	f.Replace([]Edit{{From: delFrom, To: to}, {From: dest, To: dest, Items: moved}})
	start := dest
	if dest > to {
		start = dest - (to - delFrom)
		if delFrom < from {
			start++ // the leading newline carried along is not part of the heading
		}
	}
	f.GotoOffset(max(start, 0))
	return true
}
