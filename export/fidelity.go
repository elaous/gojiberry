package export

import (
	"sort"
	"strings"

	"github.com/elaous/gojiberry/docfile"
)

// Feature names one thing a document can hold that a file format may not be able to write (or read back): a kind
// of text formatting, a block, a table feature, or a property of the whole document. The names are stable ids:
// a host translates them (the showcase's fidelity panel), a test locks them.
type Feature string

// The features a document is checked for. Each is something the editor keeps and a format may drop.
const (
	FeatBold            Feature = "bold"
	FeatItalic          Feature = "italic"
	FeatUnderline       Feature = "underline"
	FeatStrike          Feature = "strike"
	FeatSuperscript     Feature = "superscript"
	FeatSubscript       Feature = "subscript"
	FeatLink            Feature = "link"
	FeatColour          Feature = "colour"
	FeatHighlight       Feature = "highlight"
	FeatFontSize        Feature = "font-size"
	FeatFontFamily      Feature = "font-family"
	FeatTextCase        Feature = "text-case"
	FeatRunLanguage     Feature = "run-language"
	FeatSuggestion      Feature = "suggestion"
	FeatComment         Feature = "comment"
	FeatBookmark        Feature = "bookmark"
	FeatHeading         Feature = "heading"
	FeatDeepHeading     Feature = "deep-heading"
	FeatList            Feature = "list"
	FeatNestedList      Feature = "nested-list"
	FeatCheckList       Feature = "check-list"
	FeatQuote           Feature = "quote"
	FeatCode            Feature = "code"
	FeatAlignment       Feature = "alignment"
	FeatParagraphLayout Feature = "paragraph-layout"
	FeatParagraphStyle  Feature = "paragraph-style"
	FeatColumns         Feature = "columns"
	FeatPageBreak       Feature = "page-break"
	FeatRule            Feature = "rule"
	FeatFootnote        Feature = "footnote"
	FeatContents        Feature = "contents"
	FeatImage           Feature = "image"
	FeatImageAlt        Feature = "image-alt"
	FeatImageEffects    Feature = "image-effects"
	FeatTable           Feature = "table"
	FeatColSpan         Feature = "table-col-span"
	FeatRowSpan         Feature = "table-row-span"
	FeatCellShading     Feature = "table-shading"
	FeatCellAlign       Feature = "table-align"
	FeatCellTextStyle   Feature = "table-text-style"
	FeatDocLanguage     Feature = "doc-language"
	FeatTitle           Feature = "title"
	FeatHistory         Feature = "history"
	FeatPageHeader      Feature = "page-header"
	FeatPageTOC         Feature = "page-toc"
	FeatPageSize        Feature = "page-size"
	FeatVertical        Feature = "vertical-text"
	FeatMedia           Feature = "media"
	FeatMediaCaptions   Feature = "media-captions"
	// FeatEquation is a formula with its text alternative (also what a Word import leaves out, see fidelity_import.go);
	// FeatCodeLanguage the language of a code block; FeatImageLongDesc the long description of a diagram.
	FeatEquation Feature = "equation"
	// FeatFormField is a form field (every format writes its label and what was written in it as text).
	FeatFormField     Feature = "form-field"
	FeatCodeLanguage  Feature = "code-language"
	FeatImageLongDesc Feature = "image-long-desc"
)

// Features lists every Feature in the order a report shows them.
var Features = []Feature{
	FeatBold, FeatItalic, FeatUnderline, FeatStrike, FeatSuperscript, FeatSubscript, FeatLink,
	FeatColour, FeatHighlight, FeatFontSize, FeatFontFamily, FeatTextCase, FeatRunLanguage,
	FeatSuggestion, FeatComment, FeatBookmark,
	FeatHeading, FeatDeepHeading, FeatList, FeatNestedList, FeatCheckList, FeatQuote, FeatCode,
	FeatAlignment, FeatParagraphLayout, FeatParagraphStyle, FeatColumns, FeatPageBreak, FeatRule, FeatFootnote, FeatContents,
	FeatImage, FeatImageAlt, FeatImageEffects,
	FeatTable, FeatColSpan, FeatRowSpan, FeatCellShading, FeatCellAlign, FeatCellTextStyle,
	FeatDocLanguage, FeatTitle, FeatHistory,
	FeatPageHeader, FeatPageTOC, FeatPageSize, FeatVertical,
	FeatMedia, FeatMediaCaptions,
	FeatEquation, FeatCodeLanguage, FeatImageLongDesc, FeatFormField,
}

// Treatment says what a format does with a feature.
type Treatment int

const (
	// Kept: the format writes the feature, so a reader of the file sees it.
	Kept Treatment = iota
	// Simplified: the format writes something that stands for it (a placeholder, a character, a plainer
	// construct) and the meaning is only partly there.
	Simplified
	// Dropped: the format does not write it at all; any text it held stays as ordinary text.
	Dropped
)

func (t Treatment) String() string {
	switch t {
	case Simplified:
		return "simplified"
	case Dropped:
		return "dropped"
	}
	return "kept"
}

// Loss is one line of a fidelity report: a feature the document uses (Count places) and what the format does
// with it. Note is a stable id of how it is simplified or dropped ("" for the plain case), which a host
// translates.
type Loss struct {
	Feature   Feature
	Treatment Treatment
	Note      string
	Count     int
}

// Notes say how a feature is written when it is not written as it is.
const (
	NoteText         = "text"           // the format keeps the characters and nothing else
	NotePlaceholder  = "placeholder"    // an "[Image]" marker stands in
	NoteAccepted     = "accepted"       // suggestions are written as if accepted; struck text is left out
	NoteInlineHTML   = "inline-html"    // Markdown writes the HTML tag it has no syntax for
	NoteBlankCells   = "blank-cells"    // the cells a merge covers are left empty
	NoteGrid         = "grid"           // a table is drawn as a text grid
	NoteFolded       = "folded"         // heading levels 4 to 6 are written as level 3
	NoteGlyph        = "glyph"          // a check box is written as a character
	NoteRuleLine     = "rule-line"      // a rule is a line of dash characters
	NoteNotes        = "notes"          // footnotes become numbers with a Notes list at the end
	NoteBullets      = "bullets"        // list markers are written as characters, not as a real list
	NoteMediaText    = "media-text"     // a clip is written as a link with its transcript as text, not embedded
	NoteCaptionWords = "caption-words"  // the caption track is not written; its words are the transcript
	NoteAltText      = "alt-text"       // an equation is written as its text alternative, not typeset
	NoteLongDescText = "long-desc-text" // a long description is text after the picture, not linked to it
	NoteFormText     = "form-text"      // a form field is written as its label and what is in it, not as a field
	NoteFormControl  = "form-control"   // a form field is an HTML control; read back it is its label and value as text
)

type entry struct {
	t    Treatment
	note string
}

// Formats are the names of the export formats a Report can be made for, in the order a host lists them.
var Formats = []string{"docx", "odt", "pdf", "rtf", "epub", "html", "markdown", "text"}

// Report is what exporting a document as one format loses.
type Report struct {
	Format string
	// Losses lists, for the features the document uses, those the format does not write as they are.
	Losses []Loss
	// Known says the package knows what the format writes. It is false for a format it has no table for
	// (an empty Losses then means nothing is known, not that nothing is lost).
	Known bool
	// Complete says the format keeps everything the editor holds (the editor's own file formats).
	Complete bool
}

// Lossless is whether the report is known and lists no loss.
func (r Report) Lossless() bool { return (r.Known || r.Complete) && len(r.Losses) == 0 }

// completeFormats keep the whole document: they are the editor's own file formats.
var completeFormats = map[string]bool{"json": true, "proto-text": true, "proto": true, "open": true}

// Fidelity reports what exporting d as format loses, from the features the document uses and a table of what
// each format does with each (exportMatrix). Tests lock the table against what the exporters really write, so
// a loss cannot become silent: an exporter that starts dropping a feature, or stops, fails them.
func Fidelity(format string, d docfile.Document) Report {
	r := Report{Format: format, Complete: completeFormats[format]}
	m, ok := exportMatrix[format]
	if !ok {
		return r
	}
	r.Known = true
	use := Usage(d)
	for _, f := range Features {
		n := use[f]
		if n == 0 {
			continue
		}
		e := m[f]
		if e.t == Kept || !Reported(format, f) {
			continue
		}
		r.Losses = append(r.Losses, Loss{Feature: f, Treatment: e.t, Note: e.note, Count: n})
	}
	sort.SliceStable(r.Losses, func(i, j int) bool { return r.Losses[i].Treatment > r.Losses[j].Treatment })
	return r
}

// partOf names the feature a feature is a part of: when the format leaves the whole out, its parts are not
// listed again.
var partOf = map[Feature]Feature{
	FeatColSpan: FeatTable, FeatRowSpan: FeatTable, FeatCellShading: FeatTable, FeatCellAlign: FeatTable, FeatCellTextStyle: FeatTable,
	FeatImageAlt: FeatImage, FeatImageEffects: FeatImage, FeatImageLongDesc: FeatImage, FeatCodeLanguage: FeatCode,
}

// Reported says whether a report for the format lists the feature when it is lost: false for a feature that
// is part of one the format leaves out altogether (a table's merged cells in a format with no tables), which
// the loss of the whole already says.
func Reported(format string, f Feature) bool {
	if parent, ok := partOf[f]; ok {
		if pe := exportMatrix[format][parent]; pe.t == Dropped {
			return false
		}
	}
	return true
}

// Usage counts, for each feature, the places in d that use it.
func Usage(d docfile.Document) map[Feature]int {
	u := map[Feature]int{}
	add := func(f Feature) { u[f]++ }
	if d.Title != "" {
		add(FeatTitle)
	}
	if d.Language() != "" {
		add(FeatDocLanguage)
	}
	if len(d.History) > 0 {
		add(FeatHistory)
	}
	if len(d.Comments) > 0 {
		add(FeatComment)
	}
	if l := d.PageLayout(); !l.IsZero() {
		if l.Header != "" || l.Footer != "" {
			add(FeatPageHeader)
		}
		if l.TOC {
			add(FeatPageTOC)
		}
		if l.Width > 0 || l.Margins != [4]float64{} {
			add(FeatPageSize)
		}
		if l.Vertical {
			add(FeatVertical)
		}
	}
	for _, it := range d.Content {
		switch {
		case it.Chars != nil:
			usageOfRun(it.Chars.Format, add)
		case it.Para != nil:
			usageOfPara(*it.Para, add)
		case it.Columns != nil:
			add(FeatColumns)
		case it.Break != nil:
			add(FeatPageBreak)
		case it.Footnote != nil:
			add(FeatFootnote)
		case it.TOC != nil, it.Index != nil:
			add(FeatContents)
		case it.Rule != nil:
			add(FeatRule)
		case it.Equation != nil:
			add(FeatEquation)
		case it.Form != nil:
			add(FeatFormField)
		case it.Media != nil:
			add(FeatMedia)
			if it.Media.HasCaptions() {
				add(FeatMediaCaptions)
			}
		case it.Image != nil:
			add(FeatImage)
			if it.Image.Alt != "" || it.Image.Decorative {
				add(FeatImageAlt)
			}
			if strings.TrimSpace(it.Image.LongDesc) != "" {
				add(FeatImageLongDesc)
			}
			if it.Image.Alpha != 0 || it.Image.ClipLeft != 0 || it.Image.ClipTop != 0 || it.Image.ClipRight != 0 || it.Image.ClipBottom != 0 {
				add(FeatImageEffects)
			}
		case it.Table != nil:
			add(FeatTable)
			for _, m := range it.Table.Merges {
				if m.Cols > 1 {
					add(FeatColSpan)
				}
				if m.Rows > 1 {
					add(FeatRowSpan)
				}
			}
			for _, s := range it.Table.Styles {
				if s.Background != "" {
					add(FeatCellShading)
				}
				if s.Align != 0 {
					add(FeatCellAlign)
				}
				if s.Color != "" || s.Italic {
					add(FeatCellTextStyle)
				}
			}
		}
	}
	return u
}

func usageOfRun(f docfile.Format, add func(Feature)) {
	if f.FontWeight >= 600 && f.Scale == 0 {
		add(FeatBold)
	}
	if f.FontStyle == "italic" || f.FontStyle == "oblique" {
		add(FeatItalic)
	}
	if f.Underline {
		add(FeatUnderline)
	}
	if f.Strike {
		add(FeatStrike)
	}
	if f.Superscript {
		add(FeatSuperscript)
	}
	if f.Subscript {
		add(FeatSubscript)
	}
	if f.Link != "" {
		add(FeatLink)
	}
	if f.Color != "" {
		add(FeatColour)
	}
	if f.Background != "" {
		add(FeatHighlight)
	}
	if f.FontSize != 0 {
		add(FeatFontSize)
	}
	if f.FontFamily == "monospace" {
		add(FeatCode)
	} else if f.FontFamily != "" {
		add(FeatFontFamily)
	}
	if f.Case != 0 {
		add(FeatTextCase)
	}
	if f.Lang != "" {
		add(FeatRunLanguage)
	}
	if f.Proposal != "" {
		add(FeatSuggestion)
	}
	if f.Comment != "" {
		add(FeatComment)
	}
	if f.Bookmark != "" || f.Index != "" {
		add(FeatBookmark)
	}
	switch f.Scale {
	case headingScales[0], headingScales[1], headingScales[2]:
		add(FeatHeading)
	case headingScales[3], headingScales[4]:
		add(FeatDeepHeading)
	}
}

func usageOfPara(p docfile.Para, add func(Feature)) {
	switch p.List.Kind {
	case 1, 2:
		add(FeatList)
	case 3:
		add(FeatList)
		add(FeatCheckList)
	}
	if p.List.Kind != 0 && p.List.Level > 0 {
		add(FeatNestedList)
	}
	if p.QuoteBar != "" {
		add(FeatQuote)
	}
	if p.Align != 0 {
		add(FeatAlignment)
	}
	if p.IndentLeft != 0 || p.IndentRight != 0 || p.FirstLine != 0 || p.SpaceBefore != 0 || p.SpaceAfter != 0 || p.LineSpacing != 0 || p.DropCap != 0 {
		add(FeatParagraphLayout)
	}
	if p.CodeLang != "" {
		add(FeatCodeLanguage)
	}
	style := strings.ToLower(p.Style)
	switch {
	case style == "":
	case style == "code":
		add(FeatCode) // a code block: a paragraph of the Code style
	case style == "title":
		add(FeatHeading)
	case strings.HasPrefix(style, "heading "):
		if n := strings.TrimPrefix(style, "heading "); n >= "1" && n <= "3" {
			add(FeatHeading)
		} else {
			add(FeatDeepHeading)
		}
	default:
		add(FeatParagraphStyle)
	}
}
