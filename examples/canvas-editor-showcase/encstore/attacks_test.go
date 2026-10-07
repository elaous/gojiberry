package encstore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// The attacks of the review of PR #81, each made the way someone who can write the page's
// localStorage (and nothing else: no key's secret) would make it.

// anchoredRig is an encrypted store with documents that were saved while it was unlocked, so their
// frames are signed and carry the hash of the owner list: a passkey and a recovery key own it, and
// the passkey is unlocked after a reload.
func anchoredRig(t *testing.T) (*rig, string) {
	t.Helper()
	r := newRig(t)
	withPlainDocs(t, r)
	rk, secret := recovery(t)
	if err := r.st.Enable(context.Background(), Options{}, passkey(t, r), rk); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved)
	r.unlockPasskey(t)
	r.history, r.persist, r.raw.writes = nil, 0, 0
	return r, secret
}

func linkBy(t *testing.T, k NewKey, from, to []Owner) *Link {
	t.Helper()
	f, d := ownersDigest(from), ownersDigest(to)
	return &Link{From: f, To: slicesClone(to), Signer: hexOf(k.SignerPub), Sig: hexSig(k.Unlock.Signer.Sign(linkMessage(f, d)))}
}

// The reproduced attack: a writer sets owners=[attacker] and a journal that adds the attacker to the
// real keys, sealed by the attacker. The victim unlocks with their own passkey (found through the
// journal's list) and presses Finish or Undo: nothing may be re-encrypted, and the config must stay
// as it was.
func TestAForgedJournalCannotRekeyTheStoreToAnAttacker(t *testing.T) {
	ctx := context.Background()
	r, _ := anchoredRig(t)
	legit := r.st.Config()
	ak, _ := recovery(t)
	attacker := ownerOf(ak)
	before := r.snapshot(t)
	to := append(slicesClone(legit.Owners), attacker)
	forged := Config{Version: 2, Owners: []Owner{attacker}}
	forged.Pending = &Pending{Op: OpAdd, To: to, From: ownersDigest(forged.Owners),
		Seals: []Seal{makeSeal(ak.Unlock.Signer, to)}, Link: linkBy(t, ak, forged.Owners, to)}
	for _, finish := range []bool{true, false} {
		r.open(t, forged)
		r.unlockPasskey(t) // the real passkey is in the journal's list
		if err := r.st.Recover(ctx, finish); !errors.Is(err, ErrConfigChanged) {
			t.Fatalf("finish %v: %v", finish, err)
		}
		sameSnapshot(t, "the files", r.snapshot(t), before)
		if r.persist != 0 || len(r.st.Owners()) != 1 || r.st.Owners()[0].Public != attacker.Public {
			t.Fatalf("the config was touched: %d persists", r.persist)
		}
	}
}

// The same journal on the real owner list, signed by a stranger: finishing is refused; undoing is
// harmless (it goes back to the real list) and is allowed.
func TestAJournalOnTheRealListSignedByAStrangerIsNotFinished(t *testing.T) {
	ctx := context.Background()
	r, _ := anchoredRig(t)
	legit := r.st.Config()
	ak, _ := recovery(t)
	to := append(slicesClone(legit.Owners), ownerOf(ak))
	forged := cloneConfig(legit)
	forged.Pending = &Pending{Op: OpAdd, To: to, From: ownersDigest(legit.Owners),
		Seals: []Seal{makeSeal(ak.Unlock.Signer, to)}, Link: linkBy(t, ak, legit.Owners, to)}
	r.open(t, forged)
	r.unlockPasskey(t)
	before := r.snapshot(t)
	if err := r.st.Recover(ctx, true); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("finish: %v", err)
	}
	sameSnapshot(t, "the files", r.snapshot(t), before)
	if err := r.st.Recover(ctx, false); err != nil {
		t.Fatalf("undo of a journal on the real list: %v", err)
	}
	if len(r.st.Owners()) != 2 || r.st.Interrupted() {
		t.Fatalf("%+v", r.st.Config())
	}
}

// A first-enable journal with no seals, over files that are encrypted already (the config was
// deleted and a journal put in its place naming the victim's key and the attacker's): the victim's
// key opens every file, but the files were not sealed under that list.
func TestASeallessEnableJournalOverAnEncryptedStoreIsNotFinished(t *testing.T) {
	ctx := context.Background()
	r, _ := anchoredRig(t)
	ak, _ := recovery(t)
	pk := passkey(t, r)
	forged := Config{Pending: &Pending{Op: OpEnable, To: []Owner{ownerOf(pk), ownerOf(ak)}}}
	r.open(t, forged)
	r.unlockPasskey(t)
	before := r.snapshot(t)
	if err := r.st.Recover(ctx, true); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("%v", err)
	}
	if r.persist != 0 || r.st.Unlocked() && len(r.st.Owners()) != 0 {
		t.Fatal("the journal was finished")
	}
	sameSnapshot(t, "the files", r.snapshot(t), before)
}

// A journal of turning encryption on where the store has owners already is not one.
func TestAnEnableJournalOverAStoreWithOwnersIsRefused(t *testing.T) {
	ctx := context.Background()
	r, _ := anchoredRig(t)
	ak, _ := recovery(t)
	cfg := r.st.Config()
	cfg.Pending = &Pending{Op: OpEnable, To: []Owner{ownerOf(ak)}}
	r.open(t, cfg)
	r.unlockPasskey(t)
	if err := r.st.Recover(ctx, true); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("%v", err)
	}
}

// Item 2: a change that was cut short after every file was converted and is finished with the new
// key alone must not lock the old keys out: the journal carries the old list's signature over the
// new one, and it becomes a link.
func TestFinishingWithOnlyTheNewKeyKeepsTheOldKeys(t *testing.T) {
	ctx := context.Background()
	for _, noLink := range []bool{false, true} {
		t.Run(map[bool]string{false: "with the link", true: "journal without the link"}[noLink], func(t *testing.T) {
			r, rsecret := anchoredRig(t)
			secret2 := bytes.Repeat([]byte{6}, 32)
			n := passkeyFrom(t, secret2, "New passkey")
			if err := r.st.AddOwner(ctx, n); err != nil { // the whole change, to learn its journal
				t.Fatal(err)
			}
			journal := r.history[0]
			if journal.Pending == nil || journal.Pending.Link == nil || journal.Pending.From == "" {
				t.Fatalf("the journal does not carry the link: %+v", journal.Pending)
			}
			// The crash: every file is converted, the journal is what the config says.
			if noLink {
				journal.Pending.Link = nil
			}
			r.open(t, journal)
			if err := r.st.UnlockPasskey(bytes.Clone(secret2)); err != nil {
				t.Fatal(err)
			}
			err := r.st.Recover(ctx, true)
			if noLink {
				if !errors.Is(err, ErrConfigChanged) {
					t.Fatalf("finishing with a new key and no link: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			// The old keys still open the store.
			r.open(t, r.saved)
			r.unlockPasskey(t)
			r.st.Lock()
			if err := r.st.UnlockRecovery(rsecret); err != nil {
				t.Fatalf("the recovery key after the change was finished with the new key: %v", err)
			}
			r.st.Lock()
			if err := r.st.UnlockPasskey(bytes.Clone(secret2)); err != nil {
				t.Fatal(err)
			}
			entries, _, _, _ := r.saver.Overview(ctx, target)
			for _, e := range entries {
				if _, _, err := r.saver.LoadChecked(ctx, target, e.ID); err != nil {
					t.Errorf("%s: %v", e.Name, err)
				}
			}
		})
	}
}

// Stripping the seals from a Version 2 config, or presenting an older Version, does not make a
// passkey or a recovery key accept a list it has no seal for.
func TestAConfigWithoutSealsIsRefusedWhateverItsVersion(t *testing.T) {
	r, rsecret := anchoredRig(t)
	r.st.Lock()
	good := r.st.Config()
	if good.Version != 2 {
		t.Fatalf("setup: version %d", good.Version)
	}
	ak, _ := recovery(t)
	cases := map[string]func(c *Config){
		"seals stripped, version 2":             func(c *Config) { c.Seals, c.Links = nil, nil },
		"version downgraded and seals stripped": func(c *Config) { c.Version, c.Seals, c.Links = 1, nil, nil },
		"stripped and an owner added": func(c *Config) {
			c.Seals, c.Links, c.Version = nil, nil, 1
			c.Owners = append(slicesClone(c.Owners), ownerOf(ak))
		},
		"owners replaced, stripped":                       func(c *Config) { c.Seals, c.Links, c.Version = nil, nil, 1; c.Owners = []Owner{ownerOf(ak)} },
		"owners replaced, the seals left":                 func(c *Config) { c.Owners = append(slicesClone(c.Owners), ownerOf(ak)) },
		"seals of the key replaced by the attacker's own": func(c *Config) { c.Seals = []Seal{makeSeal(ak.Unlock.Signer, c.Owners)} },
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			evil := cloneConfig(good)
			tamper(&evil)
			r.open(t, evil)
			// A list that does not name the key at all is "not one of its keys"; every other
			// tampering is a changed configuration.
			refused := func(err error) bool {
				return errors.Is(err, ErrConfigChanged) || name == "owners replaced, stripped" && errors.Is(err, ErrNotAKey)
			}
			if err := r.st.UnlockPasskey(bytes.Clone(r.secret)); !refused(err) {
				t.Errorf("passkey: %v", err)
			}
			if err := r.st.UnlockRecovery(rsecret); !refused(err) {
				t.Errorf("recovery: %v", err)
			}
			if r.st.Unlocked() {
				t.Error("unlocked")
			}
		})
	}
	r.open(t, good)
	r.unlockPasskey(t)
}

// An older config that was valid once (its seals and links are genuine) is not the one the files
// were sealed under: the anchor in the files' signed frames says so.
func TestAnOlderValidConfigPutBackIsRefused(t *testing.T) {
	ctx := context.Background()
	r, rsecret := anchoredRig(t)
	old := r.st.Config() // [passkey, recovery]
	pub, _ := sshKeyPair(t, "me@laptop")
	k, _ := NewSSHKey(pub)
	if err := r.st.AddOwner(ctx, k); err != nil {
		t.Fatal(err)
	}
	// The old list is genuine: without the anchor it unlocks.
	r.st.Lock()
	r.open(t, old)
	if err := r.st.UnlockPasskey(bytes.Clone(r.secret)); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("passkey: %v", err)
	}
	if err := r.st.UnlockRecovery(rsecret); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("recovery: %v", err)
	}
	// The current one does.
	r.open(t, r.saved)
	r.unlockPasskey(t)
}

// An SSH key that has no seal yet is checked against the files: a list that adds an owner and has
// its seals stripped, or one that drops the keys that signed the files, is refused.
func TestAnSSHKeyWithoutASealIsCheckedAgainstTheFiles(t *testing.T) {
	ctx := context.Background()
	r, _ := anchoredRig(t)
	pub, priv := sshKeyPair(t, "me@laptop")
	k, _ := NewSSHKey(pub)
	if err := r.st.AddOwner(ctx, k); err != nil { // by the passkey: the SSH key has no seal
		t.Fatal(err)
	}
	good := r.st.Config()
	ak, _ := recovery(t)
	sshOwner := good.Owners[2]
	cases := map[string]Config{
		"an owner added, seals stripped": {Version: 1, Owners: append(slicesClone(good.Owners), ownerOf(ak))},
		"the signing keys dropped":       {Version: 1, Owners: []Owner{sshOwner, ownerOf(ak)}},
		"only the attacker and the key":  {Version: 2, Owners: []Owner{ownerOf(ak), sshOwner}},
	}
	for name, evil := range cases {
		t.Run(name, func(t *testing.T) {
			r.st.Lock()
			r.open(t, evil)
			if err := r.st.UnlockSSH(bytes.Clone(priv)); !errors.Is(err, ErrConfigChanged) {
				t.Fatalf("%v", err)
			}
			if !sameConfig(r.saved, good) {
				t.Fatal("the attacker's list was sealed")
			}
		})
	}
	// The genuine list unlocks it, once and again.
	r.open(t, good)
	if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
		t.Fatal(err)
	}
}

// A configuration that is gone over encrypted files is not "encryption off", and neither is one
// that cannot be read.
func TestAMissingOrCorruptConfigOverEncryptedFilesIsNotEncryptionOff(t *testing.T) {
	ctx := context.Background()
	r, _ := anchoredRig(t)
	if err := Inspect(ctx, r.raw, r.st.Config(), nil); err != nil {
		t.Fatalf("a store with its config: %v", err)
	}
	if err := Inspect(ctx, r.raw, Config{}, nil); !errors.Is(err, ErrConfigMissing) {
		t.Fatalf("deleted config: %v", err)
	}
	if err := Inspect(ctx, r.raw, Config{}, errors.New("invalid character")); !errors.Is(err, ErrConfigCorrupt) {
		t.Fatalf("corrupt config: %v", err)
	}
	// A plain store with no config is simply off.
	plain := plainRig(t)
	if err := Inspect(ctx, plain.raw, Config{}, nil); err != nil {
		t.Fatalf("a plain store: %v", err)
	}
	// Writing is refused in that state: no plain text beside the ciphertext.
	ro := ReadOnly(r.raw, ErrConfigMissing)
	if err := ro.Save(ctx, "x", []byte("plain")); !errors.Is(err, ErrConfigMissing) {
		t.Fatalf("%v", err)
	}
	if err := ro.Delete(ctx, "x"); !errors.Is(err, ErrConfigMissing) {
		t.Fatalf("%v", err)
	}
	if _, err := ro.List(ctx); err != nil {
		t.Fatal(err)
	}
}

// Restore rebuilds the configuration from a key that opens the files; the files are encrypted again
// to it, so the next unlock agrees with the anchor.
func TestRestoreRebuildsTheConfigFromAKeyThatOpensTheFiles(t *testing.T) {
	ctx := context.Background()
	for _, how := range []string{"passkey", "recovery", "ssh"} {
		t.Run(how, func(t *testing.T) {
			r := newRig(t)
			withPlainDocs(t, r)
			pub, priv := sshKeyPair(t, "me@laptop")
			rk, rsecret := recovery(t)
			sk, _ := NewSSHKey(pub)
			if err := r.st.Enable(ctx, Options{}, passkey(t, r), rk, sk); err != nil {
				t.Fatal(err)
			}
			r.open(t, Config{}) // the config is deleted
			if err := Inspect(ctx, r.raw, Config{}, nil); !errors.Is(err, ErrConfigMissing) {
				t.Fatal(err)
			}
			var k NewKey
			var err error
			switch how {
			case "passkey":
				k, err = NewPasskeyKey(bytes.Clone(r.secret), "My passkey")
			case "recovery":
				k, err = RecoveryKeyFromSecret("Recovery key", rsecret)
			default:
				k, err = SSHKeyFromPrivate(bytes.Clone(priv))
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := r.st.Restore(ctx, k); err != nil {
				t.Fatal(err)
			}
			if len(r.saved.Owners) != 1 || r.st.Interrupted() || !r.st.Unlocked() {
				t.Fatalf("%+v", r.saved)
			}
			// The next load: the restored list agrees with the files, and every document opens.
			r.open(t, r.saved)
			switch how {
			case "passkey":
				r.unlockPasskey(t)
			case "recovery":
				if err := r.st.UnlockRecovery(rsecret); err != nil {
					t.Fatal(err)
				}
			default:
				if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
					t.Fatal(err)
				}
			}
			entries, _, _, _ := r.saver.Overview(ctx, target)
			if len(entries) != 3 {
				t.Fatalf("%d entries", len(entries))
			}
			for _, e := range entries {
				if _, _, err := r.saver.LoadChecked(ctx, target, e.ID); err != nil {
					t.Errorf("%s: %v", e.Name, err)
				}
			}
		})
	}
	// A key that opens nothing restores nothing.
	r := newRig(t)
	withPlainDocs(t, r)
	rk, _ := recovery(t)
	if err := r.st.Enable(ctx, Options{}, passkey(t, r), rk); err != nil {
		t.Fatal(err)
	}
	r.open(t, Config{})
	stranger, _ := recovery(t)
	before := r.snapshot(t)
	var ur *UnreadableError
	if err := r.st.Restore(ctx, stranger); !errors.As(err, &ur) {
		t.Fatalf("%v", err)
	}
	sameSnapshot(t, "after the refusal", r.snapshot(t), before)
}

// Two tabs over one storage: the one that is out of date refuses to write and locks itself.
type sharedStorage struct {
	raw *faultyRaw
	cfg Config
}

func (sh *sharedStorage) tab(t *testing.T) *Store {
	t.Helper()
	st, err := New(sh.raw, target, sh.cfg)
	if err != nil {
		t.Fatal(err)
	}
	st.Persist = func(c Config) error { sh.cfg = cloneConfig(c); return nil }
	st.Reload = func() (Config, error) { return cloneConfig(sh.cfg), nil }
	return st
}

func TestAStaleTabCannotOverwriteKeyChangesMadeInAnother(t *testing.T) {
	ctx := context.Background()
	r, _ := anchoredRig(t)
	pub, _ := sshKeyPair(t, "me@laptop")
	sk, _ := NewSSHKey(pub)
	if err := r.st.AddOwner(context.Background(), sk); err != nil { // a third key, so one can be removed
		t.Fatal(err)
	}
	sh := &sharedStorage{raw: r.raw, cfg: r.saved}
	unlocked := func() *Store {
		st := sh.tab(t)
		if err := st.UnlockPasskey(bytes.Clone(r.secret)); err != nil {
			t.Fatal(err)
		}
		return st
	}
	fps := func(st *Store) string { return st.Recipients()[2].Fingerprint }
	ops := map[string]func(st *Store) error{
		"Save": func(st *Store) error {
			return st.Adapter().Save(ctx, "00000000-0000-4000-8000-000000000001.open", []byte("x"))
		},
		"Delete": func(st *Store) error {
			return st.Adapter().Delete(ctx, "00000000-0000-4000-8000-000000000001.open")
		},
		"AddOwner": func(st *Store) error {
			k, _ := recovery(t)
			return st.AddOwner(ctx, k)
		},
		"RemoveOwner": func(st *Store) error { return st.RemoveOwner(ctx, fps(st)) },
		"ReplaceOwner": func(st *Store) error {
			return st.ReplaceOwner(ctx, st.Recipients()[0].Fingerprint, passkeyFrom(t, bytes.Repeat([]byte{9}, 32), "Other"))
		},
		"Disable": func(st *Store) error { return st.Disable(ctx) },
		"Erase":   func(st *Store) error { return st.Erase(ctx) },
		"Unlock":  func(st *Store) error { st.Lock(); return st.UnlockPasskey(bytes.Clone(r.secret)) },
	}
	tabs := map[string]*Store{}
	for name := range ops {
		tabs[name] = unlocked()
	}
	// Another tab changes the keys.
	other := unlocked()
	k, _ := recovery(t)
	if err := other.AddOwner(ctx, k); err != nil {
		t.Fatal(err)
	}
	changed := sh.cfg
	before := r.snapshot(t)
	for name, op := range ops {
		st := tabs[name]
		if err := op(st); !errors.Is(err, ErrStaleConfig) || !errors.Is(err, ErrConfigChanged) {
			t.Errorf("%s: %v", name, err)
		}
		if st.Unlocked() || !st.Stale() {
			t.Errorf("%s: the stale tab is not locked", name)
		}
	}
	sameSnapshot(t, "the files after the stale tabs", r.snapshot(t), before)
	if !sameConfig(sh.cfg, changed) {
		t.Fatal("a stale tab wrote the config")
	}
	// The tab that made the change goes on.
	if err := other.Adapter().Save(ctx, "00000000-0000-4000-8000-000000000002.open", []byte("x")); err != nil {
		t.Fatalf("the tab that changed the keys: %v", err)
	}
}

// The storage event: a tab that is told the config changed elsewhere locks, unless it is its own.
func TestAStorageEventLocksATabWhoseConfigChanged(t *testing.T) {
	ctx := context.Background()
	r, _ := anchoredRig(t)
	sh := &sharedStorage{raw: r.raw, cfg: r.saved}
	tab := sh.tab(t)
	if err := tab.UnlockPasskey(bytes.Clone(r.secret)); err != nil {
		t.Fatal(err)
	}
	tab.ConfigChangedElsewhere() // nothing changed: its own write echoed
	if !tab.Unlocked() || tab.Stale() {
		t.Fatal("locked by an echo of its own config")
	}
	other := sh.tab(t)
	if err := other.UnlockPasskey(bytes.Clone(r.secret)); err != nil {
		t.Fatal(err)
	}
	k, _ := recovery(t)
	if err := other.AddOwner(ctx, k); err != nil {
		t.Fatal(err)
	}
	tab.ConfigChangedElsewhere()
	if tab.Unlocked() || !tab.Stale() {
		t.Fatal("the tab was not locked")
	}
}

// Room is counted in the browser's storage units (base64 characters), with a margin on what age adds.
func TestRoomIsCountedInStorageUnitsWithAMargin(t *testing.T) {
	ctx := context.Background()
	r := plainRig(t)
	if storedUnits(3) != 4+recordSlack || storedUnits(4) != 8+recordSlack {
		t.Fatalf("%d %d", storedUnits(3), storedUnits(4))
	}
	metas, _ := r.raw.List(ctx)
	var units, bytesUsed int64
	for _, m := range metas {
		units += storedUnits(int64(m.Size))
		bytesUsed += int64(m.Size)
	}
	// Counted in file bytes, as it once was, this much room is plenty for the change (about 12 KB);
	// in the storage's units, and with the margin on what age adds, it is not (about 18.7 KB).
	_ = bytesUsed
	r.st.Capacity = units + 18000
	rk, _ := recovery(t)
	err := r.st.Enable(ctx, Options{}, passkey(t, r), rk)
	var nr *NoRoomError
	if !errors.As(err, &nr) || !errors.Is(err, docstore.ErrStorageFull) {
		t.Fatalf("%v", err)
	}
	if r.persist != 0 || r.raw.writes != 0 {
		t.Fatal("it started")
	}
	if nr.Free != r.st.Capacity-units {
		t.Errorf("free %d, want %d: the used room is not counted in storage units", nr.Free, r.st.Capacity-units)
	}
	r.st.Capacity = units + 1<<20
	rk2, _ := recovery(t)
	if err := r.st.Enable(ctx, Options{}, passkey(t, r), rk2); err != nil {
		t.Fatal(err)
	}
}

// The stored text of an encrypted store holds none of the documents, and every document file has a
// signed frame (the anchor is there to be checked).
func TestEveryDocumentFileCarriesTheOwnerListAnchor(t *testing.T) {
	ctx := context.Background()
	r, _ := anchoredRig(t)
	want := ownersHash(r.st.Owners())
	ad := encrypt.New(bare{r.raw}, encrypt.Static{Keys: r.st.ids})
	ad.OwnerSigners = signersOf(r.st.Owners())
	n := 0
	for id, body := range r.snapshot(t) {
		if encrypt.DefaultSkip(id) || encrypt.DocID(id) == "" {
			continue
		}
		h, err := ad.PeekFrame(ctx, id, []byte(body))
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if h.Version != 3 || !h.Signed || h.Hash != want {
			t.Errorf("%s: %+v", id, h)
		}
		n++
	}
	if n == 0 {
		t.Fatal("no document files")
	}
	if strings.Contains(r.stored(t), "text of alpha") {
		t.Error("in the clear")
	}
}

// The owner-signed index of confidential documents (docstore/encrypt) is signed by an owner key too:
// when that key is taken out, the index is signed again, or it would no longer verify.
func TestRemovingTheKeyThatSignedTheIndexSignsItAgain(t *testing.T) {
	ctx := context.Background()
	r, rsecret, id := confidentialRig(t)
	passkeyFP := ""
	for _, o := range r.st.Recipients() {
		if o.Type == "passkey-prf" {
			passkeyFP = o.Fingerprint
		}
	}
	if sh, err := r.st.Sharing(); err != nil {
		t.Fatal(err)
	} else if idx, err := sh.Index(ctx); err != nil || idx.Docs[id].Level != encrypt.ProtectionE2EE {
		t.Fatalf("setup: %+v %v", idx, err)
	}
	if err := r.st.UnlockRecovery(rsecret); err != nil {
		t.Fatal(err)
	}
	if err := r.st.RemoveOwner(ctx, passkeyFP); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved)
	if err := r.st.UnlockRecovery(rsecret); err != nil {
		t.Fatal(err)
	}
	sh, err := r.st.Sharing()
	if err != nil {
		t.Fatal(err)
	}
	idx, err := sh.Index(ctx)
	if err != nil || idx.Docs[id].Level != encrypt.ProtectionE2EE {
		t.Fatalf("the index after the removal: %+v %v", idx, err)
	}
}

// A seal over an old owner list is not the journal of going back to it: someone with an earlier copy
// of the storage writes the current Config and a journal From the current list To the old one (a
// list with a key that was removed since), carrying the old seals and a link signed by the removed
// key. The unlocking owner pressing Finish must not make the removed key an owner again.
func TestAReplayedSealOverAnOldListIsNotFinished(t *testing.T) {
	ctx := context.Background()
	r, rsecret := anchoredRig(t)
	rk, err := RecoveryKeyFromSecret("Recovery key", rsecret)
	if err != nil {
		t.Fatal(err)
	}
	legit := r.st.Config()
	xk, _ := recovery(t) // the key that was an owner once, and is removed
	old := append(slicesClone(legit.Owners), ownerOf(xk))
	replay := cloneConfig(legit)
	replay.Pending = &Pending{Op: OpAdd, To: old, From: ownersDigest(legit.Owners),
		Seals: []Seal{makeSeal(rk.Unlock.Signer, old)}, // an old seal of a key of the current list
		Link:  linkBy(t, xk, legit.Owners, old)}        // self-signed by the removed key
	r.open(t, replay)
	r.unlockPasskey(t)
	before := r.snapshot(t)
	if err := r.st.Recover(ctx, true); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("finish: %v", err)
	}
	sameSnapshot(t, "the files", r.snapshot(t), before)
	if r.persist != 0 || len(r.st.Owners()) != len(legit.Owners) {
		t.Fatalf("the config was touched: %d persists, %d owners", r.persist, len(r.st.Owners()))
	}
}

// Finishing with only the new key: the journal's link must be From the journal's From, to its To,
// and signed by a key of the Config's owners.
func TestFinishingWithTheNewKeyNeedsTheRightLink(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(t *testing.T, j *Pending, cfg Config, rk, xk NewKey) *Link{
		"from another list": func(t *testing.T, j *Pending, cfg Config, rk, xk NewKey) *Link {
			other := []Owner{ownerOf(xk)}
			return linkBy(t, rk, other, j.To) // validly signed by an owner, but not from this list
		},
		"to another list": func(t *testing.T, j *Pending, cfg Config, rk, xk NewKey) *Link {
			return linkBy(t, rk, cfg.Owners, []Owner{ownerOf(xk)}) // from this list, but to a list that is not the journal's
		},
		"direction reversed": func(t *testing.T, j *Pending, cfg Config, rk, xk NewKey) *Link {
			return linkBy(t, rk, j.To, cfg.Owners)
		},
		"signed by a stranger": func(t *testing.T, j *Pending, cfg Config, rk, xk NewKey) *Link {
			return linkBy(t, xk, cfg.Owners, j.To)
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			r, rsecret := anchoredRig(t)
			rk, err := RecoveryKeyFromSecret("Recovery key", rsecret)
			if err != nil {
				t.Fatal(err)
			}
			secret2 := bytes.Repeat([]byte{6}, 32)
			n := passkeyFrom(t, secret2, "New passkey")
			cfg := r.st.Config()
			if err := r.st.AddOwner(ctx, n); err != nil {
				t.Fatal(err)
			}
			journal := r.history[0]
			xk, _ := recovery(t)
			journal.Pending.Link = mk(t, journal.Pending, cfg, rk, xk)
			r.open(t, journal)
			if err := r.st.UnlockPasskey(bytes.Clone(secret2)); err != nil {
				t.Fatal(err)
			}
			if err := r.st.Recover(ctx, true); !errors.Is(err, ErrConfigChanged) {
				t.Fatalf("%v", err)
			}
		})
	}
}

// A genuine link made for an earlier change is not the journal of a new one. AddOwner(X) then
// RemoveOwner(X) leaves the owner list as it was, so the old link from that list to the one with X
// still verifies from the current list. Someone with an earlier copy of the storage plants that old
// journal (its seal and its link); finishing it must not make X an owner again.
func TestAnOldLinkIsNotReplayedAsAJournalOverALaterCopyOfTheList(t *testing.T) {
	ctx := context.Background()
	r, _ := anchoredRig(t)
	x := passkeyFrom(t, bytes.Repeat([]byte{6}, 32), "Added then removed")
	if err := r.st.AddOwner(ctx, x); err != nil {
		t.Fatal(err)
	}
	old := r.history[0] // the journal of the addition, as it was stored
	if old.Pending == nil || old.Pending.Link == nil {
		t.Fatalf("setup: %+v", old.Pending)
	}
	if err := r.st.RemoveOwner(ctx, ownerOf(x).Fingerprint()); err != nil {
		t.Fatal(err)
	}
	replay := cloneConfig(r.st.Config())
	replay.Pending = old.Pending
	r.open(t, replay)
	r.unlockPasskey(t)
	owners := len(r.st.Owners())
	before := r.snapshot(t)
	r.persist = 0
	if err := r.st.Recover(ctx, true); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("finish: %v", err)
	}
	sameSnapshot(t, "the files", r.snapshot(t), before)
	if r.persist != 0 || len(r.st.Owners()) != owners {
		t.Fatalf("the config was touched: %d persists, %d owners", r.persist, len(r.st.Owners()))
	}
}

// The same change made again and cut short is a new journal, with a link of its own: it can be
// finished although an earlier change from and to the same lists is in the Config's links.
func TestTheSameChangeMadeAgainCanBeFinished(t *testing.T) {
	ctx := context.Background()
	r, _ := anchoredRig(t)
	x := passkeyFrom(t, bytes.Repeat([]byte{6}, 32), "Added, removed, added")
	if err := r.st.AddOwner(ctx, x); err != nil {
		t.Fatal(err)
	}
	if err := r.st.RemoveOwner(ctx, ownerOf(x).Fingerprint()); err != nil {
		t.Fatal(err)
	}
	r.persist, r.raw.writes = 0, 0
	r.crashAt(3)
	if err := r.st.AddOwner(ctx, x); !errors.Is(err, ErrRollbackFailed) { // cut short after the journal
		t.Fatalf("setup: %v", err)
	}
	r.raw.heal()
	r.failPersist = nil
	r.open(t, r.saved)
	if !r.st.Interrupted() {
		t.Fatal("setup: not interrupted")
	}
	r.unlockPasskey(t)
	if err := r.st.Recover(ctx, true); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if len(r.st.Owners()) != 3 {
		t.Fatalf("%d owners", len(r.st.Owners()))
	}
}
