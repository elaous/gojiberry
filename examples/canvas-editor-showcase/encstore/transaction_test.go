package encstore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
	"strings"
	"testing"
)

func plainRig(t *testing.T) *rig {
	r := newRig(t)
	withPlainDocs(t, r)
	r.raw.writes, r.persist = 0, 0
	return r
}

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
	cfg.Pending.Seals = []Seal{makeSeal(attackerKey.Unlock.Signer, cfg.Pending.To)}
	r.open(t, cfg)
	r.unlockPasskey(t)
	if err := r.st.Recover(ctx, true); !errors.Is(err, ErrConfigChanged) {
		t.Fatalf("a journal signed by a stranger: %v", err)
	}
}
