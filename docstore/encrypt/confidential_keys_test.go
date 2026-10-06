package encrypt

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"filippo.io/age"
	"golang.org/x/crypto/ssh"

	"github.com/elaous/gojiberry/docfile"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/saver"
)

// flaky is a store whose writes of chosen files can be made to fail, to cut a re-key short.
type flaky struct {
	*docstore.MemoryAdapter
	failOn atomic.Value // string: a substring of the ids whose writes fail ("" none)
}

func (f *flaky) fails(id string) error {
	if s, _ := f.failOn.Load().(string); s != "" && strings.Contains(id, s) {
		return errors.New("disk error on " + id)
	}
	return nil
}
func (f *flaky) Save(ctx context.Context, id string, b []byte) error {
	if err := f.fails(id); err != nil {
		return err
	}
	return f.MemoryAdapter.Save(ctx, id, b)
}
func (f *flaky) SaveIf(ctx context.Context, id string, b []byte, prev string) error {
	if err := f.fails(id); err != nil {
		return err
	}
	return f.MemoryAdapter.SaveIf(ctx, id, b, prev)
}

type world struct {
	inner    *flaky
	server   *age.X25519Identity
	ana, bo  person
	signer   *Signer
	ad       *Adapter
	sharing  *Sharing
	saver    *saver.Saver
	ring     ConfidentialKeyring
	recorded []string
}

func newWorld(t *testing.T) *world { return newWorldFloor(t, nil) }

// newWorldFloor is newWorld whose keyring and sharing both use a SeqFloor.
func newWorldFloor(t *testing.T, floor SeqFloor) *world {
	t.Helper()
	w := &world{inner: &flaky{MemoryAdapter: docstore.NewMemoryAdapter()}}
	var err error
	if w.server, err = age.GenerateX25519Identity(); err != nil {
		t.Fatal(err)
	}
	w.ana, w.bo = newPerson(t, "ana"), newPerson(t, "bo")
	if w.signer, err = NewSigner(bytes.Repeat([]byte{9}, 32)); err != nil {
		t.Fatal(err)
	}
	w.ring = ConfidentialKeyring{
		// The server key is (wrongly) also in the owners' list, as in a deployment that grew from
		// the at-rest level: confidential documents must still not be wrapped for it.
		People: StoreKeyring{Store: w.inner, Owners: []age.Recipient{w.ana.r.Key, w.server.Recipient()},
			Keys: []age.Identity{w.ana.id}, OwnerSigners: []ed25519.PublicKey{w.signer.Public()}, SeqFloor: floor},
		AtRest:           Static{To: []age.Recipient{w.server.Recipient(), w.ana.r.Key}, Keys: []age.Identity{w.server}},
		ServerRecipients: []age.Recipient{w.server.Recipient()},
	}
	w.ad = New(w.inner, w.ring)
	w.ad.Signer = w.signer
	w.sharing = &Sharing{Store: w.inner, Adapter: w.ad, Signer: w.signer, SeqFloor: floor}
	w.saver = &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: w.ad}}}
	w.saver.ConfidentialCheck = ConfidentialCheck(w.sharing, w.ana.r)
	w.saver.Rekey = RekeyHook(w.sharing)
	return w
}

// files lists the stored files of a document that are encrypted.
func (w *world) files(t *testing.T, id string) []string {
	t.Helper()
	metas, _ := w.inner.List(context.Background())
	var out []string
	for _, m := range metas {
		if DocID(m.ID) == id && !DefaultSkip(m.ID) {
			out = append(out, m.ID)
		}
	}
	return out
}

func (w *world) opens(id string, f string, keys ...age.Identity) bool {
	_, err := New(w.inner, Static{Keys: keys}).Load(context.Background(), f)
	return err == nil
}

// history makes a document with a current copy, a previous copy and two version snapshots.
func (w *world) history(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	d := docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: "terms v1"}}}}
	d.SetName("Merger xango.io")
	e, err := w.saver.Save(ctx, "m", docfile.Open, d)
	if err != nil {
		t.Fatal(err)
	}
	for i := 2; i <= 3; i++ {
		d := docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: fmt.Sprintf("terms v%d", i)}}}}
		d.SetName("Merger xango.io")
		d.SetID(e.ID)
		if _, err := w.saver.Save(ctx, "m", docfile.Open, d); err != nil {
			t.Fatal(err)
		}
		if _, err := w.saver.SaveVersion(ctx, "m", docfile.Open, d); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(w.files(t, e.ID)); n < 4 {
		t.Fatalf("expected a current copy, a previous copy and versions, got %v", w.files(t, e.ID))
	}
	return e.ID
}

func (w *world) assertOnly(t *testing.T, id string, when string, can []age.Identity, cannot []age.Identity) {
	t.Helper()
	for _, f := range w.files(t, id) {
		for _, k := range can {
			if !w.opens(id, f, k) {
				t.Errorf("%s: %s should open for a key it is wrapped for", when, f)
			}
		}
		for _, k := range cannot {
			if w.opens(id, f, k) {
				t.Errorf("%s: %s opens with a key it must not be wrapped for", when, f)
			}
		}
	}
}

func TestSetProtectionRekeysEveryFileBothWays(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	if err := w.sharing.Add(ctx, id, w.bo.r, w.bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	// Server level: the server key opens everything (and the people's keys, as at rest).
	w.assertOnly(t, id, "server level", []age.Identity{w.server, w.ana.id}, nil)

	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w.assertOnly(t, id, "after e2ee", []age.Identity{w.ana.id, w.bo.id}, []age.Identity{w.server})
	if p, _ := w.sharing.Pending(ctx, id); p {
		t.Error("still pending after success")
	}
	// A save after the switch stays confidential.
	d, _ := w.saver.Load(ctx, "m", id)
	d.Content = []docfile.Item{{Chars: &docfile.Chars{Text: "terms v4"}}}
	if _, err := w.saver.Save(ctx, "m", docfile.Open, d); err != nil {
		t.Fatal(err)
	}
	if _, err := w.saver.SaveVersion(ctx, "m", docfile.Open, d); err != nil {
		t.Fatal(err)
	}
	w.assertOnly(t, id, "after a later save", []age.Identity{w.ana.id, w.bo.id}, []age.Identity{w.server})
	// Reveal works for the owner; the record is the authority.
	if n, _, err := w.saver.Reveal(ctx, "m", id); err != nil || n != "Merger xango.io" {
		t.Errorf("%q %v", n, err)
	}
	if c, err := w.ring.IsConfidential(ctx, id); err != nil || !c {
		t.Errorf("%v %v", c, err)
	}
	// The server-key holder cannot even read the name through a saver.
	srv := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: New(w.inner, Static{Keys: []age.Identity{w.server}})}}}
	if _, _, err := srv.Reveal(ctx, "m", id); !errors.Is(err, docstore.ErrLocked) {
		t.Errorf("%v", err)
	}

	// Back to server level: re-keyed the other way. Idempotent when repeated.
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionServer); err != nil {
		t.Fatal(err)
	}
	w.assertOnly(t, id, "back to server", []age.Identity{w.server, w.ana.id}, nil)
	if n, err := w.sharing.SetProtection(ctx, id, ""); err != nil || n != 0 {
		t.Errorf("a settled level writes nothing: %d %v", n, err)
	}
	if got, _ := w.saver.Get(ctx, "m", id); got.Name != "Merger xango.io" || got.Protection != saver.ProtectionServer {
		t.Errorf("%+v", got)
	}
}

func TestNewConfidentialDocumentIsNeverWrappedForTheServer(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	d := docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: "payroll"}}}}
	d.SetName("Payroll")
	d.SetProtection(saver.ProtectionE2EE)
	e, err := w.saver.Save(ctx, "m", docfile.Open, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.files(t, e.ID)) == 0 {
		t.Fatal("nothing stored")
	}
	w.assertOnly(t, e.ID, "born confidential", []age.Identity{w.ana.id}, []age.Identity{w.server})
}

func TestSetProtectionFailureNamesTheStaleFilesAndRetries(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	files := w.files(t, id)
	var version string
	for _, f := range files {
		if strings.HasPrefix(f, "versions/") {
			version = f
			break
		}
	}
	w.inner.failOn.Store(version)
	err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE)
	var re *RekeyError
	if !errors.As(err, &re) || len(re.Stale) != 1 || re.Stale[0] != version {
		t.Fatalf("want the one stale file named: %v", err)
	}
	// The catalog did not move, nothing was concealed, and only the stale file is still
	// wrapped for the server.
	if got, _ := w.saver.Get(ctx, "m", id); got.Protection != saver.ProtectionServer || got.Name != "Merger xango.io" {
		t.Errorf("%+v", got)
	}
	for _, f := range files {
		if got, want := w.opens(id, f, w.server), f == version; got != want {
			t.Errorf("%s: server opens=%v, want %v", f, got, want)
		}
	}
	if p, _ := w.sharing.Pending(ctx, id); !p {
		t.Error("a failed re-key must stay pending")
	}
	// Saves meanwhile already use the people's keys.
	d, _ := w.saver.Load(ctx, "m", id)
	if _, err := w.saver.Save(ctx, "m", docfile.Open, d); err != nil {
		t.Fatal(err)
	}
	// Retry (the disk is back) finishes it, and a second retry is a no-op.
	w.inner.failOn.Store("")
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w.assertOnly(t, id, "after retry", []age.Identity{w.ana.id}, []age.Identity{w.server})
	if got, _ := w.saver.Get(ctx, "m", id); got.Protection != saver.ProtectionE2EE || got.Name != saver.ConfidentialName {
		t.Errorf("%+v", got)
	}
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}

	// The same going back: a failure leaves the document wrapped for people only (nothing
	// exposed), the catalog says server, and running it again finishes.
	w.inner.failOn.Store(version)
	err = w.saver.SetProtection(ctx, "m", id, saver.ProtectionServer)
	if !errors.As(err, &re) || len(re.Stale) != 1 {
		t.Fatalf("%v", err)
	}
	if w.opens(id, version, w.server) {
		t.Error("the stale file must still be wrapped to people")
	}
	w.inner.failOn.Store("")
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionServer); err != nil {
		t.Fatal(err)
	}
	w.assertOnly(t, id, "back to server after retry", []age.Identity{w.server, w.ana.id}, nil)
}

func TestSetProtectionOnAnUnopenableFileWritesNothing(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	// A file nobody here can open (wrapped for a stranger): the group is all or none.
	stranger := newPerson(t, "stranger")
	files := w.files(t, id)
	odd := files[len(files)-1]
	raw, _ := w.inner.Load(ctx, odd)
	other := New(docstore.NewMemoryAdapter(), Static{To: []age.Recipient{stranger.r.Key}})
	_ = other.Save(ctx, odd, []byte("x"))
	foreign, _ := other.Inner.Load(ctx, odd)
	_ = w.inner.Save(ctx, odd, foreign.Data)
	err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE)
	var re *RekeyError
	if !errors.As(err, &re) || len(re.Stale) != len(files) {
		t.Fatalf("all files are stale when none was written: %v", err)
	}
	for _, f := range files {
		if f != odd && !w.opens(id, f, w.server) {
			t.Errorf("%s was written although another file could not be opened", f)
		}
	}
	_ = w.inner.Save(ctx, odd, raw.Data) // put it back: retry works
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w.assertOnly(t, id, "after retry", []age.Identity{w.ana.id}, []age.Identity{w.server})
}

func TestSavesDuringTheSwitchAreNotLeftForTheServer(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errs := make(chan error, 100)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			d := docfile.Document{Content: []docfile.Item{{Chars: &docfile.Chars{Text: fmt.Sprintf("edit %d", i)}}}}
			d.SetName("Merger xango.io")
			d.SetID(id)
			if _, err := w.saver.Save(ctx, "m", docfile.Open, d); err != nil {
				errs <- fmt.Errorf("save: %w", err)
				return
			}
			if i%3 == 0 {
				if _, err := w.saver.SaveVersion(ctx, "m", docfile.Open, d); err != nil {
					errs <- fmt.Errorf("version: %w", err)
					return
				}
			}
		}
	}()
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	w.assertOnly(t, id, "after the switch with saves racing it", []age.Identity{w.ana.id}, []age.Identity{w.server})
	if _, err := w.saver.Load(ctx, "m", id); err != nil {
		t.Errorf("the owner reads it: %v", err)
	}
}

func TestSharedWithAPasskeyAndARecoveryKey(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	secret := bytes.Repeat([]byte{21}, 32)
	pk, err := PRFRecipient(secret, "bo's phone", "cred-21")
	if err != nil {
		t.Fatal(err)
	}
	pkID, _ := PRFIdentity(secret)
	pub, recSecret, _ := GenerateRecoveryKey()
	rec, _ := ParseAgeRecipient(pub)
	rec.Label = "bo recovery"
	recID, _ := ParseAgeIdentity(recSecret)
	for _, r := range []Recipient{pk, rec} {
		if err := w.sharing.Add(ctx, id, r, r.Fingerprint); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w.assertOnly(t, id, "e2ee with a passkey and a recovery key", []age.Identity{w.ana.id, pkID, recID}, []age.Identity{w.server})
	// Each of the shared keys alone reads the document through a saver.
	for name, k := range map[string]age.Identity{"passkey": pkID, "recovery": recID} {
		only := &saver.Saver{Targets: []saver.Target{{Name: "m", Adapter: New(w.inner, Static{Keys: []age.Identity{k}})}}}
		if n, _, err := only.Reveal(ctx, "m", id); err != nil || n != "Merger xango.io" {
			t.Errorf("%s: %q %v", name, n, err)
		}
	}
	// Removing the passkey re-keys inside the confidential level: still no server key.
	if _, err := w.sharing.Remove(ctx, id, pk.Fingerprint); err != nil {
		t.Fatal(err)
	}
	w.assertOnly(t, id, "after removing the passkey", []age.Identity{w.ana.id, recID}, []age.Identity{w.server, pkID})
	if c, _ := w.ring.IsConfidential(ctx, id); !c {
		t.Error("removing a key must not change the level")
	}
}

func TestConfidentialKeyringFailsClosed(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	if _, err := w.sharing.SetProtection(ctx, id, "bogus"); !errors.Is(err, ErrProtection) {
		t.Errorf("%v", err)
	}
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	f := id + "~aaaaaaaaaaaa.json"
	// A store writer flips the signed protection field to "server": the signature breaks and
	// saves fail; they are not wrapped for the server.
	raw, _ := w.inner.Load(ctx, KeysPrefix+id)
	var sr signedRecord
	_ = json.Unmarshal(raw.Data, &sr)
	var payload map[string]any
	_ = json.Unmarshal(sr.Payload, &payload)
	delete(payload, "protection")
	sr.Payload, _ = json.Marshal(payload)
	forged, _ := json.Marshal(sr)
	_ = w.inner.Save(ctx, KeysPrefix+id, forged)
	if err := w.ad.Save(ctx, f, []byte("x")); !errors.Is(err, ErrBadSignature) {
		t.Errorf("a record with its level stripped: %v", err)
	}
	// Unsigned, damaged: also refused.
	_ = w.inner.Save(ctx, KeysPrefix+id, []byte(`{"payload":"e30="}`))
	if err := w.ad.Save(ctx, f, []byte("x")); !errors.Is(err, ErrUnsignedRecord) {
		t.Errorf("an unsigned record: %v", err)
	}
	if _, err := w.ring.IsConfidential(ctx, id); !errors.Is(err, ErrUnsignedRecord) {
		t.Errorf("%v", err)
	}
	// A document of the at-rest level keeps the at-rest keys, and non-document ids too.
	rs, err := w.ring.Recipients(ctx, "not-a-document")
	if err != nil || len(rs) != 2 {
		t.Errorf("%d %v", len(rs), err)
	}
	// If the owners' list holds nothing but the server key, a confidential document is not saved.
	only := w.ring
	only.People.Owners = []age.Recipient{w.server.Recipient()}
	other := docfile.NewID()
	ad2 := New(w.inner, only)
	sh2 := &Sharing{Store: w.inner, Adapter: ad2, Signer: w.signer}
	if _, err := sh2.SetProtection(ctx, other, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	if err := ad2.Save(ctx, other+"~bbbbbbbbbbbb.json", []byte("x")); !errors.Is(err, ErrNoRecipients) {
		t.Errorf("%v", err)
	}
}

// replay puts back the record bytes saved earlier: a validly signed but older record.
func (w *world) replay(t *testing.T, id string, old []byte) {
	t.Helper()
	if err := w.inner.Save(context.Background(), KeysPrefix+id, old); err != nil {
		t.Fatal(err)
	}
}

func (w *world) rawRecord(t *testing.T, id string) []byte {
	t.Helper()
	d, err := w.inner.Load(context.Background(), KeysPrefix+id)
	if err != nil {
		t.Fatal(err)
	}
	return d.Data
}

// TestSaveOfAConfidentialDocumentHealsAReplayedServerLevelRecord is the saver's re-assertion,
// the layer that is left for a document whose files are still in the v1 frame (written before
// the frame carried the level): nothing in them proves the record was newer, so the replay
// reads as the record and the next save asserts e2ee again and pins the level in a signed (v3) frame.
func TestSaveOfAConfidentialDocumentHealsAReplayedServerLevelRecord(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	if err := w.sharing.Add(ctx, id, w.bo.r, w.bo.r.Fingerprint); err != nil { // a record, at server level
		t.Fatal(err)
	}
	old := w.rawRecord(t, id)
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w.legacyV1(t, id)
	w.replay(t, id, w.legacyRecordBytes(t, id, old)) // a store writer puts the older, validly signed record back
	if c, _ := w.ring.IsConfidential(ctx, id); c {
		t.Fatal("setup: the replay should read as server level")
	}
	d, err := w.saver.Load(ctx, "m", id)
	if err != nil {
		t.Fatal(err)
	}
	d.Content = []docfile.Item{{Chars: &docfile.Chars{Text: "after the replay"}}}
	if _, err := w.saver.Save(ctx, "m", docfile.Open, d); err != nil {
		t.Fatal(err)
	}
	if c, err := w.ring.IsConfidential(ctx, id); err != nil || !c {
		t.Errorf("the record was not put back to e2ee: %v %v", c, err)
	}
	w.assertOnly(t, id, "after a save over a replay", []age.Identity{w.ana.id, w.bo.id}, []age.Identity{w.server})
	for _, f := range w.files(t, id) {
		if v := w.frameVersion(t, f); v != 3 {
			t.Errorf("%s is still frame v%d after the heal", f, v)
		}
	}
}

func TestSeqFloorRefusesAReplayedRecord(t *testing.T) {
	ctx := context.Background()
	w := newWorldFloor(t, &MemorySeqFloor{})
	id := w.history(t)
	if err := w.sharing.Add(ctx, id, w.bo.r, w.bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	old := w.rawRecord(t, id)
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	saved, err := w.saver.Load(ctx, "m", id)
	if err != nil {
		t.Fatal(err)
	}
	w.replay(t, id, old)
	if _, err := w.ring.IsConfidential(ctx, id); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("IsConfidential: %v", err)
	}
	if _, err := w.ring.Recipients(ctx, id+"~aaaaaaaaaaaa.json"); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("Recipients: %v", err)
	}
	if _, err := w.saver.Load(ctx, "m", id); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("Load: %v", err)
	}
	if _, err := w.saver.Save(ctx, "m", docfile.Open, saved); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("Save: %v", err)
	}
	// A deleted record where one was seen is refused as well, and no floor means no refusal.
	if err := w.inner.Delete(ctx, KeysPrefix+id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ring.IsConfidential(ctx, id); !errors.Is(err, ErrStaleRecord) {
		t.Errorf("deleted: %v", err)
	}
	w2 := newWorld(t)
	id2 := w2.history(t)
	if err := w2.sharing.Add(ctx, id2, w2.bo.r, w2.bo.r.Fingerprint); err != nil {
		t.Fatal(err)
	}
	old2 := w2.rawRecord(t, id2)
	if err := w2.saver.SetProtection(ctx, "m", id2, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w2.replay(t, id2, old2)
	if _, err := w2.ring.IsConfidential(ctx, id2); err != nil {
		t.Errorf("without a floor a replay reads as the record: %v", err)
	}
}

func TestMemorySeqFloorOnlyRises(t *testing.T) {
	ctx := context.Background()
	f := &MemorySeqFloor{}
	_ = f.Raise(ctx, "a", 5)
	_ = f.Raise(ctx, "a", 3)
	if v, _ := f.Get(ctx, "a"); v != 5 {
		t.Errorf("%d", v)
	}
	if v, _ := f.Get(ctx, "b"); v != 0 {
		t.Errorf("%d", v)
	}
}

func TestSetProtectionAtASettledLevelNeedsNoSigner(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	reader := &Sharing{Store: w.inner, Adapter: w.ad, OwnerSigners: []ed25519.PublicKey{w.signer.Public()}}
	if n, err := reader.SetProtection(ctx, id, ProtectionE2EE); err != nil || n != 0 {
		t.Errorf("settled, no signer: %d %v", n, err)
	}
	// Not settled: changing the level does need it.
	if _, err := reader.SetProtection(ctx, id, ""); !errors.Is(err, ErrNoSigner) {
		t.Errorf("%v", err)
	}
	// And a document with no record at the server level is settled too.
	if n, err := reader.SetProtection(ctx, docfile.NewID(), ""); err != nil || n != 0 {
		t.Errorf("%d %v", n, err)
	}
}

func TestUnreadableSnapshotBlocksTheSwitchUntilTheCallerDropsIt(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	id := w.history(t)
	var lost string
	for _, f := range w.files(t, id) {
		if strings.HasPrefix(f, "versions/") {
			lost = f
			break
		}
	}
	// The snapshot becomes unopenable: wrapped to a key nobody here holds.
	stranger, _ := age.GenerateX25519Identity()
	if err := New(w.inner, Static{To: []age.Recipient{stranger.Recipient()}}).Save(ctx, lost, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE)
	var re *RekeyError
	if !errors.As(err, &re) || len(re.Unreadable) != 1 || re.Unreadable[0] != lost || !strings.Contains(err.Error(), lost) {
		t.Fatalf("want the unreadable file named: %v", err)
	}
	if keys := saver.UnreadableKeys(err); len(keys) != 1 || keys[0] != lost {
		t.Fatalf("the saver gives the keys to confirm: %v", keys)
	}
	// All or none, never silent: the other files were not touched, the switch can be retried.
	for _, f := range w.files(t, id) {
		if f != lost && !w.opens(id, f, w.server) {
			t.Errorf("%s was re-keyed although another file could not be opened", f)
		}
	}
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err == nil {
		t.Fatal("the default stays all or none")
	}
	// The caller confirms; the snapshot goes; the switch finishes.
	if err := w.saver.DropVersions(ctx, "m", id, []string{lost}); err != nil {
		t.Fatal(err)
	}
	if err := w.saver.SetProtection(ctx, "m", id, saver.ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w.assertOnly(t, id, "after dropping", []age.Identity{w.ana.id}, []age.Identity{w.server})
	// A current copy cannot be dropped through it.
	for _, f := range w.files(t, id) {
		if !strings.HasPrefix(f, "versions/") {
			if err := w.saver.DropVersions(ctx, "m", id, []string{f}); err == nil {
				t.Errorf("%s dropped", f)
			}
		}
	}
}

func TestServerSSHKeysAreNeverRecipientsOfAConfidentialDocument(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"ed25519", "rsa"} {
		t.Run(kind, func(t *testing.T) {
			w := newWorld(t)
			var srv Recipient
			var srvID age.Identity
			if kind == "rsa" {
				pub, priv := rsaKey(t)
				srv, _ = ParseSSHRecipient(pub)
				srvID, _ = ParseSSHIdentity(priv)
			} else {
				pub, priv := sshKey(t, "server")
				srv, _ = ParseSSHRecipient(pub)
				srvID, _ = ParseSSHIdentity(priv)
			}
			// The same key, parsed again, is a different pointer.
			again, _ := ParseSSHRecipient(srv.Text)
			w.ring.People.Owners = []age.Recipient{w.ana.r.Key, srv.Key}
			w.ring.ServerRecipients = []age.Recipient{again.Key}
			if err := w.sharing.Add(ctx, "00000000-0000-4000-8000-000000000001", w.bo.r, w.bo.r.Fingerprint); err != nil {
				t.Fatal(err)
			}
			if _, err := w.sharing.SetProtection(ctx, "00000000-0000-4000-8000-000000000001", ProtectionE2EE); err != nil {
				t.Fatal(err)
			}
			got, err := w.ring.Recipients(ctx, "00000000-0000-4000-8000-000000000001~aaaaaaaaaaaa.json")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 {
				t.Fatalf("want ana and bo, got %d recipients", len(got))
			}
			doc := "00000000-0000-4000-8000-000000000001"
			owner := New(w.inner, w.ring)
			owner.Signer = w.signer
			if err := owner.Save(ctx, doc+"~aaaaaaaaaaaa.json", []byte("secret")); err != nil {
				t.Fatal(err)
			}
			if _, err := New(w.inner, Static{Keys: []age.Identity{srvID}}).Load(ctx, doc+"~aaaaaaaaaaaa.json"); err == nil {
				t.Error("the server's SSH key opens a confidential document")
			}
			if _, err := New(w.inner, Static{Keys: []age.Identity{w.bo.id}}).Load(ctx, doc+"~aaaaaaaaaaaa.json"); err != nil {
				t.Errorf("a person it is shared with cannot open it: %v", err)
			}
		})
	}
}

func rsaKey(t *testing.T) (pub string, priv []byte) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := ssh.NewPublicKey(&k.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := ssh.MarshalPrivateKey(k, "server")
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sp))) + " server", pem.EncodeToMemory(blk)
}

type opaqueRecipient struct{}

func (opaqueRecipient) Wrap([]byte) ([]*age.Stanza, error) { return []*age.Stanza{{Type: "x"}}, nil }

func TestConfidentialKeyringFailsClosedOnAnUncomparableRecipient(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	doc := "00000000-0000-4000-8000-000000000002"
	if _, err := w.sharing.SetProtection(ctx, doc, ProtectionE2EE); err != nil {
		t.Fatal(err)
	}
	w.ring.People.Owners = append(w.ring.People.Owners, opaqueRecipient{})
	if _, err := w.ring.Recipients(ctx, doc+"~aaaaaaaaaaaa.json"); !errors.Is(err, ErrUncomparableRecipient) {
		t.Errorf("%v", err)
	}
}
