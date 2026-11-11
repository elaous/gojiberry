package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	"filippo.io/age"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

func (w *world) legacyV1(t *testing.T, id string) {
	t.Helper()
	ctx := context.Background()
	w.rewriteRecord(t, id, func(r *sharedRecord) { r.Signed = false }) // a record from before frames were signed
	for _, f := range w.files(t, id) {
		d, err := w.inner.Load(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		plain, _, _, err := w.ad.open(ctx, f, d.Data)
		if err != nil {
			t.Fatal(err)
		}
		rs, err := w.ring.Recipients(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		wr, err := age.Encrypt(&buf, rs...)
		if err != nil {
			t.Fatal(err)
		}
		fr := append([]byte(nil), frameMagicV1...)
		fr = binary.AppendUvarint(fr, uint64(len(f)))
		fr = append(fr, f...)
		fr = append(fr, plain...)
		if _, err := wr.Write(fr); err != nil {
			t.Fatal(err)
		}
		if err := wr.Close(); err != nil {
			t.Fatal(err)
		}
		if err := w.inner.Save(ctx, f, buf.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
}

func (w *world) rewriteRecord(t *testing.T, id string, fn func(*sharedRecord)) {
	t.Helper()
	ctx := context.Background()
	rec, digest, err := loadRecord(ctx, w.inner, id, w.ring.People.OwnerSigners, nil)
	if err != nil || digest == "" {
		return
	}
	fn(&rec)
	payload, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(signedRecord{Payload: payload, Sig: ed25519.Sign(w.signer.key, recordMessage(id, payload)), Signer: w.signer.Public()})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.inner.Save(ctx, KeysPrefix+id, data); err != nil {
		t.Fatal(err)
	}
}

func (w *world) legacyRecordBytes(t *testing.T, id string, raw []byte) []byte {
	t.Helper()
	rec, err := verifyRecord(raw, id, w.ring.People.OwnerSigners)
	if err != nil {
		t.Fatal(err)
	}
	rec.Signed = false
	payload, _ := json.Marshal(rec)
	data, _ := json.Marshal(signedRecord{Payload: payload, Sig: ed25519.Sign(w.signer.key, recordMessage(id, payload)), Signer: w.signer.Public()})
	return data
}

func (w *world) frameOf(t *testing.T, f string) frameInfo {
	t.Helper()
	d, err := w.inner.Load(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	_, fi, _, err := w.ad.open(context.Background(), f, d.Data)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

func (w *world) frameVersion(t *testing.T, f string) int { return w.frameOf(t, f).version }

func (w *world) snapshot(t *testing.T) map[string]string {
	t.Helper()
	ctx := context.Background()
	metas, err := w.inner.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range metas {
		d, err := w.inner.Load(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		out[m.ID] = docstore.Digest(d.Data)
	}
	return out
}

func sameStore(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for id, d := range before {
		if a, ok := after[id]; !ok {
			t.Errorf("%s: %s was removed", what, id)
		} else if a != d {
			t.Errorf("%s: %s was changed", what, id)
		}
	}
	for id := range after {
		if _, ok := before[id]; !ok {
			t.Errorf("%s: %s was written", what, id)
		}
	}
}

func (w *world) confidentialWithOldRecord(t *testing.T) (id string, old []byte) {
	t.Helper()
	ctx := context.Background()
	id = w.history(t)
	if err := w.sharing.Add(ctx, id, w.bo.r, w.bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	old = w.rawRecord(t, id)
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	return id, old
}

func (w *world) doc(t *testing.T, id string) docfile.Document {
	t.Helper()
	d, err := w.saver.Load(context.Background(), "m", id)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestFrameV2CarriesLevelSeqAndRecipientHash(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, _ := w.confidentialWithOldRecord(t)
	rec, _, err := loadRecord(ctx, w.inner, id, w.ring.People.OwnerSigners, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := rec.binding(true)
	for _, f := range w.files(t, id) {
		fi := w.frameOf(t, f)
		if fi.version != 3 || fi.b.Level != ProtectionE2EE || fi.b.Seq == 0 || fi.b.Seq > want.Seq || fi.b.Hash != want.Hash || fi.b.Hash == ([32]byte{}) {
			t.Errorf("%s: %+v (record %+v)", f, fi, want)
		}
	}
	w2 := newWorld(t)
	id2 := w2.history(t)
	for _, f := range w2.files(t, id2) {
		if fi := w2.frameOf(t, f); fi.version != 3 || fi.b.Seq != 0 || fi.b.Level != "" {
			t.Errorf("%s: %+v", f, fi)
		}
	}
	if _, err := w.sharing.Remove(ctx, id, w.bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	for _, f := range w.files(t, id) {
		if fi := w.frameOf(t, f); fi.b.Hash == want.Hash {
			t.Errorf("%s: the hash did not change with the list", f)
		}
	}
}

func TestReplayedRecordThenSaveIsRefusedAndNothingIsWritten(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id, old := w.confidentialWithOldRecord(t)
	saved := w.doc(t, id)
	w.replay(t, id, old) // validly signed, older, server level
	before := w.snapshot(t)

	if err := w.ad.Save(ctx, id+"~cccccccccccc.open", []byte("x")); !errors.Is(err, ErrDowngrade) || !errors.Is(err, docstore.ErrDowngrade) {
		t.Fatalf("Adapter.Save: %v", err)
	} else if msg := err.Error(); !bytes.Contains([]byte(msg), []byte("older signed record was put back")) {
		t.Errorf("the message should say what happened: %v", err)
	}
	if err := w.ad.SaveIf(ctx, w.files(t, id)[0], []byte("x"), "x"); !errors.Is(err, ErrDowngrade) && !errors.Is(err, docstore.ErrConflict) {
		t.Errorf("Adapter.SaveIf: %v", err)
	}
	if _, err := w.saver.Save(ctx, "m", docfile.Open, saved); !errors.Is(err, ErrDowngrade) {
		t.Errorf("Saver.Save: %v", err)
	}
	if _, err := w.saver.SaveVersion(ctx, "m", docfile.Open, saved); !errors.Is(err, ErrDowngrade) {
		t.Errorf("Saver.SaveVersion: %v", err)
	}
	for _, f := range w.files(t, id) {
		if _, err := w.ad.Load(ctx, f); !errors.Is(err, ErrDowngrade) {
			t.Errorf("Load %s: %v", f, err)
		}
	}
	if _, err := w.saver.Load(ctx, "m", id); !errors.Is(err, ErrDowngrade) {
		t.Errorf("Saver.Load: %v", err)
	}
	carol := newPerson(t, "carol")
	if err := w.sharing.Add(ctx, id, carol.r, carol.r.Fingerprint); !errors.Is(err, ErrDowngrade) {
		t.Errorf("Add: %v", err)
	}
	if _, err := w.sharing.Remove(ctx, id, w.bo.r.Fingerprint); !errors.Is(err, ErrDowngrade) {
		t.Errorf("Remove: %v", err)
	}
	if _, err := w.sharing.SetProtection(ctx, id, ProtectionE2EE); !errors.Is(err, ErrDowngrade) {
		t.Errorf("SetProtection: %v", err)
	}
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionServer); !errors.Is(err, ErrDowngrade) {
		t.Errorf("Saver.SetProtection: %v", err)
	}
	if _, err := w.sharing.Repair(ctx, id); !errors.Is(err, ErrDowngrade) {
		t.Errorf("Repair: %v", err)
	}
	if _, err := w.ad.RekeyDocument(ctx, id); !errors.Is(err, ErrDowngrade) {
		t.Errorf("RekeyDocument: %v", err)
	}
	if _, err := w.ad.RekeyAll(ctx); !errors.Is(err, ErrDowngrade) {
		t.Errorf("RekeyAll: %v", err)
	}
	sameStore(t, "after the refusals", before, w.snapshot(t))
}

func TestDeletedRecordThenSaveAndLoadAreRefused(t *testing.T) {
	ctx := context.Background()
	for _, level := range []string{"e2ee", "server, shared"} {
		t.Run(level, func(t *testing.T) {
			w := newWorld(t)
			id := w.history(t)
			if level == "e2ee" {
				if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
					t.Fatal(err)
				}
			} else if err := w.sharing.Add(ctx, id, w.bo.r, w.bo.r.Fingerprint); err != nil {
				t.Fatal(err)
			}
			saved := w.doc(t, id)
			if err := w.inner.Delete(ctx, KeysPrefix+id); err != nil {
				t.Fatal(err)
			}
			before := w.snapshot(t)
			if err := w.ad.Save(ctx, id+"~cccccccccccc.open", []byte("x")); !errors.Is(err, ErrDowngrade) {
				t.Errorf("Adapter.Save: %v", err)
			} else if !bytes.Contains([]byte(err.Error()), []byte("was deleted")) {
				t.Errorf("the message should say it was deleted: %v", err)
			}
			if _, err := w.saver.Save(ctx, "m", docfile.Open, saved); !errors.Is(err, ErrDowngrade) {
				t.Errorf("Saver.Save: %v", err)
			}
			if _, err := w.saver.SaveVersion(ctx, "m", docfile.Open, saved); !errors.Is(err, ErrDowngrade) {
				t.Errorf("Saver.SaveVersion: %v", err)
			}
			for _, f := range w.files(t, id) {
				if _, err := w.ad.Load(ctx, f); !errors.Is(err, ErrDowngrade) {
					t.Errorf("Load %s: %v", f, err)
				}
			}
			if _, err := w.saver.Load(ctx, "m", id); !errors.Is(err, ErrDowngrade) {
				t.Errorf("Saver.Load: %v", err)
			}
			if _, err := w.sharing.SetProtection(ctx, id, ""); !errors.Is(err, ErrDowngrade) {
				t.Errorf("Sharing.SetProtection: %v", err)
			}
			if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionServer); level == "e2ee" && !errors.Is(err, ErrDowngrade) {
				t.Errorf("Saver.SetProtection: %v", err)
			}
			if _, err := w.ad.RekeyDocument(ctx, id); !errors.Is(err, ErrDowngrade) {
				t.Errorf("RekeyDocument: %v", err)
			}
			if _, err := w.ad.RekeyAll(ctx); !errors.Is(err, ErrDowngrade) {
				t.Errorf("RekeyAll: %v", err)
			}
			if level == "e2ee" {
				sameStore(t, "after the refusals", before, w.snapshot(t))
			}
		})
	}
	w := newWorld(t)
	id := w.history(t)
	d := w.doc(t, id)
	if _, err := w.saver.Save(ctx, "m", docfile.Open, d); err != nil {
		t.Fatal(err)
	}
	if _, err := w.saver.Load(ctx, "m", id); err != nil {
		t.Fatal(err)
	}
}

func TestLegitimateSwitchesNeverLookLikeADowngrade(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	var version string
	for _, f := range w.files(t, id) {
		if len(f) > 9 && f[:9] == "versions/" {
			version = f
			break
		}
	}
	loadAll := func(when string) {
		t.Helper()
		for _, f := range w.files(t, id) {
			if _, err := w.ad.Load(ctx, f); err != nil {
				t.Errorf("%s: Load %s: %v", when, f, err)
			}
		}
	}
	save := func(when string) {
		t.Helper()
		if _, err := w.saver.Save(ctx, "m", docfile.Open, w.doc(t, id)); err != nil {
			t.Errorf("%s: Save: %v", when, err)
		}
	}
	for round := 0; round < 2; round++ {
		w.inner.failOn.Store(version)
		err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE)
		var re *RekeyError
		if !errors.As(err, &re) {
			t.Fatalf("round %d: %v", round, err)
		}
		if errors.Is(err, ErrDowngrade) {
			t.Fatalf("round %d: a cut-short switch is not a downgrade: %v", round, err)
		}
		loadAll("e2ee in flight")
		save("e2ee in flight")
		w.inner.failOn.Store("")
		if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
			t.Fatalf("round %d: retry: %v", round, err)
		}
		loadAll("e2ee settled")
		save("e2ee settled")

		w.inner.failOn.Store(version)
		err = w.saver.SetProtection(ctx, "m", id, saver.ProtectionServer)
		if !errors.As(err, &re) || errors.Is(err, ErrDowngrade) {
			t.Fatalf("round %d back: %v", round, err)
		}
		loadAll("server in flight")
		save("server in flight")
		w.inner.failOn.Store("")
		if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionServer); err != nil {
			t.Fatalf("round %d back: retry: %v", round, err)
		}
		loadAll("server settled")
		save("server settled")
	}
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	raw, err := w.inner.Load(ctx, version)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionServer); err != nil {
		t.Fatal(err)
	}
	serverWrapped, _ := w.inner.Load(ctx, version)
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	_ = raw
	if err := w.inner.Save(ctx, version, serverWrapped.Data); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ad.Load(ctx, version); !errors.Is(err, ErrDowngrade) {
		t.Fatalf("an old server-level ciphertext in an e2ee document: %v", err)
	}
	if _, err := w.sharing.Repair(ctx, id); err != nil {
		t.Fatal(err)
	}
	loadAll("after repair")
	if w.opens(id, version, w.server) {
		t.Error("repair left it wrapped for the server")
	}
}

func TestV1FilesAreAcceptedAndTheFirstSavePinsTheLevel(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w.legacyV1(t, id)
	for _, f := range w.files(t, id) {
		if v := w.frameVersion(t, f); v != 1 {
			t.Fatalf("setup: %s is v%d", f, v)
		}
		if _, err := w.ad.Load(ctx, f); err != nil {
			t.Errorf("a v1 file is read as before: %v", err)
		}
	}
	if _, err := w.saver.Save(ctx, "m", docfile.Open, w.doc(t, id)); err != nil {
		t.Fatal(err)
	}
	var v3 int
	for _, f := range w.files(t, id) {
		if w.frameVersion(t, f) == 3 {
			v3++
		}
	}
	if v3 == 0 {
		t.Fatal("the first save did not write a v3 frame")
	}
	saved := w.doc(t, id)
	if err := w.inner.Delete(ctx, KeysPrefix+id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.saver.Save(ctx, "m", docfile.Open, saved); !errors.Is(err, ErrDowngrade) {
		t.Errorf("after the upgrade the deletion is detected: %v", err)
	}

	w2 := newWorld(t)
	id2 := w2.history(t)
	w2.legacyV1(t, id2)
	if _, err := w2.sharing.Repair(ctx, id2); err != nil {
		t.Fatal(err)
	}
	for _, f := range w2.files(t, id2) {
		if v := w2.frameVersion(t, f); v != 3 {
			t.Errorf("%s is v%d after a re-key", f, v)
		}
	}

	w3 := newWorld(t)
	id3 := w3.history(t)
	if err := w3.saver.SetProtection(ctx, "m", id3, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w3.legacyV1(t, id3)
	saved3 := w3.doc(t, id3)
	if err := w3.inner.Delete(ctx, KeysPrefix+id3); err != nil {
		t.Fatal(err)
	}
	if _, err := w3.saver.Load(ctx, "m", id3); err != nil {
		t.Errorf("a v1 file cannot prove a deletion: %v", err)
	}
	findings, err := w3.sharing.VerifyIndex(ctx)
	if err != nil || len(findings) != 1 || findings[0].Doc != id3 || !errors.Is(findings[0].Err, ErrDowngrade) {
		t.Errorf("the index reports it: %v %v", findings, err)
	}
	_ = saved3
}

func TestTwoClientsOneStoreAFreshClientIsStillProtected(t *testing.T) {
	ctx := context.Background()
	a := newWorld(t)
	id, old := a.confidentialWithOldRecord(t)
	saved := a.doc(t, id)
	b := &world{inner: a.inner, server: a.server, ana: a.ana, bo: a.bo, signer: a.signer}
	b.ring = ConfidentialKeyring{
		People: StoreKeyring{Store: b.inner, Owners: []age.Recipient{b.ana.r.Key, b.server.Recipient()},
			Keys: []age.Identity{b.ana.id}, OwnerSigners: []ed25519.PublicKey{b.signer.Public()}},
		AtRest:           Static{To: []age.Recipient{b.server.Recipient(), b.ana.r.Key}, Keys: []age.Identity{b.server}},
		ServerRecipients: []age.Recipient{b.server.Recipient()},
	}
	b.ad = New(b.inner, b.ring)
	b.sharing = &Sharing{Store: b.inner, Adapter: b.ad, Signer: b.signer}
	b.saver = &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: b.ad}}}
	b.saver.Rekey = RekeyHook(b.sharing)
	b.saver.ConfidentialCheck = ConfidentialCheck(b.sharing, b.ana.r)

	a.replay(t, id, old) // the store writer replays after A's last save
	before := a.snapshot(t)
	if err := b.ad.Save(ctx, id+"~cccccccccccc.open", []byte("x")); !errors.Is(err, ErrDowngrade) {
		t.Errorf("B Save: %v", err)
	}
	if _, err := b.saver.Save(ctx, "m", docfile.Open, saved); !errors.Is(err, ErrDowngrade) {
		t.Errorf("B Saver.Save: %v", err)
	}
	if _, err := b.saver.Load(ctx, "m", id); !errors.Is(err, ErrDowngrade) {
		t.Errorf("B Load: %v", err)
	}
	sameStore(t, "B", before, a.snapshot(t))
	if _, err := a.saver.Load(ctx, "m", id); !errors.Is(err, ErrDowngrade) {
		t.Errorf("A Load: %v", err)
	}
}

func TestRecoverAfterAReplayOrADeletion(t *testing.T) {
	ctx := context.Background()
	t.Run("replay of a list that still has a removed person", func(t *testing.T) {
		w := newWorld(t)
		id := w.history(t)
		if err := w.sharing.Add(ctx, id, w.bo.r, w.bo.r.Fingerprint); err != nil {
			t.Fatal(err)
		}
		old := w.rawRecord(t, id) // lists Bo, server level
		if _, err := w.sharing.Remove(ctx, id, w.bo.r.Fingerprint); err != nil {
			t.Fatal(err)
		}
		if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
			t.Fatal(err)
		}
		w.replay(t, id, old)
		if _, err := w.saver.Load(ctx, "m", id); !errors.Is(err, ErrDowngrade) {
			t.Fatalf("%v", err)
		}
		if _, err := w.sharing.Recover(ctx, id, "nope", nil); !errors.Is(err, ErrProtection) {
			t.Errorf("%v", err)
		}
		if _, err := w.sharing.Recover(ctx, id, ProtectionE2EE, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := w.saver.Load(ctx, "m", id); err != nil {
			t.Fatalf("recovered: %v", err)
		}
		if _, err := w.saver.Save(ctx, "m", docfile.Open, w.doc(t, id)); err != nil {
			t.Fatalf("recovered: %v", err)
		}
		w.assertOnly(t, id, "recovered", []age.Identity{w.ana.id}, []age.Identity{w.server, w.bo.id})
		if list, _ := w.sharing.List(ctx, id); len(list) != 0 {
			t.Errorf("the replayed list is not carried over: %v", list)
		}
		if c, _ := w.ring.IsConfidential(ctx, id); !c {
			t.Error("not e2ee after Recover")
		}
		if idx, err := w.sharing.Index(ctx); err != nil || idx.Docs[id].Level != ProtectionE2EE {
			t.Errorf("the index follows: %+v %v", idx, err)
		}
	})
	t.Run("deleted record, with a floor that is ahead", func(t *testing.T) {
		floor := &MemorySeqFloor{}
		w := newWorldFloor(t, floor)
		id := w.history(t)
		if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
			t.Fatal(err)
		}
		if err := w.inner.Delete(ctx, KeysPrefix+id); err != nil {
			t.Fatal(err)
		}
		if _, err := w.saver.Load(ctx, "m", id); err == nil {
			t.Fatal("deleted record accepted")
		}
		if _, err := w.sharing.Recover(ctx, id, ProtectionE2EE, []Recipient{w.bo.r}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.saver.Load(ctx, "m", id); err != nil {
			t.Fatalf("recovered: %v", err)
		}
		w.assertOnly(t, id, "recovered", []age.Identity{w.ana.id, w.bo.id}, []age.Identity{w.server})
	})
	t.Run("no signer", func(t *testing.T) {
		w := newWorld(t)
		id := w.history(t)
		reader := &Sharing{Store: w.inner, Adapter: w.ad}
		if _, err := reader.Recover(ctx, id, ProtectionE2EE, nil); !errors.Is(err, ErrNoSigner) {
			t.Errorf("%v", err)
		}
	})
}

func TestIndexDetectsADeletedRecordOfADocumentNeverOpened(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	deleted, _ := w.confidentialWithOldRecord(t)
	replayed, old := w.confidentialWithOldRecord(t)
	fine, _ := w.confidentialWithOldRecord(t)
	toServer, _ := w.confidentialWithOldRecord(t)
	if err := w.saver.SetProtection(ctx, "m", toServer, saver.ProtectionServer); err != nil {
		t.Fatal(err)
	}
	idx, err := w.sharing.Index(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := idx.Docs[toServer]; ok {
		t.Error("a document switched back to server is not listed")
	}
	for _, id := range []string{deleted, replayed, fine} {
		if e := idx.Docs[id]; e.Level != ProtectionE2EE || e.Seq == 0 {
			t.Errorf("%s: %+v", id, e)
		}
	}
	if err := w.inner.Delete(ctx, KeysPrefix+deleted); err != nil {
		t.Fatal(err)
	}
	w.replay(t, replayed, old)

	floor := &MemorySeqFloor{}
	fresh := &Sharing{Store: w.inner, Adapter: w.ad, OwnerSigners: []ed25519.PublicKey{w.signer.Public()}, SeqFloor: floor}
	findings, err := fresh.VerifyIndex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]error{}
	for _, f := range findings {
		got[f.Doc] = f.Err
	}
	if len(got) != 2 || !errors.Is(got[deleted], ErrDowngrade) || !errors.Is(got[replayed], ErrDowngrade) {
		t.Fatalf("findings: %v", findings)
	}
	if !bytes.Contains([]byte(got[deleted].Error()), []byte("missing")) {
		t.Errorf("%v", got[deleted])
	}
	if f2, err := (&Sharing{Store: w.inner, OwnerSigners: []ed25519.PublicKey{w.signer.Public()}}).VerifyIndex(ctx); err != nil || len(f2) != 2 {
		t.Errorf("no floor: %v %v", f2, err)
	}
	ring := StoreKeyring{Store: w.inner, OwnerSigners: []ed25519.PublicKey{w.signer.Public()}, SeqFloor: floor}
	if _, _, err := ring.Bind(ctx, deleted+"~aaaaaaaaaaaa.open"); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("deleted, after the index: %v", err)
	}
	if _, _, err := ring.Bind(ctx, replayed+"~aaaaaaaaaaaa.open"); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("replayed, after the index: %v", err)
	}
	if _, _, err := ring.Bind(ctx, fine+"~aaaaaaaaaaaa.open"); err != nil {
		t.Errorf("fine: %v", err)
	}
	if v, _ := floor.Get(ctx, IndexKey); v != idx.Seq {
		t.Errorf("the index's own seq is the floor of the index: %d vs %d", v, idx.Seq)
	}
}

func TestIndexIsSignedByTheOwner(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	good, err := w.inner.Load(ctx, IndexKey)
	if err != nil {
		t.Fatal(err)
	}
	if !DefaultSkip(IndexKey) || docstore.IsDocumentKey(IndexKey) {
		t.Error("the index is readable and never expires")
	}
	evil, _ := NewSigner(bytes.Repeat([]byte{7}, 32))
	forged := &Sharing{Store: w.inner, Adapter: w.ad, Signer: evil}
	if err := w.inner.Delete(ctx, IndexKey); err != nil {
		t.Fatal(err)
	}
	if err := forged.setIndex(ctx, docfile.NewID(), ProtectionE2EE, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := w.sharing.Index(ctx); !errors.Is(err, ErrBadSignature) {
		t.Errorf("forged: %v", err)
	}
	_ = w.inner.Save(ctx, IndexKey, []byte(`{"payload":"e30="}`))
	if _, err := w.sharing.Index(ctx); !errors.Is(err, ErrUnsignedRecord) {
		t.Errorf("unsigned: %v", err)
	}
	rec := w.rawRecord(t, id)
	_ = w.inner.Save(ctx, IndexKey, rec)
	if _, err := w.sharing.Index(ctx); err == nil {
		t.Error("a keys record passed for the index")
	}
	_ = w.inner.Save(ctx, IndexKey, good.Data)
	if idx, err := w.sharing.Index(ctx); err != nil || len(idx.Docs) != 1 {
		t.Errorf("%+v %v", idx, err)
	}
	if docs, err := w.sharing.Usage(ctx, w.bo.r.Fingerprint); err != nil || len(docs) != 0 {
		t.Errorf("%v %v", docs, err)
	}
	floor := &MemorySeqFloor{}
	withFloor := &Sharing{Store: w.inner, Adapter: w.ad, OwnerSigners: []ed25519.PublicKey{w.signer.Public()}, SeqFloor: floor}
	if _, err := withFloor.Index(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.inner.Delete(ctx, IndexKey); err != nil {
		t.Fatal(err)
	}
	if idx, err := w.sharing.Index(ctx); err != nil || len(idx.Docs) != 0 {
		t.Errorf("no floor: a deleted index is an empty one (stated limit): %+v %v", idx, err)
	}
	if _, err := withFloor.Index(ctx); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("with a floor that saw it: %v", err)
	}
	if _, err := withFloor.VerifyIndex(ctx); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("VerifyIndex: %v", err)
	}
	_ = w.inner.Save(ctx, IndexKey, good.Data)
	id2 := w.history(t)
	if err := w.saver.SetProtection(ctx, "m", id2, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	if _, err := withFloor.Index(ctx); err != nil {
		t.Fatal(err)
	}
	_ = w.inner.Save(ctx, IndexKey, good.Data)
	if _, err := withFloor.Index(ctx); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("replayed index: %v", err)
	}
}

func TestSetProtectionIndexFailureRetries(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	w.inner.failOn.Store(IndexKey)
	if _, err := w.sharing.SetProtection(ctx, id, ProtectionE2EE); err == nil {
		t.Fatal("the index could not be written")
	}
	if p, _ := w.sharing.Pending(ctx, id); !p {
		t.Error("a switch whose index failed stays pending")
	}
	w.inner.failOn.Store("")
	if _, err := w.sharing.SetProtection(ctx, id, ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	if idx, _ := w.sharing.Index(ctx); idx.Docs[id].Level != ProtectionE2EE {
		t.Errorf("%+v", idx)
	}
	if p, _ := w.sharing.Pending(ctx, id); p {
		t.Error("still pending")
	}
}

func TestConfidentialFolderRecordsAreCrossCheckedToo(t *testing.T) {
	ctx := context.Background()
	t.Run("replay", func(t *testing.T) {
		w, _, sec, _ := folderWorld(t)
		if err := w.sharing.Add(ctx, sec, w.bo.r, w.bo.r.Fingerprint); err != nil {
			t.Fatal(err)
		}
		old := w.rawRecord(t, sec)
		if err := w.saver.SetFolderProtection(ctx, "m", sec, saver.ProtectionE2EE, false); err != nil {
			t.Fatal(err)
		}
		fi := w.frameOf(t, saver.FolderSecretKey(sec))
		if fi.version != 3 || fi.b.Level != ProtectionE2EE || fi.b.Seq == 0 {
			t.Fatalf("%+v", fi)
		}
		w.replay(t, sec, old)
		before := w.snapshot(t)
		if _, err := w.saver.RevealFolder(ctx, "m", sec); !errors.Is(err, ErrDowngrade) {
			t.Errorf("RevealFolder: %v", err)
		}
		if err := w.saver.RenameFolder(ctx, "m", sec, "New secret name"); !errors.Is(err, ErrDowngrade) {
			t.Errorf("RenameFolder: %v", err)
		}
		if err := w.saver.SetFolderProtection(ctx, "m", sec, saver.ProtectionServer, false); !errors.Is(err, ErrDowngrade) {
			t.Errorf("SetFolderProtection: %v", err)
		}
		sameStore(t, "folder", before, w.snapshot(t))
	})
	t.Run("deleted", func(t *testing.T) {
		w, _, sec, _ := folderWorld(t)
		if err := w.saver.SetFolderProtection(ctx, "m", sec, saver.ProtectionE2EE, false); err != nil {
			t.Fatal(err)
		}
		if err := w.inner.Delete(ctx, KeysPrefix+sec); err != nil {
			t.Fatal(err)
		}
		before := w.snapshot(t)
		if _, err := w.saver.RevealFolder(ctx, "m", sec); !errors.Is(err, ErrDowngrade) {
			t.Errorf("RevealFolder: %v", err)
		}
		if err := w.saver.RenameFolder(ctx, "m", sec, "New secret name"); !errors.Is(err, ErrDowngrade) {
			t.Errorf("RenameFolder: %v", err)
		}
		if _, err := w.sharing.SetProtection(ctx, sec, ""); !errors.Is(err, ErrDowngrade) {
			t.Errorf("SetProtection: %v", err)
		}
		sameStore(t, "folder", before, w.snapshot(t))
		if _, err := w.sharing.Recover(ctx, sec, ProtectionE2EE, nil); err != nil {
			t.Fatal(err)
		}
		if n, err := w.saver.RevealFolder(ctx, "m", sec); err != nil || n != folderSecret {
			t.Errorf("%q %v", n, err)
		}
	})
}

func TestVerifyReportsADowngradedDocumentAndTheRebuildRefusesIt(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	bad, _ := w.confidentialWithOldRecord(t)
	ok := w.history(t)
	if err := w.inner.Delete(ctx, KeysPrefix+bad); err != nil {
		t.Fatal(err)
	}
	rep, err := w.saver.Verify(ctx, "m")
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Downgraded) != 1 || rep.Downgraded[0] != bad || len(rep.Lost) != 0 || len(rep.Restored) != 0 {
		t.Errorf("%+v", rep)
	}
	if _, err := w.saver.Load(ctx, "m", ok); err != nil {
		t.Errorf("the others are untouched: %v", err)
	}
	if err := w.saver.Rebuild(ctx, "m"); !errors.Is(err, ErrDowngrade) {
		t.Errorf("Rebuild: %v", err)
	}
}

func TestSavesAndLoadsRacingSwitchesNeverSeeADowngrade(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 200)
	ok := func(err error) bool {
		return err == nil || errors.Is(err, saver.ErrBusy) || errors.Is(err, docstore.ErrConflict) || errors.Is(err, saver.ErrUnreadable) && !errors.Is(err, ErrDowngrade)
	}
	worker := func(name string, f func(i int) error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				if err := f(i); !ok(err) {
					errs <- fmt.Errorf("%s: %w", name, err)
					return
				}
			}
		}()
	}
	edit := func(i int) docfile.Document {
		d := docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: fmt.Sprintf("edit %d", i)}}}}
		d.SetName("Merger xango.io")
		d.SetID(id)
		return d
	}
	worker("save", func(i int) error { _, err := w.saver.Save(ctx, "m", docfile.Open, edit(i)); return err })
	worker("version", func(i int) error { _, err := w.saver.SaveVersion(ctx, "m", docfile.Open, edit(i)); return err })
	worker("load", func(int) error { _, err := w.saver.Load(ctx, "m", id); return err })
	worker("raw load", func(int) error {
		for _, f := range w.files(t, id) {
			if _, err := w.ad.Load(ctx, f); err != nil && !errors.Is(err, docstore.ErrNotFound) {
				return err
			}
		}
		return nil
	})
	for i := 0; i < 6; i++ {
		to := saver.ProtectionE2EE
		if i%2 == 1 {
			to = saver.ProtectionServer
		}
		if err := w.saver.SetProtection(ctx, "m", id, to); err != nil && !errors.Is(err, saver.ErrBusy) {
			t.Fatalf("flip %d to %s: %v", i, to, err)
		}
	}
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w.assertOnly(t, id, "after the flips", []age.Identity{w.ana.id}, []age.Identity{w.server})
	for _, f := range w.files(t, id) {
		if _, err := w.ad.Load(ctx, f); err != nil {
			t.Errorf("Load %s: %v", f, err)
		}
	}
}
