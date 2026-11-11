//go:build !(js && wasm)

package main

import (
	"testing"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export/remote/serve"
)

func TestFidelityStateChoosesAFormatOnOfferAndReportsIt(t *testing.T) {
	exports := []components.SaverChoice{{Name: "docx", Label: "Word"}, {Name: "pdf", Label: "PDF"}}
	doc := docfile.Document{Title: "T", Content: []docfile.Item{
		{Chars: &docfile.Chars{Text: "x", Format: docfile.Format{Superscript: true}}},
	}}
	done := make(chan struct{}, 8)
	s := fidelityState{call: serve.Handle, render: func() { done <- struct{}{} }}
	ask := func() components.FidelityPanelProps {
		p := s.props(doc, exports, nil)
		if p.Export.Pending {
			<-done
			p = s.props(doc, exports, nil)
		}
		return p
	}
	if got := s.choose(exports); got != "docx" {
		t.Errorf("default format %q, want the first on offer", got)
	}
	s.format = "wordperfect"
	if got := s.choose(exports); got != "docx" {
		t.Errorf("a format not on offer was chosen: %q", got)
	}
	s.format = "pdf"
	p := ask()
	if p.Format != "pdf" || !p.Export.Known || len(p.Export.Losses) != 1 || p.Export.Losses[0].Feature != "superscript" || p.Export.Losses[0].Treatment != "dropped" || p.Export.Losses[0].Count != 1 {
		t.Errorf("PDF report of a superscript: %+v", p.Export)
	}
	s.format = "docx"
	if p := ask(); !p.Export.Known || len(p.Export.Losses) != 0 {
		t.Errorf("Word keeps a superscript: %+v", p.Export)
	}
	if p := s.props(doc, exports, nil); p.ImportName != "" {
		t.Errorf("an import is reported before one happened")
	}
	s.noteImport("pdf", "scan.pdf", nil)
	<-done
	if p := s.props(doc, exports, nil); p.ImportName != "scan.pdf" || !p.Import.Known || len(p.Import.Losses) != 1 || p.Import.Losses[0].Feature != "layout" {
		t.Errorf("import of a PDF: %+v %q", p.Import, p.ImportName)
	}
	s.noteImport("markdown", "a.md", []byte("![x](https://example.com/a.png)\n"))
	<-done
	if !s.imported.Known || len(s.imported.Losses) == 0 {
		t.Errorf("a Markdown import is read for what it leaves out (here a picture kept as an address): %+v", s.imported)
	}
}

func TestImportReportIsKeptWithTheDocument(t *testing.T) {
	done := make(chan struct{}, 2)
	s := fidelityState{call: serve.Handle, render: func() { done <- struct{}{} }}
	if s.kept() != nil {
		t.Fatal("nothing was imported yet")
	}
	s.noteImport("pdf", "scan.pdf", nil)
	<-done
	var saved docfile.Document
	saved.SetImportReport(s.kept())

	var reopened fidelityState
	reopened.restore(saved)
	if reopened.importName != "scan.pdf" || !reopened.imported.Known || len(reopened.imported.Losses) != 1 || reopened.imported.Losses[0].Feature != "layout" {
		t.Errorf("after reopening: %q %+v", reopened.importName, reopened.imported)
	}
	reopened.restore(docfile.Document{})
	if reopened.importName != "" || reopened.kept() != nil {
		t.Errorf("another document keeps the old report: %q", reopened.importName)
	}
}
