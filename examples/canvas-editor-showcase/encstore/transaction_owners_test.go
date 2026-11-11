package encstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"fmt"
	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/saver"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func confidentialRig(t *testing.T) (*rig, string, string) {
	t.Helper()
	ctx := context.Background()
	r, secret := encryptedRig(t)
	withPlainDocs(t, r)
	e, err := r.saver.Save(ctx, target, docfile.Open, doc("Secret doc", "secret text"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.saver.SetProtection(ctx, target, e.ID, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	r.raw.writes, r.persist = 0, 0
	return r, secret, e.ID
}

func TestAddOwnerFailsAtEveryStepAndLeavesTheStoreUsable(t *testing.T) {
	ctx := context.Background()
	pub, priv := sshKeyPair(t, "me@laptop")
	var lastID string
	tried := everyFailure(t, func(t *testing.T) *rig {
		r, _, id := confidentialRig(t)
		lastID = id
		return r
	},
		func(r *rig) error {
			k, err := NewSSHKey(pub)
			if err != nil {
				t.Fatal(err)
			}
			return r.st.AddOwner(ctx, k)
		},
		func(t *testing.T, r *rig, before map[string]string, cfgBefore Config) {
			sameSnapshot(t, "storage", r.snapshot(t), before)
			if !reflect.DeepEqual(r.saved, cfgBefore) || len(r.st.Owners()) != 2 || r.st.Interrupted() {
				t.Errorf("the key list changed: %+v vs %+v", r.saved, cfgBefore)
			}
			if !r.st.Unlocked() {
				t.Error("locked by the failure")
			}
			if _, err := r.saver.Save(ctx, target, docfile.Open, doc("After", "after the failure")); err != nil {
				t.Errorf("saving after the failure: %v", err)
			}
			if name, _, err := r.saver.Reveal(ctx, target, lastID); err != nil || name != "Secret doc" {
				t.Errorf("the confidential document: %q %v", name, err)
			}
			k, _ := NewSSHKey(pub)
			if err := r.st.AddOwner(ctx, k); err != nil {
				t.Fatalf("the retry: %v", err)
			}
			r.open(t, r.saved)
			if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
				t.Fatal(err)
			}
			if name, _, err := r.saver.Reveal(ctx, target, lastID); err != nil || name != "Secret doc" {
				t.Errorf("the new key does not open the confidential document: %q %v", name, err)
			}
		})
	if tried < 8 {
		t.Fatalf("only %d steps were tried", tried)
	}
}

func TestACrashDuringAddOwnerIsFinishedOnTheNextLoad(t *testing.T) {
	ctx := context.Background()
	pub, priv := sshKeyPair(t, "me@laptop")
	r, _, id := confidentialRig(t)
	k, _ := NewSSHKey(pub)
	r.crashAt(4)
	if err := r.st.AddOwner(ctx, k); !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("%v", err)
	}
	r.raw.heal()
	r.failPersist = nil
	r.open(t, r.saved)
	if !r.st.Interrupted() || r.saved.Pending.Op != OpAdd {
		t.Fatalf("%+v", r.saved)
	}
	r.unlockPasskey(t)
	if err := r.st.Recover(ctx, true); err != nil {
		t.Fatal(err)
	}
	if len(r.st.Owners()) != 3 || r.st.Interrupted() {
		t.Fatalf("%+v", r.saved)
	}
	r.open(t, r.saved)
	if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
		t.Fatal(err)
	}
	if name, _, err := r.saver.Reveal(ctx, target, id); err != nil || name != "Secret doc" {
		t.Fatalf("%q %v", name, err)
	}
	entries, _, _, _ := r.saver.Overview(ctx, target)
	for _, e := range entries {
		if _, _, err := r.saver.LoadChecked(ctx, target, e.ID); err != nil {
			t.Errorf("%s: %v", e.Name, err)
		}
	}
}

func TestARecoveryKeyWithoutASavedSecretIsNotAnOwner(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	rk, _, err := NewRecoveryKey("Recovery key")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.Enable(ctx, Options{}, passkey(t, r), rk); !errors.Is(err, ErrSecretNotSaved) {
		t.Fatalf("Enable: %v", err)
	}
	if r.st.Enabled() || r.persist != 0 {
		t.Fatal("the store was touched")
	}
	if err := r.st.Enable(ctx, onlyPasskeys, passkey(t, r)); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved)
	r.unlockPasskey(t)
	e, _ := r.saver.Save(ctx, target, docfile.Open, doc("Doc", "text"))
	if err := r.st.AddOwner(ctx, rk); !errors.Is(err, ErrSecretNotSaved) {
		t.Fatalf("AddOwner: %v", err)
	}
	if len(r.st.Owners()) != 1 {
		t.Fatal("the unseen key is an owner")
	}
	if err := r.saver.SetProtection(ctx, target, e.ID, saver.ProtectionE2EE); !errors.Is(err, encrypt.ErrNeedSecondRecipient) {
		t.Fatalf("an unseen recovery key counted: %v", err)
	}
	rk.SecretSaved = true
	if err := r.st.AddOwner(ctx, rk); err != nil {
		t.Fatal(err)
	}
}

func TestPasskeysAloneNeedAnAcknowledgement(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	if err := r.st.Enable(ctx, Options{}, passkey(t, r)); !errors.Is(err, ErrPasskeyOnly) {
		t.Fatalf("%v", err)
	}
	if r.st.Enabled() || r.persist != 0 {
		t.Fatal("touched")
	}
	if err := r.st.Enable(ctx, onlyPasskeys, passkey(t, r)); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveOwnerEncryptsEverythingAgainWithoutIt(t *testing.T) {
	ctx := context.Background()
	r, rsecret, id := confidentialRig(t)
	pub, priv := sshKeyPair(t, "me@laptop")
	k, _ := NewSSHKey(pub)
	if err := r.st.AddOwner(ctx, k); err != nil {
		t.Fatal(err)
	}
	var sshFP, passkeyFP string
	for _, o := range r.st.Recipients() {
		switch o.Type {
		case "ssh-ed25519":
			sshFP = o.Fingerprint
		case "passkey-prf":
			passkeyFP = o.Fingerprint
		}
	}
	if err := r.st.RemoveOwner(ctx, "SHA256:nobody"); !errors.Is(err, ErrNoSuchOwner) {
		t.Fatalf("%v", err)
	}
	before := r.snapshot(t)
	if err := r.st.RemoveOwner(ctx, sshFP); err != nil {
		t.Fatal(err)
	}
	if len(r.st.Owners()) != 2 {
		t.Fatalf("%+v", r.st.Owners())
	}
	if reflect.DeepEqual(before, r.snapshot(t)) {
		t.Fatal("nothing was re-encrypted")
	}
	r.open(t, r.saved)
	if err := r.st.UnlockSSH(bytes.Clone(priv)); !errors.Is(err, ErrNotAKey) {
		t.Fatalf("the removed key unlocks: %v", err)
	}
	sshID, _ := encrypt.ParseSSHIdentity(priv)
	old := encrypt.New(r.raw, encrypt.Static{Keys: []age.Identity{sshID}})
	metas, _ := r.raw.List(ctx)
	for _, m := range metas {
		if encrypt.DefaultSkip(m.ID) {
			continue
		}
		if _, err := old.Load(ctx, m.ID); !errors.Is(err, encrypt.ErrNoKey) {
			t.Errorf("%s still opens with the removed key: %v", m.ID, err)
		}
	}
	if err := r.st.UnlockRecovery(rsecret); err != nil {
		t.Fatal(err)
	}
	if err := r.st.RemoveOwner(ctx, passkeyFP); !errors.Is(err, ErrWouldLoseRecovery) {
		if err != nil {
			t.Fatalf("%v", err)
		}
	}
	if len(r.st.Owners()) != 1 || r.st.Owners()[0].Kind != KindRecovery {
		t.Fatalf("%+v", r.st.Owners())
	}
	r.open(t, r.saved)
	if err := r.st.UnlockRecovery(rsecret); err != nil {
		t.Fatalf("unlocking after the passkey was removed (the config should still be sealed for the recovery key): %v", err)
	}
	if name, _, err := r.saver.Reveal(ctx, target, id); err != nil || name != "Secret doc" {
		t.Fatalf("the confidential document after the lists were re-signed: %q %v", name, err)
	}
	sh, err := r.st.Sharing()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sh.List(ctx, id); err != nil {
		t.Fatalf("the document's list no longer verifies after its signer was removed: %v", err)
	}
	if _, err := r.saver.Save(ctx, target, docfile.Open, doc("Secret doc again", "more secret text")); err != nil {
		t.Fatalf("saving after the signer was removed: %v", err)
	}
	if err := r.st.RemoveOwner(ctx, r.st.Recipients()[0].Fingerprint); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("the last key: %v", err)
	}
}

func TestRemoveOwnerRefusalsLeaveTheStoreAlone(t *testing.T) {
	ctx := context.Background()
	r, _, _ := confidentialRig(t) // passkey + recovery, unlocked with the passkey
	var recoveryFP, passkeyFP string
	for _, o := range r.st.Recipients() {
		if o.Type == "passkey-prf" {
			passkeyFP = o.Fingerprint
		} else {
			recoveryFP = o.Fingerprint
		}
	}
	before := r.snapshot(t)
	if err := r.st.RemoveOwner(ctx, recoveryFP); !errors.Is(err, ErrWouldLoseRecovery) {
		t.Fatalf("%v", err)
	}
	if err := r.st.RemoveOwner(ctx, passkeyFP); !errors.Is(err, ErrUseAnotherKey) {
		t.Fatalf("%v", err)
	}
	sameSnapshot(t, "after the refusals", r.snapshot(t), before)
	if len(r.st.Owners()) != 2 || r.persist != 0 {
		t.Fatalf("owners %d, %d persists", len(r.st.Owners()), r.persist)
	}
}

func TestRemoveOwnerFailsAtEveryStepAndLeavesTheStoreUsable(t *testing.T) {
	ctx := context.Background()
	pub, _ := sshKeyPair(t, "me@laptop")
	var sshFP string
	setup := func(t *testing.T) *rig {
		r, _, _ := confidentialRig(t)
		k, _ := NewSSHKey(pub)
		if err := r.st.AddOwner(ctx, k); err != nil {
			t.Fatal(err)
		}
		for _, o := range r.st.Recipients() {
			if o.Type == "ssh-ed25519" {
				sshFP = o.Fingerprint
			}
		}
		r.raw.writes, r.persist = 0, 0
		return r
	}
	tried := everyFailure(t, setup, func(r *rig) error { return r.st.RemoveOwner(ctx, sshFP) },
		func(t *testing.T, r *rig, before map[string]string, cfgBefore Config) {
			sameSnapshot(t, "storage", r.snapshot(t), before)
			if len(r.st.Owners()) != 3 || r.st.Interrupted() || !reflect.DeepEqual(r.saved, cfgBefore) {
				t.Errorf("the key list changed: %+v", r.saved)
			}
			if err := r.st.RemoveOwner(ctx, sshFP); err != nil {
				t.Fatalf("the retry: %v", err)
			}
		})
	if tried < 6 {
		t.Fatalf("only %d steps were tried", tried)
	}
}

func TestReplaceOwnerSwapsAPasskeyInOneTransaction(t *testing.T) {
	ctx := context.Background()
	r, rsecret, id := confidentialRig(t)
	oldSecret := bytes.Clone(r.secret)
	newSecret := bytes.Repeat([]byte{7}, 32)
	var oldFP string
	for _, o := range r.st.Recipients() {
		if o.Type == "passkey-prf" {
			oldFP = o.Fingerprint
		}
	}
	nk := passkeyFrom(t, newSecret, "My new passkey")
	before := r.snapshot(t)
	if err := r.st.ReplaceOwner(ctx, oldFP, nk); err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(before, r.snapshot(t)) {
		t.Fatal("nothing was re-encrypted")
	}
	r.open(t, r.saved)
	if err := r.st.UnlockPasskey(bytes.Clone(oldSecret)); !errors.Is(err, ErrNotAKey) {
		t.Fatalf("the old passkey still unlocks: %v", err)
	}
	if err := r.st.UnlockPasskey(bytes.Clone(newSecret)); err != nil {
		t.Fatal(err)
	}
	if name, _, err := r.saver.Reveal(ctx, target, id); err != nil || name != "Secret doc" {
		t.Fatalf("the confidential document under the new passkey: %q %v", name, err)
	}
	r.st.Lock()
	if err := r.st.UnlockRecovery(rsecret); err != nil {
		t.Fatalf("the recovery key after the swap: %v", err)
	}
	if err := r.st.ReplaceOwner(ctx, r.st.Recipients()[1].Fingerprint, passkeyFrom(t, bytes.Repeat([]byte{8}, 32), "p3")); !errors.Is(err, ErrWouldLoseRecovery) && r.st.Recipients()[1].Type == "age-x25519" {
		t.Fatalf("swapping the recovery key for a passkey: %v", err)
	}
}

func TestReplaceOwnerFailsAtEveryStep(t *testing.T) {
	ctx := context.Background()
	var oldFP string
	newSecret := bytes.Repeat([]byte{7}, 32)
	setup := func(t *testing.T) *rig {
		r, _, _ := confidentialRig(t)
		for _, o := range r.st.Recipients() {
			if o.Type == "passkey-prf" {
				oldFP = o.Fingerprint
			}
		}
		return r
	}
	tried := everyFailure(t, setup, func(r *rig) error {
		return r.st.ReplaceOwner(ctx, oldFP, passkeyFrom(t, newSecret, "new"))
	}, func(t *testing.T, r *rig, before map[string]string, cfgBefore Config) {
		sameSnapshot(t, "storage", r.snapshot(t), before)
		if r.st.Interrupted() || !reflect.DeepEqual(r.saved, cfgBefore) {
			t.Errorf("%+v", r.saved)
		}
		if err := r.st.ReplaceOwner(ctx, oldFP, passkeyFrom(t, newSecret, "new")); err != nil {
			t.Fatalf("the retry: %v", err)
		}
	})
	if tried < 6 {
		t.Fatalf("only %d steps were tried", tried)
	}
}

func TestDisableFailsAtEveryStepAndLeavesTheStoreEncrypted(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) *rig {
		r, _ := encryptedRig(t)
		withPlainDocs(t, r)
		r.raw.writes, r.persist = 0, 0
		return r
	}
	tried := everyFailure(t, setup, func(r *rig) error { return r.st.Disable(ctx) },
		func(t *testing.T, r *rig, before map[string]string, cfgBefore Config) {
			sameSnapshot(t, "storage", r.snapshot(t), before)
			if !r.st.Enabled() || r.st.Interrupted() || !reflect.DeepEqual(r.saved, cfgBefore) {
				t.Errorf("not as it was: %+v", r.saved)
			}
			if err := r.st.Disable(ctx); err != nil {
				t.Fatalf("the retry: %v", err)
			}
			if !strings.Contains(r.stored(t), "text of alpha") {
				t.Error("the retry did not write the documents back in the clear")
			}
		})
	if tried < 6 {
		t.Fatalf("only %d steps were tried", tried)
	}
}

func TestACrashDuringDisableIsFinishedOnTheNextLoad(t *testing.T) {
	ctx := context.Background()
	r, _ := encryptedRig(t)
	withPlainDocs(t, r)
	r.raw.writes, r.persist = 0, 0
	r.crashAt(3)
	if err := r.st.Disable(ctx); !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("%v", err)
	}
	r.raw.heal()
	r.failPersist = nil
	r.open(t, r.saved)
	if !r.st.Interrupted() || r.saved.Pending.Op != OpDisable {
		t.Fatalf("%+v", r.saved)
	}
	r.unlockPasskey(t)
	if err := r.st.Recover(ctx, true); err != nil {
		t.Fatal(err)
	}
	if r.st.Enabled() || len(r.saved.Owners) != 0 {
		t.Fatalf("%+v", r.saved)
	}
	r.open(t, r.saved)
	entries, _, _, err := r.saver.Overview(ctx, target)
	if err != nil || len(entries) != 3 {
		t.Fatalf("%v %v", entries, err)
	}
	for _, e := range entries {
		if _, _, err := r.saver.LoadChecked(ctx, target, e.ID); err != nil {
			t.Errorf("%s: %v", e.Name, err)
		}
	}
}

func TestDisableDoesNotRaceWithASave(t *testing.T) {
	ctx := context.Background()
	r, _ := encryptedRig(t)
	withPlainDocs(t, r)
	const newID = "00000000-0000-4000-8000-0000000000aa.open"
	var started atomic.Bool
	saveErr := make(chan error, 1)
	r.raw.writes = 0
	r.raw.loadHook = func(id string) {
		if r.raw.writes == 0 || !started.CompareAndSwap(false, true) {
			return
		}
		go func() { saveErr <- r.st.Adapter().Save(ctx, newID, []byte("saved during the disable")) }()
		time.Sleep(150 * time.Millisecond)
	}
	if err := r.st.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	r.raw.loadHook = nil
	err := <-saveErr
	for id, b := range r.snapshot(t) {
		if !encrypt.DefaultSkip(id) && strings.HasPrefix(b, "age-encryption.org/") {
			t.Errorf("%s is ciphertext in a store that is not encrypted (the concurrent save returned %v)", id, err)
		}
	}
	if err == nil {
		d, lerr := r.raw.inner.Load(ctx, newID)
		if lerr != nil || string(d.Data) != "saved during the disable" {
			t.Errorf("the save returned nil but stored %q (%v)", d.Data, lerr)
		}
	}
}

func TestEraseDeletesTheEncryptedStoreOnlyAndNeedsNoKey(t *testing.T) {
	ctx := context.Background()
	r, _, _ := confidentialRig(t)
	other := docstore.NewMemoryAdapter() // another target
	_ = other.Save(ctx, "00000000-0000-4000-8000-0000000000bb.open", []byte("elsewhere"))
	r.st.Lock() // the keys are lost
	r.open(t, r.saved)
	if len(r.snapshot(t)) == 0 {
		t.Fatal("setup")
	}
	if err := r.st.Erase(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(r.snapshot(t)); n != 0 {
		t.Fatalf("%d items left", n)
	}
	if r.st.Enabled() || len(r.saved.Owners) != 0 || r.saved.Pending != nil {
		t.Fatalf("%+v", r.saved)
	}
	if d, err := other.Load(ctx, "00000000-0000-4000-8000-0000000000bb.open"); err != nil || string(d.Data) != "elsewhere" {
		t.Fatal("another target was touched")
	}
	rk, _ := recovery(t)
	if err := r.st.Enable(ctx, Options{}, passkey(t, r), rk); err != nil {
		t.Fatal(err)
	}
}

func TestEraseWorksOnAnInterruptedStoreAndCanBeRepeated(t *testing.T) {
	ctx := context.Background()
	r := plainRig(t)
	rk, _ := recovery(t)
	r.crashAt(3)
	_ = r.st.Enable(ctx, Options{}, passkey(t, r), rk)
	r.raw.heal()
	r.failPersist = nil
	r.open(t, r.saved)
	if !r.st.Interrupted() {
		t.Fatal("setup")
	}
	r.raw.writes, r.raw.failAt = 0, 2 // the erase itself is cut short
	if err := r.st.Erase(ctx); err == nil {
		t.Fatal("no error")
	}
	if !r.st.Interrupted() || len(r.snapshot(t)) == 0 {
		t.Fatal("a half erase must not claim success")
	}
	r.raw.heal()
	if err := r.st.Erase(ctx); err != nil {
		t.Fatal(err)
	}
	if r.st.Enabled() || r.st.Interrupted() || len(r.snapshot(t)) != 0 {
		t.Fatalf("%+v", r.saved)
	}
}

func TestConfigHoldsNoPrivateMaterial(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	pk := passkey(t, r)
	rk, rsecret := recovery(t)
	pub, priv := sshKeyPair(t, "me@laptop")
	sk, _ := NewSSHKey(pub)
	if err := r.st.Enable(ctx, Options{}, pk, rk, sk); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved)
	r.unlockPasskey(t)
	cfgs := []Config{r.saved, r.st.Config()}
	idPRF, _ := encrypt.PRFIdentity(r.secret)
	secrets := []string{"AGE-SECRET-KEY", rsecret, idPRF.String(), "PRIVATE KEY"}
	hexes := [][]byte{r.secret, priv}
	for _, c := range cfgs {
		b, _ := json.Marshal(c)
		for _, s := range secrets {
			if strings.Contains(strings.ToUpper(string(b)), strings.ToUpper(s)) {
				t.Errorf("the config holds %q", s[:min(len(s), 14)])
			}
		}
		for _, h := range hexes {
			if bytes.Contains(b, h) || strings.Contains(string(b), fmt.Sprintf("%x", h)) {
				t.Error("the config holds raw secret bytes")
			}
		}
	}
	r.persist = 0
	nk, _ := recovery(t)
	var journals []Config
	r.failPersist = func(int) error { journals = append(journals, r.st.Config()); return nil }
	if err := r.st.AddOwner(ctx, nk); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(r.saved)
	if strings.Contains(string(b), "AGE-SECRET-KEY") {
		t.Fatal("secret in the config after a change")
	}
}

func TestUnlockClearsTheSecretsItIsGivenOnEveryPath(t *testing.T) {
	r, _ := encryptedRig(t)
	r.st.Lock()
	_, priv := sshKeyPair(t, "stranger")
	for name, call := range map[string]func(b []byte) error{
		"ssh stranger": r.st.UnlockSSH,
		"ssh garbage":  r.st.UnlockSSH,
		"passkey wrong": func(b []byte) error {
			return r.st.UnlockPasskey(b)
		},
	} {
		var b []byte
		switch name {
		case "ssh stranger":
			b = bytes.Clone(priv)
		case "ssh garbage":
			b = []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nnot a key\n-----END OPENSSH PRIVATE KEY-----\n")
		default:
			b = bytes.Repeat([]byte{9}, 32)
		}
		if err := call(b); err == nil {
			t.Fatalf("%s: no error", name)
		}
		if !bytes.Equal(b, make([]byte, len(b))) {
			t.Errorf("%s: the secret was left in memory", name)
		}
	}
	good := bytes.Clone(r.secret)
	if err := r.st.UnlockPasskey(good); err != nil || !bytes.Equal(good, make([]byte, 32)) {
		t.Errorf("success: %v, cleared %v", err, bytes.Equal(good, make([]byte, 32)))
	}
}

func attackerOwner(t *testing.T) Owner {
	t.Helper()
	k, _, _ := NewRecoveryKey("helpful backup")
	return ownerOf(k)
}

func TestAReplacedKeyListIsRefusedAtUnlock(t *testing.T) {
	r, rsecret := encryptedRig(t)
	r.st.Lock()
	cfg := r.st.Config()
	evil := cfg
	evil.Owners = append(slicesClone(cfg.Owners), attackerOwner(t))
	r.open(t, evil)
	if err := r.st.UnlockPasskey(bytes.Clone(r.secret)); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("passkey: %v", err)
	}
	if err := r.st.UnlockRecovery(rsecret); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("recovery: %v", err)
	}
	if r.st.Unlocked() {
		t.Fatal("unlocked on a replaced list")
	}
	r.open(t, cfg)
	r.unlockPasskey(t)
	k, _, _ := NewRecoveryKey("x")
	from, to := ownersDigest(cfg.Owners), ownersDigest(evil.Owners)
	evil.Links = append(evil.Links, Link{From: from, To: evil.Owners, Signer: hexOf(k.SignerPub),
		Sig: hexSig(k.Unlock.Signer.Sign(linkMessage(from, to)))})
	r.open(t, evil)
	if err := r.st.UnlockPasskey(bytes.Clone(r.secret)); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("a link signed by a stranger: %v", err)
	}
}

func TestAKeyAcceptsAListAnotherKeyChanged(t *testing.T) {
	ctx := context.Background()
	r, rsecret := encryptedRig(t) // passkey + recovery; the recovery key's seal is from setup
	pub, priv := sshKeyPair(t, "me@laptop")
	k, _ := NewSSHKey(pub)
	if err := r.st.AddOwner(ctx, k); err != nil { // by the passkey
		t.Fatal(err)
	}
	r.open(t, r.saved)
	if err := r.st.UnlockRecovery(rsecret); err != nil {
		t.Fatalf("the recovery key after the passkey added a key: %v", err)
	}
	r.st.Lock()
	if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
		t.Fatal(err)
	}
	evil := r.st.Config()
	evil.Owners = append(slicesClone(evil.Owners), attackerOwner(t))
	r.open(t, evil)
	if err := r.st.UnlockSSH(bytes.Clone(priv)); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("the ssh key after its first unlock: %v", err)
	}
}

func slicesClone[T any](s []T) []T { return append([]T(nil), s...) }

func hexOf(b []byte) string { return fmt.Sprintf("%x", b) }

func hexSig(b []byte) string { return fmt.Sprintf("%x", b) }

func TestStopPutsEverythingBackAndKeepsTheStoreUsable(t *testing.T) {
	ctx := context.Background()
	r, rsecret, id := confidentialRig(t)
	if err := r.st.UnlockRecovery(rsecret); err != nil {
		t.Fatal(err)
	}
	before, cfgBefore := r.snapshot(t), r.saved
	passkeyFP := ""
	for _, o := range r.st.Recipients() {
		if o.Type == "passkey-prf" {
			passkeyFP = o.Fingerprint
		}
	}
	if r.st.Stop() {
		t.Error("Stop claims there was a change to stop")
	}
	stopped := false
	r.st.Progress = func(done, total int) {
		if !stopped {
			stopped = true
			if !r.st.Stop() {
				t.Error("Stop found no running change")
			}
		}
	}
	err := r.st.RemoveOwner(ctx, passkeyFP)
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("RemoveOwner: %v, want ErrStopped", err)
	}
	if !stopped {
		t.Fatal("the progress hook never ran")
	}
	sameSnapshot(t, "storage", r.snapshot(t), before)
	if !reflect.DeepEqual(r.saved, cfgBefore) || r.st.Interrupted() {
		t.Errorf("the key list changed: %+v vs %+v", r.saved, cfgBefore)
	}
	if !r.st.Unlocked() {
		t.Error("locked by the stop")
	}
	r.st.Progress = nil
	if err := r.st.RemoveOwner(ctx, passkeyFP); err != nil {
		t.Fatalf("RemoveOwner after a stop: %v", err)
	}
	if _, _, err := r.saver.LoadChecked(ctx, target, id); err != nil {
		t.Errorf("document unreadable after the second try: %v", err)
	}
}

func TestDeviceUseSurvivesAKeyChangeAndNamesOnlyCurrentKeys(t *testing.T) {
	ctx := context.Background()
	r, rsecret, _ := confidentialRig(t)
	if err := r.st.UnlockRecovery(rsecret); err != nil {
		t.Fatal(err)
	}
	var recovery, passkey Owner
	for _, o := range r.st.Owners() {
		switch o.Kind {
		case KindRecovery:
			recovery = o
		case KindPasskey:
			passkey = o
		}
	}
	now := time.Date(2030, 6, 1, 8, 0, 0, 0, time.UTC)
	if err := r.st.RecordUse(ctx, recovery, "kitchen tablet", now); err != nil {
		t.Fatal(err)
	}
	if err := r.st.RecordUse(ctx, passkey, "phone", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := r.st.DeviceUses(ctx)
	if err != nil || len(got) != 2 {
		t.Fatalf("uses: %v, %v", got, err)
	}
	if err := r.st.RemoveOwner(ctx, passkey.Fingerprint()); err != nil {
		t.Fatalf("RemoveOwner with a record present: %v", err)
	}
	got, err = r.st.DeviceUses(ctx)
	if err != nil || len(got) != 1 || got[0].Device != "kitchen tablet" || !got[0].At.Equal(now) {
		t.Fatalf("after the removal: %v, %v", got, err)
	}
	locked, _ := New(r.raw, target, r.saved)
	if _, err := locked.DeviceUses(ctx); !errors.Is(err, ErrLocked) {
		t.Errorf("a locked store reads the records: %v", err)
	}
}

func TestKeyListMovesToAnotherDevice(t *testing.T) {
	ctx := context.Background()
	r, rsecret, id := confidentialRig(t)
	text, code, err := r.st.ExportKeyList()
	if err != nil || code == "" {
		t.Fatalf("export: %q %q %v", text, code, err)
	}
	if strings.Contains(text, "AGE-SECRET-KEY") {
		t.Fatal("the key list holds a secret")
	}
	cfg, code2, err := ParseKeyList(text)
	if err != nil || code2 != code || len(cfg.Owners) != len(r.st.Owners()) {
		t.Fatalf("parse: %+v %q %v", cfg, code2, err)
	}
	second, err := New(r.raw, target, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.UnlockRecovery(rsecret); err != nil {
		t.Fatalf("unlock on the second device: %v", err)
	}
	if _, err := second.Adapter().Load(ctx, id); err != nil && !errors.Is(err, docstore.ErrNotFound) {
		t.Errorf("the second device cannot read: %v", err)
	}
	if got := second.Owners(); len(got) != len(cfg.Owners) {
		t.Errorf("owners on the second device: %d", len(got))
	}
}

func TestParseKeyListRefusesWhatIsNotOne(t *testing.T) {
	r, _, _ := confidentialRig(t)
	good, _, _ := r.st.ExportKeyList()
	bad := map[string]string{
		"empty":      "",
		"not json":   "owners: [ana]",
		"wrong kind": strings.Replace(good, keyListKind, "something-else", 1),
		"no owners":  `{"kind":"` + keyListKind + `","config":{"v":1,"owners":[]}}`,
		"bad owner":  `{"kind":"` + keyListKind + `","config":{"v":1,"owners":[{"kind":"recovery","type":"age-x25519","public":"nope","signer":"00"}]}}`,
		"too large":  strings.Repeat("x", maxKeyListBytes+1),
	}
	for name, text := range bad {
		if _, _, err := ParseKeyList(text); !errors.Is(err, ErrNotAKeyList) {
			t.Errorf("%s: %v", name, err)
		}
	}
	cfg := r.st.Config()
	cfg.Pending = &Pending{Op: OpAdd}
	b, _ := json.Marshal(keyListEnvelope{Kind: keyListKind, Config: cfg})
	if got, _, err := ParseKeyList(string(b)); err != nil || got.Pending != nil {
		t.Errorf("pending kept: %+v %v", got.Pending, err)
	}
}

func TestExportKeyListNeedsKeysAndNoUnfinishedChange(t *testing.T) {
	off := newRig(t)
	if _, _, err := off.st.ExportKeyList(); err == nil {
		t.Error("exported a key list with encryption off")
	}
}
