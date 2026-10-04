package pdftext

import (
	"errors"
	"testing"
)

func TestOversizeAndGarbageAreRefused(t *testing.T) {
	if _, err := ExtractPagesBytes(make([]byte, MaxSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversize: %v", err)
	}
	if _, err := ExtractPagesBytes([]byte("%PDF-1.4\nnot really")); err == nil {
		t.Error("garbage accepted")
	}
}
