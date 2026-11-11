package export

// Two more notes, for what only some formats do.
const (
	// NoteContentLost: the content, not only its marking, is left out.
	NoteContentLost = "content-lost"
	// NoteMarks: marks only the editor's Markdown reads back.
	NoteMarks = "marks"
)

func kept() entry { return entry{} }

func drop(note string) entry { return entry{Dropped, note} }

func simple(note string) entry { return entry{Simplified, note} }

func formatTable(overrides ...map[Feature]entry) map[Feature]entry {
	m := map[Feature]entry{}
	for _, f := range []Feature{FeatColour, FeatFontSize, FeatFontFamily, FeatTextCase, FeatComment, FeatBookmark,
		FeatAlignment, FeatParagraphLayout, FeatParagraphStyle, FeatColumns, FeatPageBreak, FeatContents,
		FeatImageEffects, FeatCellTextStyle, FeatHistory, FeatCodeLanguage} {
		m[f] = drop("")
	}
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

var noPages = map[Feature]entry{FeatPageHeader: drop(""), FeatPageTOC: drop(""), FeatPageSize: drop(""), FeatVertical: drop("")}

var foldedFeatures = map[Feature]entry{
	FeatCheckList: simple(NoteGlyph),
	FeatRule:      simple(NoteRuleLine),
	FeatFootnote:  simple(NoteNotes),
}

var (
	textEquation = map[Feature]entry{FeatEquation: simple(NoteAltText)}
	longDescText = map[Feature]entry{FeatImageLongDesc: simple(NoteLongDescText)}
	textForm     = map[Feature]entry{FeatFormField: simple(NoteFormText)}
)

var exportMatrix = map[string]map[Feature]entry{
	"docx": formatTable(foldedFeatures, textForm, longDescText, map[Feature]entry{
		FeatEquation:  kept(),
		FeatHighlight: drop(""), FeatFootnote: kept(),
	}),
	"odt": formatTable(foldedFeatures, textForm, textEquation, longDescText, map[Feature]entry{
		FeatHighlight: drop(""), FeatFootnote: kept(), FeatSuggestion: drop(NoteAccepted),
	}),
	"pdf": formatTable(foldedFeatures, textForm, textEquation, longDescText, map[Feature]entry{
		FeatSuperscript: drop(""), FeatSubscript: drop(""), FeatHighlight: drop(""),
		FeatSuggestion: drop(NoteAccepted), FeatFootnote: kept(), // the mark links to the note and the note back
		FeatRowSpan: drop(""), FeatCellShading: drop(""), FeatCellAlign: drop(""),
		FeatVertical: drop(""), // the drawn pages are horizontal
	}),
	"rtf": formatTable(foldedFeatures, textForm, textEquation, longDescText, map[Feature]entry{
		FeatHighlight: drop(""), FeatRunLanguage: drop(""), FeatSuggestion: drop(NoteAccepted),
		FeatList: simple(NoteBullets), FeatImage: simple(NotePlaceholder), FeatImageAlt: drop(NotePlaceholder),
		FeatCellShading: drop(""), FeatCellAlign: drop(""),
		FeatDocLanguage: drop(""), FeatTitle: drop(""), FeatFootnote: kept(), FeatVertical: drop(""),
	}),
	"epub": formatTable(foldedFeatures, noPages, textForm, map[Feature]entry{FeatHighlight: drop(""), FeatFootnote: kept(), FeatCodeLanguage: kept(), FeatVertical: kept()}),
	"html": formatTable(foldedFeatures, textForm, map[Feature]entry{FeatFormField: simple(NoteFormControl), FeatHighlight: drop(""), FeatFootnote: kept(), FeatCodeLanguage: kept(), FeatMedia: kept(), FeatMediaCaptions: kept()}),
	"markdown": formatTable(noPages, textForm, textEquation, map[Feature]entry{
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
	"text": formatTable(noPages, textForm, textEquation, map[Feature]entry{
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
