package main

import (
	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
)

// fidelityState is the Import/Export tab's fidelity panel: the format whose report is shown, and what the last
// imported file left out.
type fidelityState struct {
	format     string
	importName string
	imported   components.FidelityReport
}

// fidelityReport is an export.Report as the panel shows it.
func fidelityReport(r export.Report) components.FidelityReport {
	out := components.FidelityReport{Known: r.Known || r.Complete}
	for _, l := range r.Losses {
		out.Losses = append(out.Losses, components.FidelityLoss{Feature: string(l.Feature), Treatment: l.Treatment.String(), Note: l.Note, Count: l.Count})
	}
	return out
}

// choose returns the format to show: the one chosen if it is on offer, else the first.
func (s *fidelityState) choose(exports []components.SaverChoice) string {
	for _, e := range exports {
		if e.Name == s.format {
			return s.format
		}
	}
	if len(exports) > 0 {
		return exports[0].Name
	}
	return ""
}

// props builds the panel for the document: what exporting it as the chosen format loses, and what the last
// import left out.
func (s *fidelityState) props(doc docfile.Document, exports []components.SaverChoice, onFormat func(string)) components.FidelityPanelProps {
	format := s.choose(exports)
	return components.FidelityPanelProps{
		Formats: exports, Format: format, OnFormat: onFormat,
		Export:     fidelityReport(export.Fidelity(format, doc)),
		Import:     s.imported,
		ImportName: s.importName,
	}
}

// noteImport keeps what a file left out when it was imported (the data is the file's bytes).
func (s *fidelityState) noteImport(format, file string, data []byte) {
	s.importName = file
	s.imported = fidelityReport(export.ImportFidelity(format, data))
}
