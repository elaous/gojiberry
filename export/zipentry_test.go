package export

import (
	"archive/zip"
	"bytes"
	"testing"
)

// LibreOffice refuses an ODT whose first entry has a data descriptor, and some readers a date with month 0:
// the packages start with a stored mimetype whose size is in its header, and every entry has a real date.
func TestPackagesStartWithAPlainMimetypeAndHaveDates(t *testing.T) {
	snap := Snapshot{Title: "T", Blocks: []Block{{Kind: Paragraph, Segments: []Segment{{Text: "x"}}}}}
	for name, ex := range map[string]Exporter{"odt": ODT{}, "epub": EPUB{}, "docx": DOCX{}} {
		data, err := ex.Export(snap)
		if err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		for i, f := range zr.File {
			if f.Modified.Year() != 1980 || f.Modified.Month() != 1 || f.Modified.Day() != 1 {
				t.Errorf("%s: %s is dated %v", name, f.Name, f.Modified)
			}
			if i == 0 && name != "docx" && (f.Name != "mimetype" || f.Method != zip.Store || f.Flags&8 != 0) {
				t.Errorf("%s: first entry %s method %d flags %d", name, f.Name, f.Method, f.Flags)
			}
		}
	}
}
