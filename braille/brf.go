package braille

import "github.com/elaous/gojiberry/export"

type BRF struct {
	Braille string
}

func (b BRF) Export(snap export.Snapshot) ([]byte, error) {
	return []byte(b.Braille), nil
}
