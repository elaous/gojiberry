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
