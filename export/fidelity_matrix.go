package export

// What each export format does with each Feature. Every format lists every feature exactly once (an init
// check panics otherwise, so a new Feature cannot be forgotten), and TestFidelityMatrixMatchesTheExporters
// exports a document with and without each feature to prove the entry: a feature marked Dropped must leave the
// file unchanged, one marked Kept or Simplified must change it, and one that is folded into another construct
// must be written exactly as that construct.

// Two more notes, for what only some formats do.
const (
	// NoteContentLost: not only the marking but the content is left out (the text of a table, a footnote).
	NoteContentLost = "content-lost"
	// NoteMarks: written with marks that only the editor's own Markdown reads back; other readers show them.
	NoteMarks = "marks"
)

func kept() entry { return entry{} }

func drop(note string) entry { return entry{Dropped, note} }

func simple(note string) entry { return entry{Simplified, note} }

// formatTable builds one format's table: the features no format writes, then the overrides; every Feature
// ends with an entry (Kept when nothing says otherwise).
func formatTable(overrides ...map[Feature]entry) map[Feature]entry {
	m := map[Feature]entry{}
	// What no format writes: the editor keeps it, the files do not.
	for _, f := range []Feature{FeatColour, FeatFontSize, FeatFontFamily, FeatTextCase, FeatComment, FeatBookmark,
		FeatAlignment, FeatParagraphLayout, FeatParagraphStyle, FeatColumns, FeatPageBreak, FeatContents,
		FeatImageEffects, FeatCellTextStyle, FeatHistory} {
		m[f] = drop("")
	}
	for _, o := range overrides {
		for f, e := range o {
			m[f] = e
		}
	}
	for _, f := range Features {
		if _, ok := m[f]; !ok {
			m[f] = kept()
		}
	}
	return m
}

// noPages is what the formats with no pages do with the print layout.
var noPages = map[Feature]entry{FeatPageHeader: drop(""), FeatPageTOC: drop(""), FeatPageSize: drop("")}

// foldedFeatures is what every format built on the Snapshot does with these four.
var foldedFeatures = map[Feature]entry{
	FeatDeepHeading: simple(NoteFolded),
	FeatCheckList:   simple(NoteGlyph),
	FeatRule:        simple(NoteRuleLine),
	FeatFootnote:    simple(NoteNotes),
}

var exportMatrix = map[string]map[Feature]entry{
	// Word keeps the most; it has no colour, size or font, and highlights are not written.
	"docx": formatTable(foldedFeatures, map[Feature]entry{
		FeatHighlight: drop(""),
	}),
	// The PDF is drawn: text, links, tags and tables, without raised or lowered text, shading or row spans.
	"pdf": formatTable(foldedFeatures, map[Feature]entry{
		FeatSuperscript: drop(""), FeatSubscript: drop(""), FeatHighlight: drop(""),
		FeatSuggestion: drop(NoteAccepted),
		FeatRowSpan:    drop(""), FeatCellShading: drop(""), FeatCellAlign: drop(""),
	}),
	// Rich Text Format: no embedded pictures, shading, alignment, language or title.
	"rtf": formatTable(foldedFeatures, map[Feature]entry{
		FeatHighlight: drop(""), FeatRunLanguage: drop(""), FeatSuggestion: drop(NoteAccepted),
		FeatList: simple(NoteBullets), FeatImage: simple(NotePlaceholder), FeatImageAlt: drop(NotePlaceholder),
		FeatCellShading: drop(""), FeatCellAlign: drop(""),
		FeatDocLanguage: drop(""), FeatTitle: drop(""), FeatPageTOC: drop(""),
	}),
	"epub": formatTable(foldedFeatures, noPages, map[Feature]entry{FeatHighlight: drop("")}),
	"html": formatTable(foldedFeatures, map[Feature]entry{FeatHighlight: drop("")}),
	// Markdown here is the editor's own: its marks for underline, highlight and raised or lowered text are
	// not standard. Pictures, tables, footnotes and rules are not written at all.
	"markdown": formatTable(noPages, map[Feature]entry{
		FeatUnderline: simple(NoteMarks), FeatHighlight: simple(NoteMarks),
		FeatSuperscript: simple(NoteMarks), FeatSubscript: simple(NoteMarks),
		FeatRunLanguage: drop(""), FeatSuggestion: drop(NoteAccepted),
		FeatRule: drop(""), FeatFootnote: drop(NoteContentLost),
		FeatImage: drop(NoteContentLost), FeatImageAlt: drop(NoteContentLost),
		FeatTable: drop(NoteContentLost), FeatColSpan: drop(NoteContentLost), FeatRowSpan: drop(NoteContentLost),
		FeatCellShading: drop(NoteContentLost), FeatCellAlign: drop(NoteContentLost),
		FeatDocLanguage: drop(""), FeatTitle: drop(""),
	}),
	// Plain text keeps the characters of the paragraphs and nothing else.
	"text": formatTable(noPages, map[Feature]entry{
		FeatBold: drop(NoteText), FeatItalic: drop(NoteText), FeatUnderline: drop(NoteText), FeatStrike: drop(NoteText),
		FeatSuperscript: drop(NoteText), FeatSubscript: drop(NoteText), FeatLink: drop(NoteText),
		FeatHighlight: drop(NoteText), FeatRunLanguage: drop(NoteText), FeatSuggestion: drop(NoteAccepted),
		FeatHeading: drop(NoteText), FeatDeepHeading: drop(NoteText), FeatList: drop(NoteText), FeatNestedList: drop(NoteText),
		FeatCheckList: drop(NoteText), FeatQuote: drop(NoteText), FeatCode: drop(NoteText),
		FeatRule: drop(NoteText), FeatFootnote: drop(NoteContentLost),
		FeatImage: drop(NoteContentLost), FeatImageAlt: drop(NoteContentLost),
		FeatTable: drop(NoteContentLost), FeatColSpan: drop(NoteContentLost), FeatRowSpan: drop(NoteContentLost),
		FeatCellShading: drop(NoteContentLost), FeatCellAlign: drop(NoteContentLost),
		FeatDocLanguage: drop(NoteText), FeatTitle: drop(NoteText),
	}),
}

func init() {
	for format, m := range exportMatrix {
		for _, f := range Features {
			if _, ok := m[f]; !ok {
				panic("export: format " + format + " has no fidelity entry for " + string(f))
			}
		}
		if len(m) != len(Features) {
			panic("export: format " + format + " has a fidelity entry for a feature that is not in Features")
		}
	}
}
