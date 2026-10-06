package pack

import (
	"io"

	"github.com/elaous/gojiberry/docfile"
)

// Recovered is what is left of a pack whose footer or index cannot be used.
type Recovered struct {
	Meta Meta
	// Docs are the documents whose records are intact, in file order.
	Docs []docfile.Document
	// Skipped counts intact records that were not documents that could be read.
	Skipped int
	// Complete says the walk ended exactly at a valid index: nothing was lost.
	Complete bool
}

// Recover walks the records from the header, checking each frame and keeping every
// document that parses, and stops at the first bad frame. Folder names live only in the
// index, so a recovered pack has the documents' own folder IDs and no folders; the
// documents keep their name, tags and trash flag.
func Recover(r io.ReaderAt, size int64) (Recovered, error) {
	meta, hdr, err := readHeader(r, size)
	var rec Recovered
	if err != nil {
		return rec, err
	}
	rec.Meta = meta
	off := int64(len(hdr))
	for off+frameHead <= size {
		head := make([]byte, 8)
		if _, err := r.ReadAt(head, off); err != nil {
			break
		}
		if string(head) == footerMagic {
			break // the footer: everything before it was read
		}
		kind, data, err := readFrame(r, off, size)
		if err != nil {
			break
		}
		next := off + int64(recordSize(uint64(len(data))))
		switch kind {
		case kindDocument:
			if doc, err := docfile.Open.Unmarshal(data); err == nil && docfile.ValidID(doc.ID()) {
				rec.Docs = append(rec.Docs, doc)
			} else {
				rec.Skipped++
			}
		}
		off = next
	}
	_, openErr := Open(r, size)
	rec.Complete = openErr == nil
	return rec, nil
}

// Repack writes the documents of a recovered pack as a new pack.
func Repack(rec Recovered, dst io.Writer) error {
	meta := rec.Meta
	if meta.Description != "" {
		meta.Description += "\n"
	}
	meta.Description += "recovered"
	w, err := NewWriter(dst, meta)
	if err != nil {
		return err
	}
	for _, d := range rec.Docs {
		if err := w.Add(d); err != nil {
			return err
		}
	}
	return w.Close(nil)
}
