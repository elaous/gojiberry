package pack

import (
	"io"

	"github.com/elaous/gojiberry/docfile"
)

// Recovered is what is left of a pack whose footer or index cannot be used.
type Recovered struct {
	Meta Meta
	// Docs are the documents whose records are intact, in file order of first appearance;
	// a document whose ID appears in more than one record is the last of them.
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
//
// When the pack's index is intact the index decides what is in the pack: records it does
// not list (documents removed since, an append that did not finish) are not recovered.
// When it is not, every intact record counts and, a document appended again under the same
// ID being a newer version of it, the last record with an ID wins. A document removed
// with Appender.Remove cannot be told from a live one without an index, so it comes back.
func Recover(r io.ReaderAt, size int64) (Recovered, error) {
	meta, hdr, err := readHeader(r, size)
	var rec Recovered
	if err != nil {
		return rec, err
	}
	rec.Meta = meta
	off := int64(len(hdr))
	at := map[string]int{} // ID -> position in rec.Docs
	var offs []int64       // record offset of each of rec.Docs
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
				if i, dup := at[doc.ID()]; dup {
					rec.Docs[i], offs[i] = doc, off
				} else {
					at[doc.ID()] = len(rec.Docs)
					rec.Docs = append(rec.Docs, doc)
					offs = append(offs, off)
				}
			} else {
				rec.Skipped++
			}
		}
		off = next
	}
	rd, openErr := Open(r, size)
	rec.Complete = openErr == nil
	if rd != nil {
		listed := map[int64]bool{}
		for _, e := range rd.index.Entries {
			listed[int64(e.Offset)] = true
		}
		docs := rec.Docs[:0]
		for i, d := range rec.Docs {
			if listed[offs[i]] {
				docs = append(docs, d)
			}
		}
		rec.Docs = docs
	}
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
	// A recovered document names its folder by ID only: list those folders, under a name
	// made from the ID, so the index has no dangling reference; an ID that is not a
	// folder ID is dropped.
	seen := map[string]bool{}
	for _, d := range rec.Docs {
		if f := d.Folder(); f != "" {
			if !docfile.ValidID(f) {
				d.SetFolder("")
			} else if !seen[f] {
				seen[f] = true
				if err := w.AddFolder(Folder{ID: f, Name: "recovered-" + f[:8]}); err != nil {
					return err
				}
			}
		}
		if err := w.Add(d); err != nil {
			return err
		}
	}
	return w.Close(nil)
}
