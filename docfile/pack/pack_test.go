package pack

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

func doc(id, name, text string) docfile.Document {
	d := docfile.Document{Title: name, Updated: 1700000000000000000, Content: []docfile.Item{{Chars: &docfile.Chars{Text: text}}}}
	d.SetID(id)
	d.SetName(name)
	return d
}

const (
	id1 = "11111111-1111-4111-8111-111111111111"
	id2 = "22222222-2222-4222-8222-222222222222"
	id3 = "33333333-3333-4333-8333-333333333333"
	fid = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
)

func text(d docfile.Document) string { return d.Content[0].Chars.Text }

func build(t *testing.T, key ed25519.PrivateKey) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Meta{ID: "pack-1", Name: "Work", Created: 1700000000000000000, Producer: "test"})
	if err != nil {
		t.Fatal(err)
	}
	d1 := doc(id1, "one", "first")
	d1.SetFolder(fid)
	d1.SetTags([]string{"a", "b"})
	for _, d := range []docfile.Document{d1, doc(id2, "two", "second")} {
		if err := w.Add(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.AddFolder(Folder{ID: fid, Name: "Reports"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(key); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func open(t *testing.T, data []byte) *Reader {
	t.Helper()
	r, err := Open(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestWriteAndReadBack(t *testing.T) {
	data := build(t, nil)
	if len(data)%8 != 0 {
		t.Errorf("the file is not a whole number of 8-byte units: %d", len(data))
	}
	r := open(t, data)
	if m := r.Meta(); m.Name != "Work" || m.Producer != "test" || m.ID != "pack-1" {
		t.Errorf("%+v", m)
	}
	idx := r.Index()
	if len(idx.Entries) != 2 || len(idx.Folders) != 1 || idx.Folders[0].Name != "Reports" {
		t.Fatalf("%+v", idx)
	}
	e := idx.Entries[0]
	if e.ID != id1 || e.Name != "one" || e.Folder != fid || strings.Join(e.Tags, ",") != "a,b" || e.Offset%8 != 0 {
		t.Errorf("%+v", e)
	}
	d, err := r.Read(id2)
	if err != nil || text(d) != "second" {
		t.Fatalf("%v", err)
	}
	if _, err := r.Read("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	rep := r.Verify()
	if !rep.OK() || rep.Checked != 2 || rep.Unindexed != 0 || rep.Signed {
		t.Errorf("%+v", rep)
	}
}

func TestSignature(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	data := build(t, key)
	r := open(t, data)
	sig, valid := r.Signature()
	if sig == nil || !valid || !bytes.Equal(sig.PublicKey, pub) {
		t.Fatalf("%+v %v", sig, valid)
	}
	if rep := r.Verify(); !rep.OK() || !rep.Signed || !rep.SignatureOK {
		t.Errorf("%+v", rep)
	}
	// Change a byte of the header (the meta name): the index still checks out, the signature does not.
	bad := append([]byte(nil), data...)
	i := bytes.Index(bad, []byte("Work"))
	bad[i] = 'X'
	if _, valid := open(t, bad).Signature(); valid {
		t.Error("a changed header is still signed")
	}
	// A document and its hash rewritten together: the index hash in the footer no longer matches.
	if _, err := Open(bytes.NewReader(flip(data, bytes.Index(data, []byte("first")))), int64(len(data))); err != nil {
		t.Fatal(err) // opening does not read the documents
	}
	r2 := open(t, flip(data, bytes.Index(data, []byte("first"))))
	if _, err := r2.Read(id1); !errors.Is(err, ErrChecksum) {
		t.Errorf("a changed document: %v", err)
	}
	if rep := r2.Verify(); rep.OK() || len(rep.Bad) != 1 || rep.Bad[0].ID != id1 {
		t.Errorf("%+v", rep)
	}
	if _, err := r2.Read(id2); err != nil {
		t.Errorf("the other document is still readable: %v", err)
	}
}

func flip(data []byte, i int) []byte {
	out := append([]byte(nil), data...)
	out[i] ^= 0x01
	return out
}

// memFile is a File in memory.
type memFile struct{ b []byte }

func (m *memFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m.b)) {
		return 0, errors.New("EOF")
	}
	n := copy(p, m.b[off:])
	if n < len(p) {
		return n, errors.New("short")
	}
	return n, nil
}
func (m *memFile) WriteAt(p []byte, off int64) (int, error) {
	if need := int(off) + len(p); need > len(m.b) {
		m.b = append(m.b, make([]byte, need-len(m.b))...)
	}
	return copy(m.b[off:], p), nil
}
func (m *memFile) Truncate(n int64) error { m.b = m.b[:n]; return nil }
func (m *memFile) Sync() error            { return nil }

func TestAppendInPlace(t *testing.T) {
	f := &memFile{b: build(t, nil)}
	oldIndex := open(t, f.b).foot.indexOffset
	prefix := append([]byte(nil), f.b[:oldIndex]...)
	a, err := OpenAppend(f, int64(len(f.b)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Add(doc(id3, "three", "third")); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(f.b[:oldIndex], prefix) {
		t.Error("what was written before was moved")
	}
	r := open(t, f.b)
	if len(r.Index().Entries) != 3 {
		t.Fatalf("%+v", r.Index())
	}
	for id, want := range map[string]string{id1: "first", id2: "second", id3: "third"} {
		if d, err := r.Read(id); err != nil || text(d) != want {
			t.Errorf("%s: %v", id, err)
		}
	}
	if rep := r.Verify(); !rep.OK() {
		t.Errorf("%+v", rep)
	}

	// Remove and edit: only the index changes; the bytes stay until Compact.
	a, _ = OpenAppend(f, int64(len(f.b)), nil)
	if err := a.Remove(id2); err != nil {
		t.Fatal(err)
	}
	if err := a.Edit(id1, func(e *Entry) { e.Folder = ""; e.Trashed = true }); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	r = open(t, f.b)
	if len(r.Index().Entries) != 2 || !r.Index().Entries[0].Trashed || r.Index().Entries[0].Folder != "" {
		t.Fatalf("%+v", r.Index())
	}
	if rep := r.Verify(); !rep.OK() || rep.Unindexed != 1 {
		t.Errorf("the removed document's record is reported, not an error: %+v", rep)
	}
	var compact bytes.Buffer
	if err := Compact(bytes.NewReader(f.b), int64(len(f.b)), &compact, nil); err != nil {
		t.Fatal(err)
	}
	c := open(t, compact.Bytes())
	if rep := c.Verify(); !rep.OK() || rep.Unindexed != 0 || len(c.Index().Entries) != 2 || compact.Len() >= len(f.b) {
		t.Errorf("%+v %d vs %d", rep, compact.Len(), len(f.b))
	}
}

func TestAppendToASignedPackNeedsTheKey(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(nil)
	f := &memFile{b: build(t, key)}
	before := append([]byte(nil), f.b...)
	if _, err := OpenAppend(f, int64(len(f.b)), nil); !errors.Is(err, ErrNeedKey) {
		t.Fatalf("%v", err)
	}
	if !bytes.Equal(f.b, before) {
		t.Fatal("a refused append changed the file")
	}
	a, err := OpenAppend(f, int64(len(f.b)), key)
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Add(doc(id3, "three", "third"))
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	sig, valid := open(t, f.b).Signature()
	if !valid || !bytes.Equal(sig.PublicKey, pub) {
		t.Error("signed again")
	}
	u, err := OpenAppendUnsigned(f, int64(len(f.b)))
	if err != nil {
		t.Fatal(err)
	}
	_ = u.Remove(id3)
	if err := u.Close(); err != nil {
		t.Fatal(err)
	}
	if s, _ := open(t, f.b).Signature(); s != nil {
		t.Error("the signature was dropped")
	}
}

// A pack cut at any byte is either read (cut after the footer is whole: only whole) or
// recovered to the documents whose records are whole; nothing panics.
func TestCutAtEveryByte(t *testing.T) {
	data := build(t, nil)
	r := open(t, data)
	e := r.Index().Entries
	endOf := func(i int) int { return int(e[i].Offset + recordSize(e[i].Length)) }
	for n := range len(data) {
		cut := data[:n]
		if _, err := Open(bytes.NewReader(cut), int64(n)); err == nil {
			t.Fatalf("a pack cut to %d bytes opened", n)
		}
		rec, err := Recover(bytes.NewReader(cut), int64(n))
		if err != nil {
			continue // not even a header
		}
		want := 0
		for i := range e {
			if endOf(i) <= n {
				want++
			}
		}
		if len(rec.Docs) != want || rec.Complete {
			t.Fatalf("cut at %d: recovered %d documents, want %d", n, len(rec.Docs), want)
		}
	}
	var repacked bytes.Buffer
	rec, _ := Recover(bytes.NewReader(data[:endOfLast(e)]), int64(endOfLast(e)))
	if err := Repack(rec, &repacked); err != nil {
		t.Fatal(err)
	}
	if rr := open(t, repacked.Bytes()); len(rr.Index().Entries) != 2 || !rr.Verify().OK() {
		t.Error("a recovered pack is a pack")
	}
	full, _ := Recover(bytes.NewReader(data), int64(len(data)))
	if !full.Complete || len(full.Docs) != 2 {
		t.Errorf("%+v", full.Complete)
	}
}

func endOfLast(e []Entry) int { return int(e[len(e)-1].Offset + recordSize(e[len(e)-1].Length)) }

func TestHostileInput(t *testing.T) {
	data := build(t, nil)
	// Every single bit flipped: an error or a different but valid reading, never a panic.
	for i := range data {
		bad := flip(data, i)
		if r, err := Open(bytes.NewReader(bad), int64(len(bad))); err == nil {
			r.Verify()
			for _, e := range r.Index().Entries {
				_, _ = r.Read(e.ID)
			}
		}
		_, _ = Recover(bytes.NewReader(bad), int64(len(bad)))
	}
	for _, junk := range [][]byte{nil, []byte("OPENDOC\x00"), bytes.Repeat([]byte{0xff}, 300), make([]byte, 300)} {
		if _, err := Open(bytes.NewReader(junk), int64(len(junk))); err == nil {
			t.Errorf("junk of %d bytes opened", len(junk))
		}
	}
}

func TestWriterRefusesBadInput(t *testing.T) {
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, Meta{})
	if err := w.Add(doc("not-an-id", "x", "y")); err == nil {
		t.Error("a bad ID")
	}
	if err := w.Add(doc(id1, strings.Repeat("n", 300), "y")); err == nil {
		t.Error("a name over the cap")
	}
	_ = w.Add(doc(id1, "ok", "y"))
	if err := w.Add(doc(id1, "again", "y")); err == nil {
		t.Error("the same ID twice")
	}
	_ = w.AddFolder(Folder{ID: "a", Name: "a", Parent: "b"})
	_ = w.AddFolder(Folder{ID: "b", Name: "b", Parent: "a"})
	if err := w.Close(nil); err == nil {
		t.Error("folders in a loop")
	}
}

// vectorKey is a fixed key so the committed test vector can be compared byte for byte.
func vector(t *testing.T) []byte {
	key := ed25519.NewKeyFromSeed([]byte("opendoc test vector seed 0123456")[:32])
	return build(t, key)
}

func TestTestVector(t *testing.T) {
	path := filepath.Join("testdata", "vector.opendoc")
	got := vector(t)
	if os.Getenv("WRITE_VECTOR") != "" {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the pack written is not the committed vector (%d bytes, want %d)", len(got), len(want))
	}
	r := open(t, want)
	if rep := r.Verify(); !rep.OK() || !rep.Signed {
		t.Errorf("%+v", rep)
	}
}

func FuzzOpen(f *testing.F) {
	f.Add([]byte("OPENDOC\x00"))
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := Open(bytes.NewReader(b), int64(len(b)))
		if err == nil {
			r.Verify()
			for _, e := range r.Index().Entries {
				_, _ = r.Read(e.ID)
			}
		}
		_, _ = Recover(bytes.NewReader(b), int64(len(b)))
	})
}
