package pack

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/elaous/gojiberry/docfile"
)

// builder lays a pack out at an offset, to any sink.
type builder struct {
	write   func(p []byte, off int64) error
	off     int64
	header  []byte
	entries []Entry
	folders []Folder
	closed  bool
}

func (b *builder) emit(p []byte) error {
	if err := b.write(p, b.off); err != nil {
		return err
	}
	b.off += int64(len(p))
	return nil
}

// addRaw writes data as a document record and lists it under e (whose Offset, Length and
// SHA256 are filled in here).
func (b *builder) addRaw(e Entry, data []byte) error {
	if b.closed {
		return errors.New("pack: closed")
	}
	if !docfile.ValidID(e.ID) {
		return fmt.Errorf("pack: %q is not a document ID", e.ID)
	}
	if err := checkName(e.Name); err != nil {
		return err
	}
	if len(data) > MaxRecord {
		return fmt.Errorf("pack: a document of %d bytes (at most %d)", len(data), MaxRecord)
	}
	for _, x := range b.entries {
		if x.ID == e.ID {
			return fmt.Errorf("pack: %q is already in the pack", e.ID)
		}
	}
	if len(b.entries) >= MaxEntries {
		return fmt.Errorf("pack: more than %d documents", MaxEntries)
	}
	e.Offset, e.Length, e.SHA256 = uint64(b.off), uint64(len(data)), sha256.Sum256(data)
	if err := b.emit(frame(kindDocument, data)); err != nil {
		return err
	}
	b.entries = append(b.entries, e)
	return nil
}

func entryOf(doc docfile.Document) (Entry, []byte, error) {
	data, err := docfile.Open.Marshal(doc)
	if err != nil {
		return Entry{}, nil, err
	}
	return Entry{ID: doc.ID(), Name: doc.Name(), Folder: doc.Folder(), Tags: doc.Tags(), Updated: doc.Updated, Trashed: doc.Trashed() != 0}, data, nil
}

func (b *builder) add(doc docfile.Document) error {
	e, data, err := entryOf(doc)
	if err != nil {
		return err
	}
	return b.addRaw(e, data)
}

func (b *builder) addFolder(f Folder) error {
	if f.ID == "" || checkName(f.Name) != nil {
		return errors.New("pack: a folder needs an ID and a name of at most 256 bytes")
	}
	for _, x := range b.folders {
		if x.ID == f.ID {
			return fmt.Errorf("pack: folder %q is already in the pack", f.ID)
		}
	}
	b.folders = append(b.folders, f)
	return nil
}

// finish writes the index, the signature if there is a key, and the footer, in that order.
func (b *builder) finish(key ed25519.PrivateKey) error {
	idx := Index{Entries: b.entries, Folders: b.folders}
	if err := idx.validate(int64(len(b.header)), b.off); err != nil {
		return err
	}
	data := idx.marshal()
	if len(data) > MaxIndex {
		return fmt.Errorf("pack: an index of %d bytes", len(data))
	}
	f := footer{indexOffset: uint64(b.off), indexLen: uint64(len(data)), indexSum: sha256.Sum256(data)}
	if err := b.emit(frame(kindIndex, data)); err != nil {
		return err
	}
	if key != nil {
		sdata := sign(key, b.header, f.indexSum).marshal()
		f.sigOffset, f.sigLen = uint64(b.off), uint64(len(sdata))
		if err := b.emit(frame(kindSignature, sdata)); err != nil {
			return err
		}
	}
	b.closed = true
	return b.emit(f.marshal())
}

// Writer writes a new pack to a stream.
type Writer struct{ b builder }

// NewWriter starts a pack: the header is written at once.
func NewWriter(w io.Writer, meta Meta) (*Writer, error) {
	wr := &Writer{b: builder{write: func(p []byte, _ int64) error { _, err := w.Write(p); return err }, header: headerBytes(meta)}}
	if err := wr.b.emit(wr.b.header); err != nil {
		return nil, err
	}
	return wr, nil
}

// Add writes a document; its ID, name, folder, tags, update time and trash flag go to the
// index from the document's own metadata.
func (w *Writer) Add(doc docfile.Document) error { return w.b.add(doc) }

// AddRaw writes the bytes of a document (a .open message) listed under e.
func (w *Writer) AddRaw(e Entry, data []byte) error { return w.b.addRaw(e, data) }

// AddFolder lists a folder.
func (w *Writer) AddFolder(f Folder) error { return w.b.addFolder(f) }

// Close writes the index and the footer, signing with key if it is not nil.
func (w *Writer) Close(key ed25519.PrivateKey) error { return w.b.finish(key) }

// File is what a pack is appended to: an *os.File is one.
type File interface {
	io.ReaderAt
	io.WriterAt
	Truncate(size int64) error
	Sync() error
}

// Appender adds to a pack in place: new records go over the old index, signature and
// footer, and a new index, signature and footer follow them. Nothing already written is
// moved.
type Appender struct {
	b   builder
	f   File
	key ed25519.PrivateKey
}

// OpenAppend opens the pack in f (of the given size) for appending. A signed pack is
// signed again when closed, so it needs the key now (ErrNeedKey): the new records go over
// the old index, and a pack that cannot be finished is left to Recover. A key given for an
// unsigned pack signs it.
func OpenAppend(f File, size int64, key ed25519.PrivateKey) (*Appender, error) {
	return openAppend(f, size, key, false)
}

// OpenAppendUnsigned opens a pack for appending and drops its signature, if it has one.
func OpenAppendUnsigned(f File, size int64) (*Appender, error) {
	return openAppend(f, size, nil, true)
}

func openAppend(f File, size int64, key ed25519.PrivateKey, drop bool) (*Appender, error) {
	r, err := Open(f, size)
	if err != nil {
		return nil, err
	}
	if r.sig != nil && key == nil && !drop {
		return nil, ErrNeedKey
	}
	a := &Appender{f: f, key: key}
	a.b = builder{
		write:   func(p []byte, off int64) error { _, err := f.WriteAt(p, off); return err },
		off:     int64(r.foot.indexOffset),
		header:  r.header,
		entries: append([]Entry(nil), r.index.Entries...),
		folders: append([]Folder(nil), r.index.Folders...),
	}
	return a, nil
}

// Add appends a document.
func (a *Appender) Add(doc docfile.Document) error { return a.b.add(doc) }

// AddRaw appends the bytes of a document listed under e.
func (a *Appender) AddRaw(e Entry, data []byte) error { return a.b.addRaw(e, data) }

// AddFolder lists a folder.
func (a *Appender) AddFolder(f Folder) error { return a.b.addFolder(f) }

// Edit changes what the index says about a document (its folder, tags, name, trash flag):
// no record is written. The record's own bytes are what they were.
func (a *Appender) Edit(id string, fn func(*Entry)) error {
	for i := range a.b.entries {
		if a.b.entries[i].ID == id {
			e := a.b.entries[i]
			fn(&e)
			e.ID, e.Offset, e.Length, e.SHA256 = a.b.entries[i].ID, a.b.entries[i].Offset, a.b.entries[i].Length, a.b.entries[i].SHA256
			a.b.entries[i] = e
			return nil
		}
	}
	return ErrNotFound
}

// Remove takes a document out of the index; its bytes stay until the pack is compacted.
func (a *Appender) Remove(id string) error {
	for i := range a.b.entries {
		if a.b.entries[i].ID == id {
			a.b.entries = append(a.b.entries[:i], a.b.entries[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// Close writes the index, signature and footer, cuts the file at the end and syncs it.
func (a *Appender) Close() error {
	if err := a.b.finish(a.key); err != nil {
		return err
	}
	if err := a.f.Truncate(a.b.off); err != nil {
		return err
	}
	return a.f.Sync()
}

// Compact copies the documents the index lists, and only those, from a pack to a new one
// in dst (a record that is damaged is an error), keeping folders and metadata. Sign it
// again with key, if it is not nil.
func Compact(src io.ReaderAt, size int64, dst io.Writer, key ed25519.PrivateKey) error {
	r, err := Open(src, size)
	if err != nil {
		return err
	}
	w, err := NewWriter(dst, r.meta)
	if err != nil {
		return err
	}
	for _, e := range r.index.Entries {
		data, err := r.ReadRaw(e.ID)
		if err != nil {
			return fmt.Errorf("pack: %q: %w", e.ID, err)
		}
		if err := w.AddRaw(e, data); err != nil {
			return err
		}
	}
	for _, f := range r.index.Folders {
		if err := w.AddFolder(f); err != nil {
			return err
		}
	}
	return w.Close(key)
}
