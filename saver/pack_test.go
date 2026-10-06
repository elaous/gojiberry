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

// A fake sealer, as in the pack tests: the saver only carries the envelope.
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

func TestExportPackSealsConfidentialDocuments(t *testing.T) {
	ctx := context.Background()
	s, folder, plain, trashed, secret := packSource(t)
	key := ed25519.NewKeyFromSeed([]byte("saver pack test seed 01234567890")[:32])
	var buf bytes.Buffer
	rep, err := s.ExportPack(ctx, "src", &buf, ExportOptions{Seal: fakeSeal, Sign: key, Name: "Everything"})
	if err != nil || rep.Exported != 3 || len(rep.Skipped) != 0 {
		t.Fatalf("%+v %v", rep, err)
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

func TestExportPackWithoutASealerLeavesConfidentialOnesOutNeverInTheClear(t *testing.T) {
	ctx := context.Background()
	s, _, _, _, secret := packSource(t)
	var buf bytes.Buffer
	rep, err := s.ExportPack(ctx, "src", &buf, ExportOptions{})
	if err != nil || rep.Exported != 2 || len(rep.Skipped) != 1 || rep.Skipped[0].ID != secret || rep.Skipped[0].Reason != PackNeedsKey {
		t.Fatalf("%+v %v", rep, err)
	}
	if bytes.Contains(buf.Bytes(), []byte("secret text")) {
		t.Error("in the clear")
	}
	// A failing sealer stops the export: a pack that quietly lacks it would pass for a backup.
	boom := errors.New("no recipients")
	_, err = s.ExportPack(ctx, "src", &bytes.Buffer{}, ExportOptions{Seal: func(context.Context, string, []byte) ([]byte, error) { return nil, boom }})
	if !errors.Is(err, boom) {
		t.Errorf("%v", err)
	}
}

func TestExportPackOfAFolder(t *testing.T) {
	ctx := context.Background()
	s, folder, plain, _, secret := packSource(t)
	var buf bytes.Buffer
	rep, err := s.ExportPack(ctx, "src", &buf, ExportOptions{Folder: folder.ID, Seal: fakeSeal})
	if err != nil || rep.Exported != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
	r, _ := pack.Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	var ids []string
	for _, e := range r.Index().Entries {
		ids = append(ids, e.ID)
	}
	slices.Sort(ids)
	want := []string{plain, secret}
	slices.Sort(want)
	if !slices.Equal(ids, want) || r.Meta().Name != "Reports" || len(r.Index().Folders) != 2 {
		t.Errorf("%v %+v", ids, r.Meta())
	}
	for _, f := range r.Index().Folders {
		if f.ID == folder.ID && f.Parent != "" {
			t.Errorf("the exported folder is the pack's top: %+v", f)
		}
	}
	if _, err := s.ExportPack(ctx, "src", &buf, ExportOptions{Folder: docfile.NewID()}); !errors.Is(err, ErrFolder) {
		t.Errorf("%v", err)
	}
}

func exported(t *testing.T) (data []byte, secret, plain, trashed string, folder Folder) {
	t.Helper()
	s, folder, plain, trashed, secret := packSource(t)
	var buf bytes.Buffer
	if _, err := s.ExportPack(context.Background(), "src", &buf, ExportOptions{Seal: fakeSeal}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), secret, plain, trashed, folder
}

func TestImportPackRoundTrip(t *testing.T) {
	ctx := context.Background()
	data, secret, plain, trashed, folder := exported(t)
	dst := &Saver{Targets: []Target{{Name: "dst", Adapter: docstore.NewMemoryAdapter()}}}
	rep, err := dst.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen})
	if err != nil || len(rep.Imported) != 3 || len(rep.Skipped) != 0 || rep.Folders != 2 {
		t.Fatalf("%+v %v", rep, err)
	}
	e, err := dst.Get(ctx, "dst", plain)
	if err != nil || e.Name != "Budget opendocs.io" || !slices.Equal(e.Tags, []string{"finance", "q3"}) || e.Folder == "" || e.Protection != ProtectionServer {
		t.Fatalf("%+v %v", e, err)
	}
	if d, _ := dst.Load(ctx, "dst", plain); textOf(d) != "budget text" {
		t.Errorf("%q", textOf(d))
	}
	if e, _ := dst.Get(ctx, "dst", trashed); e.Trashed.IsZero() {
		t.Errorf("not in the trash: %+v", e)
	}
	e, err = dst.Get(ctx, "dst", secret)
	if err != nil || e.Name != ConfidentialName || len(e.Tags) != 0 || e.Protection != ProtectionE2EE || e.Folder != folder.ID {
		t.Fatalf("%+v %v", e, err)
	}
	if txt := catalogText(t, dst); strings.Contains(txt, "xango") || strings.Contains(txt, secretTag) {
		t.Errorf("the catalog says what is hidden: %s", txt)
	}
	if n, tags, err := dst.Reveal(ctx, "dst", secret); err != nil || n != secretName || !slices.Equal(tags, []string{secretTag}) {
		t.Errorf("%q %v %v", n, tags, err)
	}
	fs, _ := dst.Folders(ctx, "dst")
	if len(fs) != 2 || fs[0].Name != "2026" && fs[1].Name != "2026" {
		t.Errorf("%+v", fs)
	}
	// Again: everything is there, nothing is touched.
	rep, err = dst.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen})
	if err != nil || len(rep.Imported) != 0 || len(rep.Skipped) != 3 || rep.Folders != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	for _, sk := range rep.Skipped {
		if sk.Reason != PackExists {
			t.Errorf("%+v", sk)
		}
	}
}

func TestImportPackWithoutAKeyWarnsAndImportsTheRest(t *testing.T) {
	ctx := context.Background()
	data, secret, plain, _, _ := exported(t)
	dst := &Saver{Targets: []Target{{Name: "dst", Adapter: docstore.NewMemoryAdapter()}}}
	rep, err := dst.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{})
	if err != nil || len(rep.Imported) != 2 || len(rep.Skipped) != 1 || rep.Skipped[0].ID != secret || rep.Skipped[0].Reason != PackNeedsKey {
		t.Fatalf("%+v %v", rep, err)
	}
	if _, err := dst.Get(ctx, "dst", secret); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
	if _, err := dst.Get(ctx, "dst", plain); err != nil {
		t.Error(err)
	}
	// An Open that cannot open it is the same warning, with the reason.
	dst2 := &Saver{Targets: []Target{{Name: "dst", Adapter: docstore.NewMemoryAdapter()}}}
	nokey := errors.New("wrong key")
	rep, err = dst2.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: func(context.Context, string, []byte) ([]byte, error) { return nil, nokey }})
	if err != nil || len(rep.Imported) != 2 || len(rep.Skipped) != 1 || rep.Skipped[0].Reason != PackNeedsKey || !errors.Is(rep.Skipped[0].Err, nokey) {
		t.Fatalf("%+v %v", rep, err)
	}
	// Later, with the key, the one left behind comes in.
	rep, err = dst2.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen})
	if err != nil || len(rep.Imported) != 1 || rep.Imported[0] != secret {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestImportPackRefusesConfidentialWhenTheCheckDoes(t *testing.T) {
	ctx := context.Background()
	data, secret, _, _, _ := exported(t)
	dst := &Saver{Targets: []Target{{Name: "dst", Adapter: docstore.NewMemoryAdapter()}}}
	need := errors.New("a passkey is the only key")
	dst.ConfidentialCheck = func(context.Context, string, string) error { return need }
	rep, err := dst.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen})
	if err != nil || len(rep.Imported) != 2 || len(rep.Skipped) != 1 || rep.Skipped[0].ID != secret || rep.Skipped[0].Reason != PackFailed || !errors.Is(rep.Skipped[0].Err, need) {
		t.Fatalf("%+v %v", rep, err)
	}
}

func TestImportPackDamagedRecordsAndSignature(t *testing.T) {
	ctx := context.Background()
	s, _, plain, _, _ := packSource(t)
	key := ed25519.NewKeyFromSeed([]byte("saver pack test seed 01234567890")[:32])
	var buf bytes.Buffer
	if _, err := s.ExportPack(ctx, "src", &buf, ExportOptions{Seal: fakeSeal, Sign: key}); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	pub := key.Public().(ed25519.PublicKey)
	dst := &Saver{Targets: []Target{{Name: "dst", Adapter: docstore.NewMemoryAdapter()}}}
	other := ed25519.NewKeyFromSeed([]byte("another seed 0123456789012345678")[:32]).Public().(ed25519.PublicKey)
	if _, err := dst.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen, Signer: other}); !errors.Is(err, ErrPackSignature) {
		t.Fatalf("%v", err)
	}
	if l, _ := dst.List(ctx, "dst"); len(l) != 0 {
		t.Error("something was imported from a pack with the wrong signer")
	}
	// A bit flipped in one document's record: that one is reported, the others come in.
	r, _ := pack.Open(bytes.NewReader(data), int64(len(data)))
	var at int
	for _, e := range r.Index().Entries {
		if e.ID == plain {
			at = int(e.Offset) + 16 + int(e.Length)/2
		}
	}
	bad := append([]byte(nil), data...)
	bad[at] ^= 1
	rep, err := dst.ImportPack(ctx, "dst", bytes.NewReader(bad), int64(len(bad)), ImportOptions{Open: fakeOpen})
	if err != nil || len(rep.Imported) != 2 || len(rep.Skipped) != 1 || rep.Skipped[0].ID != plain || rep.Skipped[0].Reason != PackFailed || !errors.Is(rep.Skipped[0].Err, pack.ErrChecksum) {
		t.Fatalf("%+v %v", rep, err)
	}
	// Good signer: all of it.
	dst2 := &Saver{Targets: []Target{{Name: "dst", Adapter: docstore.NewMemoryAdapter()}}}
	rep, err = dst2.ImportPack(ctx, "dst", bytes.NewReader(data), int64(len(data)), ImportOptions{Open: fakeOpen, Signer: pub})
	if err != nil || len(rep.Imported) != 3 {
		t.Fatalf("%+v %v", rep, err)
	}
	// Not a pack at all is an error.
	if _, err := dst2.ImportPack(ctx, "dst", strings.NewReader("hello"), 5, ImportOptions{}); err == nil {
		t.Error("garbage imported")
	}
}

func TestExportPackLongNamesAreCutInTheIndexOnly(t *testing.T) {
	ctx := context.Background()
	s := &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}
	long := strings.Repeat("é", 150) // 300 bytes, 150 characters: a valid name, too long for the index
	e, err := s.Save(ctx, "m", docfile.Open, docWithText(long, "x"))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := s.ExportPack(ctx, "m", &buf, ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	r, _ := pack.Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if n := r.Index().Entries[0].Name; len(n) > pack.MaxName || !strings.HasPrefix(long, n) {
		t.Errorf("%d bytes", len(n))
	}
	dst := &Saver{Targets: []Target{{Name: "m", Adapter: docstore.NewMemoryAdapter()}}}
	if _, err := dst.ImportPack(ctx, "m", bytes.NewReader(buf.Bytes()), int64(buf.Len()), ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := dst.Get(ctx, "m", e.ID); got.Name != long {
		t.Errorf("the name in the document is whole: %q", got.Name)
	}
}
