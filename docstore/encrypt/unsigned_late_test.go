package encrypt

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A document whose re-key ended in ErrUnsignedAfterFlip is not pending (the record says Signed)
// but is not done either: UnsignedDocs lists it with the file ids, so "what is left to do" shows
// it, and the listing survives a new Sharing (the marker is in the signed record).
func TestUnsignedAfterFlipDocIsListedAsNeedingAction(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, f := lockedDocument(t, w, "v2")

	// Another client (a fresh Sharing over the same store) sees it too.
	other := &Sharing{Store: w.inner, Adapter: w.ad, Signer: w.signer}
	for name, sh := range map[string]*Sharing{"same": w.sharing, "other": other} {
		docs, err := sh.UnsignedDocs(ctx)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(docs) != 1 || docs[0].Doc != id || len(docs[0].Files) != 1 || docs[0].Files[0] != f {
			t.Fatalf("%s: UnsignedDocs = %+v, want %s with %s", name, docs, id, f)
		}
		if docs[0].Since.IsZero() || !strings.Contains(docs[0].Reason, "AcceptUnsigned") {
			t.Errorf("%s: reason %q, since %v", name, docs[0].Reason, docs[0].Since)
		}
	}
	// PendingDocs stays about re-keys in flight.
	if p, err := w.sharing.PendingDocs(ctx); err != nil || len(p) != 0 {
		t.Errorf("PendingDocs = %v, %v", p, err)
	}

	// Repair with AcceptUnsigned settles it, and the list empties.
	if _, err := w.sharing.Repair(ctx, id, AcceptUnsigned()); err != nil {
		t.Fatal(err)
	}
	if docs, err := other.UnsignedDocs(ctx); err != nil || len(docs) != 0 {
		t.Fatalf("after Repair(AcceptUnsigned): %+v, %v", docs, err)
	}
	if !w.binding(t, f).Signed {
		t.Error("the record lost its Signed claim")
	}
}

// Repair without the option refuses and leaves the marker.
func TestUnsignedMarkerSurvivesARefusedRepair(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, _ := lockedDocument(t, w, "v2")
	if _, err := w.sharing.Repair(ctx, id); !errors.Is(err, ErrForgedFrame) {
		t.Fatalf("Repair = %v", err)
	}
	if docs, _ := w.sharing.UnsignedDocs(ctx); len(docs) != 1 {
		t.Fatalf("UnsignedDocs = %+v", docs)
	}
}

// Recover with AcceptUnsigned also settles it.
func TestUnsignedMarkerClearedByRecoverAcceptUnsigned(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, _ := lockedDocument(t, w, "v2")
	if _, err := w.sharing.Recover(ctx, id, "server", nil, AcceptUnsigned()); err != nil {
		t.Fatal(err)
	}
	if docs, err := w.sharing.UnsignedDocs(ctx); err != nil || len(docs) != 0 {
		t.Fatalf("UnsignedDocs = %+v, %v", docs, err)
	}
}

// A document that was never in the window is not listed, before or after a normal re-key.
func TestDocumentOutsideTheWindowIsNotListed(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	if docs, err := w.sharing.UnsignedDocs(ctx); err != nil || len(docs) != 0 {
		t.Fatalf("before: %+v, %v", docs, err)
	}
	if _, err := w.sharing.Repair(ctx, id); err != nil {
		t.Fatal(err)
	}
	if docs, err := w.sharing.UnsignedDocs(ctx); err != nil || len(docs) != 0 {
		t.Fatalf("after: %+v, %v", docs, err)
	}
}

// RepairAll's report and the list agree: the document it skips as forged, with the same file.
func TestRepairAllAndUnsignedDocsAgree(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	clean := w.history(t)
	if _, err := w.sharing.Repair(ctx, clean); err != nil {
		t.Fatal(err)
	}
	late, f := lockedDocument(t, w, "v2")
	rep, err := w.sharing.RepairAll(ctx, RepairAllOptions{})
	if err != nil {
		t.Fatal(err)
	}
	forged := map[string]bool{}
	for _, sk := range rep.Skipped {
		if sk.Reason == SkipForged {
			forged[sk.Doc] = true
		}
	}
	docs, err := w.sharing.UnsignedDocs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Doc != late || docs[0].Files[0] != f {
		t.Fatalf("UnsignedDocs = %+v", docs)
	}
	if len(forged) != 1 || !forged[late] || forged[clean] {
		t.Fatalf("RepairAll forged skips = %v, want only %s", forged, late)
	}
}
