package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"slices"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

const folderSecret = "Layoffs 2026 xango"

// rawHolds lists the stored items (read straight from the inner store, as whoever runs it
// would) that contain s in the clear.
func rawHolds(t *testing.T, a docstore.Adapter, s string) []string {
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

func TestFolderSecretRecordIsNotSkippedAndBelongsToItsFolder(t *testing.T) {
	id := docfile.NewID()
	key := saver.FolderSecretKey(id)
	if DefaultSkip(key) {
		t.Error("the encrypted folder record must be wrapped, not stored as it is")
	}
	if DocID(key) != id {
		t.Errorf("it follows the folder's own keys: %q", DocID(key))
	}
	if !DefaultSkip(saver.FolderRecordPrefix + id) {
		t.Error("the plain folder record (placeholder and shape) stays readable")
	}
	if DocID(docstore.FolderSecretPrefix+"not-an-id") != "" {
		t.Error("only a folder ID names a record")
	}
	if groupOf(key) != id {
		t.Error("re-keys group it with the folder")
	}
}

// folderWorld is newWorld with a folder tree: Work > secretFolder > a document.
func folderWorld(t *testing.T) (w *world, top, sec, doc string) {
	t.Helper()
	ctx := context.Background()
	w = newWorld(t)
	f1, err := w.saver.CreateFolder(ctx, "m", "", "Work")
	if err != nil {
		t.Fatal(err)
	}
	f2, err := w.saver.CreateFolder(ctx, "m", f1.ID, folderSecret)
	if err != nil {
		t.Fatal(err)
	}
	d := docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: "ordinary text"}}}}
	d.SetName("Plan")
	e, err := w.saver.Save(ctx, "m", docfile.Open, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.saver.Move(ctx, "m", f2.ID, e.ID); err != nil {
		t.Fatal(err)
	}
	return w, f1.ID, f2.ID, e.ID
}

func TestConfidentialFolderNameIsInsideTheEnvelopeForPeopleOnly(t *testing.T) {
	ctx := context.Background()
	w, top, sec, doc := folderWorld(t)
	if got := rawHolds(t, w.inner, folderSecret); len(got) != 2 || !slices.Contains(got, saver.FolderRecordPrefix+sec) {
		t.Fatalf("server level: the name is in the readable catalog and folder record: %v", got)
	}
	if err := w.saver.SetFolderProtection(ctx, "m", sec, saver.ProtectionE2EE, false); err != nil {
		t.Fatal(err)
	}
	// The store holds no real folder name, anywhere: not the catalog, not the folder record.
	if got := rawHolds(t, w.inner, folderSecret); len(got) != 0 {
		t.Fatalf("the store holds the real name: %v", got)
	}
	raw, err := w.inner.Load(ctx, saver.FolderSecretKey(sec))
	if err != nil || !IsEncrypted(raw.Data) {
		t.Fatalf("the record is ciphertext: %v", err)
	}
	// Wrapped for people's keys only, never the server's (which is also among the owners here).
	if !w.opens(sec, saver.FolderSecretKey(sec), w.ana.id) {
		t.Error("the owner opens it")
	}
	if w.opens(sec, saver.FolderSecretKey(sec), w.server) {
		t.Error("the server key opens a confidential folder's name")
	}
	if c, err := w.ring.IsConfidential(ctx, sec); err != nil || !c {
		t.Errorf("%v %v", c, err)
	}
	// The ordinary folder is untouched, and the document inside is not made confidential.
	if n, _ := w.saver.RevealFolder(ctx, "m", top); n != "Work" {
		t.Errorf("%q", n)
	}
	if e, _ := w.saver.Get(ctx, "m", doc); e.Protection != saver.ProtectionServer || e.Folder != sec {
		t.Errorf("%+v", e)
	}
	if n, err := w.saver.RevealFolder(ctx, "m", sec); err != nil || n != folderSecret {
		t.Errorf("%q %v", n, err)
	}

	// Rename: the new name goes into the record, still wrapped for people only.
	if err := w.saver.RenameFolder(ctx, "m", sec, "Hiring xango2"); err != nil {
		t.Fatal(err)
	}
	if got := rawHolds(t, w.inner, "Hiring xango2"); len(got) != 0 {
		t.Errorf("%v", got)
	}
	if w.opens(sec, saver.FolderSecretKey(sec), w.server) || !w.opens(sec, saver.FolderSecretKey(sec), w.ana.id) {
		t.Error("after a rename the record is still for people only")
	}
	if n, _ := w.saver.RevealFolder(ctx, "m", sec); n != "Hiring xango2" {
		t.Errorf("%q", n)
	}

	// Move keeps the shape visible and the name hidden; breadcrumbs show the placeholder.
	other, _ := w.saver.CreateFolder(ctx, "m", "", "Other")
	if err := w.saver.MoveFolder(ctx, "m", sec, other.ID); err != nil {
		t.Fatal(err)
	}
	fs, _ := w.saver.Folders(ctx, "m")
	path := saver.FolderPath(fs, sec)
	if len(path) != 2 || path[0].Name != "Other" || path[1].Name != saver.ConfidentialFolderName {
		t.Errorf("%+v", path)
	}

	// A stranger (no key at all) lists, rebuilds and verifies; sees the placeholder in its place.
	stranger := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: New(w.inner, Static{})}}}
	if err := stranger.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	if _, err := stranger.Verify(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	fs, _ = stranger.Folders(ctx, "m")
	i := slices.IndexFunc(fs, func(f saver.Folder) bool { return f.ID == sec })
	if i < 0 || fs[i].Name != saver.ConfidentialFolderName || fs[i].Parent != other.ID || fs[i].Protection != saver.ProtectionE2EE {
		t.Fatalf("%+v", fs)
	}
	if _, err := stranger.RevealFolder(ctx, "m", sec); !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("%v", err)
	}
	if e, err := stranger.Get(ctx, "m", doc); err != nil || e.Folder != sec || e.Name != "Plan" {
		t.Errorf("%+v %v", e, err)
	}
	// Search, listings and a session's props over the stranger's view leak no name.
	ss := saver.NewSession(stranger, func() docfile.Document { return docfile.Document{} }, nil)
	ss.Pending.Inline = true
	if err := ss.SetTarget(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	p := ss.Docs()
	var seen []string
	for _, f := range p.AllFolders {
		seen = append(seen, f.Name)
	}
	for _, f := range p.Folders {
		seen = append(seen, f.Name)
	}
	for _, d := range p.Docs {
		seen = append(seen, d.Name)
	}
	for _, s := range seen {
		if strings.Contains(s, "xango") {
			t.Errorf("a name leaked: %q", s)
		}
	}
	for _, e := range []string{"Hiring", "xango", "Layoffs"} {
		if got := rawHolds(t, w.inner, e); len(got) != 0 {
			t.Errorf("%q is in the store: %v", e, got)
		}
	}

	// A rebuild by the owner, then delete (the document leaves first): the record goes too.
	if err := w.saver.Rebuild(ctx, "m"); err != nil {
		t.Fatal(err)
	}
	if err := w.saver.Trash(ctx, "m", doc); err != nil {
		t.Fatal(err)
	}
	if err := w.saver.DeleteFolder(ctx, "m", sec); err != nil {
		t.Fatal(err)
	}
	if _, err := w.inner.Load(ctx, saver.FolderSecretKey(sec)); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the record should be gone: %v", err)
	}
}

func TestConfidentialFolderBackToServerAndRecursive(t *testing.T) {
	ctx := context.Background()
	w, top, sec, doc := folderWorld(t)
	if err := w.saver.SetFolderProtection(ctx, "m", top, saver.ProtectionE2EE, true); err != nil {
		t.Fatal(err)
	}
	// The documents inside went confidential too, wrapped for people only.
	w.assertOnly(t, doc, "recursive", []age.Identity{w.ana.id}, []age.Identity{w.server})
	for _, id := range []string{top, sec} {
		if w.opens(id, saver.FolderSecretKey(id), w.server) || !w.opens(id, saver.FolderSecretKey(id), w.ana.id) {
			t.Errorf("%s: for people only", id)
		}
	}
	if got := rawHolds(t, w.inner, folderSecret); len(got) != 0 {
		t.Errorf("%v", got)
	}
	if err := w.saver.SetFolderProtection(ctx, "m", top, saver.ProtectionServer, true); err != nil {
		t.Fatal(err)
	}
	w.assertOnly(t, doc, "back", []age.Identity{w.server, w.ana.id}, nil)
	if got := rawHolds(t, w.inner, folderSecret); len(got) != 2 || !slices.Contains(got, saver.FolderRecordPrefix+sec) {
		t.Errorf("the name is back in the readable record: %v", got)
	}
	if _, err := w.inner.Load(ctx, saver.FolderSecretKey(sec)); !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the record should be gone: %v", err)
	}
}

func TestOwnerOpensAConfidentialFolderWithPasskeyAloneOrRecoveryAlone(t *testing.T) {
	ctx := context.Background()
	secret := bytes.Repeat([]byte{21}, 32)
	pk, err := PRFRecipient(secret, "phone", "cred-21")
	if err != nil {
		t.Fatal(err)
	}
	pkID, _ := PRFIdentity(secret)
	pub, recSecret, _ := GenerateRecoveryKey()
	rec, _ := ParseAgeRecipient(pub)
	rec.Label = "recovery"
	recID, _ := ParseAgeIdentity(recSecret)

	inner := docstore.NewMemoryAdapter()
	server, _ := age.GenerateX25519Identity()
	sg, _ := NewSigner(bytes.Repeat([]byte{11}, 32))
	ring := ConfidentialKeyring{
		People: StoreKeyring{Store: inner, Owners: []age.Recipient{pk.Key, rec.Key}, Keys: []age.Identity{pkID, recID},
			OwnerSigners: []ed25519.PublicKey{sg.Public()}},
		AtRest:           Static{To: []age.Recipient{server.Recipient()}, Keys: []age.Identity{server}},
		ServerRecipients: []age.Recipient{server.Recipient()},
	}
	ad := New(inner, ring)
	sh := &Sharing{Store: inner, Adapter: ad, Signer: sg}
	s := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: ad}}}
	s.ConfidentialCheck = ConfidentialCheck(sh, pk, rec)
	s.Rekey = RekeyHook(sh)

	f, err := s.CreateFolder(ctx, "m", "", folderSecret)
	if err != nil {
		t.Fatal(err)
	}
	// A passkey alone is not enough to make it confidential (C.3): the check is the same.
	s.ConfidentialCheck = ConfidentialCheck(sh, pk)
	if err := s.SetFolderProtection(ctx, "m", f.ID, saver.ProtectionE2EE, false); !errors.Is(err, ErrNeedSecondRecipient) {
		t.Fatalf("%v", err)
	}
	if n, _ := s.RevealFolder(ctx, "m", f.ID); n != folderSecret {
		t.Fatal("a refused switch changes nothing")
	}
	s.ConfidentialCheck = ConfidentialCheck(sh, pk, rec)
	if err := s.SetFolderProtection(ctx, "m", f.ID, saver.ProtectionE2EE, false); err != nil {
		t.Fatal(err)
	}
	if got := rawHolds(t, inner, folderSecret); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	for name, k := range map[string]age.Identity{"passkey PRF": pkID, "recovery key": recID} {
		only := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: New(inner, Static{Keys: []age.Identity{k}})}}}
		if n, err := only.RevealFolder(ctx, "m", f.ID); err != nil || n != folderSecret {
			t.Errorf("%s: %q %v", name, n, err)
		}
	}
	srv := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: New(inner, Static{Keys: []age.Identity{server}})}}}
	if _, err := srv.RevealFolder(ctx, "m", f.ID); !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("the server key: %v", err)
	}
	// A rename by the owner holding only the recovery key still writes for people only.
	recOnly := New(inner, ConfidentialKeyring{
		People: StoreKeyring{Store: inner, Owners: []age.Recipient{pk.Key, rec.Key}, Keys: []age.Identity{recID},
			OwnerSigners: []ed25519.PublicKey{sg.Public()}},
		AtRest: Static{To: []age.Recipient{server.Recipient()}}, ServerRecipients: []age.Recipient{server.Recipient()},
	})
	rs := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: recOnly}}}
	if err := rs.RenameFolder(ctx, "m", f.ID, "Renamed xango5"); err != nil {
		t.Fatal(err)
	}
	if n, err := (&saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: New(inner, Static{Keys: []age.Identity{pkID}})}}}).RevealFolder(ctx, "m", f.ID); err != nil || n != "Renamed xango5" {
		t.Errorf("%q %v", n, err)
	}
	if got := rawHolds(t, inner, "xango5"); len(got) != 0 {
		t.Errorf("%v", got)
	}
}
