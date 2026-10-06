package saver

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
)

// lockable is a store that can stop opening documents, the way an encrypting adapter does
// without the key: the catalog and records stay readable, a document's file is locked.
type lockable struct {
	docstore.Adapter
	locked atomic.Bool
	only   string // when set, only the files of this document are locked
}

func (l *lockable) Load(ctx context.Context, id string) (docstore.Document, error) {
	if l.locked.Load() && (l.only == "" || strings.Contains(id, l.only)) && (!reserved(id) || strings.HasPrefix(id, docstore.FolderSecretPrefix)) && !strings.HasPrefix(id, FolderRecordPrefix) {
		return docstore.Document{}, fmt.Errorf("fake: %w", docstore.ErrLocked)
	}
	return l.Adapter.Load(ctx, id)
}

const secretName, secretTag = "Quarterly numbers xango", "layoffs"

func confidentialSaver(t *testing.T) (*Saver, *lockable, string) {
	t.Helper()
	ctx := context.Background()
	mem := &lockable{Adapter: docstore.NewMemoryAdapter()}
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	d := docWithText(secretName, "the secret text")
	e, err := s.Save(ctx, "m", docfile.Open, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTags(ctx, "m", e.ID, []string{secretTag, "q3"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProtection(ctx, "m", e.ID, ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	return s, mem, e.ID
}

// catalogText is the catalog as stored, readable by whoever can read the store.
func catalogText(t *testing.T, s *Saver) string {
	t.Helper()
	d, err := s.Targets[0].Adapter.Load(context.Background(), CatalogKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(d.Data)
}

func TestConfidentialCatalogHoldsAPlaceholder(t *testing.T) {
	ctx := context.Background()
	s, _, id := confidentialSaver(t)
	e, err := s.Get(ctx, "m", id)
	if err != nil || e.Name != ConfidentialName || len(e.Tags) != 0 || e.Protection != ProtectionE2EE || e.Size == 0 || e.Updated.IsZero() {
		t.Fatalf("%+v %v", e, err)
	}
	if txt := catalogText(t, s); strings.Contains(txt, secretName) || strings.Contains(txt, secretTag) || strings.Contains(txt, "q3") {
		t.Errorf("the catalog says what is hidden: %s", txt)
	}
	live, _ := s.List(ctx, "m")
	if len(live) != 1 || live[0].Name != ConfidentialName || live[0].Protection != ProtectionE2EE {
		t.Errorf("%+v", live)
	}
	// The document holds the real name and tags, and says it is confidential.
	d, err := s.Load(ctx, "m", id)
	if err != nil || d.Name() != secretName || !slices.Equal(d.Tags(), []string{secretTag, "q3"}) || d.Protection() != ProtectionE2EE || textOf(d) != "the secret text" {
		t.Fatalf("%v %v %v", d.Name(), d.Tags(), err)
	}
	name, tags, err := s.Reveal(ctx, "m", id)
	if err != nil || name != secretName || !slices.Equal(tags, []string{secretTag, "q3"}) {
		t.Errorf("%q %v %v", name, tags, err)
	}
	// Back to the server's protection: name and tags return to the catalog.
	if err := s.SetProtection(ctx, "m", id, ProtectionServer); err != nil {
		t.Fatal(err)
	}
	e, _ = s.Get(ctx, "m", id)
	if e.Name != secretName || !slices.Equal(e.Tags, []string{secretTag, "q3"}) || e.Protection != ProtectionServer {
		t.Errorf("%+v", e)
	}
	if d, _ := s.Load(ctx, "m", id); d.Protection() != "" || d.Name() != secretName {
		t.Errorf("%v %q", d.Meta, d.Name())
	}
	if err := s.SetProtection(ctx, "m", id, "paranoid"); !errors.Is(err, ErrProtection) {
		t.Errorf("%v", err)
	}
	if err := s.SetProtection(ctx, "m", docfile.NewID(), ProtectionE2EE); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestConfidentialCheckGatesEnablingNotDisabling(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}
	e, _ := s.Save(ctx, "m", docfile.Open, docWithText("plain doc", "x"))
	need := errors.New("a passkey is the only key")
	var asked []string
	s.ConfidentialCheck = func(_ context.Context, target, id string) error {
		asked = append(asked, target+"/"+id)
		return need
	}
	if err := s.SetProtection(ctx, "m", e.ID, ProtectionE2EE); !errors.Is(err, need) {
		t.Fatalf("%v", err)
	}
	if got, _ := s.Get(ctx, "m", e.ID); got.Protection != ProtectionServer || got.Name != "plain doc" {
		t.Errorf("a refused switch changed the entry: %+v", got)
	}
	// A new document marked confidential (a copy, an import) asks too.
	d := docWithText("born confidential", "x")
	d.SetProtection(ProtectionE2EE)
	if _, err := s.Save(ctx, "m", docfile.Open, d); !errors.Is(err, need) {
		t.Errorf("%v", err)
	}
	s.ConfidentialCheck = func(_ context.Context, _, _ string) error { return nil }
	if err := s.SetProtection(ctx, "m", e.ID, ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	// Switching away needs no check, and the check is not asked for what is already so.
	s.ConfidentialCheck = func(context.Context, string, string) error { return need }
	if err := s.SetProtection(ctx, "m", e.ID, ProtectionE2EE); err != nil {
		t.Errorf("already confidential: %v", err)
	}
	if err := s.SetProtection(ctx, "m", e.ID, ProtectionServer); err != nil {
		t.Errorf("%v", err)
	}
	if len(asked) != 2 || asked[0] != "m/"+e.ID {
		t.Errorf("%v", asked)
	}
}

func TestConfidentialSurvivesTheDocumentOperations(t *testing.T) {
	ctx := context.Background()
	s, _, id := confidentialSaver(t)
	hidden := func(when string) {
		t.Helper()
		e, err := s.Get(ctx, "m", id)
		if err != nil || e.Name != ConfidentialName || len(e.Tags) != 0 || e.Protection != ProtectionE2EE {
			t.Fatalf("%s: %+v %v", when, e, err)
		}
		if txt := catalogText(t, s); strings.Contains(txt, "layoffs") || strings.Contains(txt, "xango") || strings.Contains(txt, "Renamed") {
			t.Fatalf("%s: the catalog says what is hidden: %s", when, txt)
		}
	}
	// Saving from an editor that has the name but not the tags keeps the tags, in the document.
	ed := docWithText("Quarterly numbers xango", "edited text")
	ed.SetID(id)
	if _, err := s.Save(ctx, "m", docfile.Open, ed); err != nil {
		t.Fatal(err)
	}
	hidden("save")
	if d, _ := s.Load(ctx, "m", id); textOf(d) != "edited text" || !slices.Equal(d.Tags(), []string{secretTag, "q3"}) || d.Protection() != ProtectionE2EE {
		t.Errorf("%q %v", textOf(d), d.Tags())
	}
	// Rename and tags go into the document.
	if err := s.Rename(ctx, "m", id, "Renamed secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTags(ctx, "m", id, []string{"renamedtag"}); err != nil {
		t.Fatal(err)
	}
	hidden("rename and tags")
	if n, tags, _ := s.Reveal(ctx, "m", id); n != "Renamed secret" || !slices.Equal(tags, []string{"renamedtag"}) {
		t.Errorf("%q %v", n, tags)
	}
	// Folders, trash, versions.
	f, _ := s.CreateFolder(ctx, "m", "", "Work")
	if err := s.Move(ctx, "m", f.ID, id); err != nil {
		t.Fatal(err)
	}
	hidden("move")
	if err := s.Trash(ctx, "m", id); err != nil {
		t.Fatal(err)
	}
	tr, _ := s.Trashed(ctx, "m")
	if len(tr) != 1 || tr[0].Name != ConfidentialName || tr[0].Protection != ProtectionE2EE || tr[0].Folder != f.ID {
		t.Errorf("%+v", tr)
	}
	if err := s.Restore(ctx, "m", id); err != nil {
		t.Fatal(err)
	}
	hidden("trash and restore")
	cur, _ := s.Load(ctx, "m", id)
	if _, err := s.SaveVersion(ctx, "m", docfile.Open, cur); err != nil {
		t.Fatal(err)
	}
	if vs, _ := s.Versions(ctx, "m", id); len(vs) != 1 {
		t.Fatalf("%v", vs)
	}
	hidden("version")
	// A copy of a confidential document is confidential: "Copy of" the real name.
	cp, err := s.Duplicate(ctx, "m", id, "")
	if err != nil || cp.Name != ConfidentialName || cp.Protection != ProtectionE2EE {
		t.Fatalf("%+v %v", cp, err)
	}
	if n, _, _ := s.Reveal(ctx, "m", cp.ID); n != "Copy of Renamed secret" {
		t.Errorf("%q", n)
	}
	// An archive of the folder is the owner's download: named after what the document is.
	data, _, err := s.Archive(ctx, "m", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	var files []string
	for _, f := range zr.File {
		files = append(files, f.Name)
	}
	slices.Sort(files)
	if !slices.Equal(files, []string{"Copy of Renamed secret.open", "Renamed secret.open"}) {
		t.Errorf("%v", files)
	}
	// A document that goes wrong is still checked: Verify keeps level and placeholder.
	rep, err := s.Verify(ctx, "m")
	if err != nil || rep.Checked != 2 || rep.Locked != 0 || len(rep.Lost)+len(rep.Restored) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	hidden("verify")
}

func TestRebuildKeepsLevelAndPlaceholder(t *testing.T) {
	ctx := context.Background()
	s, mem, id := confidentialSaver(t)
	other, _ := s.Save(ctx, "m", docfile.Open, docWithText("an ordinary one", "x"))
	if err := s.SetTags(ctx, "m", other.ID, []string{"open"}); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		e, err := s.Get(ctx, "m", id)
		if err != nil || e.Name != ConfidentialName || len(e.Tags) != 0 || e.Protection != ProtectionE2EE {
			t.Fatalf("%s: %+v %v", when, e, err)
		}
		if txt := catalogText(t, s); strings.Contains(txt, "xango") || strings.Contains(txt, "layoffs") {
			t.Fatalf("%s: %s", when, txt)
		}
		if o, _ := s.Get(ctx, "m", other.ID); o.Name != "an ordinary one" || o.Protection != ProtectionServer || !slices.Equal(o.Tags, []string{"open"}) {
			t.Fatalf("%s: %+v", when, o)
		}
	}
	// Rebuilt with the key, over a catalog that is gone: the document says it is confidential.
	_ = mem.Delete(ctx, CatalogKey)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	check("rebuild from documents, with the key")
	// Rebuilt over the catalog there is, and by Repair.
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	check("rebuild over a catalog")
	s.RepairGrace = -1
	_ = mem.Delete(ctx, id+"~") // nothing: a stray delete of a key that does not exist is harmless
	if _, err := s.Repair(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	check("repair")
	// A catalog that lists it as confidential keeps it so even for a file that lost its mark.
	cur, _ := s.Get(ctx, "m", id)
	stored, _ := mem.Adapter.Load(ctx, cur.Key)
	d, _ := docfile.Open.Unmarshal(stored.Data)
	d.SetProtection("")
	unmarked, _ := docfile.Open.Marshal(d)
	_ = mem.Adapter.Save(ctx, cur.Key, unmarked)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	check("rebuild over a file with no mark")
}

func TestRebuildWithoutTheKeyListsAnUnreadableFileAsConfidential(t *testing.T) {
	ctx := context.Background()
	s, mem, id := confidentialSaver(t)
	f, _ := s.CreateFolder(ctx, "m", "", "Work")
	if err := s.Move(ctx, "m", f.ID, id); err != nil {
		t.Fatal(err)
	}
	other, _ := s.Save(ctx, "m", docfile.Open, docWithText("ordinary", "x"))
	before, _ := s.Get(ctx, "m", id)

	// 1. With the catalog we had: the entry is as it was (folder included), the other is read.
	mem.locked.Store(true)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatalf("a rebuild must not stop at a document it cannot open: %v", err)
	}
	e, _ := s.Get(ctx, "m", id)
	if e.Name != ConfidentialName || e.Protection != ProtectionE2EE || e.Folder != f.ID || e.Key != before.Key || e.Size != before.Size {
		t.Errorf("%+v, was %+v", e, before)
	}
	// (the ordinary one is locked too in this fake, so it is listed as it was, by the catalog)
	if o, _ := s.Get(ctx, "m", other.ID); o.Name != "ordinary" || o.Protection != ProtectionServer {
		t.Errorf("%+v", o)
	}

	// 2. With no catalog at all: nothing says what the file is but that it cannot be opened,
	// so it is listed under the placeholder as confidential; its folder is unknown.
	_ = mem.Adapter.Delete(ctx, CatalogKey)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	e, err := s.Get(ctx, "m", id)
	if err != nil || e.Name != ConfidentialName || e.Protection != ProtectionE2EE || len(e.Tags) != 0 || e.Folder != "" || e.Key != before.Key || e.Size == 0 {
		t.Fatalf("%+v %v", e, err)
	}
	if txt := catalogText(t, s); strings.Contains(txt, "xango") || strings.Contains(txt, "layoffs") {
		t.Errorf("%s", txt)
	}
	// With the key again, a rebuild reads the document, and the level stays.
	mem.locked.Store(false)
	if err := s.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Get(ctx, "m", id); e.Name != ConfidentialName || e.Protection != ProtectionE2EE {
		t.Errorf("%+v", e)
	}
	if n, tags, err := s.Reveal(ctx, "m", id); err != nil || n != secretName || len(tags) != 2 {
		t.Errorf("%q %v %v", n, tags, err)
	}
}

func TestLockedDocumentsAreReportedNotRepairedAndNotOverwritten(t *testing.T) {
	ctx := context.Background()
	s, mem, id := confidentialSaver(t)
	mem.locked.Store(true)
	rep, err := s.Verify(ctx, "m")
	if err != nil || rep.Locked != 1 || rep.Checked != 0 || len(rep.Lost) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := s.Load(ctx, "m", id); !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("load: %v", err)
	}
	if _, _, err := s.Reveal(ctx, "m", id); !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("reveal: %v", err)
	}
	// Saving over a document that cannot be read would drop what is inside it (the tags).
	ed := docWithText("x", "y")
	ed.SetID(id)
	if _, err := s.Save(ctx, "m", docfile.Open, ed); !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("save: %v", err)
	}
	if err := s.SetProtection(ctx, "m", id, ProtectionServer); !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("setprotection: %v", err)
	}
	if e, _ := s.Get(ctx, "m", id); e.Name != ConfidentialName || e.Protection != ProtectionE2EE {
		t.Errorf("%+v", e)
	}
}

func TestSessionShowsThePlaceholderUntilOpened(t *testing.T) {
	ctx := context.Background()
	s, _, id := confidentialSaver(t)
	var opened []docfile.Document
	ss := NewSession(s, func() docfile.Document { return docWithText("", "editor text") }, func(d docfile.Document) { opened = append(opened, d) })
	ss.Pending.Inline = true
	if err := ss.SetTarget(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	names := func() (n []string, tags []string) {
		for _, d := range ss.Docs().Docs {
			n = append(n, d.Name)
			tags = append(tags, d.Tags...)
		}
		return
	}
	if n, tags := names(); !slices.Equal(n, []string{ConfidentialName}) || len(tags) != 0 || len(allTags(ss.entries)) != 0 {
		t.Fatalf("%v %v", n, tags)
	}
	// A tag filter has nothing to match: the hidden tags are not in the list.
	ss.SetTagFilter(secretTag)
	for _, d := range ss.Docs().Docs {
		if slices.Contains(d.Tags, secretTag) {
			t.Fatal("the hidden tag is in the list")
		}
	}
	ss.SetTagFilter("")

	if err := ss.Open(ctx, id); err != nil {
		t.Fatal(err)
	}
	if ss.Name() != secretName || len(opened) != 1 || opened[0].Name() != secretName {
		t.Fatalf("%q", ss.Name())
	}
	if n, tags := names(); !slices.Equal(n, []string{secretName}) || !slices.Equal(tags, []string{secretTag, "q3"}) {
		t.Errorf("after the open: %v %v", n, tags)
	}
	if !strings.Contains(ss.Props().Status, secretName+" opened") {
		t.Errorf("%q", ss.Props().Status)
	}
	// Saving from the session keeps it confidential and keeps showing the real name.
	if err := ss.Save(ctx); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Get(ctx, "m", id); e.Name != ConfidentialName || e.Protection != ProtectionE2EE {
		t.Errorf("%+v", e)
	}
	if n, tags := names(); !slices.Equal(n, []string{secretName}) || !slices.Equal(tags, []string{secretTag, "q3"}) {
		t.Errorf("after the save: %v %v", n, tags)
	}
	if err := ss.SetTags(ctx, id, []string{"new"}); err != nil {
		t.Fatal(err)
	}
	if _, tags := names(); !slices.Equal(tags, []string{"new"}) {
		t.Errorf("%v", tags)
	}
	// Making it ordinary again drops the memory; the catalog has the real name.
	if err := ss.SetProtection(ctx, id, ProtectionServer); err != nil {
		t.Fatal(err)
	}
	if n, _ := names(); !slices.Equal(n, []string{secretName}) || len(ss.revealed) != 0 {
		t.Errorf("%v %v", n, ss.revealed)
	}
	// And confidential once more: the placeholder is replaced at once, since it is openable.
	if err := ss.SetProtection(ctx, id, ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	if e, _ := s.Get(ctx, "m", id); e.Name != ConfidentialName {
		t.Errorf("%+v", e)
	}
	if n, _ := names(); !slices.Equal(n, []string{secretName}) {
		t.Errorf("%v", n)
	}
}

func TestSessionRevealNeedsAKey(t *testing.T) {
	ctx := context.Background()
	s, mem, id := confidentialSaver(t)
	ss := NewSession(s, func() docfile.Document { return docfile.Document{} }, nil)
	ss.Pending.Inline = true
	_ = ss.SetTarget(ctx, "m")
	mem.locked.Store(true)
	if err := ss.Reveal(ctx, id); !errors.Is(err, docstore.ErrLocked) {
		t.Fatalf("%v", err)
	}
	if err := ss.Open(ctx, id); !errors.Is(err, docstore.ErrLocked) {
		t.Fatalf("%v", err)
	}
	if ss.Docs().Docs[0].Name != ConfidentialName {
		t.Errorf("%v", ss.Docs().Docs)
	}
	mem.locked.Store(false)
	if err := ss.Reveal(ctx, id); err != nil {
		t.Fatal(err)
	}
	if ss.Docs().Docs[0].Name != secretName || ss.Name() != "" {
		t.Errorf("%v %q", ss.Docs().Docs, ss.Name())
	}
}

func TestRekeyHookOrderAndRetry(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	e, _ := s.Save(ctx, "m", docfile.Open, docWithText("plain doc", "x"))
	// catalogLevel is what the catalog says at the moment the hook runs.
	var calls []string
	failing := true
	s.Rekey = func(_ context.Context, _, id, to string) error {
		got, _ := s.Get(ctx, "m", id)
		calls = append(calls, to+" while catalog lists "+got.Name)
		if failing {
			return errors.New("file X still wrapped")
		}
		return nil
	}
	// To e2ee: re-key first; a failure changes nothing and is reported.
	if err := s.SetProtection(ctx, "m", e.ID, ProtectionE2EE); err == nil || !strings.Contains(err.Error(), "file X still wrapped") {
		t.Fatalf("%v", err)
	}
	if got, _ := s.Get(ctx, "m", e.ID); got.Protection != ProtectionServer || got.Name != "plain doc" {
		t.Errorf("a failed re-key moved the name: %+v", got)
	}
	failing = false
	if err := s.SetProtection(ctx, "m", e.ID, ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	// Back: the catalog and file move first, the re-key to the server keys is last.
	if err := s.SetProtection(ctx, "m", e.ID, ProtectionServer); err != nil {
		t.Fatal(err)
	}
	want := []string{"e2ee while catalog lists plain doc", "e2ee while catalog lists plain doc", "server while catalog lists plain doc"}
	if !slices.Equal(calls, want) {
		t.Errorf("%q", calls)
	}
	// A failure on the way back leaves the catalog at server; the same call retries the hook.
	if err := s.SetProtection(ctx, "m", e.ID, ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	failing = true
	if err := s.SetProtection(ctx, "m", e.ID, ProtectionServer); err == nil {
		t.Fatal("want the re-key failure")
	}
	if got, _ := s.Get(ctx, "m", e.ID); got.Protection != ProtectionServer || got.Name != "plain doc" {
		t.Errorf("%+v", got)
	}
	n := len(calls)
	failing = false
	if err := s.SetProtection(ctx, "m", e.ID, ProtectionServer); err != nil || len(calls) != n+1 {
		t.Errorf("a retry at the settled catalog level runs the hook: %v %v", err, calls[n:])
	}
}

func TestRekeyHookRunsBeforeANewConfidentialDocumentIsWritten(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	need := errors.New("no recovery key")
	s.ConfidentialCheck = func(context.Context, string, string) error { return need }
	var rekeyed int
	s.Rekey = func(context.Context, string, string, string) error { rekeyed++; return nil }
	d := docWithText("born confidential", "x")
	d.SetProtection(ProtectionE2EE)
	if _, err := s.Save(ctx, "m", docfile.Open, d); !errors.Is(err, need) || rekeyed != 0 {
		t.Fatalf("a refused document is not re-keyed: %v %d", err, rekeyed)
	}
	s.ConfidentialCheck = func(context.Context, string, string) error { return nil }
	if _, err := s.Save(ctx, "m", docfile.Open, d); err != nil || rekeyed != 1 {
		t.Fatalf("%v %d", err, rekeyed)
	}
	// An ordinary save does not call it.
	if _, err := s.Save(ctx, "m", docfile.Open, docWithText("plain", "x")); err != nil || rekeyed != 1 {
		t.Fatalf("%v %d", err, rekeyed)
	}
}

// catalogHook runs before: it is called with the number of this catalog load (1 is the first)
// and can fail it or act before it returns.
type catalogHook struct {
	*docstore.MemoryAdapter
	before func(n int) error
	n      atomic.Int32
	data   []string // the non-catalog keys saved, in order
}

func (h *catalogHook) Load(ctx context.Context, id string) (docstore.Document, error) {
	if id == CatalogKey && h.before != nil {
		if err := h.before(int(h.n.Add(1))); err != nil {
			return docstore.Document{}, err
		}
	}
	return h.MemoryAdapter.Load(ctx, id)
}

func (h *catalogHook) Save(ctx context.Context, id string, b []byte) error {
	if id != CatalogKey {
		h.data = append(h.data, id)
	}
	return h.MemoryAdapter.Save(ctx, id, b)
}

func TestSaveNeverWritesAConfidentialDocumentOnAGetFault(t *testing.T) {
	ctx := context.Background()
	h := &catalogHook{MemoryAdapter: docstore.NewMemoryAdapter()}
	s := &Saver{Targets: []Target{{Name: "m", Adapter: h}}}
	var rekeyed int
	s.Rekey = func(context.Context, string, string, string) error { rekeyed++; return nil }
	d := docWithText("born confidential", "x")
	d.SetProtection(ProtectionE2EE)
	d.SetID(docfile.NewID())
	// The first catalog read (the existence check) fails with something that is not "not
	// found": that must not be read as "a new document".
	h.before = func(n int) error {
		if n == 1 {
			return errors.New("transient")
		}
		return nil
	}
	if _, err := s.Save(ctx, "m", docfile.Open, d); err == nil || !strings.Contains(err.Error(), "transient") {
		t.Fatalf("want the Get error: %v", err)
	}
	if len(h.data) != 0 {
		t.Errorf("a document was written: %v", h.data)
	}
}

func TestSaveRekeysWhenTheDocumentIsDeletedBetweenTheReadAndTheWrite(t *testing.T) {
	ctx := context.Background()
	h := &catalogHook{MemoryAdapter: docstore.NewMemoryAdapter()}
	s := &Saver{Targets: []Target{{Name: "m", Adapter: h}}}
	other := &Saver{Targets: []Target{{Name: "m", Adapter: h.MemoryAdapter}}}
	var order []string
	s.Rekey = func(_ context.Context, _, _, to string) error { order = append(order, "rekey "+to); return nil }
	e, err := s.Save(ctx, "m", docfile.Open, docWithText("plain", "x"))
	if err != nil {
		t.Fatal(err)
	}
	h.data = nil
	d := docWithText("plain", "y")
	d.SetID(e.ID)
	d.SetProtection(ProtectionE2EE) // a copy of a confidential document, saved over the server one
	// The existence check sees the (server) document; then it is deleted before the write.
	h.n.Store(0)
	h.before = func(n int) error {
		if n == 2 {
			return other.Delete(ctx, "m", e.ID)
		}
		return nil
	}
	if _, err := s.Save(ctx, "m", docfile.Open, d); err != nil {
		t.Fatal(err)
	}
	if len(order) == 0 || order[len(order)-1] != "rekey e2ee" {
		t.Fatalf("a confidential document was written without being re-keyed: %v", order)
	}
	if got, err := s.Get(ctx, "m", e.ID); err != nil || got.Protection != ProtectionE2EE {
		t.Errorf("%+v %v", got, err)
	}
}

func TestSaveOfAConfidentialDocumentAssertsItsKeys(t *testing.T) {
	ctx := context.Background()
	s, _, id := confidentialSaver(t)
	var calls []string
	s.Rekey = func(_ context.Context, _, _, to string) error { calls = append(calls, to); return nil }
	d, err := s.Load(ctx, "m", id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(ctx, "m", docfile.Open, d); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{ProtectionE2EE}) {
		t.Errorf("a save of a confidential document re-asserts its level: %v", calls)
	}
	// A failed assertion stops the save before anything is written.
	s.Rekey = func(context.Context, string, string, string) error { return errors.New("record unsigned") }
	if _, err := s.Save(ctx, "m", docfile.Open, d); err == nil || !strings.Contains(err.Error(), "record unsigned") {
		t.Errorf("%v", err)
	}
}

type fakeUnreadable struct{ files []string }

func (e fakeUnreadable) Error() string             { return "cannot open" }
func (e fakeUnreadable) UnreadableFiles() []string { return e.files }

func TestDropVersionsOnlyDropsSnapshotsOfTheDocument(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	e, _ := s.Save(ctx, "m", docfile.Open, docWithText("a", "x"))
	d := docWithText("a", "x")
	d.SetID(e.ID)
	v, err := s.SaveVersion(ctx, "m", docfile.Open, d)
	if err != nil {
		t.Fatal(err)
	}
	got := UnreadableKeys(fmt.Errorf("wrapped: %w", fakeUnreadable{[]string{v.Key, e.ID + ".json"}}))
	if !slices.Equal(got, []string{v.Key}) {
		t.Errorf("only snapshots are offered for dropping: %v", got)
	}
	if UnreadableKeys(errors.New("other")) != nil {
		t.Error("not a re-key failure")
	}
	other := docfile.NewID()
	if err := s.DropVersions(ctx, "m", e.ID, []string{v.Key, "versions/" + other + "/1.json"}); !errors.Is(err, ErrName) {
		t.Errorf("a snapshot of another document: %v", err)
	}
	if vs, _ := s.Versions(ctx, "m", e.ID); len(vs) != 1 {
		t.Errorf("nothing is dropped when one key is refused: %v", vs)
	}
	if err := s.DropVersions(ctx, "m", e.ID, []string{v.Key}); err != nil {
		t.Fatal(err)
	}
	if vs, _ := s.Versions(ctx, "m", e.ID); len(vs) != 0 {
		t.Errorf("%v", vs)
	}
}
