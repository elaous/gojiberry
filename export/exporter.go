// Package export turns a document's own Snapshot -- a plain,
// format-agnostic view of its content built by the editor package's
// own document.exportSnapshot -- into a specific file format. Each
// format (Markdown, plain text, HTML, RTF, EPUB, PDF, DOCX) is its own
// Exporter implementation, so a new format is a new type satisfying
// this one-method interface, not a new case threaded through an
// existing switch.
//
// This package deliberately knows nothing about document, editor's own
// live document/buffer type: document's own internal representation
// (a gap buffer plus per-character formatting, walked with runeAt/
// fmtAt) is exactly the kind of implementation detail an exporter
// shouldn't need to reach into, and document itself is unexported (an
// editor package internal), so nothing here *could* import it even if
// it wanted to -- Go forbids importing an unexported identifier across
// package boundaries, and document, defined in package main, can't be
// imported by any other package at all. Snapshot is the deliberate
// seam: document.exportSnapshot walks the live document exactly once,
// producing a plain data structure built from this package's own
// exported types, which every Exporter then consumes independently.
package export

// Exporter converts a Snapshot into one format's own file bytes. Every
// implementation in this package (Markdown, PlainText, HTML, RTF,
// EPUB, PDF, DOCX) is a zero-size struct -- Export needs nothing but
// the Snapshot it's given -- but the interface itself doesn't assume
// that: a future format needing configuration (a page-size override,
// a font choice) would carry it as its own struct fields, set before
// Export is called, the same shape PDF's own eventual per-export
// options would take if this package grew any.
type Exporter interface {
	// Export renders snap in this Exporter's own format. The returned
	// bytes are the complete file content -- valid UTF-8 text for the
	// plain-text formats (Markdown/PlainText/HTML/RTF), a real binary
	// container for EPUB/PDF/DOCX. An error is only ever returned by
	// the formats with a real third-party encoding step that can fail
	// (PDF/DOCX/EPUB); the hand-written text formats can't fail on any
	// Snapshot and always return a nil error.
	Export(snap Snapshot) ([]byte, error)
}
