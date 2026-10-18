package pack

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"sort"

	"github.com/elaous/gojiberry/docfile"
)

// Reader reads a pack from any io.ReaderAt (a file, a range-readable URL). Opening it
// reads the header, the footer and the index and nothing else.
type Reader struct {
	r         io.ReaderAt
	size      int64
	meta      Meta
	header    []byte
	headerEnd int64
	foot      footer
	index     Index
	byID      map[string]int
	sig       *Signature
}

// readFrame reads and checks the record at off, which must end before end.
func readFrame(r io.ReaderAt, off, end int64) (kind, flags byte, data []byte, err error) {
	if off < 0 || off%8 != 0 || off+frameHead > end {
		return 0, 0, nil, fmt.Errorf("%w: a record outside the file", ErrFormat)
	}
	head := make([]byte, frameHead)
	if _, err := r.ReadAt(head, off); err != nil {
		return 0, 0, nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if binary.LittleEndian.Uint32(head[12:]) != crc(head[:12]) {
		return 0, 0, nil, fmt.Errorf("%w: a record header that does not match its check", ErrChecksum)
	}
	n := binary.LittleEndian.Uint64(head)
	if n > MaxRecord || off+int64(recordSize(n)) > end {
		return 0, 0, nil, fmt.Errorf("%w: a record of %d bytes does not fit", ErrFormat, n)
	}
	body := make([]byte, n+4)
	if _, err := r.ReadAt(body, off+frameHead); err != nil {
		return 0, 0, nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	data = body[:n]
	if binary.LittleEndian.Uint32(body[n:]) != crc(data) {
		return 0, 0, nil, fmt.Errorf("%w: a record's data does not match its check", ErrChecksum)
	}
	return head[8], head[9], data, nil
}

// readHeader reads the header of a pack: its meta and where it ends.
func readHeader(r io.ReaderAt, size int64) (Meta, []byte, error) {
	if size < headerFixed+footerSize {
		return Meta{}, nil, fmt.Errorf("%w: too short", ErrFormat)
	}
	fixed := make([]byte, headerFixed)
	if _, err := r.ReadAt(fixed, 0); err != nil {
		return Meta{}, nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if string(fixed[:8]) != magic {
		return Meta{}, nil, fmt.Errorf("%w: wrong magic", ErrFormat)
	}
	if v := binary.LittleEndian.Uint32(fixed[8:]); v != version {
		return Meta{}, nil, fmt.Errorf("%w: version %d", ErrFormat, v)
	}
	if f := binary.LittleEndian.Uint32(fixed[12:]); f != 0 {
		return Meta{}, nil, fmt.Errorf("%w: flags %#x are not understood", ErrFormat, f)
	}
	n := int64(binary.LittleEndian.Uint32(fixed[16:]))
	if n > maxMeta || headerFixed+n > size {
		return Meta{}, nil, fmt.Errorf("%w: a meta message of %d bytes", ErrFormat, n)
	}
	hdr := make([]byte, headerFixed+n+pad8(headerFixed+n))
	if int64(len(hdr))+footerSize > size {
		return Meta{}, nil, fmt.Errorf("%w: too short", ErrFormat)
	}
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return Meta{}, nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	for _, b := range hdr[headerFixed+n:] {
		if b != 0 {
			return Meta{}, nil, fmt.Errorf("%w: padding that is not zero", ErrFormat)
		}
	}
	m, err := unmarshalMeta(hdr[headerFixed : headerFixed+n])
	return m, hdr, err
}

// Open opens the pack of the given size: the footer and index are read and checked.
func Open(r io.ReaderAt, size int64) (*Reader, error) {
	meta, hdr, err := readHeader(r, size)
	if err != nil {
		return nil, err
	}
	rd := &Reader{r: r, size: size, meta: meta, header: hdr, headerEnd: int64(len(hdr))}
	fb := make([]byte, footerSize)
	if _, err := r.ReadAt(fb, size-footerSize); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	if rd.foot, err = parseFooter(fb); err != nil {
		return nil, err
	}
	f, end := rd.foot, size-footerSize
	if f.indexLen > MaxIndex || f.indexOffset < uint64(rd.headerEnd) || f.indexOffset > uint64(end) || int64(f.indexOffset)+int64(recordSize(f.indexLen)) > end {
		return nil, fmt.Errorf("%w: the index is outside the file", ErrFormat)
	}
	kind, _, data, err := readFrame(r, int64(f.indexOffset), end)
	if err != nil {
		return nil, err
	}
	if kind != kindIndex || uint64(len(data)) != f.indexLen {
		return nil, fmt.Errorf("%w: the index record is not what the footer says", ErrFormat)
	}
	if sha256.Sum256(data) != f.indexSum {
		return nil, fmt.Errorf("%w: the index does not match the footer's hash", ErrChecksum)
	}
	if rd.index, err = unmarshalIndex(data); err != nil {
		return nil, err
	}
	if err := rd.index.validate(rd.headerEnd, int64(f.indexOffset)); err != nil {
		return nil, err
	}
	rd.byID = make(map[string]int, len(rd.index.Entries))
	for i, e := range rd.index.Entries {
		rd.byID[e.ID] = i
	}
	if f.sigOffset != 0 {
		after := int64(f.indexOffset) + int64(recordSize(f.indexLen))
		if int64(f.sigOffset) < after || f.sigLen > MaxRecord || int64(f.sigOffset)+int64(recordSize(f.sigLen)) > end {
			return nil, fmt.Errorf("%w: the signature is outside the file", ErrFormat)
		}
		kind, _, data, err := readFrame(r, int64(f.sigOffset), end)
		if err != nil {
			return nil, err
		}
		if kind != kindSignature || uint64(len(data)) != f.sigLen {
			return nil, fmt.Errorf("%w: the signature record is not what the footer says", ErrFormat)
		}
		s, err := unmarshalSignature(data)
		if err != nil {
			return nil, err
		}
		rd.sig = &s
	}
	return rd, nil
}

// validate checks an index against the area [lo, hi) records may be in: IDs are
// document IDs and unique, records lie inside the area and do not overlap, tags and names
// are within their caps, and every folder an entry or folder names exists.
func (x Index) validate(lo, hi int64) error {
	ids := make(map[string]bool, len(x.Entries))
	for _, e := range x.Entries {
		if !docfile.ValidID(e.ID) || ids[e.ID] {
			return fmt.Errorf("%w: a missing, malformed or repeated document ID", ErrFormat)
		}
		ids[e.ID] = true
		if checkName(e.Name) != nil || len(e.Tags) > MaxTags || e.Length > MaxRecord || e.Offset%8 != 0 ||
			e.Offset > uint64(hi) || int64(e.Offset) < lo || int64(e.Offset)+int64(recordSize(e.Length)) > hi {
			return fmt.Errorf("%w: entry %q points outside the records", ErrFormat, e.ID)
		}
		for _, t := range e.Tags {
			if len(t) > MaxName {
				return fmt.Errorf("%w: entry %q has a tag that is too long", ErrFormat, e.ID)
			}
		}
	}
	// No two records overlap: sorted by offset, each ends before the next begins.
	byOffset := make([]*Entry, len(x.Entries))
	for i := range x.Entries {
		byOffset[i] = &x.Entries[i]
	}
	sort.Slice(byOffset, func(i, j int) bool { return byOffset[i].Offset < byOffset[j].Offset })
	for i := 1; i < len(byOffset); i++ {
		if byOffset[i].Offset < byOffset[i-1].Offset+recordSize(byOffset[i-1].Length) {
			return fmt.Errorf("%w: entry %q overlaps another record", ErrFormat, byOffset[i].ID)
		}
	}
	folders := make(map[string]Folder, len(x.Folders))
	for _, f := range x.Folders {
		if !docfile.ValidID(f.ID) || checkName(f.Name) != nil {
			return fmt.Errorf("%w: a bad folder", ErrFormat)
		}
		if _, dup := folders[f.ID]; dup {
			return fmt.Errorf("%w: a repeated folder ID", ErrFormat)
		}
		folders[f.ID] = f
	}
	for _, e := range x.Entries {
		if _, ok := folders[e.Folder]; e.Folder != "" && !ok {
			return fmt.Errorf("%w: entry %q is in a folder the index does not list", ErrFormat, e.ID)
		}
	}
	for id, f := range folders {
		if _, ok := folders[f.Parent]; f.Parent != "" && !ok {
			return fmt.Errorf("%w: folder %q has a parent the index does not list", ErrFormat, id)
		}
		depth := 0
		for cur := id; cur != ""; cur = folders[cur].Parent {
			if depth++; depth > MaxDepth {
				return fmt.Errorf("%w: folders nested too deep, or in a loop", ErrFormat)
			}
		}
	}
	return nil
}

// Meta is the pack's own description.
func (r *Reader) Meta() Meta { return r.meta }

// Index lists the documents and folders.
func (r *Reader) Index() Index { return r.index }

// Signature returns the pack's signature record and whether it is a valid signature of
// this pack by the key it carries (whether to trust that key is the caller's to decide).
// The record is nil for an unsigned pack.
func (r *Reader) Signature() (sig *Signature, valid bool) {
	if r.sig == nil {
		return nil, false
	}
	return r.sig, r.sig.valid(r.header, r.foot.indexSum)
}

// Signers lists everyone who signed the pack, the first signer first, each with whether the signature is valid for
// this pack. It is empty for an unsigned pack.
func (r *Reader) Signers() []Signer {
	if r.sig == nil {
		return nil
	}
	var out []Signer
	for _, s := range r.sig.all() {
		out = append(out, Signer{PublicKey: s.PublicKey, Valid: s.valid(r.header, r.foot.indexSum)})
	}
	return out
}

// ReadRaw returns the stored bytes of a document, checked against its record's checksums
// and the index's hash: a .open message, or for an Encrypted entry the envelope as stored
// (no key is involved).
func (r *Reader) ReadRaw(id string) ([]byte, error) {
	i, ok := r.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	e := r.index.Entries[i]
	kind, flags, data, err := readFrame(r.r, int64(e.Offset), int64(r.foot.indexOffset))
	if err != nil {
		return nil, err
	}
	if flags&^flagEncrypted != 0 {
		return nil, fmt.Errorf("%w: the record of %q has flags %#x that are not understood", ErrFormat, id, flags)
	}
	if (flags&flagEncrypted != 0) != e.Encrypted {
		return nil, fmt.Errorf("%w: the record of %q is not flagged as the index says", ErrFormat, id)
	}
	if kind != kindDocument || uint64(len(data)) != e.Length {
		return nil, fmt.Errorf("%w: the record of %q is not what the index says", ErrFormat, id)
	}
	if sha256.Sum256(data) != e.SHA256 {
		return nil, fmt.Errorf("%w: %q does not match its hash", ErrChecksum, id)
	}
	return data, nil
}

// Read returns a document, or ErrEncrypted for one whose record is an envelope.
func (r *Reader) Read(id string) (docfile.Document, error) {
	return r.ReadWith(id, nil)
}

// ReadWith returns a document, passing an encrypted record's envelope through open (which
// decrypts it and returns the .open message). The pack holds no keys and imports no
// cryptography: open is the caller's. With a nil open an encrypted record is ErrEncrypted.
// An error from open is returned wrapped, and a record the pack's checks reject is an
// error before open is called. A plain record never goes through open.
func (r *Reader) ReadWith(id string, open func(envelope []byte) ([]byte, error)) (docfile.Document, error) {
	data, err := r.ReadRaw(id)
	if err != nil {
		return docfile.Document{}, err
	}
	if r.index.Entries[r.byID[id]].Encrypted {
		if open == nil {
			return docfile.Document{}, ErrEncrypted
		}
		if data, err = open(data); err != nil {
			return docfile.Document{}, fmt.Errorf("pack: opening %q: %w", id, err)
		}
	}
	doc, err := docfile.Open.Unmarshal(data)
	if err != nil {
		return docfile.Document{}, err
	}
	if doc.ID() != id {
		return docfile.Document{}, fmt.Errorf("%w: the document in the record of %q has the ID %q", ErrFormat, id, doc.ID())
	}
	return doc, nil
}

// Problem is a document that could not be read.
type Problem struct {
	ID, Name string
	Err      error
}

// Report is what Verify found.
type Report struct {
	// Checked is how many documents were read and compared with their hashes.
	Checked int
	// Bad are the documents that failed, the others being fine.
	Bad []Problem
	// Unindexed counts records in the file that the index does not list: an append that
	// did not finish, or documents deleted since the pack was last compacted.
	Unindexed int
	// Gap is set when the records do not follow one another exactly up to the index.
	Gap bool
	// Signed says there is a signature record; SignatureOK that it is valid for this
	// pack; Signer is the key it carries.
	// With co-signers SignatureOK says every signature is valid; Signers lists them.
	Signed, SignatureOK bool
	Signer              []byte
	Signers             []Signer
}

// OK says whether everything checked out.
func (rep Report) OK() bool { return len(rep.Bad) == 0 && !rep.Gap && (!rep.Signed || rep.SignatureOK) }

// Verify reads every document and checks it, finds records the index does not list, and
// checks the signature.
func (r *Reader) Verify() Report {
	var rep Report
	for _, e := range r.index.Entries {
		rep.Checked++
		if _, err := r.ReadRaw(e.ID); err != nil {
			rep.Bad = append(rep.Bad, Problem{ID: e.ID, Name: e.Name, Err: err})
		}
	}
	// No two records overlap.
	byOffset := append([]Entry(nil), r.index.Entries...)
	sort.Slice(byOffset, func(i, j int) bool { return byOffset[i].Offset < byOffset[j].Offset })
	for i := 1; i < len(byOffset); i++ {
		if byOffset[i].Offset < byOffset[i-1].Offset+recordSize(byOffset[i-1].Length) {
			rep.Bad = append(rep.Bad, Problem{ID: byOffset[i].ID, Name: byOffset[i].Name, Err: fmt.Errorf("%w: overlaps another record", ErrFormat)})
		}
	}
	// Walk the records: those not in the index are reported.
	indexed := map[uint64]bool{}
	for _, e := range r.index.Entries {
		indexed[e.Offset] = true
	}
	off := r.headerEnd
	for off < int64(r.foot.indexOffset) {
		kind, _, data, err := readFrame(r.r, off, int64(r.foot.indexOffset))
		if err != nil {
			rep.Gap = true
			break
		}
		if kind == kindDocument && !indexed[uint64(off)] {
			rep.Unindexed++
		}
		off += int64(recordSize(uint64(len(data))))
	}
	if off != int64(r.foot.indexOffset) {
		rep.Gap = true
	}
	if sig, _ := r.Signature(); sig != nil {
		rep.Signers = r.Signers()
		rep.Signed, rep.SignatureOK, rep.Signer = true, true, sig.PublicKey
		for _, s := range rep.Signers {
			rep.SignatureOK = rep.SignatureOK && s.Valid
		}
	}
	return rep
}
