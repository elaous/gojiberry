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
	// Block is the index of the block in the document's export.Snapshot, or -1; Item is the index in
	// Document.Content, or -1. Text is a few words of the text it concerns.
	Block, Item int
	Text        string
}

// Options tune the checks.
type Options struct {
	// Page is the colour text is read against when it has no background of its own ("#ffffff" if empty).
	Page string
}

// Check runs every rule over the document and returns the findings in document order.
func Check(doc docfile.Document, opt Options) []Finding {
	var out []Finding
	out = append(out, checkBlocks(export.FromDocument(doc))...)
	out = append(out, checkContrast(doc, opt)...)
	return out
}

func checkBlocks(snap export.Snapshot) []Finding {
	var out []Finding
	prev := 0 // the level of the last heading
	for i, b := range snap.Blocks {
		switch b.Kind {
		case export.Image:
			if b.ImageAlt == "" && !b.ImageDecorative && b.ImageCaption == "" {
				out = append(out, Finding{Rule: RuleImageAlt, Severity: Error, Block: i, Item: -1, Text: b.ImageSrc,
					Message: "An image has no description, so a reader who cannot see it misses it.",
					Fix:     "Describe the image, or mark it decorative if it carries nothing."})
			}
		case export.Heading1, export.Heading2, export.Heading3:
			text := segmentsText(b.Segments)
			if strings.TrimSpace(text) == "" {
				out = append(out, Finding{Rule: RuleHeadingEmpty, Severity: Error, Block: i, Item: -1,
					Message: fmt.Sprintf("A level %d heading has no text; a reader moving by headings lands on nothing.", b.Level),
					Fix:     "Write the heading, or turn it into a plain paragraph."})
				continue
			}
			if prev > 0 && b.Level > prev+1 {
				out = append(out, Finding{Rule: RuleHeadingSkip, Severity: Warning, Block: i, Item: -1, Text: excerpt(text),
					Message: fmt.Sprintf("The heading %q is level %d after a level %d: a level is skipped.", excerpt(text), b.Level, prev),
					Fix:     fmt.Sprintf("Make it a level %d heading, or add the missing level above it.", prev+1)})
			}
			prev = b.Level
		}
	}
	return out
}

func segmentsText(segs []export.Segment) string {
	var sb strings.Builder
	for _, s := range segs {
		if s.SoftBreak {
			sb.WriteByte(' ')
		} else {
			sb.WriteString(s.Text)
		}
	}
	return sb.String()
}

func excerpt(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}
