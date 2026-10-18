package pack

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

func buildV2(t *testing.T, key []byte) ([]byte, [32]byte, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w, err := NewWriterV2(&buf, Meta{Name: "att"})
	if err != nil {
		t.Fatal(err)
	}
	pic := bytes.Repeat([]byte("picture"), 5000)
	sum, err := w.AddAttachment("image/png", pic)
	if err != nil {
		t.Fatal(err)
	}
	if sum != sha256.Sum256(pic) {
		t.Fatal("the hash returned is not the SHA-256 of the bytes")
	}
	if again, err := w.AddAttachment("image/png", pic); err != nil || again != sum {
		t.Fatalf("the same bytes twice: %v", err)
	}
	if err := w.Add(doc(id1, "one", "x")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), sum, pic
}

func TestAttachmentsAreStoredOnceAndRead(t *testing.T) {
	data, sum, pic := buildV2(t, nil)
	r := open(t, data)
	if r.Version() != 2 || len(r.Index().Attachments) != 1 {
		t.Fatalf("version %d, %d attachments", r.Version(), len(r.Index().Attachments))
	}
	got, mime, err := r.Attachment(sum)
	if err != nil || !bytes.Equal(got, pic) || mime != "image/png" {
		t.Fatalf("read back: %v %q", err, mime)
	}
	if _, _, err := r.Attachment([32]byte{1}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown hash: %v", err)
	}
	if rep := r.Verify(); !rep.OK() || rep.Unindexed != 0 || rep.Gap {
		t.Errorf("report %+v", rep)
	}
}

func TestAPackWithoutAttachmentsStaysVersionOne(t *testing.T) {
	if r := open(t, build(t, nil)); r.Version() != 1 {
		t.Errorf("version %d", r.Version())
	}
	var buf bytes.Buffer
	w, _ := NewWriter(&buf, Meta{})
	if _, err := w.AddAttachment("image/png", []byte("x")); !errors.Is(err, ErrNoAttachments) {
		t.Errorf("version 1 took an attachment: %v", err)
	}
}

func TestAttachmentsAreSignedAndChecked(t *testing.T) {
	key := newKey(t)
	var buf bytes.Buffer
	w, _ := NewWriterV2(&buf, Meta{})
	pic := bytes.Repeat([]byte{7}, 4096)
	if _, err := w.AddAttachment("image/webp", pic); err != nil {
		t.Fatal(err)
	}
	if err := w.CloseSigned(key); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	if rep := open(t, data).Verify(); !rep.OK() || !rep.Signed {
		t.Fatalf("report %+v", rep)
	}
	// Every bit of the attachment's record, flipped, is found.
	a := open(t, data).Index().Attachments[0]
	for i := int(a.Offset); i < int(a.Offset+recordSize(a.Length)); i += 97 {
		if rep := open(t, flip(data, i)).Verify(); rep.OK() {
			t.Fatalf("a flipped byte at %d passed", i)
		}
	}
}

func TestAppendAttachmentsInPlace(t *testing.T) {
	data, sum, _ := buildV2(t, nil)
	f := &memFile{b: data}
	ap, err := OpenAppend(f, int64(len(f.b)), nil)
	if err != nil {
		t.Fatal(err)
	}
	pic2 := bytes.Repeat([]byte("two"), 3000)
	sum2, err := ap.AddAttachment("image/png", pic2)
	if err != nil {
		t.Fatal(err)
	}
	if err := ap.Add(doc(id2, "two", "y")); err != nil {
		t.Fatal(err)
	}
	if err := ap.Close(); err != nil {
		t.Fatal(err)
	}
	r := open(t, f.b)
	if len(r.Index().Attachments) != 2 || !r.Verify().OK() {
		t.Fatalf("%+v", r.Index().Attachments)
	}
	for _, s := range [][32]byte{sum, sum2} {
		if _, _, err := r.Attachment(s); err != nil {
			t.Error(err)
		}
	}
	// A version 1 pack takes none by appending.
	g := &memFile{b: build(t, nil)}
	ap1, err := OpenAppend(g, int64(len(g.b)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ap1.AddAttachment("image/png", pic2); !errors.Is(err, ErrNoAttachments) {
		t.Errorf("%v", err)
	}
}

func TestCompactAndRecoverKeepAttachments(t *testing.T) {
	data, sum, pic := buildV2(t, nil)
	var out bytes.Buffer
	if err := Compact(bytes.NewReader(data), int64(len(data)), &out, nil); err != nil {
		t.Fatal(err)
	}
	r := open(t, out.Bytes())
	if got, _, err := r.Attachment(sum); err != nil || !bytes.Equal(got, pic) || r.Version() != 2 {
		t.Fatalf("compacted: %v", err)
	}
	// Cut before the index: the attachment is found by walking.
	cut := int(open(t, data).foot.indexOffset)
	rec, err := Recover(bytes.NewReader(data[:cut]), int64(cut))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Attachments) != 1 || !bytes.Equal(rec.Attachments[0].Data, pic) {
		t.Fatalf("recovered %d attachments", len(rec.Attachments))
	}
	var re bytes.Buffer
	if err := Repack(rec, &re); err != nil {
		t.Fatal(err)
	}
	if rr := open(t, re.Bytes()); rr.Version() != 2 || len(rr.Index().Attachments) != 1 {
		t.Fatal("the repacked pack lost its attachment")
	}
}

func TestAttachmentsInAVersionOnePackAreRefused(t *testing.T) {
	data, _, _ := buildV2(t, nil)
	data[8] = 1 // the header says version 1 but the index lists attachments
	if _, err := Open(bytes.NewReader(data), int64(len(data))); err == nil {
		t.Error("accepted")
	}
}
