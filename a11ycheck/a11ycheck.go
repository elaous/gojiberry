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
	// Warning is a problem most readers can work around.
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

const (
	RuleImageAlt     = "image-alt"
	RuleHeadingSkip  = "heading-skip"
	RuleHeadingEmpty = "heading-empty"
	RuleContrast     = "contrast"
	// RuleTableHeaderMissing: the first table row has no text.
	RuleTableHeaderMissing = "table-header-missing"
	// RuleTableHeaderEmpty: some header cell has no text while others do.
	RuleTableHeaderEmpty = "table-header-empty"
	RuleFakeList         = "list-fake"
	RuleMediaCaptions    = "media-captions"
	// RuleMediaTranscript: no transcript. Error for audio, warning for video.
	RuleMediaTranscript = "media-transcript"
	RuleMediaTitle      = "media-title"
	RuleEquationAlt     = "equation-alt"
	RuleFormLabel       = "form-label"
)

// Finding is one problem.
type Finding struct {
	Rule     string
	Severity Severity
	Message  string
	Fix      string
	// Offset counts text-stream runes; objects count as one.
	Offset, Item int
	Text         string
	Action       string
	Arg, Length  int
	Spans        [][2]int
}

// The one-click actions of a Finding.
const (
	ActionHeadingLevel  = "heading-level"
	ActionDefaultColour = "default-colour"
	ActionDescribe      = "describe"
	ActionToParagraph   = "to-paragraph"
	// ActionWriteMediaText: the person writes the captions or transcript.
	ActionWriteMediaText = "write-media-text"
	ActionToList         = "to-list"
)

// GateMode says what an export does about errors.
type GateMode string

const (
	GateAllow GateMode = ""      // no check before exporting (the default)
	GateWarn  GateMode = "warn"  // export, and say how many errors it has
	GateBlock GateMode = "block" // refuse, unless the person allowed this one export
)

func (m GateMode) Decide(findings []Finding, override bool) (allow bool, errors int) {
	for _, f := range findings {
		if f.Severity == Error {
			errors++
		}
	}
	return m != GateBlock || errors == 0 || override, errors
}

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

// AtLeast returns the stricter of m and operator.
func (m GateMode) AtLeast(operator GateMode) GateMode {
	if operator.rank() > m.rank() {
		return operator
	}
	return m
}

func OverrideAllowed(operator GateMode) bool { return operator != GateBlock }

// Options tune the checks.
type Options struct {
	Page string
}

// Check runs every rule and returns findings in document order.
func Check(doc docfile.Document, opt Options) []Finding {
	var out []Finding
	out = append(out, checkStructure(doc)...)
	out = append(out, checkContrast(doc, opt)...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Offset < out[j].Offset })
	return out
}

func checkStructure(doc docfile.Document) []Finding {
	var out []Finding
	offset := 0
	paraStart := 0
	paraItem := 0
	var runs []docfile.Chars
	var style string
	var listKind int
	fake := fakeListRun{}
	prev := 0
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
					offset++
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
		case it.Equation != nil:
			out = fake.flush(out)
			out = append(out, EquationFindings(*it.Equation, offset, i)...)
			offset++
		case it.Form != nil:
			out = fake.flush(out)
			if strings.TrimSpace(it.Form.Label) == "" {
				out = append(out, Finding{Rule: RuleFormLabel, Severity: Error, Offset: offset, Item: i, Text: excerpt(it.Form.Name),
					Message: "A form field has no label, so a reader who cannot see it does not know what to write in it.",
					Fix:     "Give the field a label that says what it asks for.",
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

func itemOffsets(doc docfile.Document) []int {
	out := make([]int, len(doc.Content))
	n := 0
	for i, it := range doc.Content {
		out[i] = n
		switch {
		case it.Chars != nil:
			n += len([]rune(it.Chars.Text))
		case it.Footnote != nil, it.TOC != nil, it.Index != nil, it.Rule != nil, it.Table != nil, it.Image != nil, it.Media != nil, it.Equation != nil, it.Form != nil:
			n++
		}
	}
	return out
}

type Object struct {
	Kind, Where string
	Alt         string
	Decorative  bool
	Rows        [][]string
}

func CheckObjects(objs []Object) []Finding {
	var out []Finding
	for _, o := range objs {
		switch o.Kind {
		case "image":
			if o.Alt == "" && !o.Decorative {
				out = append(out, Finding{Rule: RuleImageAlt, Severity: Error, Offset: -1, Item: -1, Text: o.Where,
					Message: o.Where + " (floating) has no description, so a reader who cannot see it misses it.",
					Fix:     "Describe the picture, or mark it decorative if it carries nothing."})
			}
		case "table":
			rows := make([]docfile.TableRow, len(o.Rows))
			for i, r := range o.Rows {
				rows[i] = docfile.TableRow{Cells: r}
			}
			for _, f := range checkTable(&docfile.TableItem{Rows: rows}, -1, -1) {
				f.Text = o.Where
				f.Message = o.Where + " (floating): " + f.Message
				out = append(out, f)
			}
		}
	}
	return out
}
