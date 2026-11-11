package pack

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/elaous/gojiberry/docfile"
)

type builder struct {
	write   func(p []byte, off int64) error
	off     int64
	header  []byte
	entries []Entry
	folders []Folder
	atts    []Attachment
	v2      bool // the header says version 2: attachments are allowed
	closed  bool
	more    []ed25519.PrivateKey // co-signers (CloseSigned)
}

func (b *builder) emit(p []byte) error {
	if err := b.write(p, b.off); err != nil {
		return err
	}
	b.off += int64(len(p))
	return nil
}

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
	var flags byte
	if e.Encrypted {
		flags = flagEncrypted
	}
	if err := b.emit(frameFlags(kindDocument, flags, data)); err != nil {
		return err
	}
	b.entries = append(b.entries, e)
	return nil
}

var ErrNoAttachments = errors.New("pack: only a version 2 pack holds attachments")

func (b *builder) addAttachment(mime string, data []byte) ([32]byte, error) {
	sum := sha256.Sum256(data)
	if b.closed {
		return sum, errors.New("pack: closed")
	}
	if !b.v2 {
		return sum, ErrNoAttachments
	}
	if len(data) > MaxRecord {
		return sum, fmt.Errorf("pack: an attachment of %d bytes (at most %d)", len(data), MaxRecord)
	}
	if len(mime) > MaxName {
		return sum, errors.New("pack: an attachment type that is too long")
	}
	for _, a := range b.atts {
		if a.SHA256 == sum {
			return sum, nil
		}
	}
	if len(b.atts) >= MaxAttachments {
		return sum, fmt.Errorf("pack: more than %d attachments", MaxAttachments)
	}
	off := b.off
	if err := b.emit(frame(kindAttachment, data)); err != nil {
		return sum, err
	}
	b.atts = append(b.atts, Attachment{SHA256: sum, Offset: uint64(off), Length: uint64(len(data)), MIME: mime})
	return sum, nil
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
	if !docfile.ValidID(f.ID) || checkName(f.Name) != nil {
		return errors.New("pack: a folder needs a document-style ID and a name of at most 256 bytes")
	}
	for _, x := range b.folders {
		if x.ID == f.ID {
			return fmt.Errorf("pack: folder %q is already in the pack", f.ID)
		}
	}
	b.folders = append(b.folders, f)
	return nil
}

func (b *builder) finish(key ed25519.PrivateKey) error {
	idx := Index{Entries: b.entries, Folders: b.folders, Attachments: b.atts}
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
		sg := sign(key, b.header, f.indexSum)
		for _, k := range b.more {
			sg.More = append(sg.More, sign(k, b.header, f.indexSum))
		}
		sdata := sg.marshal()
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
func NewWriter(w io.Writer, meta Meta) (*Writer, error) { return newWriter(w, meta, false) }

func NewWriterV2(w io.Writer, meta Meta) (*Writer, error) { return newWriter(w, meta, true) }

func newWriter(w io.Writer, meta Meta, v2 bool) (*Writer, error) {
	ver := uint32(version)
	if v2 {
		ver = version2
	}
	wr := &Writer{b: builder{v2: v2, write: func(p []byte, _ int64) error { _, err := w.Write(p); return err }, header: headerBytesV(meta, ver)}}
	if err := wr.b.emit(wr.b.header); err != nil {
		return nil, err
	}
	return wr, nil
}

func (w *Writer) Add(doc docfile.Document) error { return w.b.add(doc) }

func (w *Writer) AddRaw(e Entry, data []byte) error { return w.b.addRaw(e, data) }

func (w *Writer) AddEncrypted(e Entry, env []byte) error {
	e.Encrypted = true
	return w.b.addRaw(e, env)
}

func (w *Writer) AddAttachment(mime string, data []byte) ([32]byte, error) {
	return w.b.addAttachment(mime, data)
}

func (w *Writer) Version2() bool { return w.b.v2 }

// AddFolder lists a folder.
func (w *Writer) AddFolder(f Folder) error { return w.b.addFolder(f) }

func (w *Writer) CloseSigned(keys ...ed25519.PrivateKey) error {
	if len(keys) > MaxSigners {
		return fmt.Errorf("pack: %d signers (at most %d)", len(keys), MaxSigners)
	}
	if len(keys) == 0 {
		return w.b.finish(nil)
	}
	w.b.more = keys[1:]
	return w.b.finish(keys[0])
}

func (w *Writer) Close(key ed25519.PrivateKey) error { return w.b.finish(key) }

type File interface {
	io.ReaderAt
	io.WriterAt
	Truncate(size int64) error
	Sync() error
}

type Appender struct {
	b   builder
	f   File
	key ed25519.PrivateKey
}

func OpenAppend(f File, size int64, key ed25519.PrivateKey) (*Appender, error) {
	return openAppend(f, size, key, false)
}

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
		atts:    append([]Attachment(nil), r.index.Attachments...),
		v2:      r.version >= version2,
	}
	return a, nil
}

// Add appends a document.
func (a *Appender) Add(doc docfile.Document) error { return a.b.add(doc) }

// AddRaw appends the bytes of a document listed under e.
func (a *Appender) AddRaw(e Entry, data []byte) error { return a.b.addRaw(e, data) }

func (a *Appender) AddEncrypted(e Entry, env []byte) error {
	e.Encrypted = true
	return a.b.addRaw(e, env)
}

func (a *Appender) AddAttachment(mime string, data []byte) ([32]byte, error) {
	return a.b.addAttachment(mime, data)
}

// AddFolder lists a folder.
func (a *Appender) AddFolder(f Folder) error { return a.b.addFolder(f) }

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

func (a *Appender) Remove(id string) error {
	for i := range a.b.entries {
		if a.b.entries[i].ID == id {
			a.b.entries = append(a.b.entries[:i], a.b.entries[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (a *Appender) Close() error {
	if err := a.b.finish(a.key); err != nil {
		return err
	}
	if err := a.f.Truncate(a.b.off); err != nil {
		return err
	}
	return a.f.Sync()
}

func Compact(src io.ReaderAt, size int64, dst io.Writer, key ed25519.PrivateKey) error {
	r, err := Open(src, size)
	if err != nil {
		return err
	}
	w, err := newWriter(dst, r.meta, r.version >= version2)
	if err != nil {
		return err
	}
	for _, a := range r.index.Attachments {
		data, mime, err := r.Attachment(a.SHA256)
		if err != nil {
			return fmt.Errorf("pack: an attachment: %w", err)
		}
		if _, err := w.AddAttachment(mime, data); err != nil {
			return err
		}
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

var ErrBadSignature = errors.New("pack: the pack's signature is not valid, so it is not co-signed")

var ErrNotSigned = errors.New("pack: the pack is not signed, so there is nothing to co-sign")

var ErrAlreadySigned = errors.New("pack: that key already signed this pack")

func Cosign(f File, size int64, key ed25519.PrivateKey) error {
	r, err := Open(f, size)
	if err != nil {
		return err
	}
	if r.sig == nil {
		return ErrNotSigned
	}
	if len(r.sig.all()) >= MaxSigners {
		return fmt.Errorf("pack: already %d signers", MaxSigners)
	}
	pub, _ := key.Public().(ed25519.PublicKey)
	for _, s := range r.Signers() {
		if !s.Valid {
			return ErrBadSignature
		}
		if bytes.Equal(s.PublicKey, pub) {
			return ErrAlreadySigned
		}
	}
	sg := *r.sig
	sg.More = append(append([]Signature(nil), sg.More...), sign(key, r.header, r.foot.indexSum))
	data := sg.marshal()
	fr := frame(kindSignature, data)
	ft := r.foot
	ft.sigLen = uint64(len(data))
	off := int64(ft.sigOffset)
	if _, err := f.WriteAt(fr, off); err != nil {
		return err
	}
	if _, err := f.WriteAt(ft.marshal(), off+int64(len(fr))); err != nil {
		return err
	}
	if err := f.Truncate(off + int64(len(fr)) + footerSize); err != nil {
		return err
	}
	return f.Sync()
}
