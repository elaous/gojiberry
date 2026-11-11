package export

import "github.com/elaous/gojiberry/docfile"

// BlockKind is the kind of a snapshot block.
type BlockKind int

const (
	Paragraph BlockKind = iota
	Heading1
	Heading2
	Heading3
	BulletItem
	NumberedItem
	Quote
	Code
	Image
	TableCell
	MediaClip // An audio or video clip, embedded only by HTML.
)

// Format is one character run's own formatting.
type Format struct {
	Bold, Italic, Underline, Strikethrough, Superscript, Subscript bool
	Href                                                           string
	// Comment is the IDs of the comments on this run.
	Comment string
	// NoteRef is the number of the footnote this run marks.
	NoteRef int
	// NoteBack is the footnote a note's end run points to.
	NoteBack int
	// Lang is the run's language tag.
	Lang string
	// Formula is the notation of a formula in this run.
	Formula string
	// Field is a form field standing in this run's line.
	Field *docfile.FormFieldItem
	// Change says the run is a suggested change.
	Change ChangeKind
	Author string
	Time   int64
}

type ChangeKind int

const (
	NoChange ChangeKind = iota
	Inserted            // text proposed to be added
	Deleted             // text proposed to be removed (still in the document)
)

type Segment struct {
	Text      string
	SoftBreak bool
	Format    Format
}

// Block is one of a document's blocks, with its metadata.
type Block struct {
	// Start is the block's first rune in the edit stream.
	Start, Lead  int
	Kind         BlockKind
	Level        int
	ImageSrc     string
	ImageW       float64
	ImageH       float64
	ImageCaption string
	// ImageAlt describes the image.
	ImageAlt        string
	ImageDecorative bool
	// ImageCredit and ImageSourceURL name the picture's source.
	ImageCredit, ImageSourceURL string
	// For a MediaClip: where the clip is and its kind.
	MediaURL, MediaKind, MediaTitle, MediaCaptions, MediaCaptionsURL, MediaTranscript string
	MediaW, MediaH                                                                    float64
	// TableRow and TableCol are the cell's zero-based grid position.
	TableRow     int
	TableCol     int
	TableRows    int
	TableCols    int
	TableColSpan int
	TableRowSpan int
	TableCovered bool
	// TableBackground is a cell's fill colour.
	TableBackground string
	TableAlign      int
	// TableColW and TableRowH give the cell's column and row sizes.
	TableColW, TableRowH float64
	// TableField is the form field in this table cell.
	TableField    *docfile.FormFieldItem
	NumberedIndex int
	// Note is the footnote number of a list item's text.
	Note int
	// CodeLang is the programming language of a code block.
	CodeLang string
	// Equation is a formula in a linear notation.
	Equation string
	// DescribesImage marks a paragraph as an image's long description.
	DescribesImage bool
	Segments       []Segment
}

// Snapshot is a complete, point-in-time view of a document.
type Snapshot struct {
	Title string
	ID    string
	// Lang is the document's language as a BCP 47 tag.
	Lang   string
	Blocks []Block

	// Theme is the document's design-token CSS.
	Theme string

	// Header and Footer are the running header and footer.
	Header, Footer string
	// FirstDiffers gives the first page its own running text.
	FirstDiffers             bool
	FirstHeader, FirstFooter string
	// EvenDiffers gives even pages their own running text.
	EvenDiffers            bool
	EvenHeader, EvenFooter string
	TOC                    bool
	TOCTitle               string
	// TOCStyle is the table of contents's leader style.
	TOCStyle string
	TOCDepth int
	// TOCAt is the block index where the contents go.
	TOCAt int

	// FormAction is where HTML export sends answers.
	FormAction, FormButton string

	// Vertical sets the text in vertical lines.
	Vertical bool

	PageWidth, PageHeight                            float64
	MarginLeft, MarginTop, MarginRight, MarginBottom float64
}

func (b Block) HeadingLevel() int {
	switch b.Kind {
	case Heading1:
		return 1
	case Heading2:
		return 2
	case Heading3:
		if b.Level >= 4 && b.Level <= 6 {
			return b.Level
		}
		return 3
	}
	return 0
}
