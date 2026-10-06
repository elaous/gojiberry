package saver

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// The document list marks confidential documents, offers the switch and Reveal, and the session
// forgets the real names when the keys are forgotten.
func TestSessionListMarksConfidentialDocumentsAndOffersTheSwitch(t *testing.T) {
	ctx := context.Background()
	s, mem, id := confidentialSaver(t)
	ss := NewSession(s, func() docfile.Document { return docWithText("", "editor text") }, nil)
	ss.Pending.Inline = true
	if err := ss.SetTarget(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	p := ss.Docs()
	if len(p.Docs) != 1 || !p.Docs[0].Confidential || !p.Docs[0].NameHidden || p.Docs[0].Name != ConfidentialName {
		t.Fatalf("%+v", p.Docs)
	}
	if p.SetProtection == nil || p.SetFolderProtection == nil || p.Reveal == nil || p.RevealFolder == nil {
		t.Fatal("the protection controls are not plugged in")
	}
	// Reveal shows the name, no longer hidden; the lock stays.
	if err := ss.Docs().Reveal(ctx, id); err != nil {
		t.Fatal(err)
	}
	if d := ss.Docs().Docs[0]; d.Name != secretName || !d.Confidential || d.NameHidden {
		t.Fatalf("%+v", d)
	}
	// Forget (the store was locked): the placeholder is back, in the lists too.
	ss.Forget()
	if d := ss.Docs().Docs[0]; d.Name != ConfidentialName || !d.NameHidden || len(d.Tags) != 0 {
		t.Fatalf("%+v", d)
	}
	// Locked: Open says what to do, and the error is still ErrLocked for code that asks.
	mem.locked.Store(true)
	err := ss.Open(ctx, id)
	if !errors.Is(err, docstore.ErrLocked) || !strings.Contains(err.Error(), "Unlock the store") || strings.Contains(err.Error(), "fake:") {
		t.Fatalf("%v", err)
	}
	mem.locked.Store(false)

	// The switch and the folder switch come through the props.
	if err := ss.Docs().SetProtection(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if d := ss.Docs().Docs[0]; d.Confidential || d.Name != secretName {
		t.Fatalf("%+v", d)
	}
	f, err := s.CreateFolder(ctx, "m", "", "Plans")
	if err != nil {
		t.Fatal(err)
	}
	_ = ss.Refresh(ctx)
	if err := ss.Docs().SetFolderProtection(ctx, f.ID, true); err != nil {
		t.Fatal(err)
	}
	if fs := ss.Docs().Folders; len(fs) != 1 || !fs[0].Confidential || fs[0].NameHidden || fs[0].Name != "Plans" {
		t.Fatalf("%+v", fs)
	}
	ss.Forget()
	if fs := ss.Docs().Folders; fs[0].Name != ConfidentialFolderName || !fs[0].NameHidden {
		t.Fatalf("%+v", fs)
	}

	// Not offered where the target cannot keep a document confidential.
	ss.ProtectionAvailable = func(target string) bool { return target != "m" }
	if p := ss.Docs(); p.SetProtection != nil || p.SetFolderProtection != nil {
		t.Error("offered on a store that cannot keep secrets")
	}
}

func TestSessionPackKeysSealAndOpen(t *testing.T) {
	ctx := context.Background()
	s, _, id := confidentialSaver(t)
	ss := NewSession(s, func() docfile.Document { return docfile.Document{} }, nil)
	ss.Pending.Inline = true
	_ = ss.SetTarget(ctx, "m")
	var file []byte
	ss.Deliver = func(_, _ string, data []byte) { file = data }
	if f, err := s.CreateFolder(ctx, "m", "", "Hush"); err == nil {
		_ = s.SetFolderProtection(ctx, "m", f.ID, ProtectionE2EE, false)
	}
	_ = ss.Refresh(ctx)

	// Locked (no keys): the document and the folder name are left out, and it says how many.
	if err := ss.DownloadPack(ctx, ""); err != nil {
		t.Fatal(err)
	}
	st := ss.Props().Status
	if !strings.Contains(st, "1 confidential documents left out") || !strings.Contains(st, "1 confidential folder names left out") {
		t.Errorf("%q", st)
	}
	if strings.Contains(string(file), secretName) {
		t.Error("in the clear")
	}

	// Unlocked: sealed into the pack, and nothing is reported as left out.
	ss.PackKeys = func() (func(context.Context, string, []byte) ([]byte, error), func(context.Context, string, []byte) ([]byte, error)) {
		return fakeSeal, fakeOpen
	}
	if err := ss.DownloadPack(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if st := ss.Props().Status; strings.Contains(st, "left out") || !strings.Contains(st, "pack of 1 documents") {
		t.Errorf("%q", st)
	}
	sealed := slices.Clone(file)
	if strings.Contains(string(sealed), secretName) || strings.Contains(string(sealed), "the secret text") {
		t.Fatal("the pack shows the document")
	}

	// Import into an empty store: locked, the item needs the key and the line says so; with keys it comes in.
	for _, c := range []struct {
		name  string
		keys  bool
		count int
		note  string
	}{{"locked", false, 0, "need a key"}, {"unlocked", true, 1, ""}} {
		dst := &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}
		is := NewSession(dst, func() docfile.Document { return docfile.Document{} }, nil)
		is.Pending.Inline = true
		_ = is.SetTarget(ctx, "m")
		if c.keys {
			is.PackKeys = ss.PackKeys
		}
		if err := is.ImportPackData(ctx, "p.opendoc", sealed); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		st := is.Props().Status
		got := len(is.Docs().Docs)
		if got != c.count || (c.note != "" && !strings.Contains(st, c.note)) || (c.note == "" && strings.Contains(st, "need a key")) {
			t.Errorf("%s: %d documents, %q", c.name, got, st)
		}
		if c.keys {
			if d := is.Docs().Docs[0]; !d.Confidential {
				t.Errorf("not confidential again: %+v", d)
			}
			if n, _, err := dst.Reveal(ctx, "m", id); err != nil || n != secretName {
				t.Errorf("%q %v", n, err)
			}
		}
	}
}
