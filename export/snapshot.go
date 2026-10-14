package export

// BlockKind mirrors editor's own document.BlockMeta.Kind, translated
// to this package's own type by document.exportSnapshot -- kept as a
// separate type (not editor's own BlockKind) so this package has no
// dependency on editor's internal block-kind numbering at all, only on
// the meaning of each value.
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
	MediaClip // an audio or video clip, written only by the formats that can embed one (HTML)
)

// Format is one character run's own formatting -- editor's own
// charFormat, reduced to only the fields any Exporter actually reads
// (Color/Highlight/FontSize/FontFamily/CommentID are real charFormat
// fields with no Exporter in this package that renders them, matching
// every hand-written format's own long-standing "content over
// presentation" scope -- see the individual Exporter doc comments).
// Href non-empty means this run is a hyperlink -- document.
// exportSnapshot only ever sets it when the run's own StyleName is
// "Link", the same condition every exporter used to check directly
// against charFormat before Snapshot existed.
type Format struct {
	Bold, Italic, Underline, Strikethrough, Superscript, Subscript bool
	Href                                                           string
	// Comment is the IDs (comma separated) of the comments on the run, "" for none.
	Comment string
	// NoteRef is the number of the footnote this run is the mark of (0 for none): the exports that can
	// link the mark to its note, and the note back to the mark, do so.
	NoteRef int
	// Lang is the run's language tag, "" for the document's own.
	Lang string
	// Change says the run is a suggested change not yet accepted or rejected (see FromDocumentTracked): text
	// proposed to be added or proposed to be removed, by Author at Time (unix seconds). A Snapshot
	// from FromDocument never has it: it shows the document as it would be if every suggestion were accepted.
	Change ChangeKind
	Author string
	Time   int64
}

// ChangeKind says whether a run is part of a suggested change.
type ChangeKind int

const (
	NoChange ChangeKind = iota
	Inserted            // text proposed to be added
	Deleted             // text proposed to be removed (still in the document)
)

// Segment is one contiguous run of a block's own text sharing one
// Format, or a single soft line break standing in place of one
// (SoftBreak true, Text/Format both zero) -- document.exportSnapshot
// does this maximal-run splitting once, so every Exporter consumes
// the same pre-split sequence instead of each re-walking the live
// document's own characters to rediscover it (five formats used to
// each do this walk independently before Snapshot existed).
//
// A Code block's own Segments is the one exception: exactly one
// Segment carrying the block's raw, unsplit text with a zero Format
// and SoftBreak false, regardless of any soft breaks or formatting the
// live text actually has -- code content is verbatim by definition
// (every Exporter already treated it that way), and different formats
// disagree on what to do with an embedded soft break inside one (RTF/
// DOCX/PDF leave it as a literal character via their own escaping,
// Markdown's fenced block does too, PlainText alone converts it to a
// real newline), so a single raw segment lets each Exporter keep
// deciding that for itself rather than Snapshot forcing one answer.
type Segment struct {
	Text      string
	SoftBreak bool
	Format    Format
}

// Block is one of a document's own blocks -- BlockMeta plus its
// already-resolved Segments and (for a numbered item) its own already-
// computed 1-based position within its consecutive numbered run
// (NumberedIndex, editor's own numberedItemIndex called once by
// document.exportSnapshot rather than by each Exporter separately).
// TableRow/TableCol/TableRows/TableCols/TableColSpan/TableRowSpan/
// TableCovered/ImageW/ImageH/ImageSrc/ImageCaption are meaningful only
// for their own matching Kind (TableCell/Image respectively) and zero
// otherwise, mirroring BlockMeta's own "fields only some kinds use"
// shape.
type Block struct {
	// Start is where the block's first rune falls in the editor's text stream (canvas.TextFlow.EditText,
	// counted in runes; an inline object such as a picture, a rule, a table or a footnote mark is one
	// rune), -1 for a block the exporter made up (the notes at the end), and of a table cell the place of
	// the table. It is that of the document passed in, so with FromDocument (suggested removals left
	// out) it counts what the stream no longer has. Lead is how many runes at the start of the first
	// segment are not in the stream (the box of a check-list item). The accessible copy uses them to
	// map the caret to its text and back.
	Start, Lead  int
	Kind         BlockKind
	Level        int
	ImageSrc     string
	ImageW       float64
	ImageH       float64
	ImageCaption string
	// ImageAlt describes the image; ImageDecorative says it needs no description
	// (written as an empty alt, left out of the reading copy).
	ImageAlt        string
	ImageDecorative bool
	// For a MediaClip: where the clip is, its kind ("audio" or "video"), its title, a caption track as WebVTT text
	// or its address, its transcript and the size of a video.
	MediaURL, MediaKind, MediaTitle, MediaCaptions, MediaCaptionsURL, MediaTranscript string
	MediaW, MediaH                                                                    float64
	// TableRow/TableCol are this cell's own zero-based grid position;
	// TableRows/TableCols are the whole table's own shape, carried on
	// every cell (mirroring model.BlockMeta's own reasoning, see that
	// field's own doc comment) so an Exporter walking blocks one at a
	// time still knows a table's full extent as soon as it reaches
	// cell (0,0), with no separate lookahead needed. TableColSpan/
	// TableRowSpan are this cell's own already-resolved effective span
	// (model.EffectiveTableSpan -- always >=1, 1 for an ordinary,
	// never-merged cell), meaningful only on an owner cell (mirroring
	// model.BlockMeta.TableColSpan/TableRowSpan's own "meaningless
	// elsewhere" scope). TableCovered is true for a cell absorbed by
	// another cell's own merge (model.BlockMeta.TableMergeOwner != "")
	// -- collapsed to a bool rather than carrying the owner's own ID
	// across, since no Exporter needs to identify *which* owner, only
	// "should I draw this cell's own grid position at all" (every
	// Exporter skips a covered cell's own output entirely, the owner's
	// span already reserves its space in the rendered grid).
	TableRow     int
	TableCol     int
	TableRows    int
	TableCols    int
	TableColSpan int
	TableRowSpan int
	TableCovered bool
	// TableBackground is a cell's fill ("#rrggbb", "" for none) and TableAlign its
	// horizontal alignment (0 left, 1 centre, 2 right).
	TableBackground string
	TableAlign      int
	NumberedIndex   int
	// Note is the number of the footnote a list item is the text of (0 for none).
	Note int
	// CodeLang is the programming language of a Code block as a short lower-case name ("go", "python"),
	// "" when unknown.
	CodeLang string
	// Equation is a formula in a linear notation (see MathML), on a Paragraph block whose Segments are
	// its text alternative: every format shows that text, the HTML export also writes MathML from the formula.
	Equation string
	// DescribesImage says a Paragraph block is the long description of the Image block before it.
	DescribesImage bool
	Segments       []Segment
}

// Snapshot is a complete, self-contained, point-in-time view of one
// document -- everything every Exporter in this package needs, and
// nothing more. ID is document.replica (EPUB's own dc:identifier
// wants a stable-enough-for-one-export unique string); PageWidth/
// PageHeight/Margin* are PDF's own page geometry, in the same CSS-px
// units document.pageDimensions/marginLeft etc. already use --
// unused by every other format, but cheap enough to always populate
// rather than making Snapshot's own shape depend on which Exporter
// the caller happens to want.
type Snapshot struct {
	Title string
	ID    string
	// Lang is the document's language as a BCP 47 tag, "" when it has none: the exporters that can
	// say so (HTML, EPUB, Word) write it, so a reader or a search engine knows how to read the text.
	Lang   string
	Blocks []Block

	// Theme is the document's design-token CSS (see docfile.ThemeKey), "" when it has none. The HTML export
	// writes it when it is a plain :root block and no theme was set on the exporter; anything else is left out.
	Theme string

	// Header and Footer are the running header and footer of the paged formats (PDF, Word, RTF, and the
	// HTML print stylesheet), with the fields {page}, {pages} and {title}; a tab separates left, centre
	// and right parts (see docfile.PageLayout). TOC puts a table of contents from the headings first,
	// under TOCTitle ("Contents" when empty).
	Header, Footer string
	// FirstDiffers gives the first page the header and footer FirstHeader and FirstFooter instead (empty
	// means none there).
	FirstDiffers             bool
	FirstHeader, FirstFooter string
	// EvenDiffers gives the even pages EvenHeader and EvenFooter; Header and Footer are then the odd pages'.
	EvenDiffers            bool
	EvenHeader, EvenFooter string
	TOC                    bool
	TOCTitle               string
	// TOCStyle is "" (a dotted leader from each entry to its page number) or "plain" (no leader), and TOCDepth the
	// deepest heading level listed (0: all three); a table of contents the document holds sets them.
	TOCStyle string
	TOCDepth int

	PageWidth, PageHeight                            float64
	MarginLeft, MarginTop, MarginRight, MarginBottom float64
}
