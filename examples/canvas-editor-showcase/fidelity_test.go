//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
)

func TestFidelityStateChoosesAFormatOnOfferAndReportsIt(t *testing.T) {
	exports := []components.SaverChoice{{Name: "docx", Label: "Word"}, {Name: "pdf", Label: "PDF"}}
	doc := docfile.Document{Title: "T", Content: []docfile.Item{
		{Chars: &docfile.Chars{Text: "x", Format: docfile.Format{Superscript: true}}},
	}}
	var s fidelityState
	if got := s.choose(exports); got != "docx" {
		t.Errorf("default format %q, want the first on offer", got)
	}
	s.format = "wordperfect"
	if got := s.choose(exports); got != "docx" {
		t.Errorf("a format not on offer was chosen: %q", got)
	}
	s.format = "pdf"
	p := s.props(doc, exports, nil)
	if p.Format != "pdf" || !p.Export.Known || len(p.Export.Losses) != 1 || p.Export.Losses[0].Feature != "superscript" || p.Export.Losses[0].Treatment != "dropped" || p.Export.Losses[0].Count != 1 {
		t.Errorf("PDF report of a superscript: %+v", p.Export)
	}
	s.format = "docx"
	if p := s.props(doc, exports, nil); !p.Export.Known || len(p.Export.Losses) != 0 {
		t.Errorf("Word keeps a superscript: %+v", p.Export)
	}
	if p.ImportName != "" {
		t.Errorf("an import is reported before one happened")
	}
	s.noteImport("pdf", "scan.pdf", nil)
	if p := s.props(doc, exports, nil); p.ImportName != "scan.pdf" || !p.Import.Known || len(p.Import.Losses) != 1 || p.Import.Losses[0].Feature != "layout" {
		t.Errorf("import of a PDF: %+v %q", p.Import, p.ImportName)
	}
	s.noteImport("markdown", "a.md", nil)
	if s.imported.Known {
		t.Errorf("what a Markdown import leaves out is not known, and must not read as nothing")
	}
}
