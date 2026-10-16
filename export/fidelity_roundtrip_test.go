package export

import (
	"archive/zip"
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/transfer"
)

// charsWith finds the first run of d whose text contains word.
func charsWith(d docfile.Document, word string) *docfile.Chars {
	for _, it := range d.Content {
		if it.Chars != nil && strings.Contains(it.Chars.Text, word) {
			return it.Chars
		}
	}
	return nil
}

func hasPara(d docfile.Document, ok func(docfile.Para) bool) bool {
	for _, it := range d.Content {
		if it.Para != nil && ok(*it.Para) {
			return true
		}
	}
	return false
}

func hasItem(d docfile.Document, ok func(docfile.Item) bool) bool {
	return slices.ContainsFunc(d.Content, ok)
}

// survives says whether a feature of the paired fixtures is in a document read back from a file.
func survives(f Feature, d docfile.Document) bool {
	marked := charsWith(d, "marked")
	inline := func(ok func(docfile.Format) bool) bool { return marked != nil && ok(marked.Format) }
	var table *docfile.TableItem
	var image *docfile.ImageItem
	for _, it := range d.Content {
		if it.Table != nil {
			table = it.Table
		}
		if it.Image != nil {
			image = it.Image
		}
	}
	headingStyle := func(p docfile.Para) bool { return strings.HasPrefix(p.Style, "Heading") }
	switch f {
	case FeatBold:
		return inline(func(x docfile.Format) bool { return x.FontWeight >= 600 })
	case FeatItalic:
		return inline(func(x docfile.Format) bool { return x.FontStyle == "italic" })
	case FeatUnderline:
		return inline(func(x docfile.Format) bool { return x.Underline })
	case FeatStrike:
		return inline(func(x docfile.Format) bool { return x.Strike })
	case FeatSuperscript:
		return inline(func(x docfile.Format) bool { return x.Superscript })
	case FeatSubscript:
		return inline(func(x docfile.Format) bool { return x.Subscript })
	case FeatLink:
		return inline(func(x docfile.Format) bool { return x.Link == "https://example.com/x" })
	case FeatColour:
		return inline(func(x docfile.Format) bool { return x.Color != "" })
	case FeatHighlight:
		return inline(func(x docfile.Format) bool { return x.Background != "" })
	case FeatFontSize:
		return inline(func(x docfile.Format) bool { return x.FontSize != 0 })
	case FeatFontFamily:
		return inline(func(x docfile.Format) bool { return x.FontFamily != "" })
	case FeatTextCase:
		return inline(func(x docfile.Format) bool { return x.Case != 0 })
	case FeatRunLanguage:
		return inline(func(x docfile.Format) bool { return x.Lang == "fr" })
	case FeatSuggestion:
		return inline(func(x docfile.Format) bool { return x.Proposal != "" })
	case FeatComment:
		return inline(func(x docfile.Format) bool { return x.Comment != "" }) || len(d.Comments) > 0
	case FeatBookmark:
		return inline(func(x docfile.Format) bool { return x.Bookmark != "" })
	case FeatHeading:
		c := charsWith(d, "Heading")
		return c != nil && c.Format.Scale > 1 || hasPara(d, headingStyle)
	case FeatDeepHeading:
		c := charsWith(d, "Deep")
		return c != nil && c.Format.Scale > 1 || hasPara(d, headingStyle)
	case FeatList:
		return hasPara(d, func(p docfile.Para) bool { return p.List.Kind != 0 })
	case FeatNestedList:
		return hasPara(d, func(p docfile.Para) bool { return p.List.Level > 0 })
	case FeatCheckList:
		return hasPara(d, func(p docfile.Para) bool { return p.List.Kind == 3 })
	case FeatQuote:
		return hasPara(d, func(p docfile.Para) bool { return p.QuoteBar != "" })
	case FeatCode:
		c := charsWith(d, "x := 1")
		return c != nil && c.Format.FontFamily == "monospace"
	case FeatAlignment:
		return hasPara(d, func(p docfile.Para) bool { return p.Align != 0 })
	case FeatParagraphLayout:
		return hasPara(d, func(p docfile.Para) bool { return p.IndentLeft != 0 || p.SpaceAfter != 0 })
	case FeatParagraphStyle:
		return hasPara(d, func(p docfile.Para) bool { return p.Style == "My style" })
	case FeatColumns:
		return hasItem(d, func(it docfile.Item) bool { return it.Columns != nil })
	case FeatPageBreak:
		return hasItem(d, func(it docfile.Item) bool { return it.Break != nil })
	case FeatRule:
		return hasItem(d, func(it docfile.Item) bool { return it.Rule != nil })
	case FeatFootnote:
		return len(d.Footnotes) > 0
	case FeatContents:
		return hasItem(d, func(it docfile.Item) bool { return it.TOC != nil })
	case FeatImage:
		return image != nil
	case FeatImageAlt:
		return image != nil && image.Alt == "A blue dot"
	case FeatImageEffects:
		return image != nil && (image.Alpha != 0 || image.ClipLeft != 0)
	case FeatTable:
		return table != nil
	case FeatColSpan:
		return table != nil && slices.ContainsFunc(table.Merges, func(m docfile.TableMerge) bool { return m.Cols > 1 })
	case FeatRowSpan:
		return table != nil && slices.ContainsFunc(table.Merges, func(m docfile.TableMerge) bool { return m.Rows > 1 })
	case FeatCellShading:
		return table != nil && slices.ContainsFunc(table.Styles, func(s docfile.TableCellStyle) bool { return s.Background != "" })
	case FeatCellAlign:
		return table != nil && slices.ContainsFunc(table.Styles, func(s docfile.TableCellStyle) bool { return s.Align != 0 })
	case FeatCellTextStyle:
		return table != nil && slices.ContainsFunc(table.Styles, func(s docfile.TableCellStyle) bool { return s.Italic || s.Color != "" })
	case FeatDocLanguage:
		return d.Language() == "de"
	case FeatTitle:
		return d.Title == "Fidelity"
	case FeatHistory:
		return len(d.History) > 0
	case FeatMedia, FeatMediaCaptions:
		return hasItem(d, func(it docfile.Item) bool { return it.Media != nil })
	case FeatEquation:
		return hasItem(d, func(it docfile.Item) bool { return it.Equation != nil })
	case FeatCodeLanguage:
		return hasPara(d, func(p docfile.Para) bool { return p.CodeLang != "" })
	case FeatImageLongDesc:
		return image != nil && image.LongDesc != ""
	case FeatPageHeader:
		l := d.PageLayout()
		return l.Header != "" || l.Footer != ""
	case FeatPageTOC:
		return d.PageLayout().TOC
	case FeatPageSize:
		return d.PageLayout().Width > 0
	case FeatVertical:
		return d.PageLayout().Vertical
	}
	return false
}

func importBack(t *testing.T, r *transfer.Registry, format string, data []byte) docfile.Document {
	t.Helper()
	name := map[string]string{"docx": "a.docx", "pdf": "a.pdf", "markdown": "a.md", "text": "a.txt"}[format]
	d, err := r.Import(format, name, data)
	if err != nil {
		t.Fatalf("%s: import: %v", format, err)
	}
	return d
}

// notReadBack is what a format writes but its importer does not read: a loss on the way in, recorded here so a
// new one fails the round-trip test. (ImportFidelity says so for a file that holds such things.)
var notReadBack = map[string]map[Feature]string{
	"docx": {
		FeatPageHeader: "headers and footers are not read (docximport.Inspect counts them)",
		FeatPageTOC:    "a generated table of contents is read as the text it shows, not as a setting",
		FeatPageSize:   "the page size and margins are not read",
		FeatFootnote:   "a footnote's mark and note are read back as a number and the numbered list at the end, not as a footnote",
	},
}

// TestRoundTripReadsBackWhatTheTableSaysIsKept exports each paired document, imports the file again and checks
// the feature against the table: what a format keeps (Kept) must come back, what it drops must not appear out of
// nowhere, and what is folded into another construct (a check box into a character) comes back as that construct,
// not as the feature. The formats that can be read again are Word, Markdown and plain text; PDF is below.
func TestRoundTripReadsBackWhatTheTableSaysIsKept(t *testing.T) {
	r := registry()
	pairs := featurePairs()
	for _, format := range []string{"docx", "markdown", "text"} {
		for _, f := range Features {
			e := exportMatrix[format][f]
			_, data, _, err := r.Export(format, pairs[f].with)
			if err != nil {
				t.Fatal(err)
			}
			back := survives(f, importBack(t, r, format, data))
			switch e.t {
			case Kept:
				if why, known := notReadBack[format][f]; known {
					if back {
						t.Errorf("%s / %s: now read back (%s is out of date)", format, f, why)
					}
				} else if !back {
					t.Errorf("%s / %s: written, but not read back by the importer: a loss on the way in that the table does not know (fix the importer, or record it)", format, f)
				}
			case Dropped:
				if back {
					t.Errorf("%s / %s: the table says the export drops it, but it came back", format, f)
				}
			case Simplified:
				if back && (f == FeatCheckList || f == FeatRule || f == FeatFootnote) {
					t.Errorf("%s / %s: written as %q, but read back as the feature itself", format, f, e.note)
				}
			}
		}
	}
}

// PDF import reads text and nothing else: not even a bold run or a heading comes back, and ImportFidelity says so.
func TestPDFRoundTripKeepsTheTextAndNothingElse(t *testing.T) {
	r := registry()
	pairs := featurePairs()
	for _, f := range Features {
		_, data, _, err := r.Export("pdf", pairs[f].with)
		if err != nil {
			t.Fatal(err)
		}
		back := importBack(t, r, "pdf", data)
		if survives(f, back) {
			t.Errorf("pdf / %s: came back from a PDF, which the importer reads as text only (update ImportFidelity)", f)
		}
	}
	_, data, _, _ := r.Export("pdf", pairs[FeatBold].with)
	back := importBack(t, r, "pdf", data)
	var text strings.Builder
	for _, it := range back.Content {
		if it.Chars != nil {
			text.WriteString(it.Chars.Text)
		}
	}
	for _, w := range []string{"before", "marked", "after"} {
		if !strings.Contains(text.String(), w) {
			t.Errorf("the text %q did not come back from the PDF: %q", w, text.String())
		}
	}
	rep := ImportFidelity("pdf", data)
	if !rep.Known || len(rep.Losses) != 1 || rep.Losses[0].Feature != FeatLayout || rep.Losses[0].Treatment != Dropped {
		t.Errorf("PDF import report: %+v", rep)
	}
}

// What Word files hold that the importer leaves out is reported from the file, and a file this editor wrote
// reports nothing: the round trip of an exported document loses nothing the table did not already say.
func TestImportFidelityOfWordFiles(t *testing.T) {
	r := registry()
	pairs := featurePairs()
	_, data, _, err := r.Export("docx", pairs[FeatImageAlt].with)
	if err != nil {
		t.Fatal(err)
	}
	rep := ImportFidelity("docx", data)
	if !rep.Known || !rep.Lossless() {
		t.Errorf("a file exported by the editor reports losses on the way in: %+v", rep)
	}

	// The same file with a comments part and a header added reports both.
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		w, _ := zw.Create(f.Name)
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		_, _ = w.Write(b)
	}
	w, _ := zw.Create("word/comments.xml")
	_, _ = w.Write([]byte(`<w:comments xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:comment w:id="0"><w:p><w:r><w:t>Hi</w:t></w:r></w:p></w:comment></w:comments>`))
	w, _ = zw.Create("word/header1.xml")
	_, _ = w.Write([]byte(`<w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>Company</w:t></w:r></w:p></w:hdr>`))
	_ = zw.Close()
	rep = ImportFidelity("docx", buf.Bytes())
	got := map[Feature]int{}
	for _, l := range rep.Losses {
		got[l.Feature] = l.Count
		if l.Treatment != Dropped {
			t.Errorf("%s: treatment %v", l.Feature, l.Treatment)
		}
	}
	if len(got) != 1 || got[FeatHeaderFooter] != 1 {
		t.Errorf("import report = %v, want only the header: comments are read now", got)
	}
	if rep := ImportFidelity("docx", []byte("not a zip")); rep.Known || len(rep.Losses) != 0 {
		t.Errorf("a file that is not a Word document is not reported on: %+v", rep)
	}
	if rep := ImportFidelity("rtf", nil); rep.Known {
		t.Errorf("a format with no importer report is Known: %+v", rep)
	}
}
