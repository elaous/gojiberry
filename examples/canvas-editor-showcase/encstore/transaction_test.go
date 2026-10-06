package encstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"github.com/elaous/gojiberry/saver"
)

// plainRig is a store with encryption off and three documents (and versions) saved in the clear.
func plainRig(t *testing.T) *rig {
	r := newRig(t)
	withPlainDocs(t, r)
	r.raw.writes, r.persist = 0, 0
	return r
}

// everyFailure runs op on a fresh rig made by setup, failing the nth write of the storage, for
// each n until op gets through (it then wrote fewer than n times). check sees the rig after each
// failure, with what the storage held before. Returns how many failures were tried.
func everyFailure(t *testing.T, setup func(t *testing.T) *rig, op func(r *rig) error, check func(t *testing.T, r *rig, before map[string]string, cfgBefore Config)) int {
	t.Helper()
	tried := 0
	for n := 1; n < 400; n++ {
		r := setup(t)
		before, cfgBefore := r.snapshot(t), r.saved
		r.raw.writes, r.persist, r.raw.failAt = 0, 0, n
		err := op(r)
		if err == nil {
			if n == 1 {
				t.Fatal("the change wrote nothing: nothing to inject a failure into")
			}
			return tried
		}
		tried++
		t.Run(fmt.Sprintf("write %d fails", n), func(t *testing.T) {
			if !strings.Contains(err.Error(), "injected failure") {
				t.Errorf("not the injected error: %v", err)
			}
			r.raw.heal()
			check(t, r, before, cfgBefore)
		})
	}
	t.Fatal("the change never got through")
	return tried
}

func TestEnableFailsAtEveryStepAndLeavesTheStoreAsItWas(t *testing.T) {
	ctx := context.Background()
	tried := everyFailure(t, plainRig,
		func(r *rig) error {
			rk, _ := recovery(t)
			return r.st.Enable(ctx, Options{}, passkey(t, r), rk)
		},
		func(t *testing.T, r *rig, before map[string]string, _ Config) {
			sameSnapshot(t, "storage", r.snapshot(t), before)
			if r.st.Enabled() || r.st.Interrupted() || len(r.saved.Owners) != 0 || r.saved.Pending != nil {
				t.Errorf("not as it was: enabled %v interrupted %v saved %+v", r.st.Enabled(), r.st.Interrupted(), r.saved)
			}
			// Nothing is half on: the clear documents still read, and a retry goes through.
			if !strings.Contains(r.stored(t), "text of alpha") {
				t.Error("the clear documents are not as they were")
			}
			rk, _ := recovery(t)
			if err := r.st.Enable(ctx, Options{}, passkey(t, r), rk); err != nil {
				t.Fatalf("the retry: %v", err)
			}
			if strings.Contains(r.stored(t), "text of alpha") || strings.Contains(r.stored(t), "version of beta") {
				t.Error("the retry left text in the clear")
			}
		})
	if tried < 6 {
		t.Fatalf("only %d steps were tried", tried)
	}
}

// The journal comes first: if it cannot be written nothing has been touched, and if the commit
// cannot be written the files are put back.
func TestEnableFailsWhenTheKeyListCannotBeKept(t *testing.T) {
	ctx := context.Background()
	for _, failAt := range []int{1, 2} {
		r := plainRig(t)
		before := r.snapshot(t)
		r.failPersist = func(n int) error {
			if n == failAt {
				return errors.New("the browser refused to keep the key list")
			}
			return nil
		}
		rk, _ := recovery(t)
		if err := r.st.Enable(ctx, Options{}, passkey(t, r), rk); err == nil {
			t.Fatalf("persist %d: no error", failAt)
		}
		sameSnapshot(t, fmt.Sprintf("persist %d failing", failAt), r.snapshot(t), before)
		if r.st.Enabled() || r.saved.Pending != nil || len(r.saved.Owners) != 0 {
			t.Errorf("persist %d: enabled %v saved %+v", failAt, r.st.Enabled(), r.saved)
		}
		if failAt == 1 && r.raw.writes != 0 {
			t.Errorf("the journal failed, but %d files were written", r.raw.writes)
		}
	}
}

// A store that says "written" and keeps something else is caught by the read-back.
func TestAStoreThatLiesAboutAWriteIsCaught(t *testing.T) {
	ctx := context.Background()
	for _, n := range []int{1, 3, 5} {
		r := plainRig(t)
		before := r.snapshot(t)
		r.raw.lieAt = n
		rk, _ := recovery(t)
		err := r.st.Enable(ctx, Options{}, passkey(t, r), rk)
		if err == nil || !strings.Contains(err.Error(), "did not keep") {
			t.Fatalf("lie at %d: %v", n, err)
		}
		sameSnapshot(t, "after a lie", r.snapshot(t), before)
		if r.st.Enabled() {
			t.Fatal("enabled after a lie")
		}
	}
}

// The browser's quota is the likely failure: with room to spare for what age adds the change goes
// through; when the storage is nearly full a store that knows its capacity refuses before it
// writes anything (not even the journal), and one that does not know finds out when a write fails
// and puts everything back.
func TestQuotaIsCheckedBeforeAnythingIsWritten(t *testing.T) {
	ctx := context.Background()
	r := plainRig(t)
	before := r.snapshot(t)
	used := r.raw.used()
	r.st.Capacity = int64(used + 500) // age needs more than that for three documents and versions
	rk, _ := recovery(t)
	err := r.st.Enable(ctx, Options{}, passkey(t, r), rk)
	var nr *NoRoomError
	if !errors.As(err, &nr) || !errors.Is(err, docstore.ErrStorageFull) || nr.Need <= nr.Free {
		t.Fatalf("%v", err)
	}
	if r.persist != 0 || r.raw.writes != 0 {
		t.Errorf("the check came late: %d journal writes, %d file writes", r.persist, r.raw.writes)
	}
	sameSnapshot(t, "after the refusal", r.snapshot(t), before)
	// Plenty of room: it goes through.
	r.st.Capacity = int64(used + 100<<10)
	rk2, _ := recovery(t)
	if err := r.st.Enable(ctx, Options{}, passkey(t, r), rk2); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaFailureMidWayPutsEverythingBack(t *testing.T) {
	ctx := context.Background()
	r := plainRig(t)
	before := r.snapshot(t)
	r.raw.limit = r.raw.used() + 600 // the first files fit, then the browser says no
	rk, _ := recovery(t)
	err := r.st.Enable(ctx, Options{}, passkey(t, r), rk) // Capacity unknown: no pre-check
	if !errors.Is(err, docstore.ErrStorageFull) {
		t.Fatalf("%v", err)
	}
	r.raw.limit = 0
	sameSnapshot(t, "after the quota failure", r.snapshot(t), before)
	if r.st.Enabled() || r.saved.Pending != nil || len(r.saved.Owners) != 0 {
		t.Errorf("enabled %v saved %+v", r.st.Enabled(), r.saved)
	}
}

// crash makes a failure that also stops the rollback (every write from the nth on fails, and so
// does keeping the key list after the journal): all the store can do is leave the journal.
func (r *rig) crashAt(n int) {
	r.raw.failAt, r.raw.failFrom = n, true
	r.failPersist = func(p int) error {
		if p > 1 {
			return errors.New("crashed")
		}
		return nil
	}
}

func TestACrashDuringEnableIsFinishedOrUndoneOnTheNextLoad(t *testing.T) {
	ctx := context.Background()
	for _, finish := range []bool{true, false} {
		t.Run(fmt.Sprintf("finish %v", finish), func(t *testing.T) {
			r := plainRig(t)
			before := r.snapshot(t)
			r.crashAt(3)
			rk, rsecret := recovery(t)
			err := r.st.Enable(ctx, Options{}, passkey(t, r), rk)
			if !errors.Is(err, ErrRollbackFailed) {
				t.Fatalf("%v", err)
			}
			if !r.st.Interrupted() {
				t.Fatal("the store does not know it is interrupted")
			}
			// The next load: the journal is in the config; the storage is a mix.
			r.raw.heal()
			r.failPersist = nil
			r.open(t, r.saved)
			if !r.st.Interrupted() || r.saved.Pending == nil || r.saved.Pending.Op != OpEnable {
				t.Fatalf("no journal: %+v", r.saved)
			}
			enc, plain := 0, 0
			for id, b := range r.snapshot(t) {
				if encrypt.DefaultSkip(id) {
					continue
				}
				if strings.HasPrefix(b, "age-encryption.org/") {
					enc++
				} else {
					plain++
				}
			}
			if enc == 0 || plain == 0 {
				t.Fatalf("the crash left %d encrypted and %d clear files: not a mix", enc, plain)
			}
			// Until it is settled: reads work (the clear ones as they are), writes are refused, and
			// nothing can be made confidential.
			if err := r.st.Adapter().Save(ctx, "00000000-0000-4000-8000-000000000001.open", []byte("x")); !errors.Is(err, ErrInterrupted) {
				t.Errorf("a write while interrupted: %v", err)
			}
			if err := r.st.Check(ctx, target, "x"); !errors.Is(err, ErrInterrupted) {
				t.Errorf("Check: %v", err)
			}
			if err := r.st.AddOwner(ctx, rk); !errors.Is(err, ErrInterrupted) {
				t.Errorf("AddOwner: %v", err)
			}
			_ = rsecret
			r.unlockPasskey(t) // the new key is already a key of the journal
			if err := r.st.Recover(ctx, finish); err != nil {
				t.Fatal(err)
			}
			if r.st.Interrupted() || r.saved.Pending != nil {
				t.Fatal("still interrupted")
			}
			if finish {
				if !r.st.Enabled() || len(r.saved.Owners) != 2 {
					t.Fatalf("not enabled: %+v", r.saved)
				}
				r.open(t, r.saved)
				r.unlockPasskey(t)
				for _, w := range []string{"text of alpha", "version of beta"} {
					if strings.Contains(r.stored(t), w) {
						t.Errorf("%q is in the clear after finishing", w)
					}
				}
				entries, _, _, err := r.saver.Overview(ctx, target)
				if err != nil || len(entries) != 3 {
					t.Fatalf("%v %v", entries, err)
				}
				for _, e := range entries {
					if _, _, err := r.saver.LoadChecked(ctx, target, e.ID); err != nil {
						t.Errorf("%s does not open: %v", e.Name, err)
					}
				}
			} else {
				if r.st.Enabled() || len(r.saved.Owners) != 0 {
					t.Fatalf("still on: %+v", r.saved)
				}
				sameSnapshot(t, "after undoing", r.snapshot(t), before)
			}
		})
	}
}

// Sealing files that are still in the clear needs no private key, but once some file is encrypted a
// seal-less journal (nobody could sign it: SSH keys only) is finished only by a key that opens every
// encrypted file, so it was sealed to it: an SSH-only enable cut short is finished with the SSH key.
func TestAnInterruptedSSHOnlyEnableFinishesWithTheSSHKey(t *testing.T) {
	ctx := context.Background()
	r := plainRig(t)
	pub, priv := sshKeyPair(t, "me@laptop")
	k, err := NewSSHKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	r.crashAt(3)
	if err := r.st.Enable(ctx, Options{}, k); !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("%v", err)
	}
	r.raw.heal()
	r.failPersist = nil
	r.open(t, r.saved)
	// Some files are encrypted already: without the key nothing vouches for the journal.
	if err := r.st.Recover(ctx, true); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("finishing without a key: %v", err)
	}
	if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
		t.Fatal(err)
	}
	if err := r.st.Recover(ctx, true); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved)
	if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
		t.Fatal(err)
	}
	entries, _, _, _ := r.saver.Overview(ctx, target)
	for _, e := range entries {
		if _, _, err := r.saver.LoadChecked(ctx, target, e.ID); err != nil {
			t.Errorf("%s: %v", e.Name, err)
		}
	}
	if strings.Contains(r.stored(t), "text of gamma") {
		t.Error("in the clear")
	}
}

// A seal-less enable journal over files that are all still in the clear is finished without a key.
func TestAnSSHOnlyEnableThatConvertedNothingFinishesWithoutAKey(t *testing.T) {
	ctx := context.Background()
	r := plainRig(t)
	pub, priv := sshKeyPair(t, "me@laptop")
	k, _ := NewSSHKey(pub)
	r.crashAt(1)
	if err := r.st.Enable(ctx, Options{}, k); !errors.Is(err, ErrRollbackFailed) {
		t.Fatalf("%v", err)
	}
	r.raw.heal()
	r.failPersist = nil
	r.open(t, r.saved)
	if err := r.st.Recover(ctx, true); err != nil {
		t.Fatal(err)
	}
	r.open(t, r.saved)
	if err := r.st.UnlockSSH(bytes.Clone(priv)); err != nil {
		t.Fatal(err)
	}
}

// A journal that a store writer made up is not finished: it must carry the signature of one of the
// store's own keys.
func TestAForgedJournalIsNotFinished(t *testing.T) {
	ctx := context.Background()
	r, _ := encryptedRig(t)
	attackerKey, _ := recovery(t)
	cfg := r.st.Config()
	cfg.Pending = &Pending{Op: OpAdd, To: append(r.st.Owners(), ownerOf(attackerKey))} // no seal
	r.open(t, cfg)
	r.unlockPasskey(t)
	if err := r.st.Recover(ctx, true); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("an unsigned journal: %v", err)
	}
	// Signed by a key that is not an owner: no better.
	cfg.Pending.Seals = []Seal{makeSeal(attackerKey.Unlock.Signer, cfg.Pending.To)}
	r.open(t, cfg)
	r.unlockPasskey(t)
	if err := r.st.Recover(ctx, true); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("a journal signed by a stranger: %v", err)
	}
}

// confidentialRig is encryptedRig with three documents and a confidential one.
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
			// Still unlocked, still working: it is not left half changed.
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
	// The key list is still the old one, so the old keys unlock; the retry action finishes it.
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

// A recovery key becomes an owner only after its secret was shown and confirmed saved: a key whose
// secret nobody saw would count as a way back that does not exist.
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
	// And it does not make the passkey-only store look recoverable.
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
	// Refusals: unknown, and a way back that would be lost.
	if err := r.st.RemoveOwner(ctx, "SHA256:nobody"); !errors.Is(err, ErrNoSuchOwner) {
		t.Fatalf("%v", err)
	}
	before := r.snapshot(t)
	// The store is unlocked with the passkey: removing the SSH key is allowed; the document list
	// signed by the passkey stays valid.
	if err := r.st.RemoveOwner(ctx, sshFP); err != nil {
		t.Fatal(err)
	}
	if len(r.st.Owners()) != 2 {
		t.Fatalf("%+v", r.st.Owners())
	}
	if reflect.DeepEqual(before, r.snapshot(t)) {
		t.Fatal("nothing was re-encrypted")
	}
	// The removed key no longer opens anything saved.
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
	// Now the passkey is removed too, while unlocked with the recovery key: the lists signed by it
	// are signed again by the recovery key, so the confidential document still opens afterwards.
	if err := r.st.UnlockRecovery(rsecret); err != nil {
		t.Fatal(err)
	}
	if err := r.st.RemoveOwner(ctx, passkeyFP); !errors.Is(err, ErrWouldLoseRecovery) {
		// A recovery key alone is recoverable (it is not a passkey): this must work.
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
	// The signed list of the document verifies under the keys that remain: it was signed again.
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
	// Taking the recovery key out would leave passkeys only.
	if err := r.st.RemoveOwner(ctx, recoveryFP); !errors.Is(err, ErrWouldLoseRecovery) {
		t.Fatalf("%v", err)
	}
	// Unlocked with the passkey alone, the passkey cannot be the one removed.
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
	// The recovery key (whose seal predates the swap) still unlocks: the link chain reaches the
	// new list.
	r.st.Lock()
	if err := r.st.UnlockRecovery(rsecret); err != nil {
		t.Fatalf("the recovery key after the swap: %v", err)
	}
	// A passkey-only store may swap its passkey (it is no worse); a swap that gives up a way back is refused.
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

// Disable holds the storage so that a save made while it runs cannot leave ciphertext behind: the
// save waits, and then finds encryption off (it is refused rather than written under a key that is
// gone). Without the hold the save lands in the middle of the pass and its ciphertext is never
// decrypted.
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
			return // wait for the pass to be under way: a save before it would only be part of it
		}
		go func() { saveErr <- r.st.Adapter().Save(ctx, newID, []byte("saved during the disable")) }()
		// Give the save every chance to get in: without the hold it lands now.
		time.Sleep(150 * time.Millisecond)
	}
	if err := r.st.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	r.raw.loadHook = nil
	err := <-saveErr
	// Whatever the save did, no ciphertext may be left in a store that is no longer encrypted.
	for id, b := range r.snapshot(t) {
		if !encrypt.DefaultSkip(id) && strings.HasPrefix(b, "age-encryption.org/") {
			t.Errorf("%s is ciphertext in a store that is not encrypted (the concurrent save returned %v)", id, err)
		}
	}
	if err == nil {
		// It was allowed: it must then be readable as plain text.
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
	// A store can be turned on again from nothing.
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
		// Cut short: the journal is still there, and part of the data too.
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

// The config holds public keys, signatures and the journal: no secret of any key, the passkey's
// PRF output and identity included.
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
	// Every config that was ever handed to Persist, and the one the store reports.
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
	// And during a change: the journal is a config too.
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
	// A key that is not an owner, a file that is not a key, a passkey secret that is not the owner's.
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
	// And on success.
	good := bytes.Clone(r.secret)
	if err := r.st.UnlockPasskey(good); err != nil || !bytes.Equal(good, make([]byte, 32)) {
		t.Errorf("success: %v, cleared %v", err, bytes.Equal(good, make([]byte, 32)))
	}
}

// --- binding the key list to the keys ---

func attackerOwner(t *testing.T) Owner {
	t.Helper()
	k, _, _ := NewRecoveryKey("helpful backup")
	return ownerOf(k)
}

func TestAReplacedKeyListIsRefusedAtUnlock(t *testing.T) {
	r, rsecret := encryptedRig(t)
	r.st.Lock()
	cfg := r.st.Config()
	// Someone who can write the storage adds their own key to the list.
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
	// The genuine list unlocks.
	r.open(t, cfg)
	r.unlockPasskey(t)
	// A forged link from the genuine list to the evil one, signed by the attacker's own key.
	k, _, _ := NewRecoveryKey("x")
	from, to := ownersDigest(cfg.Owners), ownersDigest(evil.Owners)
	evil.Links = append(evil.Links, Link{From: from, To: evil.Owners, Signer: hexOf(k.SignerPub),
		Sig: hexSig(k.Unlock.Signer.Sign(linkMessage(from, to)))})
	r.open(t, evil)
	if err := r.st.UnlockPasskey(bytes.Clone(r.secret)); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("a link signed by a stranger: %v", err)
	}
}

// A legitimate change made with one key is accepted by the others, whose seals predate it.
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
	// The SSH key has no seal yet (trust on first use) and gets one on this unlock; after that a
	// replaced list is caught for it too.
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

// --- small helpers ---

func slicesClone[T any](s []T) []T { return append([]T(nil), s...) }

func hexOf(b []byte) string { return fmt.Sprintf("%x", b) }

func hexSig(b []byte) string { return fmt.Sprintf("%x", b) }
