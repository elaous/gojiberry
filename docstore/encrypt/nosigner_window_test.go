package encrypt

import (
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
	strict.Signer = w.signer
	if err := strict.Save(ctx, f, []byte("x")); err != nil {
		t.Fatalf("Save with the Signer: %v", err)
	}
	if v := w.frameVersion(t, f); v != 3 {
		t.Fatalf("frame v%d, want v3", v)
	}
}
