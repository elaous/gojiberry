package pdftext

import (
	"bytes"
	"errors"
	"fmt"
	"time"

	"github.com/dslipak/pdf"
)

const (
	MaxSize = 64 << 20
	Timeout = 20 * time.Second
)

// Errors for a PDF that is refused.
var (
	ErrTooLarge = errors.New("pdftext: PDF too large")
	ErrTimeout  = errors.New("pdftext: reading the PDF took too long")
)

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
