//go:build js && wasm

package main

import (
	"encoding/json"
	"unicode"
)

func (d *document) clearHistory() { d.hist = history{} }

func (d *document) moveLineDown() { d.edit(d.moveLineDownNow) }

func (d *document) moveLineDownNow() {
	d.collapseCursors()
	lines := d.logicalLines()
	li := lineContaining(lines, d.Cursor)
	if li >= len(lines)-1 {
		return
	}
	d.swapLines(lines[li], lines[li+1])
}

func (d *document) duplicateLine() { d.edit(d.duplicateLineNow) }

func (d *document) duplicateLineNow() {
	d.collapseCursors()
	lines := d.logicalLines()
	li := lineContaining(lines, d.Cursor)
	l := lines[li]
	col := d.Cursor - l.Start

	lineChars := d.buf.Slice(l.Start, l.End)
	insert := make([]char, 0, len(lineChars)+1)
	insert = append(insert, char{Rune: '\n'})
	insert = append(insert, lineChars...)
	d.buf.InsertAt(l.End, insert...)

	d.Cursor = l.End + 1 + col
	d.Anchor = d.Cursor
}

func isWordChar(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func wordBoundaryForward(d *document, idx int) int {
	n := d.Len()
	i := idx
	for i < n && !isWordChar(d.runeAt(i)) {
		i++
	}
	for i < n && isWordChar(d.runeAt(i)) {
		i++
	}
	return i
}

func wordBoundaryBackward(d *document, idx int) int {
	i := idx
	for i > 0 && !isWordChar(d.runeAt(i-1)) {
		i--
	}
	for i > 0 && isWordChar(d.runeAt(i-1)) {
		i--
	}
	return i
}

func (d *document) isNewline(i int) bool { return i >= 0 && i < d.Len() && d.runeAt(i) == '\n' }

func (d *document) moveCursor(idx int, extend bool) {
	d.hist.typingAt = 0 // moving the caret ends the step that typing was
	idx = clamp(idx, 0, d.Len())
	d.Cursor = idx
	if !extend {
		d.Anchor = idx
	}
}

type run struct {
	Text   string `json:"text"`
	Bold   bool   `json:"bold"`
	Italic bool   `json:"italic"`
}

func (d *document) runs() []run {
	var out []run
	n := d.Len()
	for i := 0; i < n; i++ {
		c := d.buf.At(i)
		if m := len(out); m > 0 && out[m-1].Bold == c.Fmt.Bold && out[m-1].Italic == c.Fmt.Italic {
			out[m-1].Text += string(c.Rune)
			continue
		}
		out = append(out, run{Text: string(c.Rune), Bold: c.Fmt.Bold, Italic: c.Fmt.Italic})
	}
	return out
}

func marshalDocument(d *document) (string, error) {
	b, err := json.Marshal(d.runs())
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalDocument(data string) (*document, error) {
	var runs []run
	if err := json.Unmarshal([]byte(data), &runs); err != nil {
		return nil, err
	}
	var items []char
	for _, r := range runs {
		for _, ch := range r.Text {
			items = append(items, char{Rune: ch, Fmt: charFormat{Bold: r.Bold, Italic: r.Italic}})
		}
	}
	d := &document{}
	d.buf.InsertAt(0, items...)
	return d, nil
}
