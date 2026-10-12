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
	// RuleTableHeaderMissing: the first row of a table (its header row, in every export) has no text.
	RuleTableHeaderMissing = "table-header-missing"
	// RuleTableHeaderEmpty: some header cell has no text while others do.
	RuleTableHeaderEmpty = "table-header-empty"
	// RuleFakeList: paragraphs that start with a typed bullet or number instead of being a list.
	RuleFakeList = "list-fake"
	// RuleMediaCaptions: a video with no caption track, so a deaf reader gets none of what is said.
	RuleMediaCaptions = "media-captions"
	// RuleMediaTranscript: a clip with no transcript text (audio: an error, it is all of the content; video:
	// a warning, the captions are the speech but a transcript also covers what is shown).
	RuleMediaTranscript = "media-transcript"
	// RuleMediaTitle: a clip with no title, so a reader hears only "video" or an address as its name.
	RuleMediaTitle = "media-title"
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
	// Action names what a host can do for the person in one click ("" for nothing that is safe to do
	// for them): ActionHeadingLevel (set the paragraph at Offset to heading level Arg), ActionDefaultColour
	// (return the Length runes at Offset to the default text colour), ActionDescribe (go and write the
	// description; nothing is invented), ActionToParagraph (make the empty heading a plain paragraph).
	Action      string
	Arg, Length int
}

// The one-click actions of a Finding.
const (
	ActionHeadingLevel  = "heading-level"
	ActionDefaultColour = "default-colour"
	ActionDescribe      = "describe"
	// ActionToParagraph turns the empty heading at Offset into a plain paragraph: its paragraph style is
	// cleared and the Length runes of blank text it holds lose any heading size.
	ActionToParagraph = "to-paragraph"
	// ActionWriteMediaText: go to the clip and write its captions or transcript; nothing is invented.
	ActionWriteMediaText = "write-media-text"
)

// GateMode says what an export does about the errors a document has.
type GateMode string

const (
	GateAllow GateMode = ""      // no check before exporting (the default)
	GateWarn  GateMode = "warn"  // export, and say how many errors it has
	GateBlock GateMode = "block" // refuse, unless the person allowed this one export
)

// Decide says whether an export may go on, given the findings, and how many of them are errors.
// override is the person's explicit "export anyway" for this one export.
func (m GateMode) Decide(findings []Finding, override bool) (allow bool, errors int) {
	for _, f := range findings {
		if f.Severity == Error {
			errors++
		}
	}
	return m != GateBlock || errors == 0 || override, errors
}

// ParseGateMode reads a gate from a setting: "" or "allow" (none), "warn", "block". ok is false for any
// other word, so a caller can tell a typo from a choice.
func ParseGateMode(s string) (m GateMode, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "allow", "none":
		return GateAllow, true
	case "warn":
		return GateWarn, true
	case "block":
		return GateBlock, true
	}
	return GateAllow, false
}

func (m GateMode) rank() int {
	switch m {
	case GateWarn:
		return 1
	case GateBlock:
		return 2
	}
	return 0
}

// AtLeast is the stricter of m and the operator's minimum: the person's choice of gate can raise what the
// operator set but never lower it.
func (m GateMode) AtLeast(operator GateMode) GateMode {
	if operator.rank() > m.rank() {
		return operator
	}
	return m
}

// OverrideAllowed says whether the person may allow one export past a blocking gate. An operator who
// sets the minimum to "block" has decided that errors stop an export, so there is no override then;
// under a lower minimum the person's own "block" keeps its explicit override.
func OverrideAllowed(operator GateMode) bool { return operator != GateBlock }

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
	var listKind int
	fake := fakeListRun{}
	prev := 0 // the level of the last heading
	endPara := func() {
		defer func() { runs, style, listKind = nil, "", 0 }()
		var text strings.Builder
		for _, r := range runs {
			text.WriteString(r.Text)
		}
		level := export.HeadingLevel(runs, style)
		if level == 0 {
			out = fake.add(out, text.String(), listKind, paraStart, paraItem)
			return
		}
		out = fake.flush(out)
		switch {
		case strings.TrimSpace(text.String()) == "":
			out = append(out, Finding{Rule: RuleHeadingEmpty, Severity: Error, Offset: paraStart, Item: paraItem,
				Message: fmt.Sprintf("A level %d heading has no text; a reader moving by headings lands on nothing.", level),
				Fix:     "Write the heading, or turn it into a plain paragraph.",
				Action:  ActionToParagraph, Length: len([]rune(text.String()))})
		default:
			if prev > 0 && level > prev+1 {
				out = append(out, Finding{Rule: RuleHeadingSkip, Severity: Warning, Offset: paraStart, Item: paraItem, Text: excerpt(text.String()),
					Message: fmt.Sprintf("The heading %q is level %d after a level %d: a level is skipped.", excerpt(text.String()), level, prev),
					Fix:     fmt.Sprintf("Make it a level %d heading, or add the missing level above it.", prev+1),
					Action:  ActionHeadingLevel, Arg: prev + 1})
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
			style, listKind = it.Para.Style, it.Para.List.Kind
		case it.Image != nil:
			out = fake.flush(out)
			if it.Image.Alt == "" && !it.Image.Decorative {
				out = append(out, Finding{Rule: RuleImageAlt, Severity: Error, Offset: offset, Item: i, Text: it.Image.URL,
					Message: "An image has no description, so a reader who cannot see it misses it.",
					Fix:     "Describe the image, or mark it decorative if it carries nothing.",
					Action:  ActionDescribe})
			}
			offset++
		case it.Table != nil:
			out = fake.flush(out)
			out = append(out, checkTable(it.Table, offset, i)...)
			offset++
		case it.Media != nil:
			out = fake.flush(out)
			out = append(out, MediaFindings(*it.Media, offset, i)...)
			offset++
		case it.Footnote != nil, it.TOC != nil, it.Index != nil, it.Rule != nil:
			out = fake.flush(out)
			offset++ // one object in the text stream
		}
	}
	endPara()
	out = fake.flush(out)
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
		case it.Footnote != nil, it.TOC != nil, it.Index != nil, it.Rule != nil, it.Table != nil, it.Image != nil, it.Media != nil:
			n++
		}
	}
	return out
}
