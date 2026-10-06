package saver

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/components"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
	"github.com/elaous/gojiberry/docstore"
)

const secretFolder = "Layoffs 2026 xango"

// storeHolds says whether any stored item (catalog, folder records, files) contains s.
func storeHolds(t *testing.T, a docstore.Adapter, s string) []string {
	t.Helper()
	metas, err := a.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range metas {
		d, err := a.Load(context.Background(), m.ID)
		if err == nil && bytes.Contains(d.Data, []byte(s)) {
			out = append(out, m.ID)
		}
	}
	return out
}

// folderWorld has a top folder holding a subfolder (named secret) with a document in it.
func folderWorld(t *testing.T) (s *Saver, mem *lockable, top, sec, doc string) {
	t.Helper()
	ctx := context.Background()
	mem = &lockable{Adapter: docstore.NewMemoryAdapter()}
	s = &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	f1, err := s.CreateFolder(ctx, "m", "", "Work")
	if err != nil {
		t.Fatal(err)
	}
	f2, err := s.CreateFolder(ctx, "m", f1.ID, secretFolder)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.Save(ctx, "m", docfile.Open, docWithText("Plain doc", "text"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Move(ctx, "m", f2.ID, e.ID); err != nil {
		t.Fatal(err)
	}
	return s, mem, f1.ID, f2.ID, e.ID
}

func TestConfidentialFolderHidesItsNameKeepsTheShape(t *testing.T) {
	ctx := context.Background()
	s, mem, top, sec, doc := folderWorld(t)
	if err := s.SetFolderProtection(ctx, "m", sec, "bogus", false); !errors.Is(err, ErrProtection) {
		t.Fatalf("%v", err)
	}
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionE2EE, false); err != nil {
		t.Fatal(err)
	}
	if got := storeHolds(t, mem, secretFolder); len(got) != 1 || got[0] != FolderSecretKey(sec) {
		t.Fatalf("the real name is only in its record: %v", got)
	}
	fs, _ := s.Folders(ctx, "m")
	var names []string
	for _, f := range fs {
		names = append(names, f.Name)
		if f.ID == sec && (f.Protection != ProtectionE2EE || f.Parent != top) {
			t.Errorf("shape and level: %+v", f)
		}
	}
	if !slices.Contains(names, ConfidentialFolderName) || slices.Contains(names, secretFolder) {
		t.Errorf("%v", names)
	}
	if n, err := s.RevealFolder(ctx, "m", sec); err != nil || n != secretFolder {
		t.Errorf("%q %v", n, err)
	}
	if n, _ := s.RevealFolder(ctx, "m", top); n != "Work" {
		t.Errorf("%q", n)
	}
	// Documents inside are NOT made confidential by it.
	if e, _ := s.Get(ctx, "m", doc); e.Protection != ProtectionServer || e.Name != "Plain doc" || e.Folder != sec {
		t.Errorf("%+v", e)
	}
	// Locked: no key opens the record, the placeholder stays.
	mem.locked.Store(true)
	if _, err := s.RevealFolder(ctx, "m", sec); !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("%v", err)
	}
	if n, err := s.RevealFolder(ctx, "m", top); err != nil || n != "Work" {
		t.Errorf("a server folder needs no key: %q %v", n, err)
	}
	mem.locked.Store(false)

	// Rename writes into the record; the catalog does not change.
	before := catalogText(t, s)
	if err := s.RenameFolder(ctx, "m", sec, "Hiring xango2"); err != nil {
		t.Fatal(err)
	}
	if catalogText(t, s) != before {
		t.Error("renaming a confidential folder changed the catalog")
	}
	if n, _ := s.RevealFolder(ctx, "m", sec); n != "Hiring xango2" {
		t.Errorf("%q", n)
	}
	if got := storeHolds(t, mem, "Hiring xango2"); len(got) != 1 || got[0] != FolderSecretKey(sec) {
		t.Errorf("%v", got)
	}
	if got := storeHolds(t, mem, secretFolder); len(got) != 0 {
		t.Errorf("the old name is still stored: %v", got)
	}
	// Move: the shape changes, the name stays hidden.
	other, _ := s.CreateFolder(ctx, "m", "", "Other")
	if err := s.MoveFolder(ctx, "m", sec, other.ID); err != nil {
		t.Fatal(err)
	}
	fs, _ = s.Folders(ctx, "m")
	path := FolderPath(fs, sec)
	if len(path) != 2 || path[0].Name != "Other" || path[1].Name != ConfidentialFolderName {
		t.Errorf("breadcrumb: %+v", path)
	}
	if got := storeHolds(t, mem, "Hiring xango2"); len(got) != 1 {
		t.Errorf("%v", got)
	}
	// Back to server: the name comes out, the record goes.
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionServer, false); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.RevealFolder(ctx, "m", sec); n != "Hiring xango2" {
		t.Errorf("%q", n)
	}
	if _, err := mem.Adapter.Load(ctx, FolderSecretKey(sec)); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the record should be gone: %v", err)
	}
	fs, _ = s.Folders(ctx, "m")
	if i := slices.IndexFunc(fs, func(f Folder) bool { return f.ID == sec }); fs[i].Name != "Hiring xango2" || fs[i].Protection != ProtectionServer {
		t.Errorf("%+v", fs[i])
	}
}

func TestConfidentialFolderCheckHookAndOrder(t *testing.T) {
	ctx := context.Background()
	s, mem, _, sec, _ := folderWorld(t)
	var calls []string
	s.ConfidentialCheck = func(_ context.Context, _, id string) error {
		calls = append(calls, "check")
		return errors.New("needs a recovery key")
	}
	s.Rekey = func(_ context.Context, _, id, to string) error {
		_, err := mem.Adapter.Load(ctx, FolderSecretKey(id))
		calls = append(calls, "rekey-"+to+"-secret:"+map[bool]string{true: "absent", false: "present"}[errors.Is(err, docstore.ErrNotFound)])
		return nil
	}
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionE2EE, false); err == nil || !strings.Contains(err.Error(), "recovery key") {
		t.Fatalf("%v", err)
	}
	if f, _ := s.folder(ctx, "m", sec); f.Protection != "" || f.Name != secretFolder || len(calls) != 1 {
		t.Fatalf("a refusal changes nothing: %+v %v", f, calls)
	}
	calls = nil
	s.ConfidentialCheck = nil
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionE2EE, false); err != nil {
		t.Fatal(err)
	}
	// To e2ee the re-key runs before the name is written into the record.
	if !slices.Equal(calls, []string{"rekey-e2ee-secret:absent"}) {
		t.Errorf("%v", calls)
	}
	calls = nil
	// A rename asserts the keys again before it writes.
	if err := s.RenameFolder(ctx, "m", sec, "New xango3"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"rekey-e2ee-secret:present"}) {
		t.Errorf("%v", calls)
	}
	calls = nil
	// The same level again runs the hook (retry), changes nothing.
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionE2EE, false); err != nil || len(calls) != 1 {
		t.Errorf("%v %v", err, calls)
	}
	// To server the name moves out first, then the hook runs.
	calls = nil
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionServer, false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"rekey-server-secret:absent"}) {
		t.Errorf("%v", calls)
	}
	// A failing hook to e2ee leaves the folder as it was, the name unmoved.
	s.Rekey = func(context.Context, string, string, string) error { return errors.New("disk") }
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionE2EE, false); err == nil {
		t.Fatal("expected the failure")
	}
	if f, _ := s.folder(ctx, "m", sec); f.Protection != "" || f.Name != "New xango3" {
		t.Errorf("%+v", f)
	}
	if got := storeHolds(t, mem, FolderSecretKey(sec)); len(got) != 0 {
		t.Error(got)
	}
}

func TestSetFolderProtectionRecursive(t *testing.T) {
	ctx := context.Background()
	s, _, top, sec, doc := folderWorld(t)
	sub, _ := s.CreateFolder(ctx, "m", sec, "Deeper")
	e2, _ := s.Save(ctx, "m", docfile.Open, docWithText("Deep doc", "x"))
	if err := s.Move(ctx, "m", sub.ID, e2.ID); err != nil {
		t.Fatal(err)
	}
	outside, _ := s.Save(ctx, "m", docfile.Open, docWithText("Outside", "y"))
	// Not recursive: documents keep their level.
	if err := s.SetFolderProtection(ctx, "m", top, ProtectionE2EE, false); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Get(ctx, "m", doc); e.Protection != ProtectionServer {
		t.Error("a document in a confidential folder is not made confidential by it")
	}
	if f, _ := s.folder(ctx, "m", sec); f.Protection != "" {
		t.Error("nor a subfolder")
	}
	if err := s.SetFolderProtection(ctx, "m", top, ProtectionE2EE, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{doc, e2.ID} {
		if e, _ := s.Get(ctx, "m", id); e.Protection != ProtectionE2EE || e.Name != ConfidentialName {
			t.Errorf("%+v", e)
		}
	}
	for _, id := range []string{sec, sub.ID} {
		if f, _ := s.folder(ctx, "m", id); f.Protection != ProtectionE2EE || f.Name != ConfidentialFolderName {
			t.Errorf("%+v", f)
		}
	}
	if e, _ := s.Get(ctx, "m", outside.ID); e.Protection != ProtectionServer {
		t.Error("a document outside is not touched")
	}
	if n, _ := s.RevealFolder(ctx, "m", sec); n != secretFolder {
		t.Errorf("%q", n)
	}
	if err := s.SetFolderProtection(ctx, "m", top, ProtectionServer, true); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Get(ctx, "m", doc); e.Protection != ProtectionServer || e.Name != "Plain doc" {
		t.Errorf("%+v", e)
	}
	if f, _ := s.folder(ctx, "m", sub.ID); f.Protection != "" || f.Name != "Deeper" {
		t.Errorf("%+v", f)
	}
}

func TestRebuildKeepsAConfidentialFolderLockedOrNot(t *testing.T) {
	ctx := context.Background()
	s, mem, top, sec, doc := folderWorld(t)
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionE2EE, false); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		fs, _ := s.Folders(ctx, "m")
		i := slices.IndexFunc(fs, func(f Folder) bool { return f.ID == sec })
		if i < 0 || fs[i].Name != ConfidentialFolderName || fs[i].Protection != ProtectionE2EE || fs[i].Parent != top {
			t.Fatalf("%s: %+v", when, fs)
		}
		if e, err := s.Get(ctx, "m", doc); err != nil || e.Folder != sec {
			t.Fatalf("%s: %+v %v", when, e, err)
		}
	}
	// A stranger (no key) rebuilds from the files: the folder is where it was, by its placeholder.
	mem.locked.Store(true)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	check("rebuild without a key")
	if _, err := s.Verify(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	check("verify without a key")
	mem.locked.Store(false)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	check("rebuild with the key")
	if n, _ := s.RevealFolder(ctx, "m", sec); n != secretFolder {
		t.Errorf("%q", n)
	}
}

func TestDeleteConfidentialFolderRemovesItsRecord(t *testing.T) {
	ctx := context.Background()
	s, mem, _, sec, doc := folderWorld(t)
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionE2EE, false); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFolder(ctx, "m", sec); err == nil {
		t.Fatal("a folder with a document cannot be deleted")
	}
	if err := s.Trash(ctx, "m", doc); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteFolder(ctx, "m", sec); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.Adapter.Load(ctx, FolderSecretKey(sec)); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the record should be gone: %v", err)
	}
	if got := storeHolds(t, mem, secretFolder); len(got) != 0 {
		t.Error(got)
	}
	if e, _ := s.Get(ctx, "m", doc); e.Folder != "" {
		t.Errorf("%+v", e)
	}
	if _, err := s.RevealFolder(ctx, "m", sec); !errors.Is(err, ErrFolder) {
		t.Errorf("%v", err)
	}
}

func TestPackSealsConfidentialFolderNames(t *testing.T) {
	ctx := context.Background()
	s, _, _, sec, doc := folderWorld(t)
	if err := s.SetFolderProtection(ctx, "m", sec, ProtectionE2EE, false); err != nil {
		t.Fatal(err)
	}
	// Without Seal: the folder goes with the placeholder name, and the export says so.
	var plain bytes.Buffer
	var warned []PackIssue
	if _, err := s.ExportPack(ctx, "m", "", &plain, PackOptions{Warn: func(i PackIssue) { warned = append(warned, i) }}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plain.Bytes(), []byte(secretFolder)) || len(warned) != 1 || warned[0].ID != sec || warned[0].Name != ConfidentialFolderName {
		t.Fatalf("%v", warned)
	}
	dst := newDst()
	rep, err := dst.ImportPack(ctx, "dst", bytes.NewReader(plain.Bytes()), int64(plain.Len()), ImportOptions{})
	if err != nil || rep.Folders != 2 || rep.Documents != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	fs, _ := dst.Folders(ctx, "dst")
	if i := slices.IndexFunc(fs, func(f Folder) bool { return f.ID == sec }); fs[i].Name != ConfidentialFolderName || fs[i].Protection != ProtectionServer {
		t.Errorf("an unsealed pack makes a plain folder with the placeholder name: %+v", fs[i])
	}

	// With Seal the name travels sealed and comes back confidential with Open.
	var sealed bytes.Buffer
	if _, err := s.ExportPack(ctx, "m", "", &sealed, PackOptions{Seal: fakeSeal}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed.Bytes(), []byte(secretFolder)) {
		t.Fatal("the pack holds the folder name in the clear")
	}
	r, err := pack.Open(bytes.NewReader(sealed.Bytes()), int64(sealed.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if rp := r.Verify(); !rp.OK() {
		t.Fatalf("%+v", rp)
	}
	for _, f := range r.Index().Folders {
		if f.ID == sec && f.Name != ConfidentialFolderName {
			t.Errorf("%+v", f)
		}
	}
	// No Open: left under the placeholder with a needs-key warning, the document still comes.
	d2 := newDst()
	rep, err = d2.ImportPack(ctx, "dst", bytes.NewReader(sealed.Bytes()), int64(sealed.Len()), ImportOptions{})
	if err != nil || rep.Folders != 2 || rep.Documents != 1 || len(rep.NeedsKey) != 1 || rep.NeedsKey[0].ID != sec {
		t.Fatalf("%+v %v", rep, err)
	}
	if e, err := d2.Get(ctx, "dst", doc); err != nil || e.Folder != sec {
		t.Errorf("%+v %v", e, err)
	}
	// With Open: a confidential folder again, under its real name.
	d3 := newDst()
	rep, err = d3.ImportPack(ctx, "dst", bytes.NewReader(sealed.Bytes()), int64(sealed.Len()), ImportOptions{Open: fakeOpen})
	if err != nil || rep.Folders != 2 || len(rep.NeedsKey) != 0 || len(rep.Failed) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	fs, _ = d3.Folders(ctx, "dst")
	if i := slices.IndexFunc(fs, func(f Folder) bool { return f.ID == sec }); fs[i].Name != ConfidentialFolderName || fs[i].Protection != ProtectionE2EE {
		t.Errorf("%+v", fs[i])
	}
	if n, err := d3.RevealFolder(ctx, "dst", sec); err != nil || n != secretFolder {
		t.Errorf("%q %v", n, err)
	}
	if got := storeHolds(t, d3.Targets[0].Adapter, secretFolder); len(got) != 1 || got[0] != FolderSecretKey(sec) {
		t.Errorf("%v", got)
	}
	// A refusing ConfidentialCheck makes that folder fail, its documents go to the parent.
	d4 := newDst()
	d4.ConfidentialCheck = func(context.Context, string, string) error { return errors.New("no recovery key") }
	rep, _ = d4.ImportPack(ctx, "dst", bytes.NewReader(sealed.Bytes()), int64(sealed.Len()), ImportOptions{Open: fakeOpen})
	if len(rep.Failed) != 1 || rep.Folders != 1 {
		t.Errorf("%+v", rep)
	}
	// A failing Seal stops the export.
	boom := errors.New("no recipients")
	if _, err := s.ExportPack(ctx, "m", sec, &bytes.Buffer{}, PackOptions{Seal: func(context.Context, string, []byte) ([]byte, error) { return nil, boom }}); !errors.Is(err, boom) {
		t.Errorf("%v", err)
	}
}

func TestSessionShowsFolderPlaceholdersUntilRevealed(t *testing.T) {
	ctx := context.Background()
	s, mem, top, sec, _ := folderWorld(t)
	ss := NewSession(s, func() docfile.Document { return docfile.Document{} }, nil)
	ss.Pending.Inline = true
	if err := ss.SetTarget(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	if err := ss.SetFolderProtection(ctx, sec, ProtectionE2EE, false); err != nil {
		t.Fatal(err)
	}
	// Just made confidential here: the person knows the name, so it is shown.
	if n := ss.Docs().AllFolders; n[slices.IndexFunc(n, func(f components.DocFolder) bool { return f.ID == sec })].Name != secretFolder {
		t.Fatalf("%+v", n)
	}
	// A new session (a fresh load) shows the placeholder everywhere: list, move menu, breadcrumb.
	ss2 := NewSession(s, func() docfile.Document { return docfile.Document{} }, nil)
	ss2.Pending.Inline = true
	if err := ss2.SetTarget(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	if err := ss2.OpenFolder(ctx, sec); err != nil {
		t.Fatal(err)
	}
	p := ss2.Docs()
	if len(p.Path) != 2 || p.Path[1].Name != ConfidentialFolderName || !p.Path[1].Confidential {
		t.Errorf("breadcrumb %+v", p.Path)
	}
	for _, f := range p.AllFolders {
		if f.Name == secretFolder {
			t.Error("the move menu shows the real name")
		}
	}
	if err := ss2.OpenFolder(ctx, top); err != nil {
		t.Fatal(err)
	}
	if f := ss2.Docs().Folders; len(f) != 1 || f[0].Name != ConfidentialFolderName {
		t.Errorf("list %+v", f)
	}
	if ss2.folderName(sec) != ConfidentialFolderName {
		t.Error("the pack's name would be the real one")
	}
	// Reveal needs a key.
	mem.locked.Store(true)
	if err := ss2.RevealFolder(ctx, sec); !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("%v", err)
	}
	mem.locked.Store(false)
	if err := ss2.RevealFolder(ctx, sec); err != nil {
		t.Fatal(err)
	}
	if f := ss2.Docs().Folders; f[0].Name != secretFolder || !f[0].Confidential {
		t.Errorf("%+v", f)
	}
	if err := ss2.RenameFolder(ctx, sec, "Renamed xango4"); err != nil {
		t.Fatal(err)
	}
	if f := ss2.Docs().Folders; f[0].Name != "Renamed xango4" {
		t.Errorf("%+v", f)
	}
	if got := storeHolds(t, mem, "Renamed xango4"); len(got) != 1 {
		t.Errorf("%v", got)
	}
	if err := ss2.SetFolderProtection(ctx, sec, ProtectionServer, false); err != nil {
		t.Fatal(err)
	}
	if f := ss2.Docs().Folders; f[0].Name != "Renamed xango4" || f[0].Confidential || len(ss2.revealedFolders) != 0 {
		t.Errorf("%+v %v", f, ss2.revealedFolders)
	}
}

// lockedRecords is a store whose folder records are encrypted and locked (an adapter whose
// Skip does not leave them readable).
type lockedRecords struct {
	docstore.Adapter
	locked bool
}

func (l *lockedRecords) Load(ctx context.Context, id string) (docstore.Document, error) {
	if l.locked && strings.HasPrefix(id, FolderRecordPrefix) {
		return docstore.Document{}, docstore.ErrLocked
	}
	return l.Adapter.Load(ctx, id)
}

func TestRebuildListsALockedFolderRecordUnderThePlaceholderInItsPlace(t *testing.T) {
	ctx := context.Background()
	mem := &lockedRecords{Adapter: docstore.NewMemoryAdapter()}
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	top, _ := s.CreateFolder(ctx, "m", "", "Top")
	kid, _ := s.CreateFolder(ctx, "m", top.ID, "Kid")
	mem.locked = true
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	fs, _ := s.Folders(ctx, "m")
	if len(fs) != 2 {
		t.Fatalf("%+v", fs)
	}
	for _, f := range fs {
		if f.Name != ConfidentialFolderName || f.Protection != ProtectionE2EE {
			t.Errorf("%+v", f)
		}
		if f.ID == kid.ID && f.Parent != top.ID {
			t.Errorf("its place in the tree is kept from the old catalog: %+v", f)
		}
	}
}
