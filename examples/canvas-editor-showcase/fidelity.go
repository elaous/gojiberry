package main

import (
	"crypto/sha1"
	"encoding/hex"
	"sync"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/export"
	"github.com/elaous/gojiberry/export/remote"
)

// fidelityState is the Import/Export tab's fidelity panel: the format whose report is shown, and what the last
// imported file left out.
//
// The reports are worked out by the file worker (files.wasm), so they arrive a moment after they are asked for:
// the panel says it is checking, and render draws it again when the answer is in.
type fidelityState struct {
	format     string
	importName string
	imported   components.FidelityReport
	render     func()
	call       remote.Caller // the file worker (callFiles when nil)

	mu     sync.Mutex
	cache  map[string]components.FidelityReport
	asking map[string]bool
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
		Export:     s.exportReport(format, doc),
		Import:     s.imported,
		ImportName: s.importName,
	}
}

// exportReport is what exporting doc as format loses: from the cache, or "pending" while the worker works it
// out (the panel is drawn again then).
func (s *fidelityState) exportReport(format string, doc docfile.Document) components.FidelityReport {
	b, err := docfile.JSON.Marshal(doc)
	if err != nil {
		return components.FidelityReport{}
	}
	sum := sha1.Sum(b)
	key := format + "|" + hex.EncodeToString(sum[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.cache[key]; ok {
		return r
	}
	if s.asking == nil {
		s.asking, s.cache = map[string]bool{}, map[string]components.FidelityReport{}
	}
	if !s.asking[key] {
		s.asking[key] = true
		go func() {
			rep, err := remote.Fidelity(s.caller(), format, doc)
			r := components.FidelityReport{}
			if err == nil {
				r = fidelityReport(rep)
			}
			s.mu.Lock()
			if len(s.cache) >= 16 {
				s.cache = map[string]components.FidelityReport{}
			}
			s.cache[key] = r
			delete(s.asking, key)
			s.mu.Unlock()
			if s.render != nil {
				s.render()
			}
		}()
	}
	return components.FidelityReport{Pending: true}
}

// noteImport asks the worker what a file left out when it was imported (the data is the file's bytes), and
// shows it when the answer comes.
func (s *fidelityState) noteImport(format, file string, data []byte) {
	go func() {
		rep, err := remote.ImportFidelity(s.caller(), format, data)
		r := components.FidelityReport{}
		if err == nil {
			r = fidelityReport(rep)
		}
		s.mu.Lock()
		s.importName, s.imported = file, r
		s.mu.Unlock()
		if s.render != nil {
			s.render()
		}
	}()
}

// kept is the import report to save with the document: nil when the document was not imported (or the
// report is still being worked out).
func (s *fidelityState) kept() *docfile.ImportReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.importName == "" {
		return nil
	}
	r := &docfile.ImportReport{Name: s.importName, Known: s.imported.Known}
	for _, l := range s.imported.Losses {
		r.Losses = append(r.Losses, docfile.ImportLoss{Feature: l.Feature, Treatment: l.Treatment, Note: l.Note, Count: l.Count})
	}
	return r
}

// restore shows the report a document was saved with (or none, for a document that was not imported).
func (s *fidelityState) restore(d docfile.Document) {
	r, ok := d.ImportReport()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !ok {
		s.importName, s.imported = "", components.FidelityReport{}
		return
	}
	s.importName, s.imported = r.Name, components.FidelityReport{Known: r.Known}
	for _, l := range r.Losses {
		s.imported.Losses = append(s.imported.Losses, components.FidelityLoss{Feature: l.Feature, Treatment: l.Treatment, Note: l.Note, Count: l.Count})
	}
}

func (s *fidelityState) caller() remote.Caller {
	if s.call != nil {
		return s.call
	}
	return callFiles
}
