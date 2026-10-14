package saver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docfile"
)

// A copy saved elsewhere since this one was opened is not replaced without a trace: it is kept as a named
// version, and the comparison with what is saved now is ready to show.
func TestSaveKeepsACopySavedElsewhere(t *testing.T) {
	ctx := context.Background()
	sv := &Saver{Targets: targets(t)}
	mine := "my text"
	a := NewSession(sv, func() docfile.Document { return docWithText("plan", mine) }, func(docfile.Document) {})
	a.Pending.Inline = true
	a.SetName("plan")
	if err := a.Save(ctx); err != nil {
		t.Fatal(err)
	}
	id := a.ID()

	// Someone else (another tab, another person) opens it and saves a change.
	theirs := "their text"
	b := NewSession(sv, func() docfile.Document {
		return docWithText("plan", theirs)
	}, func(docfile.Document) {})
	b.Pending.Inline = true
	if err := b.Open(ctx, id); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := b.Save(ctx); err != nil {
		t.Fatal(err)
	}

	// Ana saves her version of the document: nothing of theirs is lost.
	time.Sleep(5 * time.Millisecond)
	mine = "my text, edited"
	if err := a.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.status, "replaced a newer copy") {
		t.Errorf("the status does not say it: %q", a.status)
	}
	vs, err := sv.Versions(ctx, a.targetName(), id)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range vs {
		if v.Name == elsewhereVersion {
			found = true
			d, err := sv.LoadVersion(ctx, a.targetName(), v.Key)
			if err != nil || textOf(d) != "their text" {
				t.Errorf("the kept version holds %q (%v)", textOf(d), err)
			}
			if a.diffKey != v.Key || len(a.diff) == 0 {
				t.Errorf("the comparison is not ready: key %q, %d changes", a.diffKey, len(a.diff))
			}
		}
	}
	if !found {
		t.Fatalf("no version %q among %+v", elsewhereVersion, vs)
	}
	// Saving again, with nothing new elsewhere, is a plain save.
	if err := a.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.status, "replaced a newer copy") {
		t.Errorf("a plain save said it replaced something: %q", a.status)
	}
}
