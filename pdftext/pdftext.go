// Package pdftext extracts a PDF's text, and imports it as a document (Import).
// ExtractBytes and ExtractPagesBytes are pure Go and in memory (no external
// binary, no filesystem), so they work in the browser's wasm build too:
// github.com/dslipak/pdf (BSD-3-Clause, a fork of rsc.io/pdf) parses the bytes.
// It does not rebuild the spaces between glyphs from positioning data the way
// Poppler's pdftotext -layout does, so a bullet glyph directly followed by a
// word comes back with no space in between.
package pdftext

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/dslipak/pdf"
)

// MaxSize is the largest PDF read, and Timeout how long one may take. A
// malformed file can send the parser into a loop it never leaves; the wait is
// then abandoned, but the goroutine is not, so a caller taking PDFs from
// strangers should also run the import out of process. Under wasm, where one
// thread runs everything, the timeout cannot fire while the parser spins.
const (
	MaxSize = 64 << 20
	Timeout = 20 * time.Second
)

// Errors for a PDF that is refused.
var (
	ErrTooLarge = errors.New("pdftext: PDF too large")
	ErrTimeout  = errors.New("pdftext: reading the PDF took too long")
)

// ExtractPagesBytes parses data (a whole PDF file's own bytes, already
// read into memory -- e.g. via a browser File's ReadAsArrayBuffer) and
// returns each page's own plain text, in order. dslipak/pdf's parser
// doesn't reconstruct inter-glyph whitespace from PDF positioning data
// the way Poppler's pdftotext -layout does -- a bullet glyph
// immediately followed by a word (no literal space character between
// them in the PDF's own content stream) comes back with no space in
// between. Callers matching exact substrings, or wanting readable
// prose out of pdfimport_wire.go's own Import PDF… feature, need to
// account for that gap themselves.
func ExtractPagesBytes(data []byte) (pages []string, err error) {
	if len(data) > MaxSize {
		return nil, ErrTooLarge
	}
	type result struct {
		pages []string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		// The parser panics on some malformed files.
		defer func() {
			if p := recover(); p != nil {
				done <- result{err: fmt.Errorf("pdftext: malformed PDF: %v", p)}
			}
		}()
		r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			done <- result{err: err}
			return
		}
		pages, err := extractPages(r)
		done <- result{pages, err}
	}()
	select {
	case res := <-done:
		return res.pages, res.err
	case <-time.After(Timeout):
		return nil, ErrTimeout
	}
}

// ExtractBytes is ExtractPagesBytes with every page's own text
// concatenated into one string.
func ExtractBytes(data []byte) (string, error) {
	pages, err := ExtractPagesBytes(data)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	for _, p := range pages {
		buf.WriteString(p)
	}
	return buf.String(), nil
}

// extractPages walks every page in r, in order, collecting each one's
// own GetPlainText output -- the shared core ExtractPagesBytes/
// DslipakPDF both build on, whether r was opened from bytes or a file.
func extractPages(r *pdf.Reader) ([]string, error) {
	pages := make([]string, 0, r.NumPage())
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", i, err)
		}
		pages = append(pages, text)
	}
	return pages, nil
}
