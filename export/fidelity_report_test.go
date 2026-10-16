package export

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/pdftext"
	"github.com/elaous/gojiberry/transfer"
)

// utf16hex is a PDF text string's hex form after the byte order mark.
func utf16hex(s string) string {
	var sb strings.Builder
	for _, u := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&sb, "%04X", u)
	}
	return sb.String()
}

func rawOne(t *testing.T, r *transfer.Registry, format string, d docfile.Document) []byte {
	t.Helper()
	_, data, _, err := r.Export(format, d)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// everything is one document that uses every feature.
func everything() docfile.Document {
	d := docfile.Document{Title: "Everything"}
	d.SetLanguage("de")
	for _, f := range Features {
		p := featurePairs()[f]
		d.Content = append(d.Content, p.with.Content...)
		d.Footnotes = append(d.Footnotes, p.with.Footnotes...)
		d.History = append(d.History, p.with.History...)
	}
	d.Comments = []docfile.Comment{{ID: "c1", Text: "a comment"}}
	d.SetPageLayout(docfile.PageLayout{Header: "{title}", Footer: "{page}", TOC: true, Vertical: true, Width: 595, Height: 842, Margins: [4]float64{50, 50, 50, 50}})
	return d
}

func TestUsageFindsEveryFeatureInItsPairAndOnlyThere(t *testing.T) {
	for _, f := range Features {
		p := featurePairs()[f]
		if Usage(p.with)[f] == 0 {
			t.Errorf("%s: Usage does not see it in the document that uses it", f)
		}
		if n := Usage(p.without)[f]; n != 0 {
			t.Errorf("%s: Usage sees it %d times in the document without it", f, n)
		}
	}
	use := Usage(everything())
	for _, f := range Features {
		if use[f] == 0 {
			t.Errorf("%s: not counted in the document that uses everything", f)
		}
	}
}

// The report lists exactly the losses the table records, for the features the document uses, the dropped ones
// first, each with how many places it concerns; and nothing for a feature the document does not use.
func TestFidelityReportListsEveryLossAndOnlyThose(t *testing.T) {
	all := everything()
	for _, format := range Formats {
		rep := Fidelity(format, all)
		if !rep.Known || rep.Complete {
			t.Errorf("%s: Known=%v Complete=%v", format, rep.Known, rep.Complete)
		}
		got := map[Feature]Loss{}
		seenSimplified := false
		for _, l := range rep.Losses {
			got[l.Feature] = l
			if l.Count <= 0 {
				t.Errorf("%s / %s: count %d", format, l.Feature, l.Count)
			}
			if l.Treatment == Simplified {
				seenSimplified = true
			} else if seenSimplified {
				t.Errorf("%s: a dropped feature (%s) is listed after a simplified one", format, l.Feature)
			}
		}
		for _, f := range Features {
			e := exportMatrix[format][f]
			l, listed := got[f]
			want := e.t != Kept && Reported(format, f)
			if listed != want {
				t.Errorf("%s / %s: listed=%v, want %v (table: %s)", format, f, listed, want, e.t)
			}
			if listed && (l.Treatment != e.t || l.Note != e.note) {
				t.Errorf("%s / %s: reported %s/%q, table says %s/%q", format, f, l.Treatment, l.Note, e.t, e.note)
			}
		}
		// A document without the feature does not list it.
		for _, f := range Features {
			if _, listed := got[f]; !listed {
				continue
			}
			for _, l := range Fidelity(format, featurePairs()[f].without).Losses {
				if l.Feature == f {
					t.Errorf("%s / %s: listed for a document that does not use it", format, f)
				}
			}
			found := false
			for _, l := range Fidelity(format, featurePairs()[f].with).Losses {
				found = found || l.Feature == f
			}
			if !found {
				t.Errorf("%s / %s: not listed for the document that uses it", format, f)
			}
		}
	}
}

// The facts the person is told, spelled out for the examples the roadmap names.
func TestFidelityReportNamedExamples(t *testing.T) {
	pairs := featurePairs()
	find := func(format string, f Feature) (Loss, bool) {
		for _, l := range Fidelity(format, pairs[f].with).Losses {
			if l.Feature == f {
				return l, true
			}
		}
		return Loss{}, false
	}
	for _, c := range []struct {
		format string
		f      Feature
		t      Treatment
		note   string
	}{
		{"pdf", FeatSuperscript, Dropped, ""},
		{"pdf", FeatRowSpan, Dropped, ""},
		{"text", FeatComment, Dropped, ""},
		{"text", FeatLink, Dropped, NoteText},
		{"markdown", FeatColSpan, Dropped, NoteContentLost},
		{"rtf", FeatImage, Simplified, NotePlaceholder},
		{"docx", FeatCheckList, Simplified, NoteGlyph},
		{"epub", FeatColour, Dropped, ""},
	} {
		l, ok := find(c.format, c.f)
		if !ok || l.Treatment != c.t || l.Note != c.note {
			t.Errorf("%s / %s: got %+v (listed %v), want %s %q", c.format, c.f, l, ok, c.t, c.note)
		}
	}
	// What is kept is not listed.
	for _, c := range []struct {
		format string
		f      Feature
	}{{"docx", FeatSuperscript}, {"html", FeatRowSpan}, {"docx", FeatImageAlt}, {"docx", FeatFootnote}, {"pdf", FeatImageAlt}, {"markdown", FeatHeading}} {
		if _, ok := find(c.format, c.f); ok {
			t.Errorf("%s / %s is kept but listed as lost", c.format, c.f)
		}
	}
}

func TestFidelityOfFormatsThatKeepEverythingAndOfUnknownOnes(t *testing.T) {
	all := everything()
	for _, f := range []string{"json", "proto-text", "proto", "open"} {
		rep := Fidelity(f, all)
		if !rep.Complete || !rep.Lossless() || len(rep.Losses) != 0 {
			t.Errorf("%s: %+v", f, rep)
		}
	}
	rep := Fidelity("wordperfect", all)
	if rep.Known || rep.Lossless() || len(rep.Losses) != 0 {
		t.Errorf("an unknown format must not read as lossless: %+v", rep)
	}
	if rep := Fidelity("docx", docfile.Document{Title: "Plain", Content: []docfile.Item{plain("just words\n")}}); !rep.Lossless() {
		t.Errorf("a plain document loses something in Word: %+v", rep)
	}
}

// Where the table says the content itself is left out (not only its marking), the content is not in the file.
func TestContentLostMeansTheTextIsNotInTheFile(t *testing.T) {
	r := registry()
	pairs := featurePairs()
	words := map[Feature]string{FeatTable: "h", FeatFootnote: "The note."}
	for _, format := range Formats {
		for f, word := range words {
			e := exportMatrix[format][f]
			out := string(exportOne(t, r, format, pairs[f].with))
			if format == "pdf" { // the text of a PDF is drawn in a compressed stream
				out, _ = pdftext.ExtractBytes(rawOne(t, r, "pdf", pairs[f].with))
			}
			has := strings.Contains(out, word)
			if e.note == NoteContentLost && has {
				t.Errorf("%s / %s: the table says the text is lost, but %q is in the file", format, f, word)
			}
			if e.note != NoteContentLost && !has && f == FeatFootnote {
				t.Errorf("%s / %s: the text %q is missing from the file but the table does not say it is lost", format, f, word)
			}
		}
	}
}

// The accessibility data the report promises (alt text, language, headings, links) is in the file of each
// format that is said to keep it, and the exact words are there.
func TestAccessibilityDataIsInTheFileWhereTheTableSaysKept(t *testing.T) {
	r := registry()
	pairs := featurePairs()
	type probe struct {
		f    Feature
		want string
	}
	byFormat := map[string][]probe{
		"html":     {{FeatImageAlt, `alt="A blue dot"`}, {FeatDocLanguage, `<html lang="de">`}, {FeatHeading, "<h2>"}, {FeatLink, `href="https://example.com/x"`}, {FeatRunLanguage, `lang="fr"`}},
		"epub":     {{FeatDocLanguage, "<dc:language>de</dc:language>"}, {FeatHeading, "<h2>"}},
		"markdown": {{FeatHeading, "## Heading"}, {FeatLink, "(https://example.com/x)"}},
		"pdf":      {{FeatImageAlt, "/Alt <FEFF" + utf16hex("A blue dot") + ">"}, {FeatDocLanguage, "/Lang"}, {FeatLink, "https://example.com/x"}},
	}
	for format, probes := range byFormat {
		for _, p := range probes {
			if e := exportMatrix[format][p.f]; e.t == Dropped {
				t.Errorf("%s / %s: the probe expects it to be kept", format, p.f)
				continue
			}
			if out := exportOne(t, r, format, pairs[p.f].with); !bytes.Contains(out, []byte(p.want)) {
				t.Errorf("%s / %s: %q is not in the file", format, p.f, p.want)
			}
		}
	}
	// Word keeps them in its parts.
	docx := string(unzipEntry(t, rawOne(t, r, "docx", pairs[FeatImageAlt].with), "word/document.xml"))
	if !strings.Contains(docx, `descr="A blue dot"`) {
		t.Errorf("docx: the alternative text is not in the picture's description: %s", docx)
	}
	if styles := string(unzipEntry(t, rawOne(t, r, "docx", pairs[FeatDocLanguage].with), "word/styles.xml")); !strings.Contains(styles, `<w:lang w:val="de"/>`) {
		t.Errorf("docx: the document language is not in the default run properties")
	}
	if doc := string(unzipEntry(t, rawOne(t, r, "docx", pairs[FeatRunLanguage].with), "word/document.xml")); !strings.Contains(doc, `<w:lang w:val="fr"/>`) {
		t.Errorf("docx: the run language is not written: %s", doc)
	}
	if doc := string(unzipEntry(t, rawOne(t, r, "docx", pairs[FeatHeading].with), "word/document.xml")); !strings.Contains(doc, `w:val="Heading2"`) {
		t.Errorf("docx: the heading style is not written")
	}
}
