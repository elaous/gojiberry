package encrypt

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

// Backlog item 4: what is left of the window in which a client with no Signer can write an
// unsigned frame. Every unsigned file that lands in it is reported to the owner or to its own
// writer; with RequireSigned a client that cannot sign does not write one at all.

// A save in the steps between the owner's last look and the write of the record: the owner's
// re-key reports it (it does not stay silent), the document is refused as forged until the
// owner accepts the file, and AcceptUnsigned is the way out.
func TestWindowSaveIsReportedToTheOwnerAfterTheFlip(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, f := lockedDocument(t, w, "v2")
	if _, err := w.ad.Load(ctx, f); !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("Load of the window file: %v, want ErrForgedFrame", err)
	}
	// An unsigned writer that saves after the record says Signed is refused at its own Save, and
	// a raw unsigned frame put in the store is forged for every reader.
	if err := w.otherClient().Save(ctx, f, []byte("late")); !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("unsigned Save under a signed record: %v, want ErrForgedFrame", err)
	}
	if _, err := w.sharing.Repair(ctx, id, AcceptUnsigned()); err != nil {
		t.Fatalf("Repair(AcceptUnsigned): %v", err)
	}
	if d, err := w.ad.Load(ctx, f); err != nil || string(d.Data) != "latest" {
		t.Fatalf("after the way out: %v", err)
	}
}

// The writer is told too: the owner flips the record between the writer's read of it and its
// write, so the file it wrote is unsigned under a Signed record. Save and SaveIf say so.
func TestUnsignedWriterIsToldWhenTheRecordFlipsUnderIt(t *testing.T) {
	for _, mode := range []string{"Save", "SaveIf"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			w := newWorld(t)
			id := w.history(t)
			f := id + "~cccccccccccc.open"
			other := w.otherClient()
			other.afterBind = func() { // the record was read as not signed; now the owner flips it
				other.afterBind = nil
				if _, err := w.sharing.Repair(ctx, id); err != nil {
					t.Error(err)
				}
			}
			var err error
			if mode == "Save" {
				err = other.Save(ctx, f, []byte("late"))
			} else {
				err = other.SaveIf(ctx, f, []byte("late"), "")
			}
			if !errors.Is(err, ErrNoFrameSigner) || !strings.Contains(err.Error(), "became signed") {
				t.Fatalf("%s = %v, want ErrNoFrameSigner saying the document became signed", mode, err)
			}
			if !w.binding(t, f).Signed {
				t.Fatal("setup: the record says Signed")
			}
			if _, err := w.ad.Load(ctx, f); !errors.Is(err, ErrForgedFrame) {
				t.Fatalf("Load: %v, want ErrForgedFrame", err)
			}
		})
	}
}

// An unsigned save before the record says Signed is the normal case and is not an error.
func TestUnsignedSaveBeforeSignedIsNotAnError(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	f := id + "~cccccccccccc.open"
	if err := w.otherClient().Save(ctx, f, []byte("x")); err != nil {
		t.Fatalf("unsigned Save of a server-level document: %v", err)
	}
	if v := w.frameVersion(t, f); v != 2 {
		t.Fatalf("frame v%d, want the unsigned v2", v)
	}
	if _, err := w.sharing.Repair(ctx, id); err != nil {
		t.Fatalf("legitimate migration: %v", err)
	}
	if !w.binding(t, f).Signed || w.frameVersion(t, f) != 3 {
		t.Fatal("the migration did not leave a signed document")
	}
}

// With RequireSigned an Adapter that cannot sign refuses to write an unsigned frame (it could
// not read it back, and it would be the one that enters the window); nothing is written. One
// that signs, and one without RequireSigned, are unchanged.
func TestRequireSignedAdapterWithoutSignerDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	f := id + "~cccccccccccc.open"
	strict := New(w.inner, w.ring)
	strict.RequireSigned = true
	if err := strict.Save(ctx, f, []byte("x")); !errors.Is(err, ErrNoFrameSigner) {
		t.Fatalf("Save: %v, want ErrNoFrameSigner", err)
	}
	if err := strict.SaveIf(ctx, f, []byte("x"), ""); !errors.Is(err, ErrNoFrameSigner) {
		t.Fatalf("SaveIf: %v, want ErrNoFrameSigner", err)
	}
	if _, err := w.inner.Load(ctx, f); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("a refused Save wrote a file: %v", err)
	}
	// Ids that are not bound to a document's record are still saved: the refusal is for the
	// documents' files only (a plain item, a settings item, a key record).
	for _, other := range []string{"notes/plain", "settings", KeysPrefix + "xyz"} {
		if err := strict.Save(ctx, other, []byte("y")); err != nil {
			t.Errorf("Save %s: %v", other, err)
		}
		if _, err := w.inner.Load(ctx, other); err != nil {
			t.Errorf("%s was not written: %v", other, err)
		}
	}
	strict.Signer = w.signer
	if err := strict.Save(ctx, f, []byte("x")); err != nil {
		t.Fatalf("Save with the Signer: %v", err)
	}
	if v := w.frameVersion(t, f); v != 3 {
		t.Fatalf("frame v%d, want v3", v)
	}
}

// evilV3 stores, as a file of the document, a v3 frame signed by a key that is not an owner's.
func (w *world) evilV3(t *testing.T, id, name string) string {
	t.Helper()
	f := id + "~" + name + ".open"
	evil, err := NewSigner(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	b := w.frameOf(t, w.mainFile(t, id)).b
	if err := w.inner.Save(context.Background(), f, sealRaw(t, w.publicRecipients(), frame(f, Binding{Level: b.Level, Seq: b.Seq, Hash: b.Hash}, []byte("evil"), evil))); err != nil {
		t.Fatal(err)
	}
	return f
}

// signedPending leaves a document whose record says Signed and Pending: the re-key that
// finishes it has no unsigned file to look for before the flip, and flips nothing it was not.
func (w *world) signedPending(t *testing.T) string {
	t.Helper()
	id := w.history(t)
	if _, err := w.sharing.Repair(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	w.rewriteRecord(t, id, func(r *sharedRecord) { r.Pending = true })
	return id
}

// afterRecordWrite runs hook once, right after a conditional write of a keys: record succeeds.
type afterRecordWrite struct {
	*flaky
	hook func()
}

func (a *afterRecordWrite) SaveIf(ctx context.Context, id string, b []byte, prev string) error {
	err := a.flaky.SaveIf(ctx, id, b, prev)
	if err == nil && strings.HasPrefix(id, KeysPrefix) && a.hook != nil {
		h := a.hook
		a.hook = nil
		h()
	}
	return err
}

// A v3 file with a bad signature that the second look finds is not "saved unsigned": it is
// reported as a forgery, with its own message, and still wraps ErrForgedFrame.
func TestSecondLookReportsABadSignatureAsForged(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.signedPending(t)
	var f string
	// The file lands after the record was written (the check before the write would refuse it).
	st := &afterRecordWrite{flaky: w.inner, hook: func() { f = w.evilV3(t, id, "dddddddddddd") }}
	sh := &Sharing{Store: st, Adapter: w.ad, Signer: w.signer}
	_, err := sh.Repair(ctx, id)
	if !errors.Is(err, ErrForgedFrame) || errors.Is(err, ErrUnsignedAfterFlip) {
		t.Fatalf("Repair = %v, want ErrForgedFrame and not ErrUnsignedAfterFlip", err)
	}
	if strings.Contains(err.Error(), "saved unsigned") || !strings.Contains(err.Error(), "bad signature") || !strings.Contains(err.Error(), f) {
		t.Errorf("message %q", err)
	}
}

// A file saved unsigned in the window is named once in the message, not as "<doc>: <doc>~file".
func TestSecondLookMessageNamesTheDocumentOnce(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.signedPending(t)
	ad := New(w.inner, w.ring)
	ad.Signer, ad.AllowPlaintext = w.signer, true
	sh := &Sharing{Store: w.inner, Adapter: ad, Signer: w.signer}
	f := id + "~dddddddddddd.open"
	sh.beforeFlip = func() {
		sh.beforeFlip = nil
		if err := w.inner.Save(ctx, f, []byte("in the clear")); err != nil {
			t.Fatal(err)
		}
	}
	_, err := sh.Repair(ctx, id)
	if !errors.Is(err, ErrUnsignedAfterFlip) || !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("Repair = %v, want ErrUnsignedAfterFlip wrapping ErrForgedFrame", err)
	}
	if n := strings.Count(err.Error(), id); n != 1 || !strings.Contains(err.Error(), f) {
		t.Errorf("the document id is in the message %d times: %q", n, err)
	}
}

// A Sharing with a Signer over an Adapter without one cannot sign, so it does not flip the record
// and takes no second look: a bad file that appears in the window is not its business, and
// nothing dereferences the missing Signer.
func TestSharingSignerWithAdapterWithoutOneDoesNotFlip(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	sh := &Sharing{Store: w.inner, Adapter: New(w.inner, w.ring), Signer: w.signer}
	sh.beforeFlip = func() { sh.beforeFlip = nil; w.evilV3(t, id, "dddddddddddd") }
	if _, err := sh.Repair(ctx, id); err != nil {
		t.Fatalf("Repair = %v, want no second look", err)
	}
	if w.binding(t, w.mainFile(t, id)).Signed {
		t.Error("the record was marked Signed by a Sharing whose Adapter cannot sign")
	}
}
