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
		FeatImageEffects, FeatCellTextStyle, FeatHistory, FeatCodeLanguage} {
		m[f] = drop("")
	}
	// No format plays a clip: it is written as a link with its transcript, and its caption track is not written.
	m[FeatMedia] = simple(NoteMediaText)
	m[FeatMediaCaptions] = drop(NoteCaptionWords)
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
var noPages = map[Feature]entry{FeatPageHeader: drop(""), FeatPageTOC: drop(""), FeatPageSize: drop(""), FeatVertical: drop("")}

// foldedFeatures is what every format built on the Snapshot does with these three (a heading of level 4 to 6 is
// kept: it has its own level, tag or style in each format that has headings).
var foldedFeatures = map[Feature]entry{
	FeatCheckList: simple(NoteGlyph),
	FeatRule:      simple(NoteRuleLine),
	FeatFootnote:  simple(NoteNotes),
}

// textEquation and longDescText are what the formats without MathML do with an equation (its text alternative
// stands for it) and with a diagram's long description (a paragraph after the picture, not linked to it).
var (
	textEquation = map[Feature]entry{FeatEquation: simple(NoteAltText)}
	longDescText = map[Feature]entry{FeatImageLongDesc: simple(NoteLongDescText)}
)

var exportMatrix = map[string]map[Feature]entry{
	// Word keeps the most; it has no colour, size or font, and highlights are not written.
	"docx": formatTable(foldedFeatures, longDescText, map[Feature]entry{
		FeatEquation:  kept(),                         // Office Math, with the words that stand for it as hidden text
		FeatHighlight: drop(""), FeatFootnote: kept(), // a footnote's mark and note link both ways (bookmarks and internal links)
	}),
	// OpenDocument text: like Word it has no colour, size or font here and writes no highlights; the footnotes
	// are real notes. The contents page is not written.
	"odt": formatTable(foldedFeatures, textEquation, longDescText, map[Feature]entry{
		FeatHighlight: drop(""), FeatFootnote: kept(), FeatSuggestion: drop(NoteAccepted),
	}),
	// The PDF is drawn: text, links, tags and tables, without raised or lowered text, shading or row spans.
	"pdf": formatTable(foldedFeatures, textEquation, longDescText, map[Feature]entry{
		FeatSuperscript: drop(""), FeatSubscript: drop(""), FeatHighlight: drop(""),
		FeatSuggestion: drop(NoteAccepted), FeatFootnote: kept(), // the mark links to the note and the note back
		FeatRowSpan: drop(""), FeatCellShading: drop(""), FeatCellAlign: drop(""),
		FeatVertical: drop(""), // the drawn pages are horizontal
	}),
	// Rich Text Format: no embedded pictures, shading, alignment, language or title.
	"rtf": formatTable(foldedFeatures, textEquation, longDescText, map[Feature]entry{
		FeatHighlight: drop(""), FeatRunLanguage: drop(""), FeatSuggestion: drop(NoteAccepted),
		FeatList: simple(NoteBullets), FeatImage: simple(NotePlaceholder), FeatImageAlt: drop(NotePlaceholder),
		FeatCellShading: drop(""), FeatCellAlign: drop(""),
		FeatDocLanguage: drop(""), FeatTitle: drop(""), FeatFootnote: kept(), FeatVertical: drop(""),
	}),
	// HTML and EPUB link a footnote's mark to its note and back, so they keep footnotes.
	"epub": formatTable(foldedFeatures, noPages, map[Feature]entry{FeatHighlight: drop(""), FeatFootnote: kept(), FeatCodeLanguage: kept(), FeatVertical: kept()}),
	// HTML embeds a clip (a player with its caption track and the transcript under it).
	"html": formatTable(foldedFeatures, map[Feature]entry{FeatHighlight: drop(""), FeatFootnote: kept(), FeatCodeLanguage: kept(), FeatMedia: kept(), FeatMediaCaptions: kept()}),
	// Markdown here is the editor's own: its marks for underline, highlight and raised or lowered text are
	// not standard. Tables are written as pipe tables (cell text only: spans, shading and alignment are not) and rules as ---; pictures and footnotes are not written.
	"markdown": formatTable(noPages, textEquation, map[Feature]entry{
		FeatImageLongDesc: drop(NoteContentLost),
		FeatUnderline:     simple(NoteMarks), FeatHighlight: simple(NoteMarks),
		FeatSuperscript: simple(NoteMarks), FeatSubscript: simple(NoteMarks),
		FeatRunLanguage: drop(""), FeatSuggestion: drop(NoteAccepted),
		FeatFootnote: drop(NoteContentLost),
		FeatImage:    drop(NoteContentLost), FeatImageAlt: drop(NoteContentLost),
		FeatColSpan: drop(NoteContentLost), FeatRowSpan: drop(NoteContentLost),
		FeatCellShading: drop(NoteContentLost), FeatCellAlign: drop(NoteContentLost),
		FeatDocLanguage: drop(""), FeatTitle: drop(""),
	}),
	// Plain text keeps the characters of the paragraphs and nothing else.
	"text": formatTable(noPages, textEquation, map[Feature]entry{
		FeatImageLongDesc: drop(NoteContentLost),
		FeatBold:          drop(NoteText), FeatItalic: drop(NoteText), FeatUnderline: drop(NoteText), FeatStrike: drop(NoteText),
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
