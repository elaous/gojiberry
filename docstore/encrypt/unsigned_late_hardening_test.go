package encrypt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

// legacyFile stores, as a file of the document, an unsigned v2 frame (what a client with no
// Signer writes).
func (w *world) legacyFile(t *testing.T, id, name string) string {
	t.Helper()
	f := id + "~" + name + ".open"
	b := w.frameOf(t, w.mainFile(t, id)).b
	fr := frame(f, Binding{Level: b.Level, Seq: b.Seq, Hash: b.Hash}, []byte("legacy"), nil)
	if err := w.inner.Save(context.Background(), f, sealRaw(t, w.publicRecipients(), fr)); err != nil {
		t.Fatal(err)
	}
	return f
}

// A store writer that puts back an older, validly signed record that says Signed in the gap
// between the flip and the mark must not get the owner's marker write to sign it again: that
// would make the replay the truth. The mark is refused, the error says so, the replayed bytes
// stay as they are.
func TestMarkIsRefusedOnAReplayedOlderRecord(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	if _, err := w.sharing.Repair(ctx, id); err != nil {
		t.Fatal(err)
	}
	old := w.rawRecord(t, id) // validly signed, says Signed
	w.rewriteRecord(t, id, func(r *sharedRecord) { r.Pending = true })

	st := &afterRecordWrite{flaky: w.inner, hook: func() {
		w.legacyFile(t, id, "dddddddddddd")
		w.replay(t, id, old)
	}}
	sh := &Sharing{Store: st, Adapter: w.ad, Signer: w.signer}
	_, err := sh.Repair(ctx, id)
	if !errors.Is(err, ErrUnsignedAfterFlip) || !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("Repair = %v, want ErrUnsignedAfterFlip wrapping ErrForgedFrame", err)
	}
	if !strings.Contains(err.Error(), "could not be marked") {
		t.Errorf("message %q does not say the mark was refused", err)
	}
	if got := w.rawRecord(t, id); !bytes.Equal(got, old) {
		t.Error("the marker write signed the replayed record again")
	}
	if docs, _ := w.sharing.UnsignedDocs(ctx); len(docs) != 0 {
		t.Errorf("UnsignedDocs = %+v: the replayed record must not carry a mark", docs)
	}
}

// The record and the error text are bounded however many files are found unsigned.
func TestUnsignedLateIsCapped(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.signedPending(t)
	const total = 200
	st := &afterRecordWrite{flaky: w.inner, hook: func() {
		for i := range total {
			w.legacyFile(t, id, fmt.Sprintf("%012x", i+1))
		}
	}}
	sh := &Sharing{Store: st, Adapter: w.ad, Signer: w.signer}
	_, err := sh.Repair(ctx, id)
	if !errors.Is(err, ErrUnsignedAfterFlip) {
		t.Fatalf("Repair = %v", err)
	}
	if !strings.Contains(err.Error(), "and 136 more") {
		t.Errorf("the error does not give the rest as a count: %.300q", err)
	}
	if len(err.Error()) > 8000 {
		t.Errorf("the error text is %d bytes", len(err.Error()))
	}
	rec, _, lerr := loadRecord(ctx, w.inner, id, w.ring.People.OwnerSigners, nil)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(rec.UnsignedLate) != unsignedLateCap || rec.UnsignedLateCount != total {
		t.Fatalf("record keeps %d ids and count %d, want %d and %d", len(rec.UnsignedLate), rec.UnsignedLateCount, unsignedLateCap, total)
	}
	docs, err := w.sharing.UnsignedDocs(ctx)
	if err != nil || len(docs) != 1 || docs[0].Count != total || len(docs[0].Files) != unsignedLateCap {
		t.Fatalf("UnsignedDocs = %+v, %v", docs, err)
	}
}

// clearUnsignedLate on a document that still has an unsigned file keeps the mark.
func TestClearUnsignedLateKeepsTheMarkWhileAFileIsUnsigned(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, _ := lockedDocument(t, w, "v2")
	if err := w.sharing.clearUnsignedLate(ctx, id); err != nil {
		t.Fatal(err)
	}
	if docs, err := w.sharing.UnsignedDocs(ctx); err != nil || len(docs) != 1 {
		t.Fatalf("the mark was cleared with an unsigned file left: %+v, %v", docs, err)
	}
}

// loadFailsAfterSave makes the reads of one file fail once it was written: the re-key worked,
// the look afterwards cannot read it.
type loadFailsAfterSave struct {
	*flaky
	file  string
	armed bool
}

func (l *loadFailsAfterSave) Save(ctx context.Context, id string, b []byte) error {
	err := l.flaky.Save(ctx, id, b)
	if err == nil && id == l.file {
		l.armed = true
	}
	return err
}

func (l *loadFailsAfterSave) SaveIf(ctx context.Context, id string, b []byte, prev string) error {
	err := l.flaky.SaveIf(ctx, id, b, prev)
	if err == nil && id == l.file {
		l.armed = true
	}
	return err
}

func (l *loadFailsAfterSave) Load(ctx context.Context, id string) (docstore.Document, error) {
	if l.armed && id == l.file {
		return docstore.Document{}, errors.New("disk error reading " + id)
	}
	return l.flaky.Load(ctx, id)
}

// A successful re-key is not failed by the tidy-up of the mark: when the files cannot be
// looked at, the mark stays and the re-key reports success.
func TestClearUnsignedLateFailureDoesNotFailTheRekey(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, f := lockedDocument(t, w, "v2")
	st := &loadFailsAfterSave{flaky: w.inner, file: f}
	sh := w.over(st, nil)
	if _, err := sh.Repair(ctx, id, AcceptUnsigned()); err != nil {
		t.Fatalf("Repair = %v: the re-key worked, only the mark could not be checked", err)
	}
	if docs, _ := w.sharing.UnsignedDocs(ctx); len(docs) != 1 {
		t.Fatalf("the mark must stay when it cannot be checked: %+v", docs)
	}
	st.armed = false
	if err := sh.clearUnsignedLate(ctx, id); err != nil {
		t.Fatal(err)
	}
	if docs, _ := w.sharing.UnsignedDocs(ctx); len(docs) != 0 {
		t.Fatalf("the mark stays after a clean look: %+v", docs)
	}
}

// Verify reports the documents in the unsigned-late state (Saver.UnsignedCheck), apart from the
// pending ones, and the entry goes when the owner settles the document.
func TestVerifyReportsUnsignedLateDocuments(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.saver.PendingCheck = PendingHook(w.sharing)
	w.saver.UnsignedCheck = UnsignedHook(w.sharing)
	id, _ := lockedDocument(t, w, "v2")
	rep, err := w.saver.Verify(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unsigned) != 1 || rep.Unsigned[0] != id || len(rep.Pending) != 0 {
		t.Fatalf("Verify: Unsigned %v, Pending %v, want only %s unsigned", rep.Unsigned, rep.Pending, id)
	}
	if _, err := w.sharing.Repair(ctx, id, AcceptUnsigned()); err != nil {
		t.Fatal(err)
	}
	if rep, err = w.saver.Verify(ctx, "m"); err != nil || len(rep.Unsigned) != 0 {
		t.Fatalf("after Repair: %v, %v", rep.Unsigned, err)
	}
}
