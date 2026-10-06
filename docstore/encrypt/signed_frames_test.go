package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

// Tests of the signed frame, the scan of a document's files and the races between clients
// (docs/encryption-and-passkeys.md C.5).

// sealRaw encrypts an already framed plaintext to recipients: what anyone who knows only their
// public keys can do, with any frame they like.
func sealRaw(t *testing.T, rs []age.Recipient, framed []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	wr, err := age.Encrypt(&buf, rs...)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wr.Write(framed); err != nil {
		t.Fatal(err)
	}
	if err := wr.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// publicRecipients are the keys a store writer is assumed to know: the owner's, the server's.
func (w *world) publicRecipients() []age.Recipient {
	return []age.Recipient{w.ana.r.Key, w.server.Recipient()}
}

// mainFile is one of the document's generations (not a version snapshot).
func (w *world) mainFile(t *testing.T, id string) string {
	t.Helper()
	for _, f := range w.files(t, id) {
		if strings.HasPrefix(f, id+"~") {
			return f
		}
	}
	t.Fatal("no generation")
	return ""
}

func (w *world) binding(t *testing.T, f string) Binding {
	t.Helper()
	b, err := w.ring.Binding(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// e2eeDoc makes a document with a history and switches it to end to end.
func (w *world) e2eeDoc(t *testing.T) string {
	t.Helper()
	id := w.history(t)
	if err := w.saver.SetProtection(context.Background(), "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	return id
}

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ageHeaderJunk starts like an age file and is not one.
func ageHeaderJunk(n int) []byte {
	return append(append([]byte(nil), header...), bytes.Repeat([]byte{byte(n)}, 64)...)
}

// ---- finding 1: decoy files ---------------------------------------------------------------

func TestDecoyFilesCannotHideADeletedRecord(t *testing.T) {
	ctx := context.Background()
	decoys := map[string]func(t *testing.T, w *world, id string){
		"junk with the age header": func(t *testing.T, w *world, id string) {
			for i := 0; i < 3; i++ {
				if err := w.inner.Save(ctx, id+"~deadbeef000"+string(rune('1'+i))+".open", ageHeaderJunk(i)); err != nil {
					t.Fatal(err)
				}
			}
		},
		"well formed files of someone else's": func(t *testing.T, w *world, id string) {
			stranger := newPerson(t, "stranger")
			for i := 0; i < 5; i++ {
				f := id + "~deadbeef000" + string(rune('1'+i)) + ".open"
				if err := w.inner.Save(ctx, f, sealRaw(t, []age.Recipient{stranger.r.Key}, frame(f, Binding{Level: ProtectionE2EE, Seq: 1}, []byte("x"), w.signer))); err != nil {
					t.Fatal(err)
				}
			}
		},
	}
	for name, plant := range decoys {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			id := w.e2eeDoc(t)
			saved := w.doc(t, id)
			if err := w.inner.Delete(ctx, KeysPrefix+id); err != nil {
				t.Fatal(err)
			}
			plant(t, w, id) // newest by the store's own times
			before := w.snapshot(t)
			err := w.ad.Save(ctx, id+"~cccccccccccc.open", []byte("x"))
			if !errors.Is(err, ErrDowngrade) && !errors.Is(err, ErrUnreadable) {
				t.Fatalf("Adapter.Save over a deleted record with decoys: %v", err)
			}
			if _, err := w.saver.Save(ctx, "m", docfile.Open, saved); err == nil {
				t.Error("Saver.Save over a deleted record with decoys returned nil")
			}
			// The owner's own change must not sign a fresh record over the deleted one.
			carol := newPerson(t, "carol")
			if err := w.sharing.Add(ctx, id, carol.r, carol.r.Fingerprint); !errors.Is(err, ErrDowngrade) && !errors.Is(err, ErrUnreadable) {
				t.Errorf("Add with decoys: %v", err)
			}
			if _, err := w.inner.Load(ctx, KeysPrefix+id); !errors.Is(err, docstore.ErrNotFound) {
				t.Errorf("a record was written over the deleted one: %v", err)
			}
			sameStore(t, "after the refusals", before, w.snapshot(t))
		})
	}
	// The junk is named: an unreadable file is not skipped as "no evidence".
	w := newWorld(t)
	id := w.e2eeDoc(t)
	_ = w.inner.Delete(ctx, KeysPrefix+id)
	junk := id + "~deadbeef0001.open"
	_ = w.inner.Save(ctx, junk, ageHeaderJunk(1))
	if err := w.ad.Save(ctx, id+"~cccccccccccc.open", []byte("x")); !errors.Is(err, ErrUnreadable) || !strings.Contains(err.Error(), junk) {
		t.Errorf("want ErrUnreadable naming the file: %v", err)
	}
}

// A document's files that are provably someone else's (the document is shared with us, a
// version snapshot was sealed to a key we do not have) are not an obstacle.
func TestForeignFilesAreSkippedNotFatal(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	stranger := newPerson(t, "stranger")
	f := id + "~deadbeef0001.open"
	if err := w.inner.Save(ctx, f, sealRaw(t, []age.Recipient{stranger.r.Key}, frame(f, Binding{}, []byte("x"), w.signer))); err != nil {
		t.Fatal(err)
	}
	if err := w.ad.Save(ctx, id+"~cccccccccccc.open", []byte("x")); err != nil {
		t.Errorf("a foreign file stopped a save: %v", err)
	}
}

// The store reports the times, so the file that looks newest proves nothing: the highest seq of
// ANY file that opens is what the record is compared with.
func TestHighestSeqOfAllFilesCountsNotTheNewestByTime(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	if err := w.sharing.Add(ctx, id, w.bo.r, w.bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	old := w.rawRecord(t, id)
	genuine := map[string][]byte{}
	for _, f := range w.files(t, id) {
		d, _ := w.inner.Load(ctx, f)
		genuine[f] = d.Data
	}
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w.replay(t, id, old) // validly signed, older, server level
	// One old, genuine generation put back: it is now the newest the store lists.
	f := w.mainFile(t, id)
	time.Sleep(2 * time.Millisecond)
	if err := w.inner.Save(ctx, f, genuine[f]); err != nil {
		t.Fatal(err)
	}
	if err := w.ad.Save(ctx, id+"~cccccccccccc.open", []byte("x")); !errors.Is(err, ErrDowngrade) {
		t.Errorf("Adapter.Save: %v", err)
	}
	carol := newPerson(t, "carol")
	if err := w.sharing.Add(ctx, id, carol.r, carol.r.Fingerprint); !errors.Is(err, ErrDowngrade) {
		t.Errorf("Add: %v", err)
	}
}

// ---- finding 2: the frame is signed --------------------------------------------------------

func TestForgedFramesAreRefused(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	f := w.mainFile(t, id)
	rb := w.binding(t, f)
	if !rb.Signed {
		t.Fatal("setup: the record of a document re-keyed by a signing adapter says its files are signed")
	}
	pub := w.publicRecipients()
	attacker, _ := NewSigner(bytes.Repeat([]byte{7}, 32))
	good := Binding{Level: ProtectionE2EE, Seq: rb.Seq, Hash: rb.Hash}

	forge := map[string][]byte{
		"unsigned (v2) frame claiming the record's level, seq and recipients": frame(f, good, []byte("forged"), nil),
		"unsigned level server frame":                                         frame(f, Binding{Seq: 0}, []byte("forged"), nil),
		"signed by a key that is not an owner's":                              frame(f, good, []byte("forged"), attacker),
	}
	// A genuine signed header put on other content.
	d, _ := w.inner.Load(ctx, f)
	r, _, err := w.ad.decrypt(ctx, f, d.Data)
	if err != nil {
		t.Fatal(err)
	}
	framed := readAll(t, r)
	data, _, err := unframe(f, framed)
	if err != nil {
		t.Fatal(err)
	}
	forge["a genuine signed header on other content"] = append(append([]byte(nil), framed[:len(framed)-len(data)]...), "forged"...)

	for name, fr := range forge {
		t.Run(name, func(t *testing.T) {
			if err := w.inner.Save(ctx, f, sealRaw(t, pub, fr)); err != nil {
				t.Fatal(err)
			}
			_, err := w.ad.Load(ctx, f)
			if !errors.Is(err, ErrForgedFrame) || !errors.Is(err, docstore.ErrDowngrade) {
				t.Errorf("Load: %v", err)
			}
			if err := w.ad.Save(ctx, id+"~cccccccccccc.open", []byte("x")); err == nil && name != "a genuine signed header on other content" {
				t.Error("Save went on over a forged file")
			}
		})
	}
}

// The limit of the old formats, stated: until a signing client re-keys a document its files may
// be unsigned, and an unsigned frame can then be minted by anyone who knows a recipient's public
// key. Repair writes signed frames and the record then says so.
func TestUnsignedFramesAreAcceptedUntilRepairThenForgedAndRequireSigned(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	w.legacyV1(t, id)
	f := w.mainFile(t, id)
	rb := w.binding(t, f)
	good := Binding{Level: ProtectionE2EE, Seq: rb.Seq, Hash: rb.Hash}
	forged := sealRaw(t, w.publicRecipients(), frame(f, good, []byte("forged"), nil))
	if err := w.inner.Save(ctx, f, forged); err != nil {
		t.Fatal(err)
	}
	// Before the upgrade the limit holds: this is accepted (with a warning logged once).
	if d, err := w.ad.Load(ctx, f); err != nil || string(d.Data) != "forged" {
		t.Fatalf("an unsigned frame of a not yet upgraded document is read as before: %v", err)
	}
	// RequireSigned closes it for a deployment whose files are all upgraded.
	strict := New(w.inner, w.ring)
	strict.RequireSigned = true
	if _, err := strict.Load(ctx, f); !errors.Is(err, ErrForgedFrame) {
		t.Errorf("RequireSigned: %v", err)
	}
	// A re-key (the owner's Repair) upgrades every file and pins it in the record.
	w.inner.Delete(ctx, f) // the forged file is not something to re-key
	if _, err := w.sharing.Repair(ctx, id); err != nil {
		t.Fatal(err)
	}
	for _, g := range w.files(t, id) {
		if v := w.frameVersion(t, g); v != 3 {
			t.Errorf("%s is v%d after Repair", g, v)
		}
	}
	rb = w.binding(t, id+"~aaaaaaaaaaaa.open")
	if !rb.Signed {
		t.Fatal("Repair did not say in the record that every file is signed")
	}
	g := w.mainFile(t, id)
	good = Binding{Level: ProtectionE2EE, Seq: rb.Seq, Hash: rb.Hash}
	if err := w.inner.Save(ctx, g, sealRaw(t, w.publicRecipients(), frame(g, good, []byte("forged"), nil))); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ad.Load(ctx, g); !errors.Is(err, ErrForgedFrame) {
		t.Errorf("after the upgrade an unsigned frame is forged: %v", err)
	}
}

func TestConfidentialSaveNeedsTheSigner(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	nosign := New(w.inner, w.ring) // no Signer
	before := w.snapshot(t)
	f := id + "~cccccccccccc.open"
	if err := nosign.Save(ctx, f, []byte("x")); !errors.Is(err, ErrNoFrameSigner) {
		t.Errorf("Save: %v", err)
	}
	if err := nosign.SaveIf(ctx, w.files(t, id)[0], []byte("x"), "x"); !errors.Is(err, ErrNoFrameSigner) && !errors.Is(err, docstore.ErrConflict) {
		t.Errorf("SaveIf: %v", err)
	}
	if _, err := nosign.RekeyDocument(ctx, id); !errors.Is(err, ErrNoFrameSigner) {
		t.Errorf("RekeyDocument: %v", err)
	}
	sameStore(t, "no signer", before, w.snapshot(t))
	// A document that is not confidential and has no signed record is still written without one.
	plain := w.history(t)
	if err := nosign.Save(ctx, plain+"~cccccccccccc.open", []byte("x")); err != nil {
		t.Errorf("server level, no signer: %v", err)
	}
	if fi := w.frameOf(t, plain+"~cccccccccccc.open"); fi.version != 2 {
		t.Errorf("v%d", fi.version)
	}
}

// Frames signed by any of the owner's signers verify; each owner client has its own.
func TestFramesOfAnotherOwnerSignerVerify(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	second, _ := NewSigner(bytes.Repeat([]byte{5}, 32))
	w.ring.People.OwnerSigners = append(w.ring.People.OwnerSigners, second.Public())
	other := New(w.inner, w.ring)
	other.Signer = second
	f := id + "~cccccccccccc.open"
	if err := other.Save(ctx, f, []byte("from another owner client")); err != nil {
		t.Fatal(err)
	}
	reader := New(w.inner, w.ring)
	if d, err := reader.Load(ctx, f); err != nil || string(d.Data) != "from another owner client" {
		t.Errorf("%v", err)
	}
	// Without the second signer listed it is forged.
	solo := w.ring
	solo.People.OwnerSigners = []ed25519.PublicKey{w.signer.Public()}
	if _, err := New(w.inner, solo).Load(ctx, f); !errors.Is(err, ErrForgedFrame) {
		t.Errorf("%v", err)
	}
}

// ---- finding 5: the equal-seq check --------------------------------------------------------

func TestSameSeqFileThatDisagreesWithTheRecordIsADowngrade(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.e2eeDoc(t)
	f := w.mainFile(t, id)
	rb := w.binding(t, f)
	wrongHash := rb.Hash
	wrongHash[0] ^= 0xff
	cases := map[string]Binding{
		"level":      {Level: "", Seq: rb.Seq, Hash: rb.Hash},
		"recipients": {Level: ProtectionE2EE, Seq: rb.Seq, Hash: wrongHash},
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			// Signed by the owner's signer, so it is not forged: the frame is genuine and
			// disagrees with the record at the same seq.
			crafted := sealRaw(t, w.publicRecipients(), frame(f, b, []byte("x"), w.signer))
			if err := w.inner.Save(ctx, f, crafted); err != nil {
				t.Fatal(err)
			}
			if _, err := w.ad.Load(ctx, f); !errors.Is(err, ErrDowngrade) || errors.Is(err, ErrForgedFrame) {
				t.Errorf("Load: %v", err)
			} else if !strings.Contains(err.Error(), "disagree") {
				t.Errorf("the message: %v", err)
			}
			if err := w.ad.Save(ctx, id+"~cccccccccccc.open", []byte("x")); !errors.Is(err, ErrDowngrade) {
				t.Errorf("Save: %v", err)
			}
		})
	}
}

// ---- finding 3: a switch completed between the reads ---------------------------------------

func TestSaveRacingASwitchByAnotherClientIsNotADowngrade(t *testing.T) {
	ctx := context.Background()
	other := func(w *world) *Sharing {
		ad := New(w.inner, w.ring)
		ad.Signer = w.signer
		return &Sharing{Store: w.inner, Adapter: ad, Signer: w.signer}
	}
	t.Run("Save", func(t *testing.T) {
		w := newWorld(t)
		id := w.history(t)
		b := other(w)
		fired := 0
		w.ad.afterBind = func() { // the record was read as "none"; now the other client switches
			if fired++; fired == 1 {
				if _, err := b.SetProtection(ctx, id, ProtectionE2EE); err != nil {
					t.Error(err)
				}
			}
		}
		f := id + "~cccccccccccc.open"
		if err := w.ad.Save(ctx, f, []byte("mine")); err != nil {
			t.Fatalf("a completed switch in between is not a downgrade: %v", err)
		}
		if fi := w.frameOf(t, f); fi.b.Level != ProtectionE2EE {
			t.Errorf("the save was sealed under the stale record: %+v", fi)
		}
		w.assertOnly(t, id, "after the race", []age.Identity{w.ana.id}, []age.Identity{w.server})
	})
	t.Run("SaveIf", func(t *testing.T) {
		w := newWorld(t)
		id := w.history(t)
		b := other(w)
		f := w.files(t, id)[0]
		d, err := w.ad.Load(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		fired := 0
		w.ad.afterBind = func() {
			if fired++; fired == 1 {
				if _, err := b.SetProtection(ctx, id, ProtectionE2EE); err != nil {
					t.Error(err)
				}
			}
		}
		err = w.ad.SaveIf(ctx, f, []byte("mine"), docstore.Digest(d.Data))
		if errors.Is(err, ErrDowngrade) {
			t.Fatalf("a completed switch in between is not a downgrade: %v", err)
		}
	})
	t.Run("mutate", func(t *testing.T) {
		w := newWorld(t)
		id := w.history(t)
		if err := w.sharing.Add(ctx, id, w.bo.r, w.bo.r.Fingerprint); err != nil {
			t.Fatal(err)
		}
		b := other(w)
		fired := 0
		w.sharing.afterRead = func() {
			if fired++; fired == 1 {
				if _, err := b.SetProtection(ctx, id, ProtectionE2EE); err != nil {
					t.Error(err)
				}
			}
		}
		carol := newPerson(t, "carol")
		if err := w.sharing.Add(ctx, id, carol.r, carol.r.Fingerprint); err != nil {
			t.Fatalf("a completed switch in between is not a downgrade: %v", err)
		}
		if c, err := w.ring.IsConfidential(ctx, id); err != nil || !c {
			t.Errorf("the other client's switch was undone: %v %v", c, err)
		}
		list, _ := w.sharing.List(ctx, id)
		if len(list) != 2 {
			t.Errorf("both changes should be in the list: %v", list)
		}
	})
	// A real replay is still refused the second time.
	w := newWorld(t)
	id, old := w.confidentialWithOldRecord(t)
	w.replay(t, id, old)
	if err := w.ad.Save(ctx, id+"~cccccccccccc.open", []byte("x")); !errors.Is(err, ErrDowngrade) {
		t.Errorf("replay: %v", err)
	}
}

// ---- finding 4: a stuck pending flag --------------------------------------------------------

func TestStuckPendingIsSurfacedByVerifyAndTheErrorNamesRepair(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	now := time.Unix(1_800_000_000, 0)
	w.sharing.now = func() time.Time { return now }
	w.saver.PendingCheck = PendingHook(w.sharing)

	// A switch cut short at a version snapshot: the record is pending, one file still server-wrapped.
	var version string
	for _, f := range w.files(t, id) {
		if strings.HasPrefix(f, "versions/") {
			version = f
			break
		}
	}
	w.inner.failOn.Store(version)
	var re *RekeyError
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); !errors.As(err, &re) {
		t.Fatalf("%v", err)
	}
	w.inner.failOn.Store("")

	st, err := w.sharing.PendingStatus(ctx, id)
	if err != nil || !st.Pending || st.Stuck || !st.Since.Equal(now) {
		t.Fatalf("a fresh pending flag is in flight, not stuck: %+v %v", st, err)
	}
	rep, err := w.saver.Verify(ctx, "m")
	if err != nil || len(rep.Pending) != 1 || rep.Pending[0] != id || len(rep.PendingStuck) != 0 {
		t.Fatalf("Verify: %+v %v", rep, err)
	}

	// An hour later the flag is stuck, and Verify says so.
	now = now.Add(time.Hour)
	if st, _ := w.sharing.PendingStatus(ctx, id); !st.Stuck {
		t.Errorf("%+v", st)
	}
	rep, err = w.saver.Verify(ctx, "m")
	if err != nil || len(rep.PendingStuck) != 1 || rep.PendingStuck[0] != id {
		t.Fatalf("Verify: %+v %v", rep, err)
	}
	// The limit is configurable.
	w.sharing.PendingWarnAfter = 2 * time.Hour
	if st, _ := w.sharing.PendingStatus(ctx, id); st.Stuck {
		t.Errorf("%+v", st)
	}
	w.sharing.PendingWarnAfter = 0

	// While pending the strict Load rule is off for the document (the weakness being reported).
	if _, err := w.ad.Load(ctx, version); err != nil {
		t.Errorf("a stale file of a pending document loads: %v", err)
	}
	// Repair finishes it, and nothing is reported afterwards.
	if _, err := w.sharing.Repair(ctx, id); err != nil {
		t.Fatal(err)
	}
	if rep, _ := w.saver.Verify(ctx, "m"); len(rep.Pending) != 0 || len(rep.PendingStuck) != 0 {
		t.Errorf("%+v", rep)
	}
	if st, _ := w.sharing.PendingStatus(ctx, id); st.Pending || !st.Since.IsZero() {
		t.Errorf("%+v", st)
	}

	// Once not pending, a file wrapped below the level is a downgrade, and the message says how
	// to go on.
	below := New(w.inner, Static{To: []age.Recipient{w.server.Recipient(), w.ana.r.Key}, Keys: []age.Identity{w.ana.id}})
	below.Signer = w.signer // a server-level copy the owner's own tooling wrote
	if err := below.Save(ctx, version, []byte("server wrapped")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ad.Load(ctx, version); !errors.Is(err, ErrDowngrade) || !strings.Contains(err.Error(), "Repair") {
		t.Errorf("the error should name Repair: %v", err)
	}
}

// ---- the Load side of finding 3 ----------------------------------------------------------------

func TestLoadRacingASwitchByAnotherClientIsNotADowngrade(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	f := w.mainFile(t, id)
	b := New(w.inner, w.ring)
	b.Signer = w.signer
	other := &Sharing{Store: w.inner, Adapter: b, Signer: w.signer}
	fired := 0
	w.ad.beforeBinding = func() { // the file was read at the server level; now the switch completes
		if fired++; fired == 1 {
			if _, err := other.SetProtection(ctx, id, ProtectionE2EE); err != nil {
				t.Error(err)
			}
		}
	}
	d, err := w.ad.Load(ctx, f)
	if err != nil {
		t.Fatalf("a completed switch in between is not a downgrade: %v", err)
	}
	if len(d.Data) == 0 {
		t.Error("no data")
	}
}

// ---- finding 7: the index ----------------------------------------------------------------------

func TestVerifyIndexWarnsWhenAnEmptyIndexCannotBeTold(t *testing.T) {
	ctx := context.Background()
	hasWarning := func(fs []IndexFinding) bool {
		for _, f := range fs {
			if f.Doc == IndexKey && errors.Is(f.Err, ErrIndexUnverifiable) && !errors.Is(f.Err, ErrDowngrade) {
				return true
			}
		}
		return false
	}
	// No floor, no index: an index that was deleted looks like none.
	w := newWorld(t)
	fs, err := w.sharing.VerifyIndex(ctx)
	if err != nil || !hasWarning(fs) {
		t.Errorf("no floor, no index: %v %v", fs, err)
	}
	// An index that lists a document is not empty: no warning, and a deleted one is a finding.
	id := w.e2eeDoc(t)
	if fs, err := w.sharing.VerifyIndex(ctx); err != nil || len(fs) != 0 {
		t.Errorf("%v %v", fs, err)
	}
	// Emptied again (the document is back at server level): empty, and without a floor it
	// cannot be told from a deleted one.
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionServer); err != nil {
		t.Fatal(err)
	}
	if fs, _ := w.sharing.VerifyIndex(ctx); !hasWarning(fs) {
		t.Errorf("an emptied index without a floor: %v", fs)
	}
	// With a floor that has seen an index, an empty one is believed (a deleted one would be
	// ErrStaleRecord).
	wf := newWorldFloor(t, &MemorySeqFloor{})
	idf := wf.e2eeDoc(t)
	if err := wf.saver.SetProtection(ctx, "m", idf, saver.ProtectionServer); err != nil {
		t.Fatal(err)
	}
	if fs, err := wf.sharing.VerifyIndex(ctx); err != nil || hasWarning(fs) {
		t.Errorf("a floor has seen the index: %v %v", fs, err)
	}
	if err := wf.inner.Delete(ctx, IndexKey); err != nil {
		t.Fatal(err)
	}
	if _, err := wf.sharing.VerifyIndex(ctx); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("a deleted index with a floor: %v", err)
	}
	// A floor that never saw an index does not make an empty one trustworthy.
	wn := newWorldFloor(t, &MemorySeqFloor{})
	if fs, _ := wn.sharing.VerifyIndex(ctx); !hasWarning(fs) {
		t.Errorf("an empty floor: %v", fs)
	}
}

// plainStore is a store with no compare-and-swap but a Lock, and a Load of the index that waits
// (up to a timeout) for a second reader: two index changes that are not serialized both read the
// same index.
type plainStore struct {
	m        *docstore.MemoryAdapter
	lock     sync.Mutex
	mu       sync.Mutex
	readers  int
	arrivals int
}

func (p *plainStore) Save(ctx context.Context, id string, b []byte) error {
	return p.m.Save(ctx, id, b)
}
func (p *plainStore) List(ctx context.Context) ([]docstore.Meta, error) { return p.m.List(ctx) }
func (p *plainStore) Delete(ctx context.Context, id string) error       { return p.m.Delete(ctx, id) }
func (p *plainStore) Lock(context.Context) (func(), error) {
	p.lock.Lock()
	return p.lock.Unlock, nil
}
func (p *plainStore) Load(ctx context.Context, id string) (docstore.Document, error) {
	d, err := p.m.Load(ctx, id)
	if id == IndexKey {
		p.mu.Lock()
		p.arrivals++
		p.mu.Unlock()
		for i := 0; i < 150; i++ {
			p.mu.Lock()
			n := p.arrivals
			p.mu.Unlock()
			if n >= 2 {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	return d, err
}

func TestIndexChangesOnAStoreWithoutCASAreSerializedByItsLock(t *testing.T) {
	ctx := context.Background()
	p := &plainStore{m: docstore.NewMemoryAdapter()}
	signer, _ := NewSigner(bytes.Repeat([]byte{3}, 32))
	s := &Sharing{Store: p, Signer: signer}
	docs := []string{"11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"}
	var wg sync.WaitGroup
	for _, d := range docs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.setIndex(ctx, d, ProtectionE2EE, 1); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	idx, err := s.Index(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if _, ok := idx.Docs[d]; !ok {
			t.Errorf("%s was lost from the index by a concurrent change: %v", d, idx.Docs)
		}
	}
}
