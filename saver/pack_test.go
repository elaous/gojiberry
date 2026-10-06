package saver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docfile"
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
