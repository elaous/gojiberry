package export

import (
	"archive/zip"
	"bytes"
	"io"
	"regexp"
	"testing"
	"time"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/transfer"
)

const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="

func ch(text string, f docfile.Format) docfile.Item {
	return docfile.Item{Chars: &docfile.Chars{Text: text, Format: f}}
}

func plain(text string) docfile.Item { return ch(text, docfile.Format{}) }

func docOf(items ...docfile.Item) docfile.Document {
	return docfile.Document{Title: "Fidelity", Content: items}
}

// inlinePair is a paragraph with one run that has the format, and the same paragraph with the run plain.
func inlinePair(f docfile.Format) pair {
	return pair{
		with:    docOf(plain("before "), ch("marked", f), plain(" after\n")),
		without: docOf(plain("before "), plain("marked"), plain(" after\n")),
	}
}

func paraItem(p docfile.Para) docfile.Item { return docfile.Item{Para: &p} }

// blockPair is a paragraph with the paragraph format and the same paragraph without it.
func blockPair(p docfile.Para) pair {
	return pair{
		with:    docOf(paraItem(p), plain("the paragraph\n"), plain("next\n")),
		without: docOf(plain("the paragraph\n"), plain("next\n")),
	}
}

func tableOf(t docfile.TableItem) docfile.Document {
	return docOf(plain("lead\n"), docfile.Item{Table: &t}, plain("tail"))
}

func rows4() []docfile.TableRow {
	return []docfile.TableRow{{Cells: []string{"a", "b", "c"}}, {Cells: []string{"d", "e", "f"}}, {Cells: []string{"g", "h", "i"}}}
}

// pair is a document using one feature and the same document with the feature left out. Where the feature is
// folded into another construct (a rule into a line of characters), folded is what the document turns into:
// the exporters must write it exactly as they write the folded document.
type pair struct {
	with, without docfile.Document
	folded        *docfile.Document
}

func suggestionMark() string {
	return canvas.ProposalMark{Kind: canvas.ProposalInsert, ID: "s1", Author: "Ann", Time: time.Unix(1700000000, 0)}.String()
}

// featurePairs has a pair for every Feature (a test checks that none is missing).
func featurePairs() map[Feature]pair {
	bold := docfile.Format{FontWeight: 700}
	m := map[Feature]pair{
		FeatBold:        inlinePair(bold),
		FeatItalic:      inlinePair(docfile.Format{FontStyle: "italic"}),
		FeatUnderline:   inlinePair(docfile.Format{Underline: true}),
		FeatStrike:      inlinePair(docfile.Format{Strike: true}),
		FeatSuperscript: inlinePair(docfile.Format{Superscript: true}),
		FeatSubscript:   inlinePair(docfile.Format{Subscript: true}),
		FeatLink:        inlinePair(docfile.Format{Link: "https://example.com/x"}),
		FeatColour:      inlinePair(docfile.Format{Color: "#ff0000"}),
		FeatHighlight:   inlinePair(docfile.Format{Background: "#ffff00"}),
		FeatFontSize:    inlinePair(docfile.Format{FontSize: 30}),
		FeatFontFamily:  inlinePair(docfile.Format{FontFamily: "Georgia"}),
		FeatTextCase:    inlinePair(docfile.Format{Case: 1}),
		FeatRunLanguage: inlinePair(docfile.Format{Lang: "fr"}),
		FeatSuggestion:  inlinePair(docfile.Format{Proposal: suggestionMark()}),
		FeatBookmark:    inlinePair(docfile.Format{Bookmark: "mark1"}),
		FeatComment: {
			with:    docOf(plain("before "), ch("marked", docfile.Format{Comment: "c1"}), plain(" after\n")),
			without: docOf(plain("before "), plain("marked"), plain(" after\n")),
		},
		FeatHeading: {
			with:    docOf(ch("Heading", docfile.Format{Scale: 1.6}), plain("\n"), plain("body\n")),
			without: docOf(plain("Heading\n"), plain("body\n")),
		},
		FeatCode: {
			with:    docOf(ch("x := 1", docfile.Format{FontFamily: "monospace"}), plain("\n"), plain("body\n")),
			without: docOf(plain("x := 1\n"), plain("body\n")),
		},
		FeatList:            blockPair(docfile.Para{List: docfile.List{Kind: 1}}),
		FeatQuote:           blockPair(docfile.Para{QuoteBar: "#999999"}),
		FeatAlignment:       blockPair(docfile.Para{Align: 2}),
		FeatParagraphLayout: blockPair(docfile.Para{IndentLeft: 40, SpaceAfter: 12}),
		FeatParagraphStyle:  blockPair(docfile.Para{Style: "My style"}),
	}
	m[FeatNestedList] = pair{
		with: docOf(paraItem(docfile.Para{List: docfile.List{Kind: 1}}), plain("top\n"),
			paraItem(docfile.Para{List: docfile.List{Kind: 1, Level: 1}}), plain("inner\n")),
		without: docOf(paraItem(docfile.Para{List: docfile.List{Kind: 1}}), plain("top\n"),
			paraItem(docfile.Para{List: docfile.List{Kind: 1}}), plain("inner\n")),
	}
	deepFolded := docOf(paraItem(docfile.Para{Style: "Heading 3"}), plain("Deep\n"), plain("body\n"))
	m[FeatDeepHeading] = pair{
		with:    docOf(paraItem(docfile.Para{Style: "Heading 5"}), plain("Deep\n"), plain("body\n")),
		without: docOf(plain("Deep\n"), plain("body\n")),
		folded:  &deepFolded,
	}
	checkFolded := docOf(paraItem(docfile.Para{List: docfile.List{Kind: 1}}), plain("☑ "), plain("done\n"), plain("body\n"))
	m[FeatCheckList] = pair{
		with:    docOf(paraItem(docfile.Para{List: docfile.List{Kind: 3, Checked: true}}), plain("done\n"), plain("body\n")),
		without: docOf(plain("done\n"), plain("body\n")),
		folded:  &checkFolded,
	}
	m[FeatColumns] = pair{
		with:    docOf(plain("one\n"), docfile.Item{Columns: &docfile.Columns{Count: 2, Gap: 20}}, plain("two\n")),
		without: docOf(plain("one\n"), plain("two\n")),
	}
	m[FeatPageBreak] = pair{
		with:    docOf(plain("one\n"), docfile.Item{Break: &docfile.Break{Kind: 1}}, plain("two\n")),
		without: docOf(plain("one\n"), plain("two\n")),
	}
	ruleFolded := docOf(plain("one\n"), plain("────────────────────────\n"), plain("two\n"))
	m[FeatRule] = pair{
		with:    docOf(plain("one\n"), docfile.Item{Rule: &docfile.RuleItem{}}, plain("two\n")),
		without: docOf(plain("one\n"), plain("two\n")),
		folded:  &ruleFolded,
	}
	footWith := docOf(plain("see"), docfile.Item{Footnote: &docfile.FootnoteMark{ID: "n1"}}, plain(" here\n"))
	footWith.Footnotes = []docfile.Footnote{{ID: "n1", Text: "The note."}}
	footFolded := docOf(plain("see"), ch("1", docfile.Format{Superscript: true}), plain(" here\n"),
		ch("Notes", docfile.Format{Scale: 1.6}), plain("\n"),
		paraItem(docfile.Para{List: docfile.List{Kind: 2}}), plain("The note.\n"))
	m[FeatFootnote] = pair{with: footWith, without: docOf(plain("see here\n")), folded: &footFolded}
	m[FeatContents] = pair{
		with:    docOf(plain("one\n"), docfile.Item{TOC: &docfile.Block{}}, plain("two\n")),
		without: docOf(plain("one\n"), plain("two\n")),
	}
	m[FeatImage] = pair{
		with:    docOf(plain("one\n"), docfile.Item{Image: &docfile.ImageItem{URL: tinyPNG, Width: 40, Height: 40}}, plain("two\n")),
		without: docOf(plain("one\n"), plain("two\n")),
	}
	m[FeatImageAlt] = pair{
		with:    docOf(plain("one\n"), docfile.Item{Image: &docfile.ImageItem{URL: tinyPNG, Width: 40, Height: 40, Alt: "A blue dot"}}, plain("two\n")),
		without: docOf(plain("one\n"), docfile.Item{Image: &docfile.ImageItem{URL: tinyPNG, Width: 40, Height: 40}}, plain("two\n")),
	}
	m[FeatImageEffects] = pair{
		with:    docOf(plain("one\n"), docfile.Item{Image: &docfile.ImageItem{URL: tinyPNG, Width: 40, Height: 40, Alpha: 0.5, ClipLeft: 1}}, plain("two\n")),
		without: docOf(plain("one\n"), docfile.Item{Image: &docfile.ImageItem{URL: tinyPNG, Width: 40, Height: 40}}, plain("two\n")),
	}
	m[FeatTable] = pair{
		with:    tableOf(docfile.TableItem{Rows: rows4()}),
		without: docOf(plain("lead\n"), plain("tail")),
	}
	merged := func(mg docfile.TableMerge) docfile.Document {
		return tableOf(docfile.TableItem{Rows: []docfile.TableRow{{Cells: []string{"a", "", "c"}}, {Cells: []string{"", "", "f"}}, {Cells: []string{"g", "h", "i"}}}, Merges: []docfile.TableMerge{mg}})
	}
	unmerged := tableOf(docfile.TableItem{Rows: []docfile.TableRow{{Cells: []string{"a", "", "c"}}, {Cells: []string{"", "", "f"}}, {Cells: []string{"g", "h", "i"}}}})
	m[FeatColSpan] = pair{with: merged(docfile.TableMerge{Row: 0, Col: 0, Rows: 1, Cols: 2}), without: unmerged}
	m[FeatRowSpan] = pair{with: merged(docfile.TableMerge{Row: 0, Col: 0, Rows: 2, Cols: 1}), without: unmerged}
	styled := func(s docfile.TableCellStyle) docfile.Document {
		return tableOf(docfile.TableItem{Rows: rows4(), Styles: []docfile.TableCellStyle{s}})
	}
	m[FeatCellShading] = pair{with: styled(docfile.TableCellStyle{Row: 1, Col: 1, Background: "#ddeeff"}), without: tableOf(docfile.TableItem{Rows: rows4()})}
	m[FeatCellAlign] = pair{with: styled(docfile.TableCellStyle{Row: 1, Col: 1, Align: 2}), without: tableOf(docfile.TableItem{Rows: rows4()})}
	m[FeatCellTextStyle] = pair{with: styled(docfile.TableCellStyle{Row: 1, Col: 1, Italic: true, Color: "#ff0000"}), without: tableOf(docfile.TableItem{Rows: rows4()})}
	langDoc := docOf(plain("hello\n"))
	langDoc.SetLanguage("de")
	m[FeatDocLanguage] = pair{with: langDoc, without: docOf(plain("hello\n"))}
	m[FeatTitle] = pair{with: docOf(plain("hello\n")), without: func() docfile.Document { d := docOf(plain("hello\n")); d.Title = ""; return d }()}
	hist := docOf(plain("hello\n"))
	hist.History = []docfile.Revision{{Saved: 1, Doc: &docfile.Document{Title: "old"}}}
	m[FeatHistory] = pair{with: hist, without: docOf(plain("hello\n"))}
	talk := docfile.MediaItem{URL: "https://example.com/talk.mp4", Title: "The talk", Transcript: "Spoken words."}
	captioned := talk
	captioned.Captions = "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nSpoken words.\n"
	m[FeatMedia] = pair{
		with:    docOf(plain("one\n"), docfile.Item{Media: &talk}, plain("two\n")),
		without: docOf(plain("one\n"), plain("two\n")),
	}
	m[FeatMediaCaptions] = pair{
		with:    docOf(plain("one\n"), docfile.Item{Media: &captioned}, plain("two\n")),
		without: docOf(plain("one\n"), docfile.Item{Media: &talk}, plain("two\n")),
	}
	m[FeatEquation] = pair{
		with:    docOf(plain("one\n"), docfile.Item{Equation: &docfile.EquationItem{Formula: "x^2+y^2=z^2", Alt: "x squared plus y squared equals z squared"}}, plain("two\n")),
		without: docOf(plain("one\n"), plain("two\n")),
	}
	codeBlock := func(lang string) docfile.Document {
		return docOf(paraItem(docfile.Para{Style: "Code", CodeLang: lang}), plain("x := 1\n"), plain("body\n"))
	}
	m[FeatCodeLanguage] = pair{with: codeBlock("go"), without: codeBlock("")}
	m[FeatImageLongDesc] = pair{
		with:    docOf(plain("one\n"), docfile.Item{Image: &docfile.ImageItem{URL: tinyPNG, Width: 40, Height: 40, Alt: "Flow chart", LongDesc: "The chart has three boxes: input, process and output, joined by arrows."}}, plain("two\n")),
		without: docOf(plain("one\n"), docfile.Item{Image: &docfile.ImageItem{URL: tinyPNG, Width: 40, Height: 40, Alt: "Flow chart"}}, plain("two\n")),
	}
	layout := func(l docfile.PageLayout) pair {
		with := docOf(ch("Heading", docfile.Format{Scale: 1.6}), plain("\n"), plain("body\n"))
		with.SetPageLayout(l)
		return pair{with: with, without: docOf(ch("Heading", docfile.Format{Scale: 1.6}), plain("\n"), plain("body\n"))}
	}
	m[FeatPageHeader] = layout(docfile.PageLayout{Header: "{title}\tPage {page} of {pages}", Footer: "End"})
	m[FeatPageTOC] = layout(docfile.PageLayout{TOC: true})
	m[FeatVertical] = layout(docfile.PageLayout{Vertical: true})
	m[FeatPageSize] = layout(docfile.PageLayout{Width: 595, Height: 842, Margins: [4]float64{50, 50, 50, 50}})
	return m
}

func registry() *transfer.Registry {
	r := transfer.Default()
	Register(r)
	return r
}

var dateRE = regexp.MustCompile(`<meta property="dcterms:modified">[^<]*</meta>|D:\d{14}[^)]*|/CreationDate ?\([^)]*\)|/ModDate ?\([^)]*\)|/ID ?\[[^\]]*\]`)

// normalised hides what changes from one export to the next of the same document (dates and ids).
// A zip (Word, EPUB) is reduced to its entries' names and contents: the file's own bytes carry the time it was
// written. Search in what this returns for the words of a zip's parts (they are stored uncompressed here).
func normalised(b []byte) []byte {
	if zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b))); err == nil {
		var out bytes.Buffer
		for _, f := range zr.File {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(rc)
			_ = rc.Close()
			out.WriteString("\x00" + f.Name + "\x00")
			out.Write(data)
		}
		return dateRE.ReplaceAll(out.Bytes(), nil)
	}
	return dateRE.ReplaceAll(b, nil)
}

func exportOne(t *testing.T, r *transfer.Registry, format string, d docfile.Document) []byte {
	t.Helper()
	_, data, _, err := r.Export(format, d)
	if err != nil {
		t.Fatalf("%s: %v", format, err)
	}
	return normalised(data)
}

// TestFidelityMatrixMatchesTheExporters is what keeps a loss from becoming silent: for every feature and
// format it exports a document with the feature and the same document without it, and the table must say what
// happened. A feature the table calls Dropped must leave the file unchanged (the format does not write it); one
// it calls Kept or Simplified must change it; one folded into another construct must be written exactly as that
// construct. An exporter that starts losing a feature, or stops losing it, fails here until the table (and with
// it the report people see) is updated.
func TestFidelityMatrixMatchesTheExporters(t *testing.T) {
	r := registry()
	pairs := featurePairs()
	for _, f := range Features {
		p, ok := pairs[f]
		if !ok {
			t.Fatalf("feature %q has no with/without pair in featurePairs", f)
		}
		for _, format := range Formats {
			e := exportMatrix[format][f]
			with, without := exportOne(t, r, format, p.with), exportOne(t, r, format, p.without)
			switch {
			case e.t == Dropped:
				if !bytes.Equal(with, without) {
					t.Errorf("%s / %s: the table says it is dropped, but the file changes with it: the format writes it now (update exportMatrix)", format, f)
				}
			case e.t == Simplified && p.folded != nil:
				if folded := exportOne(t, r, format, *p.folded); !bytes.Equal(with, folded) {
					t.Errorf("%s / %s: the table says it is written as %q, but the file differs from the folded document", format, f, e.note)
				}
			default:
				if bytes.Equal(with, without) {
					t.Errorf("%s / %s: the table says it is %s, but the file is the same without it: the format drops it now (update exportMatrix)", format, f, e.t)
				}
			}
		}
	}
}
