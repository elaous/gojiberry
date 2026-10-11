// Package docdiff compares two versions of a document. The comparison is by block (a paragraph, a
// heading, a list item, a table cell, a picture) and, inside a block that was edited, by word, so that
// a changed sentence shows which words went and which came. The result is plain data, with a
// description in words for a screen reader (Describe), so the view built on it is not only coloured
// text. It is the diff of text and structure (a heading that became a paragraph is a change); formatting
// within a run (bold, a colour) is not compared.
package docdiff

import (
	"fmt"
	"strings"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
)

// Op says what happened to a block or a run of words.
type Op int

const (
	Equal  Op = iota // in both
	Insert           // only in the newer version
	Delete           // only in the older version
	Edit             // in both, with other words (Runs says which)
)

func (o Op) String() string {
	return [...]string{"equal", "inserted", "deleted", "edited"}[o]
}

// Run is a piece of an edited block's text.
type Run struct {
	Op   Op // Equal, Insert or Delete
	Text string
}

// Change is one block of the comparison.
type Change struct {
	Op   Op
	Kind export.BlockKind
	// Level is the heading or list level of the block.
	Level int
	// Text is the block's text (the newer one for Equal and Edit, the older for Delete).
	Text string
	// OldKind is the older block's kind for an Edit whose kind changed (a paragraph made a heading).
	OldKind export.BlockKind
	// Runs, for an Edit, is the text word by word.
	Runs []Run
}

// maxCells bounds the work of the block comparison (blocks in the older times blocks in the newer,
// after the common start and end are set aside); past it, the middle is reported as deleted and inserted.
const maxCells = 4_000_000

type blockText struct {
	kind  export.BlockKind
	level int
	text  string
}

func blocksOf(d docfile.Document) []blockText {
	var out []blockText
	for _, b := range export.FromDocument(d).Blocks {
		var sb strings.Builder
		for _, s := range b.Segments {
			if s.SoftBreak {
				sb.WriteByte('\n')
			} else {
				sb.WriteString(s.Text)
			}
		}
		text := sb.String()
		if b.Kind == export.Image {
			text = b.ImageAlt
			if text == "" {
				text = b.ImageCaption
			}
		}
		out = append(out, blockText{kind: b.Kind, level: b.Level, text: text})
	}
	return out
}

func (b blockText) key() string { return fmt.Sprintf("%d/%d/%s", b.kind, b.level, b.text) }

// Compare returns the blocks of the two documents in order, each marked Equal, Insert, Delete or Edit.
func Compare(older, newer docfile.Document) []Change {
	return CompareBlocks(blocksOf(older), blocksOf(newer))
}

// CompareBlocks is Compare for blocks already read.
func CompareBlocks(a, b []blockText) []Change {
	var out []Change
	start := 0
	for start < len(a) && start < len(b) && a[start].key() == b[start].key() {
		out = append(out, equal(b[start]))
		start++
	}
	endA, endB := len(a), len(b)
	var tail []Change
	for endA > start && endB > start && a[endA-1].key() == b[endB-1].key() {
		tail = append(tail, equal(b[endB-1]))
		endA, endB = endA-1, endB-1
	}
	out = append(out, pair(middle(a[start:endA], b[start:endB]))...)
	for i := len(tail) - 1; i >= 0; i-- {
		out = append(out, tail[i])
	}
	return out
}

func equal(b blockText) Change {
	return Change{Op: Equal, Kind: b.kind, Level: b.level, Text: b.text}
}

// step is one position of the edit script before pairing.
type step struct {
	op Op
	a  blockText // for Delete
	b  blockText // for Insert and Equal
}

func middle(a, b []blockText) []step {
	var out []step
	if len(a) == 0 || len(b) == 0 || len(a)*len(b) > maxCells {
		for _, x := range a {
			out = append(out, step{op: Delete, a: x})
		}
		for _, x := range b {
			out = append(out, step{op: Insert, b: x})
		}
		return out
	}
	// longest common subsequence
	n, m := len(a), len(b)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	ka, kb := make([]string, n), make([]string, m)
	for i := range a {
		ka[i] = a[i].key()
	}
	for j := range b {
		kb[j] = b[j].key()
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if ka[i] == kb[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case ka[i] == kb[j]:
			out = append(out, step{op: Equal, b: b[j]})
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, step{op: Delete, a: a[i]})
			i++
		default:
			out = append(out, step{op: Insert, b: b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, step{op: Delete, a: a[i]})
	}
	for ; j < m; j++ {
		out = append(out, step{op: Insert, b: b[j]})
	}
	return out
}

// pair turns a run of deletes followed by inserts into edits where the blocks correspond one to one
// (the n-th deleted with the n-th inserted), so a reworded paragraph is one edit, not a delete and an
// insert.
func pair(steps []step) []Change {
	var out []Change
	for i := 0; i < len(steps); {
		if steps[i].op == Equal {
			out = append(out, equal(steps[i].b))
			i++
			continue
		}
		var dels, ins []blockText
		for i < len(steps) && steps[i].op != Equal {
			if steps[i].op == Delete {
				dels = append(dels, steps[i].a)
			} else {
				ins = append(ins, steps[i].b)
			}
			i++
		}
		k := 0
		for ; k < len(dels) && k < len(ins); k++ {
			if similar(dels[k], ins[k]) {
				out = append(out, edit(dels[k], ins[k]))
			} else {
				out = append(out, Change{Op: Delete, Kind: dels[k].kind, Level: dels[k].level, Text: dels[k].text},
					Change{Op: Insert, Kind: ins[k].kind, Level: ins[k].level, Text: ins[k].text})
			}
		}
		for _, d := range dels[k:] {
			out = append(out, Change{Op: Delete, Kind: d.kind, Level: d.level, Text: d.text})
		}
		for _, n := range ins[k:] {
			out = append(out, Change{Op: Insert, Kind: n.kind, Level: n.level, Text: n.text})
		}
	}
	return out
}

// similar says two blocks are one block edited: they share at least a third of their words (or one is empty).
func similar(a, b blockText) bool {
	wa, wb := words(a.text), words(b.text)
	if len(wa) == 0 || len(wb) == 0 {
		return a.kind == b.kind
	}
	common := 0
	for _, r := range wordRuns(wa, wb) {
		if r.Op == Equal {
			common += len(words(r.Text))
		}
	}
	return common*3 >= min(len(wa), len(wb))
}

func edit(a, b blockText) Change {
	return Change{Op: Edit, Kind: b.kind, OldKind: a.kind, Level: b.level, Text: b.text, Runs: wordRuns(words(a.text), words(b.text))}
}

// words splits text into words, each keeping the space after it, so that joining them gives the text.
func words(s string) []string {
	var out []string
	start := 0
	inSpace := false
	for i, r := range s {
		sp := r == ' ' || r == '\n' || r == '\t'
		if inSpace && !sp {
			out = append(out, s[start:i])
			start = i
		}
		inSpace = sp
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// wordRuns compares two word lists and returns runs of equal, deleted and inserted words, equal
// neighbours joined.
func wordRuns(a, b []string) []Run {
	n, m := len(a), len(b)
	if n*m > maxCells {
		return []Run{{Op: Delete, Text: strings.Join(a, "")}, {Op: Insert, Text: strings.Join(b, "")}}
	}
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if strings.TrimSpace(a[i]) == strings.TrimSpace(b[j]) {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []Run
	add := func(op Op, t string) {
		if t == "" {
			return
		}
		if len(out) > 0 && out[len(out)-1].Op == op {
			out[len(out)-1].Text += t
			return
		}
		out = append(out, Run{Op: op, Text: t})
	}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case strings.TrimSpace(a[i]) == strings.TrimSpace(b[j]):
			add(Equal, b[j])
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			add(Delete, a[i])
			i++
		default:
			add(Insert, b[j])
			j++
		}
	}
	for ; i < n; i++ {
		add(Delete, a[i])
	}
	for ; j < m; j++ {
		add(Insert, b[j])
	}
	return out
}

// Summary counts the changes.
type Summary struct {
	Inserted, Deleted, Edited, Unchanged int
}

// Summarize counts the blocks of each kind of change.
func Summarize(changes []Change) Summary {
	var s Summary
	for _, c := range changes {
		switch c.Op {
		case Insert:
			s.Inserted++
		case Delete:
			s.Deleted++
		case Edit:
			s.Edited++
		default:
			s.Unchanged++
		}
	}
	return s
}

// Changed reports whether anything differs.
func (s Summary) Changed() bool { return s.Inserted+s.Deleted+s.Edited > 0 }

// Describe says a change in words, for a screen reader or a plain-text report: "Inserted paragraph:
// …", "Deleted heading 2: …", "Edited paragraph: removed 'old', added 'new'". Unchanged blocks are "".
func Describe(c Change) string {
	if c.Op == Equal {
		return ""
	}
	name := kindName(c.Kind, c.Level)
	switch c.Op {
	case Insert:
		return "Inserted " + name + ": " + c.Text
	case Delete:
		return "Deleted " + name + ": " + c.Text
	}
	var removed, added []string
	for _, r := range c.Runs {
		switch r.Op {
		case Delete:
			removed = append(removed, "'"+strings.TrimSpace(r.Text)+"'")
		case Insert:
			added = append(added, "'"+strings.TrimSpace(r.Text)+"'")
		}
	}
	out := "Edited " + name + ":"
	if c.OldKind != c.Kind {
		out += " was " + kindName(c.OldKind, c.Level) + ";"
	}
	if len(removed) > 0 {
		out += " removed " + strings.Join(removed, ", ") + ";"
	}
	if len(added) > 0 {
		out += " added " + strings.Join(added, ", ") + ";"
	}
	return strings.TrimSuffix(out, ";")
}

// KindName is the English name of a block kind ("heading 2", "bullet item", "paragraph").
func KindName(k export.BlockKind) string { return kindName(k, 0) }

func kindName(k export.BlockKind, level int) string {
	switch k {
	case export.Heading1:
		return "heading 1"
	case export.Heading2:
		return "heading 2"
	case export.Heading3:
		return "heading 3"
	case export.BulletItem:
		return "bullet item"
	case export.NumberedItem:
		return "numbered item"
	case export.Quote:
		return "quote"
	case export.Code:
		return "code block"
	case export.Image:
		return "picture"
	case export.TableCell:
		return "table cell"
	}
	return "paragraph"
}
