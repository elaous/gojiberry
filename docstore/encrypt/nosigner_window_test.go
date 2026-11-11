package encrypt

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
)

func TestWindowSaveIsReportedToTheOwnerAfterTheFlip(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, f := lockedDocument(t, w, "v2")
	if _, err := w.ad.Load(ctx, f); !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("Load of the window file: %v, want ErrForgedFrame", err)
	}
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

func TestUnsignedWriterIsToldWhenTheRecordFlipsUnderIt(t *testing.T) {
	for _, mode := range []string{"Save", "SaveIf"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			w := newWorld(t)
			id := w.history(t)
			f := id + "~cccccccccccc.open"
			other := w.otherClient()
			other.afterBind = func() {
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

func (w *world) signedPending(t *testing.T) string {
	t.Helper()
	id := w.history(t)
	if _, err := w.sharing.Repair(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	w.rewriteRecord(t, id, func(r *sharedRecord) { r.Pending = true })
	return id
}

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

func TestSecondLookReportsABadSignatureAsForged(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.signedPending(t)
	var f string
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

func TestSecondLookReportsAnUnsignedFrameAsSavedUnsigned(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.signedPending(t)
	f := id + "~dddddddddddd.open"
	st := &afterRecordWrite{flaky: w.inner, hook: func() {
		b := w.frameOf(t, w.mainFile(t, id)).b
		fr := frame(f, Binding{Level: b.Level, Seq: b.Seq, Hash: b.Hash}, []byte("legacy"), nil)
		if err := w.inner.Save(ctx, f, sealRaw(t, w.publicRecipients(), fr)); err != nil {
			t.Fatal(err)
		}
	}}
	sh := &Sharing{Store: st, Adapter: w.ad, Signer: w.signer}
	_, err := sh.Repair(ctx, id)
	if !errors.Is(err, ErrUnsignedAfterFlip) || !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("Repair = %v, want ErrUnsignedAfterFlip wrapping ErrForgedFrame", err)
	}
	if !strings.Contains(err.Error(), "saved unsigned") || !strings.Contains(err.Error(), f) {
		t.Errorf("message %q", err)
	}
}
