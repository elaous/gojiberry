package pack

import (
	"bytes"
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/elaous/gojiberry/docfile"
)

// A fake sealer: the pack never sees a cipher, so any reversible, recognisable transform
// shows it only moves bytes. seal is "SEAL" plus the message xor 0x5a.
func seal(p []byte) []byte {
	out := []byte("SEAL")
	for _, c := range p {
		out = append(out, c^0x5a)
	}
	return out
}

func unseal(env []byte) ([]byte, error) {
	if !bytes.HasPrefix(env, []byte("SEAL")) {
		return nil, errors.New("not an envelope")
	}
	out := make([]byte, 0, len(env)-4)
	for _, c := range env[4:] {
		out = append(out, c^0x5a)
	}
	return out, nil
}

// buildMixed is a pack with a plain document (id1) and an encrypted one (id2, listed as a
// placeholder with no tags), signed when key is not nil.
func buildMixed(t testing.TB, key ed25519.PrivateKey) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriter(&buf, Meta{ID: "pack-2", Name: "Mixed", Created: 1700000000000000000})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Add(doc(id1, "one", "first")); err != nil {
		t.Fatal(err)
	}
	secret := doc(id2, "Quarterly numbers", "the secret text")
	secret.SetTags([]string{"finance"})
	plain, err := docfile.Open.Marshal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddEncrypted(Entry{ID: id2, Name: "Confidential document", Updated: secret.Updated}, seal(plain)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(key); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestEncryptedRoundTrip(t *testing.T) {
	data := buildMixed(t, nil)
	if bytes.Contains(data, []byte("secret text")) || bytes.Contains(data, []byte("Quarterly")) || bytes.Contains(data, []byte("finance")) {
		t.Fatal("plaintext of the encrypted document is in the pack")
	}
	r := open(t, data)
	var enc *Entry
	for i, e := range r.Index().Entries {
		if e.ID == id2 {
			enc = &r.Index().Entries[i]
		} else if e.Encrypted {
			t.Errorf("%q is flagged encrypted", e.ID)
		}
	}
	if enc == nil || !enc.Encrypted || enc.Name != "Confidential document" || len(enc.Tags) != 0 {
		t.Fatalf("%+v", enc)
	}
	if _, err := r.Read(id2); !errors.Is(err, ErrEncrypted) {
		t.Errorf("Read of an encrypted record: %v", err)
	}
	if _, err := r.ReadWith(id2, nil); !errors.Is(err, ErrEncrypted) {
		t.Errorf("ReadWith(nil): %v", err)
	}
	got, err := r.ReadWith(id2, unseal)
	if err != nil || got.Name() != "Quarterly numbers" || text(got) != "the secret text" || got.Tags()[0] != "finance" {
		t.Fatalf("%+v %v", got, err)
	}
	// A plain record is read as before, and never goes through open.
	called := false
	if d, err := r.ReadWith(id1, func([]byte) ([]byte, error) { called = true; return nil, nil }); err != nil || text(d) != "first" || called {
		t.Errorf("plain record: %v %v", err, called)
	}
	// A failing open is an error that keeps the cause.
	boom := errors.New("wrong key")
	if _, err := r.ReadWith(id2, func([]byte) ([]byte, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Errorf("%v", err)
	}
	// An opener that returns another document's bytes is refused by the ID check.
	other, _ := docfile.Open.Marshal(doc(id3, "x", "y"))
	if _, err := r.ReadWith(id2, func([]byte) ([]byte, error) { return other, nil }); !errors.Is(err, ErrFormat) {
		t.Errorf("a document of another ID: %v", err)
	}
}

func TestEncryptedWithoutAKey(t *testing.T) {
	key := ed25519.NewKeyFromSeed([]byte("opendoc encrypted seed 012345678")[:32])
	data := buildMixed(t, key)
	r := open(t, data)
	if rep := r.Verify(); !rep.OK() || !rep.Signed || rep.Checked != 2 {
		t.Fatalf("%+v", rep)
	}
	raw, err := r.ReadRaw(id2)
	if err != nil || !bytes.HasPrefix(raw, []byte("SEAL")) {
		t.Fatalf("%q %v", raw, err)
	}

	// Compact keeps the envelope and its flag, and the entry's placeholder.
	var out bytes.Buffer
	if err := Compact(bytes.NewReader(data), int64(len(data)), &out, key); err != nil {
		t.Fatal(err)
	}
	c := open(t, out.Bytes())
	if rep := c.Verify(); !rep.OK() || !rep.SignatureOK {
		t.Fatalf("%+v", rep)
	}
	if e := c.Index().Entries[1]; !e.Encrypted || e.Name != "Confidential document" {
		t.Errorf("%+v", e)
	}
	if got, err := c.ReadWith(id2, unseal); err != nil || text(got) != "the secret text" {
		t.Errorf("%v", err)
	}

	// Recover of a pack with a cut footer: without the index an encrypted record cannot be
	// placed (no ID), so it is counted; with an intact index it is carried as Sealed.
	rec, err := Recover(bytes.NewReader(data), int64(len(data)))
	if err != nil || !rec.Complete || len(rec.Docs) != 1 || len(rec.Sealed) != 1 || rec.Skipped != 0 || rec.SealedLost != 0 || rec.Sealed[0].Entry.ID != id2 {
		t.Fatalf("%+v %v", rec, err)
	}
	cut := data[:r.Index().Entries[1].Offset+recordSize(r.Index().Entries[1].Length)]
	rec, err = Recover(bytes.NewReader(cut), int64(len(cut)))
	if err != nil || rec.Complete || len(rec.Docs) != 1 || len(rec.Sealed) != 0 || rec.Skipped != 1 || rec.SealedLost != 1 {
		t.Fatalf("cut: %+v %v", rec, err)
	}
	rec, _ = Recover(bytes.NewReader(data), int64(len(data)))
	var re bytes.Buffer
	if err := Repack(rec, &re); err != nil {
		t.Fatal(err)
	}
	rp := open(t, re.Bytes())
	if rep := rp.Verify(); !rep.OK() || rep.Checked != 2 {
		t.Fatalf("%+v", rep)
	}
	if got, err := rp.ReadWith(id2, unseal); err != nil || text(got) != "the secret text" {
		t.Errorf("%v", err)
	}
}

func TestAppendEncrypted(t *testing.T) {
	f := &memFile{b: build(t, nil)}
	a, err := OpenAppend(f, int64(len(f.b)), nil)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := docfile.Open.Marshal(doc(id3, "Hidden", "late secret"))
	if err := a.AddEncrypted(Entry{ID: id3, Name: "Confidential document"}, seal(plain)); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	r := open(t, f.b)
	if rep := r.Verify(); !rep.OK() || rep.Checked != 3 {
		t.Fatalf("%+v", rep)
	}
	if _, err := r.Read(id3); !errors.Is(err, ErrEncrypted) {
		t.Errorf("%v", err)
	}
	if d, err := r.ReadWith(id3, unseal); err != nil || text(d) != "late secret" {
		t.Errorf("%v", err)
	}
	if d, err := r.Read(id1); err != nil || text(d) != "first" {
		t.Errorf("%v", err)
	}
}

// Every bit of the encrypted record's stored bytes and frame is
// covered: flipping any is found by Verify (or Open) without a key.
func TestEncryptedTamperIsDetected(t *testing.T) {
	data := buildMixed(t, ed25519.NewKeyFromSeed([]byte("opendoc encrypted seed 012345678")[:32]))
	e := open(t, data).Index().Entries[1]
	for i := int(e.Offset); i < int(e.Offset+frameHead+e.Length+4); i++ { // padding after the data is not checked
		bad := flip(data, i)
		r, err := Open(bytes.NewReader(bad), int64(len(bad)))
		if err != nil {
			continue
		}
		if rep := r.Verify(); rep.OK() {
			t.Fatalf("a flipped bit at %d (record starts at %d) was not noticed", i, e.Offset)
		}
	}
	// The flag in the frame: a rewritten flag with a matching header check still disagrees
	// with the index, and the other way round.
	reflag := func(b []byte, off uint64, flags byte) []byte {
		out := append([]byte(nil), b...)
		out[off+9] = flags
		binary.LittleEndian.PutUint32(out[off+12:], crc(out[off:off+12]))
		return out
	}
	for _, c := range []struct {
		off   uint64
		flags byte
	}{{e.Offset, 0}, {open(t, data).Index().Entries[0].Offset, flagEncrypted}, {e.Offset, 0x80}} {
		bad := reflag(data, c.off, c.flags)
		r := open(t, bad)
		if rep := r.Verify(); rep.OK() || len(rep.Bad) == 0 {
			t.Errorf("a record flagged %#x at %d passed", c.flags, c.off)
		}
	}
}

func TestPlainPacksCarryNoFlag(t *testing.T) {
	data := build(t, nil)
	for _, e := range open(t, data).Index().Entries {
		if e.Encrypted {
			t.Fatal("flag on a plain entry")
		}
	}
	// A plain index entry marshals without field 10, so old packs are byte-identical.
	if b := (Entry{ID: id1}).marshal(); bytes.Contains(b, []byte{10 << 3}) {
		t.Errorf("% x", b)
	}
}
