package encstore

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/saver"
)

const target = "browser"

// faultyRaw is the storage under the store, with failures on demand: the nth write fails (or, with
// failFrom, every write from the nth on, which is a crash as far as the store can tell), the nth
// write is silently truncated (a store that lies), and a byte quota like the browser's.
type faultyRaw struct {
	inner docstore.Adapter

	mu       sync.Mutex
	writes   int
	failAt   int
	failFrom bool
	lieAt    int
	limit    int
	loadHook func(id string)
}

func (f *faultyRaw) used() int {
	metas, _ := f.inner.List(context.Background())
	n := 0
	for _, m := range metas {
		n += m.Size
	}
	return n
}

// write counts one write and says what to do with it: fail it, truncate it, or let it through.
func (f *faultyRaw) write(id string, data []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.writes++
	if f.failAt > 0 && (f.writes == f.failAt || f.failFrom && f.writes > f.failAt) {
		return nil, fmt.Errorf("injected failure at write %d (%s)", f.writes, id)
	}
	if f.lieAt > 0 && f.writes == f.lieAt && len(data) > 4 {
		return data[:len(data)/2], nil
	}
	if f.limit > 0 {
		cur := 0
		if d, err := f.inner.Load(context.Background(), id); err == nil {
			cur = len(d.Data)
		}
		if f.used()-cur+len(data) > f.limit {
			return nil, fmt.Errorf("%w: quota", docstore.ErrStorageFull)
		}
	}
	return data, nil
}

func (f *faultyRaw) Save(ctx context.Context, id string, data []byte) error {
	data, err := f.write(id, data)
	if err != nil {
		return err
	}
	return f.inner.Save(ctx, id, data)
}

func (f *faultyRaw) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	data, err := f.write(id, data)
	if err != nil {
		return err
	}
	return f.inner.(docstore.ConditionalAdapter).SaveIf(ctx, id, data, prev)
}

func (f *faultyRaw) Delete(ctx context.Context, id string) error {
	f.mu.Lock()
	f.writes++
	fail := f.failAt > 0 && (f.writes == f.failAt || f.failFrom && f.writes > f.failAt)
	f.mu.Unlock()
	if fail {
		return fmt.Errorf("injected failure at delete %s", id)
	}
	return f.inner.Delete(ctx, id)
}

func (f *faultyRaw) Load(ctx context.Context, id string) (docstore.Document, error) {
	if f.loadHook != nil {
		f.loadHook(id)
	}
	return f.inner.Load(ctx, id)
}

func (f *faultyRaw) List(ctx context.Context) ([]docstore.Meta, error) { return f.inner.List(ctx) }

func (f *faultyRaw) heal() {
	f.mu.Lock()
	f.failAt, f.failFrom, f.lieAt = 0, false, 0
	f.mu.Unlock()
}

type rig struct {
	raw     *faultyRaw
	st      *Store
	saved   Config
	persist int
	// history is every Config that was persisted, in order (the first of a change is its journal).
	history []Config
	// failPersist, when set, is asked at each Persist (1 is the first since persist was reset).
	failPersist func(n int) error
	secret      []byte // the passkey's PRF output
	saver       *saver.Saver
	session     *saver.Session
}

func newRig(t *testing.T) *rig { return newRigOver(t, docstore.NewMemoryAdapter()) }

// newRigOver is newRig over the given storage.
func newRigOver(t *testing.T, inner docstore.Adapter) *rig {
	t.Helper()
	r := &rig{raw: &faultyRaw{inner: inner}, secret: bytes.Repeat([]byte{5}, 32)}
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
	st.Persist = func(c Config) error {
		r.persist++
		if r.failPersist != nil {
			if err := r.failPersist(r.persist); err != nil {
				return err
			}
		}
		r.saved = c
		r.history = append(r.history, c)
		return nil
	}
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
	for _, b := range r.snapshot(t) {
		all.WriteString(b)
	}
	return all.String()
}

// snapshot is every stored item, by id.
func (r *rig) snapshot(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	metas, _ := r.raw.inner.List(context.Background())
	for _, m := range metas {
		d, err := r.raw.inner.Load(context.Background(), m.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[m.ID] = string(d.Data)
	}
	return out
}

func sameSnapshot(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d stored items, want %d", what, len(got), len(want))
	}
	for id, w := range want {
		if g, ok := got[id]; !ok || g != w {
			t.Errorf("%s: %s is not as it was (present %v)", what, id, ok)
		}
	}
}

func passkey(t *testing.T, r *rig) NewKey {
	t.Helper()
	k, err := NewPasskeyKey(r.secret, "My passkey")
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func passkeyFrom(t *testing.T, secret []byte, label string) NewKey {
	t.Helper()
	k, err := NewPasskeyKey(secret, label)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// recovery makes a recovery key whose secret was shown and saved, and that is not kept unlocked.
func recovery(t *testing.T) (NewKey, string) {
	t.Helper()
	k, secret, err := NewRecoveryKey("Recovery key")
	if err != nil {
		t.Fatal(err)
	}
	k.SecretSaved, k.Forget = true, true
	return k, secret
}

func (r *rig) unlockPasskey(t *testing.T) {
	t.Helper()
	if err := r.st.UnlockPasskey(bytes.Clone(r.secret)); err != nil {
		t.Fatal(err)
	}
}

var onlyPasskeys = Options{AcceptPasskeyOnly: true}

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
	if err := r.st.Enable(ctx, onlyPasskeys, passkey(t, r)); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved) // the page now uses the encrypted adapter
	r.unlockPasskey(t)
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
	rk, secret := recovery(t)
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
	rk, _ := recovery(t)
	// Only the recovery key and the SSH public key are known: nothing carries a private side, so
	// the store starts locked, and the private key file unlocks it.
	if _, err := r.saver.Save(ctx, target, docfile.Open, doc("Before", "text from before")); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Enable(ctx, Options{}, k, rk); err != nil {
		t.Fatal(err)
	}
	if !r.st.Enabled() || r.st.Unlocked() || strings.Contains(r.stored(t), "text from before") {
		t.Fatalf("enabled %v unlocked %v", r.st.Enabled(), r.st.Unlocked())
	}
	r.open(t, r.saved)
	if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
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
	if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
		t.Fatal(err)
	}
	if n, _, err := r.saver.Reveal(ctx, target, e.ID); err != nil || n != "Contract" {
		t.Fatalf("%q %v", n, err)
	}
	// The unlocked key's private side opens a challenge (proof of possession); locked, none is given.
	if id, ok := r.st.IdentityFor(k.Recipient.Text); !ok || id == nil {
		t.Fatal("the unlocked SSH key has no identity")
	}
	if _, ok := r.st.IdentityFor(rk.Recipient.Text); ok {
		t.Fatal("an identity for the recovery key, which is not held")
	}
	r.st.Lock()
	if _, ok := r.st.IdentityFor(k.Recipient.Text); ok {
		t.Fatal("an identity while locked")
	}
	if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
		t.Fatal(err)
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
	rk, secret := recovery(t)
	if err := r.st.Enable(ctx, Options{}, pk, rk); err != nil {
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
	if st := r.session.Props().Status; !strings.Contains(st, "need a key") || strings.Contains(st, "another key") {
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

// A pack sealed for another store's keys, imported while this store is unlocked, says it was
// sealed for another key: unlocking again would not help, so the message must not say it would.
func TestPackSealedForAnotherKeySaysSo(t *testing.T) {
	ctx := context.Background()
	mk := func(seed byte) (*rig, string) {
		r := newRig(t)
		r.secret = bytes.Repeat([]byte{seed}, 32)
		rk, secret := recovery(t)
		if err := r.st.Enable(ctx, Options{}, passkey(t, r), rk); err != nil {
			t.Fatal(err)
		}
		r.open(t, r.saved)
		if err := r.st.UnlockRecovery(secret); err != nil {
			t.Fatal(err)
		}
		return r, secret
	}
	a, _ := mk(1)
	e, _ := a.saver.Save(ctx, target, docfile.Open, doc("Other people's", "text"))
	if err := a.saver.SetProtection(ctx, target, e.ID, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	var file []byte
	a.session.Deliver = func(_, _ string, b []byte) { file = b }
	_ = a.session.SetTarget(ctx, target)
	if err := a.session.DownloadPack(ctx, ""); err != nil {
		t.Fatal(err)
	}
	b, _ := mk(2)
	if err := b.session.ImportPackData(ctx, "p.opendoc", file); err != nil {
		t.Fatal(err)
	}
	st := b.session.Props().Status
	if !strings.Contains(st, "sealed for another key") || strings.Contains(st, "unlock the store") {
		t.Fatalf("%q", st)
	}
}

func TestDisableDecryptsEverythingBack(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	pk := passkey(t, r)
	rk, _ := recovery(t)
	if err := r.st.Enable(ctx, Options{}, pk, rk); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved)
	r.unlockPasskey(t)
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
	// A store with encryption off says so.
	if err := r.st.Disable(ctx); !errors.Is(err, ErrDisabled) {
		t.Fatalf("%v", err)
	}
}

// Disable refuses while a document is still confidential (the doc says the caller checks; the
// store enforces it too, so its error cannot be skipped by a caller that forgets).
func TestDisableRefusesWhileSomethingIsConfidential(t *testing.T) {
	ctx := context.Background()
	r, secret := encryptedRig(t)
	e, _ := r.saver.Save(ctx, target, docfile.Open, doc("Secret", "secret text"))
	if err := r.saver.SetProtection(ctx, target, e.ID, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	before := r.snapshot(t)
	if err := r.st.Disable(ctx); !errors.Is(err, ErrConfidentialLeft) {
		t.Fatalf("%v", err)
	}
	sameSnapshot(t, "after the refusal", r.snapshot(t), before)
	if !r.st.Enabled() {
		t.Fatal("encryption was turned off")
	}
	_ = secret
	if err := r.saver.SetProtection(ctx, target, e.ID, saver.ProtectionServer); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Disable(ctx); err != nil {
		t.Fatalf("after making it not confidential: %v", err)
	}
}

func TestLockedAdapterRefusesToWrite(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	pk := passkey(t, r)
	if err := r.st.Enable(ctx, onlyPasskeys, pk); err != nil {
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

// encryptedRig is a store that is on, with a passkey and a recovery key, unlocked with the passkey
// (after a reload, as a page would be), and the recovery key's secret.
func encryptedRig(t *testing.T) (*rig, string) {
	t.Helper()
	r := newRig(t)
	rk, secret := recovery(t)
	if err := r.st.Enable(context.Background(), Options{}, passkey(t, r), rk); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved)
	r.unlockPasskey(t)
	return r, secret
}

// withPlainDocs saves three documents in the clear (one in a folder's versions too) before
// encryption.
func withPlainDocs(t *testing.T, r *rig) {
	t.Helper()
	ctx := context.Background()
	for _, n := range []string{"alpha", "beta", "gamma"} {
		e, err := r.saver.Save(ctx, target, docfile.Open, doc("Doc "+n, "text of "+n))
		if err != nil {
			t.Fatal(err)
		}
		v := doc("Doc "+n, "version of "+n)
		v.SetID(e.ID)
		if _, err := r.saver.SaveVersion(ctx, target, docfile.Open, v); err != nil {
			t.Fatal(err)
		}
	}
}

// CheckRecoveryKey tells the right recovery key from another key and from rubbish, without unlocking
// the store or keeping the secret.
func TestCheckRecoveryKey(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	rk, secret := recovery(t)
	if err := r.st.Enable(ctx, onlyPasskeys, passkey(t, r), rk); err != nil {
		t.Fatal(err)
	}
	r.st.Lock()
	if err := r.st.CheckRecoveryKey("  " + secret + "\n"); err != nil {
		t.Errorf("the right key: %v", err)
	}
	if r.st.Unlocked() {
		t.Error("checking a key unlocked the store")
	}
	_, other, _ := NewRecoveryKey("another")
	if err := r.st.CheckRecoveryKey(other); !errors.Is(err, ErrNotAKey) {
		t.Errorf("another recovery key: %v", err)
	}
	for _, bad := range []string{"", "hello", "AGE-SECRET-KEY-1NOPE"} {
		if err := r.st.CheckRecoveryKey(bad); err == nil || errors.Is(err, ErrNotAKey) {
			t.Errorf("%q: %v, want a parse error", bad, err)
		}
	}
}

// With ConfirmFirstUse the first unlock with an SSH key of a list nothing on this device sealed stops and
// shows the list's fingerprint; once the person confirmed it (TrustList) the unlock goes through, and a
// different list is a different fingerprint. A key that opened the list through the chain of seals needs
// no confirmation for the SSH key afterwards.
func TestFirstUnlockWithAnSSHKeyAsksForTheListsFingerprint(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	pub, priv := sshKeyPair(t, "me@laptop")
	k, _ := NewSSHKey(pub)
	rk, rsecret := recovery(t)
	if err := r.st.Enable(ctx, Options{}, k, rk); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved) // a page load on another device: nothing sealed here
	r.st.ConfirmFirstUse = true
	var kept []string
	r.st.OnTrust = func(d string) { kept = append(kept, d) }

	err := r.st.UnlockSSH(bytes.Clone(priv))
	var fu *FirstUseError
	if !errors.As(err, &fu) || !errors.Is(err, ErrFirstUse) || fu.Fingerprint == "" || strings.Count(fu.Fingerprint, " ") != 7 {
		t.Fatalf("first use: %v", err)
	}
	if r.st.Unlocked() {
		t.Fatal("the store unlocked before the list was confirmed")
	}
	r.st.TrustList(fu.Digest)
	if len(kept) != 1 || kept[0] != fu.Digest {
		t.Errorf("OnTrust: %v", kept)
	}
	if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
		t.Fatalf("after confirming: %v", err)
	}
	// the key is sealed now: no question again, even on a fresh page load
	r.open(t, r.saved)
	r.st.ConfirmFirstUse = true
	if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
		t.Errorf("a sealed key is not asked again: %v", err)
	}

	// unlocking with the recovery key (which proves the list through the seals) trusts the list for the
	// SSH key too
	r2 := newRig(t)
	pub2, priv2 := sshKeyPair(t, "me@desk")
	k2, _ := NewSSHKey(pub2)
	rk2, rsecret2 := recovery(t)
	if err := r2.st.Enable(ctx, Options{}, k2, rk2); err != nil {
		t.Fatal(err)
	}
	r2.open(t, r2.saved)
	r2.st.ConfirmFirstUse = true
	if err := r2.st.UnlockRecovery(rsecret2); err != nil {
		t.Fatal(err)
	}
	r2.st.Lock()
	if err := r2.st.UnlockSSH(bytes.Clone(priv2)); err != nil {
		t.Errorf("the recovery key vouched for the list: %v", err)
	}
	_ = rsecret
}

func TestListFingerprintGroupsTheDigest(t *testing.T) {
	d := strings.Repeat("0123456789abcdef", 4)
	if got := ListFingerprint(d); got != "0123 4567 89ab cdef 0123 4567 89ab cdef" {
		t.Errorf("fingerprint %q", got)
	}
}
