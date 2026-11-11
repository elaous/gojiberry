package pack

import (
	"crypto/sha256"
	"io"

	"github.com/elaous/gojiberry/docfile"
)

type Recovered struct {
	Meta        Meta
	Docs        []docfile.Document
	Sealed      []Sealed
	Skipped     int
	SealedLost  int
	Attachments []RecoveredAttachment
	Complete    bool
}

// RecoveredAttachment is an attachment found by Recover.
type RecoveredAttachment struct {
	MIME string
	Data []byte
}

type Sealed struct {
	Entry    Entry
	Envelope []byte
}

func Recover(r io.ReaderAt, size int64) (Recovered, error) {
	meta, hdr, ver, err := readHeaderV(r, size)
	var rec Recovered
	if err != nil {
		return rec, err
	}
	rec.Meta = meta
	off := int64(len(hdr))
	at := map[string]int{} // ID -> position in rec.Docs
	var offs []int64       // record offset of each of rec.Docs
	sealed := map[int64][]byte{}
	for off+frameHead <= size {
		head := make([]byte, 8)
		if _, err := r.ReadAt(head, off); err != nil {
			break
		}
		if string(head) == footerMagic {
			break // the footer: everything before it was read
		}
		kind, flags, data, err := readFrame(r, off, size)
		if err != nil {
			break
		}
		next := off + int64(recordSize(uint64(len(data))))
		switch kind {
		case kindAttachment:
			if ver >= version2 {
				rec.Attachments = append(rec.Attachments, RecoveredAttachment{Data: data})
			}
		case kindDocument:
			if flags&flagEncrypted != 0 {
				sealed[off] = data
				rec.Skipped++
			} else if doc, err := docfile.Open.Unmarshal(data); err == nil && docfile.ValidID(doc.ID()) {
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
		mime := map[[32]byte]string{}
		for _, a := range rd.index.Attachments {
			mime[a.SHA256] = a.MIME
		}
		seen := map[[32]byte]bool{}
		kept := rec.Attachments[:0]
		for _, a := range rec.Attachments {
			sum := sha256.Sum256(a.Data)
			if !seen[sum] {
				seen[sum] = true
				a.MIME = mime[sum]
				kept = append(kept, a)
			}
		}
		rec.Attachments = kept
	}
	if rd == nil {
		rec.SealedLost = len(sealed) // nothing says whose they are
	}
	if rd != nil {
		listed := map[int64]bool{}
		for _, e := range rd.index.Entries {
			listed[int64(e.Offset)] = true
		}
		for _, e := range rd.index.Entries {
			if env, ok := sealed[int64(e.Offset)]; ok && e.Encrypted && uint64(len(env)) == e.Length && sha256.Sum256(env) == e.SHA256 {
				rec.Sealed = append(rec.Sealed, Sealed{Entry: e, Envelope: env})
				rec.Skipped--
			} else if ok && e.Encrypted {
				rec.SealedLost++ // listed, but not the record the index describes
			}
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

func Repack(rec Recovered, dst io.Writer) error {
	meta := rec.Meta
	if meta.Description != "" {
		meta.Description += "\n"
	}
	meta.Description += "recovered"
	w, err := newWriter(dst, meta, len(rec.Attachments) > 0)
	if err != nil {
		return err
	}
	for _, a := range rec.Attachments {
		if _, err := w.AddAttachment(a.MIME, a.Data); err != nil {
			return err
		}
	}
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
	for _, s := range rec.Sealed {
		if s.Entry.Folder != "" {
			if !docfile.ValidID(s.Entry.Folder) {
				s.Entry.Folder = ""
			} else if !seen[s.Entry.Folder] {
				seen[s.Entry.Folder] = true
				if err := w.AddFolder(Folder{ID: s.Entry.Folder, Name: "recovered-" + s.Entry.Folder[:8]}); err != nil {
					return err
				}
			}
		}
		if err := w.AddEncrypted(s.Entry, s.Envelope); err != nil {
			return err
		}
	}
	return w.Close(nil)
}
