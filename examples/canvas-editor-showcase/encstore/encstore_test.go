package encstore

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/saver"
)

const target = "browser"

type rig struct {
	raw     *docstore.MemoryAdapter
	st      *Store
	saved   Config
	secret  []byte // the passkey's PRF output
	saver   *saver.Saver
	session *saver.Session
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{raw: docstore.NewMemoryAdapter(), secret: bytes.Repeat([]byte{5}, 32)}
	r.open(t, Config{})
	return r
}

// open is a page load: a Store over the same storage with the config that was persisted.
func (r *rig) open(t *testing.T, cfg Config) {
	t.Helper()
	st, err := New(r.raw, target, cfg)
	if err != nil {
		t.Fatal(err)
	}
	st.Persist = func(c Config) error { r.saved = c; return nil }
	r.st = st
	adapter := docstore.Adapter(r.raw)
	if st.Enabled() {
		adapter = st.Adapter()
	}
	r.saver = &saver.Saver{Targets: []saver.Target{{Name: target, Adapter: adapter}}}
	r.saver.ConfidentialCheck, r.saver.Rekey = st.Check, st.Rekey
	r.session = saver.NewSession(r.saver, func() docfile.Document { return docfile.Document{} }, nil)
	r.session.Pending.Inline = true
	r.session.PackKeys = st.PackKeys
}

func doc(name, text string) docfile.Document {
	d := docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: text}}}}
	d.SetName(name)
	return d
}

// everything the storage holds, as one string: what a thief of the browser's storage reads.
func (r *rig) stored(t *testing.T) string {
	t.Helper()
	var all strings.Builder
	metas, _ := r.raw.List(context.Background())
	for _, m := range metas {
		d, err := r.raw.Load(context.Background(), m.ID)
		if err != nil {
			t.Fatal(err)
		}
		all.Write(d.Data)
	}
	return all.String()
}

func passkey(t *testing.T, r *rig) NewKey {
	k, err := NewPasskeyKey(r.secret, "My passkey")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestPasskeyAloneCannotMakeADocumentConfidentialARecoveryKeyCan(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	// A document saved in the clear before encryption is on.
	e, err := r.saver.Save(ctx, target, docfile.Open, doc("Old plan", "ancient clear text"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.stored(t), "ancient clear text") {
		t.Fatal("setup: the clear document should be readable in the store")
	}
	if err := r.st.Enable(ctx, passkey(t, r)); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved) // the page now uses the encrypted adapter
	if err := r.st.UnlockPasskey(r.secret); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.stored(t), "ancient clear text") {
		t.Fatal("what was saved before is still in the clear")
	}
	if _, _, err := r.saver.LoadChecked(ctx, target, e.ID); err != nil {
		t.Fatalf("the migrated document opens: %v", err)
	}
	// New documents are encrypted at rest too.
	n, err := r.saver.Save(ctx, target, docfile.Open, doc("Salary", "salary text 123"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.stored(t), "salary text 123") {
		t.Fatal("a new document is in the clear")
	}
	// A passkey alone: refused, with the rule's own words.
	err = r.saver.SetProtection(ctx, target, n.ID, saver.ProtectionE2EE)
	if !errors.Is(err, encrypt.ErrNeedSecondRecipient) {
		t.Fatalf("passkey alone: %v", err)
	}
	// A recovery key as a second owner key: allowed, and now every document opens with it alone.
	rk, secret, err := NewRecoveryKey("Recovery key")
	if err != nil {
		t.Fatal(err)
	}
	rk.Unlock = nil
	if err := r.st.AddOwner(ctx, rk); err != nil {
		t.Fatal(err)
	}
	if err := r.saver.SetProtection(ctx, target, n.ID, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"Salary", "salary text 123"} {
		if strings.Contains(r.stored(t), s) {
			t.Fatalf("%q is in the store", s)
		}
	}
	if got, _ := r.saver.Get(ctx, target, n.ID); got.Name != saver.ConfidentialName {
		t.Fatalf("%+v", got)
	}

	// Lock: nothing opens, the list shows the placeholder, saving says it is locked.
	r.st.Lock()
	if _, _, err := r.saver.Reveal(ctx, target, n.ID); !errors.Is(err, docstore.ErrLocked) {
		t.Fatalf("locked reveal: %v", err)
	}
	if _, err := r.saver.Save(ctx, target, docfile.Open, doc("New", "x")); !errors.Is(err, docstore.ErrLocked) {
		t.Fatalf("locked save: %v", err)
	}
	if _, _, err := r.saver.Reveal(ctx, target, n.ID); err == nil {
		t.Fatal("opened while locked")
	}

	// A reload: the page has the persisted keys (public) and starts locked. The wrong key is refused.
	r.open(t, r.saved)
	if !r.st.Enabled() || r.st.Unlocked() {
		t.Fatal("a fresh page should be locked")
	}
	if err := r.st.UnlockPasskey(bytes.Repeat([]byte{6}, 32)); !errors.Is(err, ErrNotAKey) {
		t.Fatalf("another passkey: %v", err)
	}
	other, _, _ := NewRecoveryKey("x")
	_ = other
	if err := r.st.UnlockRecovery("AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQQ"); err == nil {
		t.Fatal("a nonsense key unlocked it")
	}
	// The recovery key alone opens everything, including the confidential name.
	if err := r.st.UnlockRecovery(secret); err != nil {
		t.Fatal(err)
	}
	if name, _, err := r.saver.Reveal(ctx, target, n.ID); err != nil || name != "Salary" {
		t.Fatalf("%q %v", name, err)
	}
	// Making it not confidential again moves the name back out.
	if err := r.saver.SetProtection(ctx, target, n.ID, saver.ProtectionServer); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.saver.Get(ctx, target, n.ID); got.Name != "Salary" {
		t.Fatalf("%+v", got)
	}
}

func TestSSHKeyOwnsAndUnlocksTheStore(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	pub, priv := sshKeyPair(t, "me@laptop")
	k, err := NewSSHKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	rk, _, err := NewRecoveryKey("Recovery key")
	if err != nil {
		t.Fatal(err)
	}
	rk.Unlock = nil
	// Only the recovery key and the SSH public key are known: nothing carries a private side, so
	// the store starts locked, and the private key file unlocks it.
	if _, err := r.saver.Save(ctx, target, docfile.Open, doc("Before", "text from before")); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Enable(ctx, k, rk); err != nil {
		t.Fatal(err)
	}
	if !r.st.Enabled() || r.st.Unlocked() || strings.Contains(r.stored(t), "text from before") {
		t.Fatalf("enabled %v unlocked %v", r.st.Enabled(), r.st.Unlocked())
	}
	r.open(t, r.saved)
	if err := r.st.UnlockSSH(priv); err != nil {
		t.Fatal(err)
	}
	e, err := r.saver.Save(ctx, target, docfile.Open, doc("Contract", "contract text"))
	if err != nil {
		t.Fatal(err)
	}
	// The SSH key is not a passkey, so it is enough for the rule.
	if err := r.saver.SetProtection(ctx, target, e.ID, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	r.st.Lock()
	_, otherPriv := sshKeyPair(t, "stranger")
	if err := r.st.UnlockSSH(otherPriv); !errors.Is(err, ErrNotAKey) {
		t.Fatalf("a stranger's key: %v", err)
	}
	if err := r.st.UnlockSSH(priv); err != nil {
		t.Fatal(err)
	}
	if n, _, err := r.saver.Reveal(ctx, target, e.ID); err != nil || n != "Contract" {
		t.Fatalf("%q %v", n, err)
	}
	// An RSA public key cannot be an owner here (it cannot sign the lists).
	if _, err := NewSSHKey("ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQC7 x"); err == nil {
		t.Fatal("an unparsable key was accepted")
	}
}

func TestPackSealedForTheOwnerKeysAndOpenedAgain(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	pk := passkey(t, r)
	rk, secret, _ := NewRecoveryKey("Recovery key")
	rk.Unlock = nil
	if err := r.st.Enable(ctx, pk, rk); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved)
	if seal, open := r.st.PackKeys(); seal != nil || open != nil {
		t.Fatal("a locked store has no pack keys")
	}
	_ = r.st.UnlockRecovery(secret)
	e, _ := r.saver.Save(ctx, target, docfile.Open, doc("Merger xango", "merger text"))
	if err := r.saver.SetProtection(ctx, target, e.ID, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	var file []byte
	r.session.Deliver = func(_, _ string, b []byte) { file = b }
	if err := r.session.SetTarget(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := r.session.DownloadPack(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(file, []byte("Merger")) || bytes.Contains(file, []byte("merger text")) {
		t.Fatal("the pack shows the document")
	}
	if st := r.session.Props().Status; strings.Contains(st, "left out") {
		t.Fatalf("%q", st)
	}
	sealed := bytes.Clone(file)
	// Locked: the same document is left out and counted.
	r.st.Lock()
	if err := r.session.DownloadPack(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if st := r.session.Props().Status; !strings.Contains(st, "1 confidential documents left out") {
		t.Fatalf("%q", st)
	}
	// Delete everything, then import the sealed pack with the store unlocked.
	_ = r.st.UnlockRecovery(secret)
	if err := r.saver.Purge(ctx, target, e.ID); err != nil {
		// not trashed yet
		if err := r.saver.Trash(ctx, target, e.ID); err != nil {
			t.Fatal(err)
		}
		if err := r.saver.Purge(ctx, target, e.ID); err != nil {
			t.Fatal(err)
		}
	}
	r.st.Lock()
	if err := r.session.ImportPackData(ctx, "p.opendoc", sealed); err != nil {
		t.Fatal(err)
	}
	if st := r.session.Props().Status; !strings.Contains(st, "need a key") {
		t.Fatalf("locked import: %q", st)
	}
	_ = r.st.UnlockRecovery(secret)
	if err := r.session.ImportPackData(ctx, "p.opendoc", sealed); err != nil {
		t.Fatal(err)
	}
	if n, _, err := r.saver.Reveal(ctx, target, e.ID); err != nil || n != "Merger xango" {
		t.Fatalf("%q %v", n, err)
	}
	if strings.Contains(r.stored(t), "Merger xango") {
		t.Fatal("the name is in the store after the import")
	}
}

func TestDisableDecryptsEverythingBack(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	pk := passkey(t, r)
	rk, _, _ := NewRecoveryKey("Recovery key")
	rk.Unlock = nil
	if err := r.st.Enable(ctx, pk, rk); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved)
	_ = r.st.UnlockPasskey(r.secret)
	e, _ := r.saver.Save(ctx, target, docfile.Open, doc("Plan", "plain after disable"))
	if strings.Contains(r.stored(t), "plain after disable") {
		t.Fatal("not encrypted")
	}
	if err := r.st.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if r.st.Enabled() || len(r.saved.Owners) != 0 {
		t.Fatalf("still on: %+v", r.saved)
	}
	// The store is a plain one again, opened with no key.
	r.open(t, r.saved)
	if _, _, err := r.saver.LoadChecked(ctx, target, e.ID); err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(r.stored(t), "plain after disable") {
		t.Error("the document was not written back in the clear")
	}
	// Disabling a locked store is refused; a store with encryption off says so.
	if err := r.st.Disable(ctx); !errors.Is(err, ErrDisabled) {
		t.Fatalf("%v", err)
	}
}

func TestLockedAdapterRefusesToWrite(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	pk := passkey(t, r)
	if err := r.st.Enable(ctx, pk); err != nil {
		t.Fatal(err)
	}
	r.st.Lock()
	err := r.st.Adapter().Save(ctx, "00000000-0000-4000-8000-000000000001.open", []byte("x"))
	if !errors.Is(err, docstore.ErrLocked) {
		t.Fatalf("%v", err)
	}
	if err := r.st.Check(ctx, target, "x"); !errors.Is(err, docstore.ErrLocked) {
		t.Fatalf("%v", err)
	}
	if err := r.st.Check(ctx, "memory", "x"); !errors.Is(err, ErrNotThisStore) {
		t.Fatalf("%v", err)
	}
	if err := r.st.Rekey(ctx, "memory", "x", "e2ee"); err != nil {
		t.Fatalf("another target has nothing to re-key: %v", err)
	}
}

func sshKeyPair(t *testing.T, comment string) (pub string, priv []byte) {
	t.Helper()
	p, k, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(p)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKey(k, comment)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " " + comment, pem.EncodeToMemory(blk)
}
