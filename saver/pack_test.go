package saver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docfile/pack"
	"github.com/elaous/gojiberry/docstore"
)

func memSaver() *Saver {
	return &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}
}

// seed makes: Work (folder) > Reports (folder); "top" at the top; "r1" in Reports with a tag
// and a version; "w1" in Work, in the trash.
func seed(t *testing.T, s *Saver) (work, reports Folder, ids map[string]string) {
	t.Helper()
	ctx := context.Background()
	work, _ = s.CreateFolder(ctx, "m", "", "Work")
	reports, _ = s.CreateFolder(ctx, "m", work.ID, "Reports")
	ids = map[string]string{}
	save := func(name, text string, folder string) string {
		d := docWithText(name, text)
		d.SetFolder(folder)
		e, err := s.Save(ctx, "m", docfile.JSON, d)
		if err != nil {
			t.Fatal(err)
		}
		ids[name] = e.ID
		return e.ID
	}
	save("top", "at the top", "")
	r1 := save("r1", "report one", reports.ID)
	save("w1", "work one", work.ID)
	if err := s.SetTags(ctx, "m", r1, []string{"q1", "draft"}); err != nil {
		t.Fatal(err)
	}
	d := docWithText("r1", "report one, earlier")
	d.SetID(r1)
	if _, err := s.SaveVersion(ctx, "m", docfile.JSON, d); err != nil {
		t.Fatal(err)
	}
	if err := s.Trash(ctx, "m", ids["w1"]); err != nil {
		t.Fatal(err)
	}
	return work, reports, ids
}

func TestPackExportAndImportKeepFoldersTagsTrashAndHistory(t *testing.T) {
	ctx := context.Background()
	src := memSaver()
	work, reports, ids := seed(t, src)
	var buf bytes.Buffer
	n, err := src.ExportPack(ctx, "m", "", &buf, PackOptions{Name: "everything", History: true})
	if err != nil || n != 3 {
		t.Fatalf("%d %v", n, err)
	}
	dst := memSaver()
	rep, err := dst.ImportPack(ctx, "m", bytes.NewReader(buf.Bytes()), int64(buf.Len()), ImportOptions{})
	if err != nil || rep.Documents != 3 || rep.Folders != 2 || len(rep.Failed) != 0 || len(rep.Skipped) != 0 || rep.Signed {
		t.Fatalf("%+v %v", rep, err)
	}
	folders, _ := dst.Folders(ctx, "m")
	if len(folders) != 2 {
		t.Fatalf("%+v", folders)
	}
	e, err := dst.Get(ctx, "m", ids["r1"])
	if err != nil || e.Folder != reports.ID || strings.Join(e.Tags, ",") != "q1,draft" {
		t.Fatalf("%+v %v", e, err)
	}
	if got, _ := dst.Folders(ctx, "m"); !hasFolder(got, reports.ID, work.ID) {
		t.Errorf("the nesting is kept: %+v", got)
	}
	if vs, _ := dst.Versions(ctx, "m", ids["r1"]); len(vs) != 1 {
		t.Errorf("the history came with it: %d", len(vs))
	}
	_, trashed, _ := dst.Lists(ctx, "m")
	if len(trashed) != 1 || trashed[0].ID != ids["w1"] {
		t.Errorf("the trash flag is kept: %+v", trashed)
	}
	if d, err := dst.Load(ctx, "m", ids["top"]); err != nil || textOf(d) != "at the top" {
		t.Errorf("%v", err)
	}
}

func hasFolder(fs []Folder, id, parent string) bool {
	for _, f := range fs {
		if f.ID == id && f.Parent == parent {
			return true
		}
	}
	return false
}

func TestPackOfAFolderHasThatFolderAtTheTop(t *testing.T) {
	ctx := context.Background()
	src := memSaver()
	work, reports, ids := seed(t, src)
	var buf bytes.Buffer
	n, err := src.ExportPack(ctx, "m", work.ID, &buf, PackOptions{})
	if err != nil || n != 2 { // r1 and the trashed w1; not "top"
		t.Fatalf("%d %v", n, err)
	}
	dst := memSaver()
	into, _ := dst.CreateFolder(ctx, "m", "", "Inbox")
	rep, err := dst.ImportPack(ctx, "m", bytes.NewReader(buf.Bytes()), int64(buf.Len()), ImportOptions{Into: into.ID})
	if err != nil || rep.Documents != 2 || rep.Folders != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
	fs, _ := dst.Folders(ctx, "m")
	if !hasFolder(fs, work.ID, into.ID) || !hasFolder(fs, reports.ID, work.ID) {
		t.Errorf("Work goes into Inbox, Reports stays in Work: %+v", fs)
	}
	if _, err := dst.Get(ctx, "m", ids["top"]); err == nil {
		t.Error("a document outside the folder is not in the pack")
	}
	if _, err := src.ExportPack(ctx, "m", "nope", &buf, PackOptions{}); err == nil {
		t.Error("an unknown folder")
	}
}

func TestPackImportConflictPolicies(t *testing.T) {
	ctx := context.Background()
	src := memSaver()
	_, _, ids := seed(t, src)
	var buf bytes.Buffer
	if _, err := src.ExportPack(ctx, "m", "", &buf, PackOptions{}); err != nil {
		t.Fatal(err)
	}
	// Change "top" in the target, then import again.
	d := docWithText("top", "edited here")
	d.SetID(ids["top"])
	_, _ = src.Save(ctx, "m", docfile.JSON, d)
	rd := bytes.NewReader(buf.Bytes())
	rep, err := src.ImportPack(ctx, "m", rd, int64(buf.Len()), ImportOptions{})
	if err != nil || rep.Documents != 0 || len(rep.Skipped) != 3 {
		t.Fatalf("skip: %+v %v", rep, err)
	}
	if got, _ := src.Load(ctx, "m", ids["top"]); textOf(got) != "edited here" {
		t.Error("skip leaves the document alone")
	}
	rep, _ = src.ImportPack(ctx, "m", rd, int64(buf.Len()), ImportOptions{Conflict: ConflictReplace})
	if rep.Documents != 3 {
		t.Fatalf("replace: %+v", rep)
	}
	if got, _ := src.Load(ctx, "m", ids["top"]); textOf(got) != "at the top" {
		t.Error("replace puts the pack's document back")
	}
	rep, _ = src.ImportPack(ctx, "m", rd, int64(buf.Len()), ImportOptions{Conflict: ConflictCopy})
	if rep.Documents != 3 {
		t.Fatalf("copy: %+v", rep)
	}
	live, trashed, _ := src.Lists(ctx, "m")
	if len(live)+len(trashed) != 6 {
		t.Errorf("copies are new documents: %d", len(live)+len(trashed))
	}
}

func TestPackSignedAndDamaged(t *testing.T) {
	ctx := context.Background()
	src := memSaver()
	_, _, _ = seed(t, src)
	pub, key, _ := ed25519.GenerateKey(nil)
	var buf bytes.Buffer
	if _, err := src.ExportPack(ctx, "m", "", &buf, PackOptions{Key: key}); err != nil {
		t.Fatal(err)
	}
	dst := memSaver()
	rep, err := dst.ImportPack(ctx, "m", bytes.NewReader(buf.Bytes()), int64(buf.Len()), ImportOptions{})
	if err != nil || !rep.Signed || !rep.SignatureOK || !bytes.Equal(rep.Signer, pub) {
		t.Fatalf("%+v %v", rep, err)
	}
	// One document's bytes changed: that one fails, the others come in.
	bad := append([]byte(nil), buf.Bytes()...)
	i := bytes.Index(bad, []byte("at the top"))
	bad[i] ^= 1
	dst2 := memSaver()
	rep, err = dst2.ImportPack(ctx, "m", bytes.NewReader(bad), int64(len(bad)), ImportOptions{})
	if err != nil || rep.Documents != 2 || len(rep.Failed) != 1 || !strings.HasPrefix(rep.Failed[0], "top") {
		t.Fatalf("%+v %v", rep, err)
	}
	// A cut pack is refused, not half imported.
	cut := buf.Bytes()[:buf.Len()-50]
	if _, err := memSaver().ImportPack(ctx, "m", bytes.NewReader(cut), int64(len(cut)), ImportOptions{}); err == nil {
		t.Error("a cut pack")
	}
	// A damaged document in the source stops an export.
	mem := docstore.NewMemoryAdapter()
	s2 := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	e, _ := s2.Save(ctx, "m", docfile.JSON, docWithText("x", "y"))
	_ = mem.Save(ctx, e.Key, []byte("{ not json"))
	if _, err := s2.ExportPack(ctx, "m", "", &bytes.Buffer{}, PackOptions{}); err == nil || !strings.Contains(err.Error(), `"x"`) {
		t.Errorf("%v", err)
	}
}

func exportOne(t *testing.T, s *Saver, folder string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := s.ExportPack(context.Background(), "m", folder, &buf, PackOptions{}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestPackReplaceTakesTheFoldersTagsAndTrashFlagOfThePack(t *testing.T) {
	ctx := context.Background()
	src := memSaver()
	_, reports, ids := seed(t, src)
	// The pack: r1 live in Reports with tags q1,draft; w1 trashed.
	data := exportOne(t, src, "")
	imp := func() ImportReport {
		rep, err := src.ImportPack(ctx, "m", bytes.NewReader(data), int64(len(data)), ImportOptions{Conflict: ConflictReplace})
		if err != nil || len(rep.Failed) != 0 || len(rep.Warnings) != 0 {
			t.Fatalf("%+v %v", rep, err)
		}
		return rep
	}
	// Here: r1 trashed, retagged and moved to the top; w1 restored.
	if err := src.Trash(ctx, "m", ids["r1"]); err != nil {
		t.Fatal(err)
	}
	_ = src.SetTags(ctx, "m", ids["r1"], []string{"other"})
	if err := src.Move(ctx, "m", "", ids["r1"]); err != nil {
		t.Fatal(err)
	}
	if err := src.Restore(ctx, "m", ids["w1"]); err != nil {
		t.Fatal(err)
	}
	imp()
	e, _ := src.Get(ctx, "m", ids["r1"])
	if !e.Trashed.IsZero() || e.Folder != reports.ID || strings.Join(e.Tags, ",") != "q1,draft" {
		t.Errorf("a trashed document replaced by a live one comes back with the pack's folder and tags: %+v", e)
	}
	w, _ := src.Get(ctx, "m", ids["w1"])
	if w.Trashed.IsZero() {
		t.Errorf("the pack says trashed: %+v", w)
	}
	// What the store holds in the document itself agrees.
	d, _ := src.Load(ctx, "m", ids["r1"])
	if d.Trashed() != 0 || d.Folder() != reports.ID || strings.Join(d.Tags(), ",") != "q1,draft" {
		t.Errorf("document file: %d %q %v", d.Trashed(), d.Folder(), d.Tags())
	}
}

func TestPackWithDeepNestingAndAFolderCycle(t *testing.T) {
	ctx := context.Background()
	// A folder loop is refused by the pack format itself, before it can be imported.
	var loop bytes.Buffer
	lw, _ := pack.NewWriter(&loop, pack.Meta{ID: docfile.NewID(), Name: "loop"})
	a, b := docfile.NewID(), docfile.NewID()
	_ = lw.AddFolder(pack.Folder{ID: a, Name: "a", Parent: b})
	_ = lw.AddFolder(pack.Folder{ID: b, Name: "b", Parent: a})
	if err := lw.Close(nil); err == nil {
		t.Error("a folder loop")
	}
	// A pack that nests deeper than the target allows once put under Into: the folders
	// that do not fit fail, are reported, and their documents go to the folder above.
	s := memSaver()
	into := ""
	for i := 0; i < MaxFolderDepth-2; i++ {
		f, err := s.CreateFolder(ctx, "m", into, "base")
		if err != nil {
			t.Fatal(err)
		}
		into = f.ID
	}
	var buf bytes.Buffer
	pw, _ := pack.NewWriter(&buf, pack.Meta{ID: docfile.NewID(), Name: "deep"})
	parent := ""
	for i := 0; i < 6; i++ {
		id := docfile.NewID()
		if err := pw.AddFolder(pack.Folder{ID: id, Name: "d", Parent: parent}); err != nil {
			t.Fatal(err)
		}
		parent = id
	}
	d := docWithText("deep", "text")
	d.SetID(docfile.NewID())
	d.SetFolder(parent)
	_ = pw.Add(d)
	if err := pw.Close(nil); err != nil {
		t.Fatal(err)
	}
	rep, err := s.ImportPack(ctx, "m", bytes.NewReader(buf.Bytes()), int64(buf.Len()), ImportOptions{Into: into})
	if err != nil || rep.Documents != 1 || len(rep.Failed) == 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	folders, _ := s.Folders(ctx, "m")
	for _, f := range folders {
		if n := len(FolderPath(folders, f.ID)); n > MaxFolderDepth {
			t.Errorf("deeper than allowed: %d", n)
		}
	}
	e, err := s.Get(ctx, "m", d.ID())
	if err != nil || !hasFolderID(folders, e.Folder) {
		t.Errorf("the document is in a folder that is there: %+v %v", e, err)
	}
}

func hasFolderID(fs []Folder, id string) bool {
	for _, f := range fs {
		if f.ID == id {
			return true
		}
	}
	return false
}

func TestPackDocumentWithAnInvalidNameFailsAlone(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	pw, _ := pack.NewWriter(&buf, pack.Meta{ID: docfile.NewID(), Name: "names"})
	bad := docWithText("bad\x01name", "x")
	bad.SetID(docfile.NewID())
	good := docWithText("good", "y")
	good.SetID(docfile.NewID())
	if err := pw.Add(bad); err != nil {
		t.Skipf("the pack writer refuses it: %v", err)
	}
	_ = pw.Add(good)
	if err := pw.Close(nil); err != nil {
		t.Fatal(err)
	}
	s := memSaver()
	rep, err := s.ImportPack(ctx, "m", bytes.NewReader(buf.Bytes()), int64(buf.Len()), ImportOptions{})
	if err != nil || rep.Documents != 1 || len(rep.Failed) != 1 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := s.Get(ctx, "m", bad.ID()); err == nil {
		t.Error("the badly named document is not stored")
	}
}

func TestExportPackWithHistoryStopsOnADamagedVersion(t *testing.T) {
	ctx := context.Background()
	mem := docstore.NewMemoryAdapter()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: mem}}}
	e, _ := s.Save(ctx, "m", docfile.JSON, docWithText("ok", "fine"))
	e2, _ := s.Save(ctx, "m", docfile.JSON, docWithText("hist", "now"))
	d := docWithText("hist", "before")
	d.SetID(e2.ID)
	v, err := s.SaveVersion(ctx, "m", docfile.JSON, d)
	if err != nil {
		t.Fatal(err)
	}
	_ = mem.Save(ctx, v.Key, []byte("{ not json"))
	var buf bytes.Buffer
	n, err := s.ExportPack(ctx, "m", "", &buf, PackOptions{History: true})
	if err == nil || !strings.Contains(err.Error(), `"hist"`) {
		t.Fatalf("%d %v", n, err)
	}
	if n >= 2 {
		t.Errorf("the count is of what was written: %d", n)
	}
	if _, err := pack.Open(bytes.NewReader(buf.Bytes()), int64(buf.Len())); err == nil {
		t.Error("a failed export leaves no pack that opens as complete")
	}
	// Without history the same store exports.
	if n, err := s.ExportPack(ctx, "m", "", &bytes.Buffer{}, PackOptions{}); err != nil || n != 2 {
		t.Errorf("%d %v", n, err)
	}
	_ = e
}

// failTrashWrite fails the write of a document file that carries the trash flag.
type failTrashWrite struct{ docstore.Adapter }

func (f failTrashWrite) Save(ctx context.Context, id string, data []byte) error {
	if strings.HasSuffix(id, ".json") {
		if d, err := docfile.JSON.Unmarshal(data); err == nil && d.Trashed() != 0 {
			return errors.New("disk on fire")
		}
	}
	return f.Adapter.Save(ctx, id, data)
}

func TestPackTrashFailingAfterSaveIsAWarningNotAFailure(t *testing.T) {
	ctx := context.Background()
	src := memSaver()
	_, _, _ = seed(t, src) // w1 is trashed in the pack
	data := exportOne(t, src, "")
	fa := failTrashWrite{docstore.NewMemoryAdapter()}
	dst := &Saver{Targets: []Target{{Name: "m", Adapter: fa}}}
	rep, err := dst.ImportPack(ctx, "m", bytes.NewReader(data), int64(len(data)), ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Documents != 3 || len(rep.Failed) != 0 || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "trash") {
		t.Fatalf("saved, so imported, with the trash failure as a warning: %+v", rep)
	}
}

// The confidential documents of a pack: sealed with an injected function, the fake one here,
// since the saver only carries the envelope (docstore/encrypt has the real one's tests).

func fakeSeal(_ context.Context, id string, plain []byte) ([]byte, error) {
	out := []byte("SEAL:" + id + ":")
	for _, c := range plain {
		out = append(out, c^0x5a)
	}
	return out, nil
}

func fakeOpen(_ context.Context, id string, env []byte) ([]byte, error) {
	rest, ok := bytes.CutPrefix(env, []byte("SEAL:"+id+":"))
	if !ok {
		return nil, errors.New("not sealed for this document")
	}
	out := make([]byte, 0, len(rest))
	for _, c := range rest {
		out = append(out, c^0x5a)
	}
	return out, nil
}

// packSource is a store with a folder, an ordinary tagged document, one in the trash and a
// confidential one in the folder.
func packSource(t *testing.T) (s *Saver, folder Folder, plain, trashed, secret string) {
	t.Helper()
	ctx := context.Background()
	s = &Saver{Targets: []Target{{Name: "src", Adapter: docstore.NewMemoryAdapter()}}}
	folder, err := s.CreateFolder(ctx, "src", "", "Reports")
	if err != nil {
		t.Fatal(err)
	}
	sub, _ := s.CreateFolder(ctx, "src", folder.ID, "2026")
	d := docWithText("Budget opendocs.io", "budget text")
	d.SetFolder(sub.ID)
	e1, _ := s.Save(ctx, "src", docfile.Open, d)
	_ = s.SetTags(ctx, "src", e1.ID, []string{"finance", "q3"})
	e2, _ := s.Save(ctx, "src", docfile.Open, docWithText("Old draft", "draft text"))
	_ = s.Trash(ctx, "src", e2.ID)
	d = docWithText(secretName, "the secret text")
	d.SetFolder(folder.ID)
	e3, _ := s.Save(ctx, "src", docfile.Open, d)
	_ = s.SetTags(ctx, "src", e3.ID, []string{secretTag})
	if err := s.SetProtection(ctx, "src", e3.ID, ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	return s, folder, e1.ID, e2.ID, e3.ID
}

func sealedPack(t *testing.T, o PackOptions) (data []byte, secret, plain, trashed string, folder Folder) {
	t.Helper()
	s, folder, plain, trashed, secret := packSource(t)
	o.Seal = fakeSeal
	var buf bytes.Buffer
	if _, err := s.ExportPack(context.Background(), "src", "", &buf, o); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), secret, plain, trashed, folder
}

func newDst() *Saver {
	return &Saver{Targets: []Target{{Name: "dst", Adapter: docstore.NewMemoryAdapter()}}}
}

func TestExportPackSealsConfidentialDocuments(t *testing.T) {
	ctx := context.Background()
	s, folder, plain, trashed, secret := packSource(t)
	key := ed25519.NewKeyFromSeed([]byte("saver pack test seed 01234567890")[:32])
	var buf bytes.Buffer
	var warned []PackIssue
	n, err := s.ExportPack(ctx, "src", "", &buf, PackOptions{Name: "Everything", History: true, Key: key, Seal: fakeSeal, Warn: func(i PackIssue) { warned = append(warned, i) }})
	if err != nil || n != 3 || len(warned) != 0 {
		t.Fatalf("%d %v %v", n, warned, err)
	}
	if b := buf.Bytes(); bytes.Contains(b, []byte(secretName)) || bytes.Contains(b, []byte("the secret text")) || bytes.Contains(b, []byte(secretTag)) {
		t.Fatal("the pack holds the confidential document in the clear")
	}
	// Without a key: the pack lists, verifies and is signed.
	r, err := pack.Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if rp := r.Verify(); !rp.OK() || !rp.Signed || rp.Checked != 3 {
		t.Fatalf("%+v", rp)
	}
	if r.Meta().Name != "Everything" || len(r.Index().Folders) != 2 {
		t.Errorf("%+v", r.Meta())
	}
	byID := map[string]pack.Entry{}
	for _, e := range r.Index().Entries {
		byID[e.ID] = e
	}
	if e := byID[secret]; !e.Encrypted || e.Name != ConfidentialName || len(e.Tags) != 0 || e.Folder != folder.ID {
		t.Errorf("%+v", e)
	}
	if e := byID[plain]; e.Encrypted || e.Name != "Budget opendocs.io" || !slices.Equal(e.Tags, []string{"finance", "q3"}) {
		t.Errorf("%+v", e)
	}
	if e := byID[trashed]; !e.Trashed || e.Encrypted {
		t.Errorf("%+v", e)
	}
	// The envelope holds the real name, tags and text.
	got, err := r.ReadWith(secret, func(env []byte) ([]byte, error) { return fakeOpen(ctx, secret, env) })
	if err != nil || got.Name() != secretName || !slices.Equal(got.Tags(), []string{secretTag}) || textOf(got) != "the secret text" {
		t.Fatalf("%v %v", got.Name(), err)
	}
	if _, err := r.Read(secret); !errors.Is(err, pack.ErrEncrypted) {
		t.Errorf("%v", err)
	}
}

func TestExportPackWithoutASealLeavesConfidentialOnesOutNeverInTheClear(t *testing.T) {
	ctx := context.Background()
	s, _, _, _, secret := packSource(t)
	var buf bytes.Buffer
	var warned []PackIssue
	n, err := s.ExportPack(ctx, "src", "", &buf, PackOptions{Warn: func(i PackIssue) { warned = append(warned, i) }})
	if err != nil || n != 2 || len(warned) != 1 || warned[0].ID != secret || warned[0].Reason != PackNeedsKey {
		t.Fatalf("%d %+v %v", n, warned, err)
	}
	if bytes.Contains(buf.Bytes(), []byte("secret text")) || bytes.Contains(buf.Bytes(), []byte(secretName)) {
		t.Error("in the clear")
	}
	// No Warn is fine too.
	if n, err := s.ExportPack(ctx, "src", "", &bytes.Buffer{}, PackOptions{}); err != nil || n != 2 {
		t.Errorf("%d %v", n, err)
	}
	// A failing Seal stops the export: a pack that quietly lacks it would pass for a backup.
	boom := errors.New("no recipients")
	_, err = s.ExportPack(ctx, "src", "", &bytes.Buffer{}, PackOptions{Seal: func(context.Context, string, []byte) ([]byte, error) { return nil, boom }})
	if !errors.Is(err, boom) {
		t.Errorf("%v", err)
	}
	if _, err = s.ExportPack(ctx, "src", "", &bytes.Buffer{}, PackOptions{Seal: func(context.Context, string, []byte) ([]byte, error) { return nil, nil }}); err == nil {
		t.Error("an empty envelope was written")
	}
}

func TestImportPackOpensConfidentialDocumentsAndKeepsThemConfidential(t *testing.T) {
	ctx := context.Background()
	data, secret, plain, trashed, folder := sealedPack(t, PackOptions{History: true})
	dst := newDst()
	rep, err := dst.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen})
	if err != nil || rep.Documents != 3 || rep.Folders != 2 || len(rep.Failed) != 0 || len(rep.NeedsKey) != 0 || len(rep.Warnings) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	e, err := dst.Get(ctx, "dst", plain)
	if err != nil || e.Name != "Budget opendocs.io" || !slices.Equal(e.Tags, []string{"finance", "q3"}) || e.Folder == "" || e.Protection == ProtectionE2EE {
		t.Fatalf("%+v %v", e, err)
	}
	if e, _ := dst.Get(ctx, "dst", trashed); e.Trashed.IsZero() {
		t.Errorf("not in the trash: %+v", e)
	}
	e, err = dst.Get(ctx, "dst", secret)
	if err != nil || e.Name != ConfidentialName || len(e.Tags) != 0 || e.Protection != ProtectionE2EE || e.Folder != folder.ID {
		t.Fatalf("%+v %v", e, err)
	}
	if txt := catalogText(t, dst); strings.Contains(txt, secretName) || strings.Contains(txt, secretTag) {
		t.Errorf("the catalog says what is hidden: %s", txt)
	}
	if n, tags, err := dst.Reveal(ctx, "dst", secret); err != nil || n != secretName || !slices.Equal(tags, []string{secretTag}) {
		t.Errorf("%q %v %v", n, tags, err)
	}
	// Again: everything is there and skipped, nothing is touched.
	rep, err = dst.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen})
	if err != nil || rep.Documents != 0 || len(rep.Skipped) != 3 || rep.Folders != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestImportPackWithoutAKeyWarnsAndImportsTheRest(t *testing.T) {
	ctx := context.Background()
	data, secret, plain, _, _ := sealedPack(t, PackOptions{})
	dst := newDst()
	rep, err := dst.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{})
	if err != nil || rep.Documents != 2 || len(rep.NeedsKey) != 1 || rep.NeedsKey[0].ID != secret || rep.NeedsKey[0].Reason != PackNeedsKey || len(rep.Warnings) != 1 || len(rep.Failed) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := dst.Get(ctx, "dst", secret); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := dst.Get(ctx, "dst", plain); err != nil {
		t.Error(err)
	}
	// An Open that cannot open it is the same warning, with the reason.
	dst2 := newDst()
	nokey := errors.New("wrong key")
	rep, err = dst2.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: func(context.Context, string, []byte) ([]byte, error) { return nil, nokey }})
	if err != nil || rep.Documents != 2 || len(rep.NeedsKey) != 1 || !errors.Is(rep.NeedsKey[0].Err, nokey) || !strings.Contains(rep.Warnings[0], "wrong key") {
		t.Fatalf("%+v %v", rep, err)
	}
	// Later, with the key, the one left behind comes in (the others are skipped as there).
	rep, err = dst2.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen})
	if err != nil || rep.Documents != 1 || len(rep.Skipped) != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
	if e, err := dst2.Get(ctx, "dst", secret); err != nil || e.Protection != ProtectionE2EE {
		t.Errorf("%+v %v", e, err)
	}
}

func TestImportPackRefusesConfidentialWhenTheCheckDoes(t *testing.T) {
	ctx := context.Background()
	data, secret, _, _, _ := sealedPack(t, PackOptions{})
	dst := newDst()
	need := errors.New("a passkey is the only key")
	dst.ConfidentialCheck = func(context.Context, string, string) error { return need }
	rep, err := dst.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen})
	if err != nil || rep.Documents != 2 || len(rep.Failed) != 1 || !strings.Contains(rep.Failed[0], need.Error()) {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := dst.Get(ctx, "dst", secret); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestImportPackConflictPoliciesWithConfidentialDocuments(t *testing.T) {
	ctx := context.Background()
	data, secret, _, _, _ := sealedPack(t, PackOptions{})
	dst := newDst()
	if _, err := dst.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen}); err != nil {
		t.Fatal(err)
	}
	// Copy: a new confidential document under a new ID, with its real name inside.
	rep, err := dst.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen, Conflict: ConflictCopy})
	if err != nil || rep.Documents != 3 {
		t.Fatalf("%+v %v", rep, err)
	}
	l, _ := dst.List(ctx, "dst")
	copies := 0
	for _, e := range l {
		if e.ID == secret || e.Protection != ProtectionE2EE {
			continue
		}
		copies++
		if n, _, err := dst.Reveal(ctx, "dst", e.ID); err != nil || n != secretName+" (imported)" {
			t.Errorf("%q %v", n, err)
		}
	}
	if copies != 1 {
		t.Errorf("%d copies", copies)
	}
	// Replace puts a confidential document over an ordinary one with that ID: it is confidential after.
	d := docWithText("Plain stand-in", "x")
	d.SetID(secret)
	dst2 := newDst()
	if _, err := dst2.Save(ctx, "dst", docfile.Open, d); err != nil {
		t.Fatal(err)
	}
	rep, err = dst2.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen, Conflict: ConflictReplace})
	if err != nil || rep.Documents != 3 || len(rep.Warnings) != 0 || len(rep.Failed) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	e, _ := dst2.Get(ctx, "dst", secret)
	if e.Protection != ProtectionE2EE || e.Name != ConfidentialName || len(e.Tags) != 0 {
		t.Errorf("%+v", e)
	}
	if n, tags, err := dst2.Reveal(ctx, "dst", secret); err != nil || n != secretName || !slices.Equal(tags, []string{secretTag}) {
		t.Errorf("%q %v %v", n, tags, err)
	}
}
