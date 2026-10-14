package export

import (
	"strings"
	"testing"

	"github.com/elaous/gojiberry/canvas"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/pdftext"
)

// heldContentsDoc is a document with a table of contents in it (not the Page tab's switch), a chapter and a section.
func heldContentsDoc(toc docfile.Block) docfile.Document {
	f := &canvas.TextFlow{Items: []canvas.Item{
		canvas.Inline{Object: &canvas.TOCObject{Title: toc.Title, Style: toc.Style, Depth: toc.Depth}, Block: true}, canvas.Chars{Text: "\n"},
		canvas.Chars{Text: "Chapter one", Format: canvas.Format{Scale: 2, FontWeight: 700}}, canvas.Chars{Text: "\nbody\n"},
		canvas.Chars{Text: "A section", Format: canvas.Format{Scale: 1.6, FontWeight: 700}}, canvas.Chars{Text: "\nmore\n"},
	}}
	return docfile.FromFlow("Report", f)
}

// A table of contents the document holds is what the exports write: once, with its title, look and depth.
func TestExportsWriteTheContentsTheDocumentHolds(t *testing.T) {
	d := heldContentsDoc(docfile.Block{Title: "Inhalt", Style: "plain", Depth: 1})
	snap := FromDocument(d)
	if !snap.TOC || snap.TOCTitle != "Inhalt" || snap.TOCStyle != "plain" || snap.TOCDepth != 1 {
		t.Fatalf("snapshot: %+v", snap)
	}
	if got := snap.Outline(); len(got) != 1 || got[0].Text != "Chapter one" {
		t.Fatalf("depth 1 lists the chapters only: %+v", got)
	}

	html, err := HTML{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(html); strings.Count(s, `<nav class="toc toc-plain"`) != 1 || !strings.Contains(s, "Inhalt") || strings.Contains(s, ">A section</a>") {
		t.Errorf("HTML contents: %s", s[:min(len(s), 1200)])
	}

	data, err := DOCX{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	doc := docxParts(t, data)["word/document.xml"]
	if !strings.Contains(doc, `TOC \o "1-1"`) || !strings.Contains(doc, `w:leader="none"`) || strings.Contains(doc, `w:leader="dot"`) {
		t.Errorf("Word contents: depth or leader wrong")
	}

	rtf, _ := RTF{}.Export(snap)
	if s := string(rtf); strings.Contains(s, `\tldot`) || !strings.Contains(s, "Chapter one") {
		t.Errorf("RTF contents: %s", s[:min(len(s), 600)])
	}

	// The default look keeps its dots and lists every level.
	dotted := FromDocument(heldContentsDoc(docfile.Block{}))
	if got := dotted.Outline(); len(got) != 2 {
		t.Errorf("all levels by default: %+v", got)
	}
	data, _ = DOCX{}.Export(dotted)
	if doc := docxParts(t, data)["word/document.xml"]; !strings.Contains(doc, `w:leader="dot"`) || !strings.Contains(doc, `TOC \o "1-3"`) {
		t.Error("Word contents by default")
	}
	odt, _ := ODT{}.Export(snap)
	if !strings.Contains(docxParts(t, odt)["content.xml"], `text:outline-level="1"`) {
		t.Error("ODT contents")
	}

	pdf, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	txt, err := pdftext.ExtractPagesBytes(pdf)
	if err != nil || len(txt) == 0 || !strings.Contains(txt[0], "Inhalt") || strings.Contains(txt[0], ". . .") {
		t.Errorf("PDF contents (plain: no dots): %q %v", txt, err)
	}
}

// midContentsDoc has a paragraph before its table of contents, the chapters after it, and (optionally) a last paragraph.
func midContentsDoc(atEnd bool) docfile.Document {
	items := []canvas.Item{canvas.Chars{Text: "Intro text\n"}}
	chapters := []canvas.Item{
		canvas.Chars{Text: "Chapter one", Format: canvas.Format{Scale: 2, FontWeight: 700}}, canvas.Chars{Text: "\nbody\n"},
		canvas.Chars{Text: "A section", Format: canvas.Format{Scale: 1.6, FontWeight: 700}}, canvas.Chars{Text: "\nmore\n"},
	}
	toc := canvas.Inline{Object: &canvas.TOCObject{Title: "Inhalt"}, Block: true}
	if atEnd {
		items = append(items, chapters...)
		items = append(items, toc)
	} else {
		items = append(items, toc, canvas.Chars{Text: "\n"})
		items = append(items, chapters...)
	}
	return docfile.FromFlow("Report", &canvas.TextFlow{Items: items})
}

// The contents are written where the document has them: after the text before them, before the chapters after them.
func TestContentsAreWrittenWhereTheDocumentHasThem(t *testing.T) {
	snap := FromDocument(midContentsDoc(false))
	if !snap.TOC || snap.TOCAt <= 0 || snap.TOCAt >= len(snap.Blocks) {
		t.Fatalf("TOCAt = %d of %d blocks", snap.TOCAt, len(snap.Blocks))
	}
	if snap.Blocks[snap.TOCAt-1].Segments[0].Text != "Intro text" {
		t.Errorf("the block before the contents is %q", snap.Blocks[snap.TOCAt-1].Segments[0].Text)
	}
	order := func(name, s string, marks ...string) {
		t.Helper()
		last := -1
		for _, m := range marks {
			i := strings.Index(s, m)
			if i < 0 || i < last {
				t.Errorf("%s: %q is missing or out of order (at %d, after %d)", name, m, i, last)
			}
			last = i
		}
	}
	html, _ := HTML{}.Export(snap)
	order("HTML", string(html), "Intro text", `<nav class="toc"`, `<h1 id=`)
	docx, _ := DOCX{}.Export(snap)
	d := docxParts(t, docx)["word/document.xml"]
	order("Word", d, "Intro text", `TOC \o`, `<w:bookmarkStart`)
	if strings.Count(d, `<w:bookmarkStart`) != 2 {
		t.Errorf("Word: every heading has its bookmark, got %d", strings.Count(d, `<w:bookmarkStart`))
	}
	rtf, _ := RTF{}.Export(snap)
	order("RTF", string(rtf), "Intro text", `\fldinst HYPERLINK`, `\bkmkstart`)
	odt, _ := ODT{}.Export(snap)
	order("ODT", docxParts(t, odt)["content.xml"], "Intro text", "<text:table-of-content ", "<text:bookmark-start")
	pdf, err := PDF{}.Export(snap)
	if err != nil {
		t.Fatal(err)
	}
	pages, _ := pdftext.ExtractPagesBytes(pdf)
	all := strings.Join(pages, "\n")
	order("PDF", all, "Intro text", "Inhalt", "Chapter one")

	// A table of contents at the end of the document is written there.
	end := FromDocument(midContentsDoc(true))
	if end.TOCAt != len(end.Blocks) {
		t.Fatalf("at the end: TOCAt = %d of %d", end.TOCAt, len(end.Blocks))
	}
	html, _ = HTML{}.Export(end)
	order("HTML end", string(html), "A section", `<nav class="toc"`)
	docx, _ = DOCX{}.Export(end)
	order("Word end", docxParts(t, docx)["word/document.xml"], "A section", `TOC \o`)
	rtf, _ = RTF{}.Export(end)
	order("RTF end", string(rtf), "A section", `\fldinst HYPERLINK`)
	pdf, _ = PDF{}.Export(end)
	pages, _ = pdftext.ExtractPagesBytes(pdf)
	order("PDF end", strings.Join(pages, "\n"), "Chapter one", "Inhalt")

	// At the start (the usual case, and the Page tab's switch) nothing moves.
	start := FromDocument(heldContentsDoc(docfile.Block{}))
	if start.TOCAt != 0 {
		t.Errorf("at the start: TOCAt = %d", start.TOCAt)
	}
}
