// Package a11ycheck looks for places where a document leaves a reader out: an image with no
// description, a heading that skips a level or has no text, text too faint to read against its
// background. Each problem is a Finding with a rule name, a severity, where it is, and what would fix
// it, so a panel can list them, jump to each and offer the fix (roadmap items 61 and 62).
//
// The checks read the saved document (docfile.Document) and need no browser. They are judgements about
// what a reader is likely to meet, not proofs: a document that passes can still be hard to use.
package a11ycheck

import (
	"fmt"
	"sort"
	"strings"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
)

// Severity says how much a finding matters.
type Severity int

const (
	// Warning is something most readers will notice but can work around.
	Warning Severity = iota
	// Error leaves some readers without the content.
	Error
)

func (s Severity) String() string {
	if s == Error {
		return "error"
	}
	return "warning"
}

// Rule names, stable so that a setting or a test can refer to them.
const (
	RuleImageAlt     = "image-alt"
	RuleHeadingSkip  = "heading-skip"
	RuleHeadingEmpty = "heading-empty"
	RuleContrast     = "contrast"
)

// Finding is one problem.
type Finding struct {
	Rule     string
	Severity Severity
	// Message says what is wrong; Fix says what to do about it.
	Message string
	Fix     string
	// Offset is where it is, as a rune offset into the document's text stream (the position a
	// caret can be put at, with each image, table, rule and footnote mark counting as one rune), and
	// Item the index in Document.Content. Text is a few words of the text it concerns.
	Offset, Item int
	Text         string
}

// Options tune the checks.
type Options struct {
	// Page is the colour text is read against when it has no background of its own ("#ffffff" if empty).
	Page string
}

// Check runs every rule over the document and returns the findings in document order.
func Check(doc docfile.Document, opt Options) []Finding {
	var out []Finding
	out = append(out, checkStructure(doc)...)
	out = append(out, checkContrast(doc, opt)...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Offset < out[j].Offset })
	return out
}

// checkStructure walks the content once, paragraph by paragraph, for images and headings.
func checkStructure(doc docfile.Document) []Finding {
	var out []Finding
	offset := 0    // the rune offset of the item being read
	paraStart := 0 // where the current paragraph begins
	paraItem := 0  // the item it begins at
	var runs []docfile.Chars
	var style string
	prev := 0 // the level of the last heading
	endPara := func() {
		defer func() { runs, style = nil, "" }()
		level := export.HeadingLevel(runs, style)
		if level == 0 {
			return
		}
		var text strings.Builder
		for _, r := range runs {
			text.WriteString(r.Text)
		}
		switch {
		case strings.TrimSpace(text.String()) == "":
			out = append(out, Finding{Rule: RuleHeadingEmpty, Severity: Error, Offset: paraStart, Item: paraItem,
				Message: fmt.Sprintf("A level %d heading has no text; a reader moving by headings lands on nothing.", level),
				Fix:     "Write the heading, or turn it into a plain paragraph."})
		default:
			if prev > 0 && level > prev+1 {
				out = append(out, Finding{Rule: RuleHeadingSkip, Severity: Warning, Offset: paraStart, Item: paraItem, Text: excerpt(text.String()),
					Message: fmt.Sprintf("The heading %q is level %d after a level %d: a level is skipped.", excerpt(text.String()), level, prev),
					Fix:     fmt.Sprintf("Make it a level %d heading, or add the missing level above it.", prev+1)})
			}
			prev = level
		}
	}
	for i, it := range doc.Content {
		switch {
		case it.Chars != nil:
			parts := strings.Split(it.Chars.Text, "\n")
			for k, part := range parts {
				if k > 0 {
					endPara()
					offset++ // the newline
					paraStart, paraItem = offset, i
				}
				if part != "" {
					if len(runs) == 0 {
						paraStart, paraItem = offset, i
					}
					runs = append(runs, docfile.Chars{Text: part, Format: it.Chars.Format})
					offset += len([]rune(part))
				}
			}
		case it.Para != nil:
			style = it.Para.Style
		case it.Image != nil:
			if it.Image.Alt == "" && !it.Image.Decorative {
				out = append(out, Finding{Rule: RuleImageAlt, Severity: Error, Offset: offset, Item: i, Text: it.Image.URL,
					Message: "An image has no description, so a reader who cannot see it misses it.",
					Fix:     "Describe the image, or mark it decorative if it carries nothing."})
			}
			offset++
		case it.Footnote != nil, it.TOC != nil, it.Index != nil, it.Rule != nil, it.Table != nil:
			offset++ // one object in the text stream
		}
	}
	endPara()
	return out
}

func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}

// itemOffsets is the rune offset in the text stream at which each item of the content starts: text counts
// its runes, a footnote mark, contents list, index, rule, table or image counts one (an object in the stream),
// and paragraph, column and break items none.
func itemOffsets(doc docfile.Document) []int {
	out := make([]int, len(doc.Content))
	n := 0
	for i, it := range doc.Content {
		out[i] = n
		switch {
		case it.Chars != nil:
			n += len([]rune(it.Chars.Text))
		case it.Footnote != nil, it.TOC != nil, it.Index != nil, it.Rule != nil, it.Table != nil, it.Image != nil:
			n++
		}
	}
	return out
}
