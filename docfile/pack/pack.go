// Package pack reads and writes the .opendoc pack format (docs/opendoc-format.md): many
// documents and their folders in one file, each with a SHA-256, listed by an index at the
// end so a pack opens without being read through, appended to in place, checked, and
// recovered when it is cut. It depends on docfile and the standard library only.
package pack

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"unicode/utf8"
)

// Limits, always checked against the file size before anything is allocated.
const (
	MaxRecord  = 256 << 20
	MaxEntries = 1_000_000
	MaxIndex   = 256 << 20
	MaxName    = 256
	MaxDepth   = 16
	maxMeta    = 1 << 20
)

const (
	magic       = "OPENDOC\x00"
	footerMagic = "OPENDEND"
	version     = 1
	headerFixed = 24
	frameHead   = 16
	footerSize  = 80

	kindDocument  = 1
	kindIndex     = 2
	kindSignature = 3
)

var (
	// ErrFormat is wrapped in the error for a file that is not a pack, or is damaged.
	ErrFormat = errors.New("pack: not a valid .opendoc pack")
	// ErrChecksum is wrapped in the error for a record whose bytes are not the ones
	// that were written.
	ErrChecksum = errors.New("pack: checksum mismatch")
	// ErrNotFound is returned for a document the index does not list.
	ErrNotFound = errors.New("pack: no such document")
	// ErrNeedKey is returned when appending to a signed pack without the key to sign it
	// again with.
	ErrNeedKey = errors.New("pack: the pack is signed; give the key to sign it again, or drop the signature")
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

func crc(b []byte) uint32 { return crc32.Checksum(b, castagnoli) }

func pad8(n int64) int64 { return (8 - n%8) % 8 }

// Meta describes the pack as a whole.
type Meta struct {
	ID          string
	Name        string
	Created     int64 // unix nanoseconds
	Producer    string
	Description string
}

// Entry is one document in the index.
type Entry struct {
	ID      string
	Name    string
	Folder  string // "" for the top level
	Tags    []string
	Updated int64  // unix nanoseconds
	Offset  uint64 // of the record, from the start of the file
	Length  uint64 // of the record's data
	SHA256  [32]byte
	Trashed bool
}

// Folder is a folder of the index. Folders are virtual: a document names its folder and
// a folder names its parent.
type Folder struct {
	ID, Name, Parent string
}

// Index lists what a pack holds.
type Index struct {
	Entries []Entry
	Folders []Folder
}

// Signature is the optional signature record.
type Signature struct {
	Alg       string // "ed25519"
	PublicKey []byte
	Sig       []byte
}

// signedDigest is what a signature covers: the header, and through the index's hash, every
// document.
func signedDigest(headerBytes []byte, indexSum [32]byte) []byte {
	h := sha256.New()
	h.Write([]byte("opendoc-v1"))
	hh := sha256.Sum256(headerBytes)
	h.Write(hh[:])
	h.Write(indexSum[:])
	return h.Sum(nil)
}

func sign(key ed25519.PrivateKey, headerBytes []byte, indexSum [32]byte) Signature {
	pub, _ := key.Public().(ed25519.PublicKey)
	return Signature{Alg: "ed25519", PublicKey: pub, Sig: ed25519.Sign(key, signedDigest(headerBytes, indexSum))}
}

func (s Signature) valid(headerBytes []byte, indexSum [32]byte) bool {
	return s.Alg == "ed25519" && len(s.PublicKey) == ed25519.PublicKeySize &&
		ed25519.Verify(ed25519.PublicKey(s.PublicKey), signedDigest(headerBytes, indexSum), s.Sig)
}

// ---- protobuf wire format, just what the pack's messages need ----

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendTag(b []byte, num int, wire int) []byte {
	return appendVarint(b, uint64(num)<<3|uint64(wire))
}

func appendString(b []byte, num int, s string) []byte {
	if s == "" {
		return b
	}
	return appendBytes(b, num, []byte(s))
}

func appendBytes(b []byte, num int, v []byte) []byte {
	if len(v) == 0 {
		return b
	}
	b = appendTag(b, num, 2)
	b = appendVarint(b, uint64(len(v)))
	return append(b, v...)
}

func appendInt(b []byte, num int, v uint64) []byte {
	if v == 0 {
		return b
	}
	b = appendTag(b, num, 0)
	return appendVarint(b, v)
}

// field is one field read from a message.
type field struct {
	num    int
	wire   int
	varint uint64
	bytes  []byte
}

// fields reads every field of a message, skipping none: the caller picks what it knows.
func fields(b []byte) ([]field, error) {
	var out []field
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 || tag>>3 == 0 {
			return nil, fmt.Errorf("%w: bad field tag", ErrFormat)
		}
		b = b[n:]
		f := field{num: int(tag >> 3), wire: int(tag & 7)}
		switch f.wire {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return nil, fmt.Errorf("%w: bad varint", ErrFormat)
			}
			f.varint, b = v, b[n:]
		case 1:
			if len(b) < 8 {
				return nil, fmt.Errorf("%w: short fixed64", ErrFormat)
			}
			b = b[8:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || l > uint64(len(b)-n) {
				return nil, fmt.Errorf("%w: bad length", ErrFormat)
			}
			f.bytes, b = b[n:n+int(l)], b[n+int(l):]
		case 5:
			if len(b) < 4 {
				return nil, fmt.Errorf("%w: short fixed32", ErrFormat)
			}
			b = b[4:]
		default:
			return nil, fmt.Errorf("%w: unsupported wire type %d", ErrFormat, f.wire)
		}
		out = append(out, f)
	}
	return out, nil
}

func utf8String(f field) (string, error) {
	if !utf8.Valid(f.bytes) {
		return "", fmt.Errorf("%w: text that is not UTF-8", ErrFormat)
	}
	return string(f.bytes), nil
}

// ---- messages ----

func (m Meta) marshal() []byte {
	var b []byte
	b = appendString(b, 1, m.ID)
	b = appendString(b, 2, m.Name)
	b = appendInt(b, 3, uint64(m.Created))
	b = appendString(b, 4, m.Producer)
	b = appendString(b, 5, m.Description)
	return b
}

func unmarshalMeta(b []byte) (m Meta, err error) {
	fs, err := fields(b)
	if err != nil {
		return m, err
	}
	for _, f := range fs {
		switch {
		case f.num == 3 && f.wire == 0:
			m.Created = int64(f.varint)
		case f.wire == 2 && f.num >= 1 && f.num <= 5 && f.num != 3:
			s, err := utf8String(f)
			if err != nil {
				return m, err
			}
			switch f.num {
			case 1:
				m.ID = s
			case 2:
				m.Name = s
			case 4:
				m.Producer = s
			case 5:
				m.Description = s
			}
		}
	}
	return m, nil
}

func (e Entry) marshal() []byte {
	var b []byte
	b = appendString(b, 1, e.ID)
	b = appendString(b, 2, e.Name)
	b = appendString(b, 3, e.Folder)
	for _, t := range e.Tags {
		b = appendTag(b, 4, 2)
		b = appendVarint(b, uint64(len(t)))
		b = append(b, t...)
	}
	b = appendInt(b, 5, uint64(e.Updated))
	b = appendInt(b, 6, e.Offset)
	b = appendInt(b, 7, e.Length)
	b = appendBytes(b, 8, e.SHA256[:])
	if e.Trashed {
		b = appendInt(b, 9, 1)
	}
	return b
}

func unmarshalEntry(b []byte) (e Entry, err error) {
	fs, err := fields(b)
	if err != nil {
		return e, err
	}
	for _, f := range fs {
		switch {
		case f.wire == 2 && (f.num == 1 || f.num == 2 || f.num == 3 || f.num == 4):
			s, err := utf8String(f)
			if err != nil {
				return e, err
			}
			switch f.num {
			case 1:
				e.ID = s
			case 2:
				e.Name = s
			case 3:
				e.Folder = s
			case 4:
				e.Tags = append(e.Tags, s)
			}
		case f.wire == 0 && f.num == 5:
			e.Updated = int64(f.varint)
		case f.wire == 0 && f.num == 6:
			e.Offset = f.varint
		case f.wire == 0 && f.num == 7:
			e.Length = f.varint
		case f.wire == 2 && f.num == 8:
			if len(f.bytes) != 32 {
				return e, fmt.Errorf("%w: a hash of %d bytes", ErrFormat, len(f.bytes))
			}
			copy(e.SHA256[:], f.bytes)
		case f.wire == 0 && f.num == 9:
			e.Trashed = f.varint != 0
		}
	}
	return e, nil
}

func (f Folder) marshal() []byte {
	var b []byte
	b = appendString(b, 1, f.ID)
	b = appendString(b, 2, f.Name)
	b = appendString(b, 3, f.Parent)
	return b
}

func unmarshalFolder(b []byte) (fo Folder, err error) {
	fs, err := fields(b)
	if err != nil {
		return fo, err
	}
	for _, f := range fs {
		if f.wire != 2 || f.num < 1 || f.num > 3 {
			continue
		}
		s, err := utf8String(f)
		if err != nil {
			return fo, err
		}
		switch f.num {
		case 1:
			fo.ID = s
		case 2:
			fo.Name = s
		case 3:
			fo.Parent = s
		}
	}
	return fo, nil
}

func (x Index) marshal() []byte {
	var b []byte
	for _, e := range x.Entries {
		m := e.marshal()
		b = appendTag(b, 1, 2)
		b = appendVarint(b, uint64(len(m)))
		b = append(b, m...)
	}
	for _, f := range x.Folders {
		m := f.marshal()
		b = appendTag(b, 2, 2)
		b = appendVarint(b, uint64(len(m)))
		b = append(b, m...)
	}
	return b
}

func unmarshalIndex(b []byte) (x Index, err error) {
	fs, err := fields(b)
	if err != nil {
		return x, err
	}
	for _, f := range fs {
		if f.wire != 2 {
			continue
		}
		switch f.num {
		case 1:
			if len(x.Entries) >= MaxEntries {
				return x, fmt.Errorf("%w: more than %d entries", ErrFormat, MaxEntries)
			}
			e, err := unmarshalEntry(f.bytes)
			if err != nil {
				return x, err
			}
			x.Entries = append(x.Entries, e)
		case 2:
			if len(x.Folders) >= MaxEntries {
				return x, fmt.Errorf("%w: more than %d folders", ErrFormat, MaxEntries)
			}
			fo, err := unmarshalFolder(f.bytes)
			if err != nil {
				return x, err
			}
			x.Folders = append(x.Folders, fo)
		}
	}
	return x, nil
}

func (s Signature) marshal() []byte {
	var b []byte
	b = appendString(b, 1, s.Alg)
	b = appendBytes(b, 2, s.PublicKey)
	b = appendBytes(b, 3, s.Sig)
	return b
}

func unmarshalSignature(b []byte) (s Signature, err error) {
	fs, err := fields(b)
	if err != nil {
		return s, err
	}
	for _, f := range fs {
		if f.wire != 2 {
			continue
		}
		switch f.num {
		case 1:
			if s.Alg, err = utf8String(f); err != nil {
				return s, err
			}
		case 2:
			s.PublicKey = append([]byte(nil), f.bytes...)
		case 3:
			s.Sig = append([]byte(nil), f.bytes...)
		}
	}
	return s, nil
}

// ---- layout ----

// headerBytes is the file's header: the fixed part, the meta message, padding.
func headerBytes(m Meta) []byte {
	meta := m.marshal()
	b := make([]byte, headerFixed, headerFixed+len(meta)+8)
	copy(b, magic)
	binary.LittleEndian.PutUint32(b[8:], version)
	binary.LittleEndian.PutUint32(b[12:], 0)
	binary.LittleEndian.PutUint32(b[16:], uint32(len(meta)))
	b = append(b, meta...)
	return append(b, make([]byte, pad8(int64(len(b))))...)
}

// frame is a record: length, kind, check of those, data, check of the data, padding.
func frame(kind byte, data []byte) []byte {
	b := make([]byte, frameHead, frameHead+len(data)+4+7)
	binary.LittleEndian.PutUint64(b, uint64(len(data)))
	b[8] = kind
	binary.LittleEndian.PutUint32(b[12:], crc(b[:12]))
	b = append(b, data...)
	b = binary.LittleEndian.AppendUint32(b, crc(data))
	return append(b, make([]byte, pad8(int64(len(b))))...)
}

// recordSize is the size of a record whose data is n bytes.
func recordSize(n uint64) uint64 {
	t := uint64(frameHead) + n + 4
	return t + uint64(pad8(int64(t)))
}

type footer struct {
	indexOffset, indexLen uint64
	indexSum              [32]byte
	sigOffset, sigLen     uint64
}

func (f footer) marshal() []byte {
	b := make([]byte, footerSize)
	copy(b, footerMagic)
	binary.LittleEndian.PutUint64(b[8:], f.indexOffset)
	binary.LittleEndian.PutUint64(b[16:], f.indexLen)
	copy(b[24:], f.indexSum[:])
	binary.LittleEndian.PutUint64(b[56:], f.sigOffset)
	binary.LittleEndian.PutUint64(b[64:], f.sigLen)
	binary.LittleEndian.PutUint32(b[72:], crc(b[:72]))
	return b
}

func parseFooter(b []byte) (footer, error) {
	var f footer
	if len(b) != footerSize || string(b[:8]) != footerMagic || binary.LittleEndian.Uint32(b[72:]) != crc(b[:72]) {
		return f, fmt.Errorf("%w: no valid footer", ErrFormat)
	}
	f.indexOffset = binary.LittleEndian.Uint64(b[8:])
	f.indexLen = binary.LittleEndian.Uint64(b[16:])
	copy(f.indexSum[:], b[24:56])
	f.sigOffset = binary.LittleEndian.Uint64(b[56:])
	f.sigLen = binary.LittleEndian.Uint64(b[64:])
	return f, nil
}

// checkName enforces the length of a name.
func checkName(s string) error {
	if len(s) > MaxName {
		return fmt.Errorf("pack: a name of %d bytes (at most %d)", len(s), MaxName)
	}
	return nil
}
