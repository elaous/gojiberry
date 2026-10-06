package encrypt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
)

// hookStore is a conditional store whose reads and writes the tests can intercept, to make a
// race between two directory instances (replicas) happen at an exact point.
type hookStore struct {
	*docstore.MemoryAdapter
	mu         sync.Mutex
	afterLoad  func(id string)
	failSaveIf func(id string) error
	saveIfs    int
}

func newHookStore() *hookStore { return &hookStore{MemoryAdapter: docstore.NewMemoryAdapter()} }

func (h *hookStore) Load(ctx context.Context, id string) (docstore.Document, error) {
	doc, err := h.MemoryAdapter.Load(ctx, id)
	h.mu.Lock()
	f := h.afterLoad
	h.mu.Unlock()
	if f != nil {
		f(id)
	}
	return doc, err
}

func (h *hookStore) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	h.mu.Lock()
	h.saveIfs++
	f := h.failSaveIf
	h.mu.Unlock()
	if f != nil {
		if err := f(id); err != nil {
			return err
		}
	}
	return h.MemoryAdapter.SaveIf(ctx, id, data, prev)
}

func (h *hookStore) Save(ctx context.Context, id string, data []byte) error {
	h.mu.Lock()
	f := h.failSaveIf
	h.mu.Unlock()
	if f != nil {
		if err := f(id); err != nil {
			return err
		}
	}
	return h.MemoryAdapter.Save(ctx, id, data)
}

// rotation returns the old signer and a rotated one that still knows it.
func rotation(t *testing.T) (oldSigner, rotated DirectorySigner) {
	t.Helper()
	oldSigner = testSigner(t, 1)
	rotated, err := DirectorySignerFromSeed([]byte(strings.Repeat("\x02", 32)), []byte(strings.Repeat("\x01", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return oldSigner, rotated
}

func logOps(t *testing.T, d *AdapterDirectory) []LogEntry {
	t.Helper()
	l, err := d.ReadLog(context.Background(), 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return l.Entries
}

// --- finding 1: Resign must not resurrect a revoked key ---------------------------------------

func TestResignDoesNotResurrectAKeyRevokedWhileItRuns(t *testing.T) {
	ctx := context.Background()
	hs, clk := newHookStore(), newTestClock()
	oldSigner, rotated := rotation(t)
	d1 := &AdapterDirectory{Store: hs, Signer: oldSigner, Now: clk.Now}
	// One record, so that Resign's reads happen in a known order (the store lists in any order).
	k1 := newRecovery(t)
	if err := d1.Publish(ctx, ana, k1); err != nil {
		t.Fatal(err)
	}
	d2 := &AdapterDirectory{Store: hs, Signer: rotated, Now: clk.Now} // the replica that re-signs
	d3 := &AdapterDirectory{Store: hs, Signer: rotated, Now: clk.Now} // another replica, already on the new key
	oldID := d1.directoryID("ana@x.io", k1.Fingerprint)

	// The rolling deploy: while d2 re-signs k1 (it has read the record), the user revokes k1
	// through d3. The revoke writes its tombstone under the current key's name.
	loads, fired := 0, false
	hs.afterLoad = func(id string) {
		if id != oldID || fired {
			return
		}
		if loads++; loads == 2 { // the first read is the survey, the second the one under the address lock
			fired = true
			if _, err := d3.Revoke(ctx, ana, k1.Fingerprint, nil); err != nil {
				t.Errorf("revoke: %v", err)
			}
		}
	}
	rep, err := d2.Resign(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hs.afterLoad = nil
	if !fired {
		t.Fatal("the interleaving did not happen")
	}
	got, err := d2.LookupWith(ctx, "ana@x.io", LookupOptions{IncludeRevoked: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range got {
		if k.Fingerprint == k1.Fingerprint && !k.Revoked {
			t.Errorf("the revoked key is live again after Resign: %+v (report %+v)", k, rep)
		}
	}
	if len(got) != 1 || !got[0].Revoked {
		t.Errorf("the tombstone is what is there: %+v", got)
	}
	if plain, _ := d2.Lookup(ctx, "ana@x.io"); len(plain) != 0 {
		t.Errorf("lookup after Resign: %+v", plain)
	}
	if _, err := d2.Revoke(ctx, ana, k1.Fingerprint, nil); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("the key stays revoked: %v", err)
	}
	if err := d2.Publish(ctx, ana, k1); !errors.Is(err, ErrRevoked) {
		t.Errorf("and cannot be published again within the window: %v", err)
	}
}

// --- finding 2: Resign must not launder a record forged with a compromised retired key ----------

func TestResignLeavesRecordsWithoutALogEntryUnendorsed(t *testing.T) {
	ctx := context.Background()
	store, clk := docstore.NewMemoryAdapter(), newTestClock()
	oldSigner, rotated := rotation(t)
	d1 := &AdapterDirectory{Store: store, Signer: oldSigner, Now: clk.Now}
	good := newRecovery(t)
	if err := d1.Publish(ctx, ana, good); err != nil {
		t.Fatal(err)
	}
	// Someone who holds the retired key (it leaked) writes records for keys of their own: one
	// with no log entry at all, one that borrows the number of a real entry.
	forge := func(key Recipient, seq int64) string {
		id := d1.directoryID("ana@x.io", key.Fingerprint)
		rec := d1.sign(directoryRecord{Key: key.Text, Publisher: ana.ID, Provider: "google", Email: "ana@x.io", At: clk.Now().Unix(), Seq: seq}, id)
		data, _ := json.Marshal(rec)
		if err := store.Save(ctx, id, data); err != nil {
			t.Fatal(err)
		}
		return id
	}
	evil1, evil2 := newRecovery(t), newRecovery(t)
	id1, id2 := forge(evil1, 0), forge(evil2, 1)

	d2 := &AdapterDirectory{Store: store, Signer: rotated, Now: clk.Now}
	// Before and after Resign a record that a retired key signed and no entry backs is unverified.
	verified := func() map[string]bool {
		got, err := d2.Lookup(ctx, "ana@x.io")
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, k := range got {
			m[k.Fingerprint] = k.Verified
		}
		return m
	}
	if v := verified(); !v[good.Fingerprint] || v[evil1.Fingerprint] || v[evil2.Fingerprint] || len(v) != 3 {
		t.Errorf("before Resign: %v", v)
	}
	rep, err := d2.Resign(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Resigned != 1 || rep.Skipped != 2 || rep.Unknown != 0 {
		t.Errorf("report: %+v", rep)
	}
	if v := verified(); !v[good.Fingerprint] || v[evil1.Fingerprint] || v[evil2.Fingerprint] {
		t.Errorf("after Resign: %v", v)
	}
	for _, id := range []string{id1, id2} { // left under the old name, not copied under the current one
		if _, err := store.Load(ctx, id); err != nil {
			t.Errorf("the forged record was touched: %v", err)
		}
	}
	for _, k := range []Recipient{evil1, evil2} {
		if _, err := store.Load(ctx, d2.directoryID("ana@x.io", k.Fingerprint)); !errors.Is(err, docstore.ErrNotFound) {
			t.Errorf("a forged record was moved to the current key: %v", err)
		}
	}
	if !strings.Contains(rep.Summary(), "2 skipped") {
		t.Errorf("summary: %s", rep.Summary())
	}
}

// --- finding 6: a wrong or missing key is visible ----------------------------------------------

func TestStartupCountsRecordsNamedUnderUnknownKeys(t *testing.T) {
	ctx := context.Background()
	store := docstore.NewMemoryAdapter()
	dA := &AdapterDirectory{Store: store, Signer: testSigner(t, 1)}
	for range 3 {
		if err := dA.Publish(ctx, ana, newRecovery(t)); err != nil {
			t.Fatal(err)
		}
	}
	dB := &AdapterDirectory{Store: store, Signer: testSigner(t, 2)} // the operator set the wrong key
	srv := docstore.NewServer(store, "memory").WithDirectory(DirectoryService(dB))
	health := func() map[string]any {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if _, ok := health()["directory"]; !ok {
		t.Error("/health shows the directory")
	}
	rep, err := dB.Startup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Unknown != 3 || rep.Resigned != 0 {
		t.Errorf("report: %+v", rep)
	}
	if !strings.Contains(rep.Summary(), "3 directory records are named under unknown keys") {
		t.Errorf("summary: %s", rep.Summary())
	}
	dir, _ := health()["directory"].(map[string]any)
	if dir["unknown_keys"] != float64(3) || dir["records"] != float64(3) {
		t.Errorf("/health: %v", dir)
	}
	// With the right key (or the old one listed) nothing is unknown.
	dC := &AdapterDirectory{Store: store, Signer: testSigner(t, 1)}
	if rep, err := dC.Startup(ctx); err != nil || rep.Unknown != 0 || rep.Current != 3 {
		t.Errorf("right key: %+v %v", rep, err)
	}
	if h := dC.Health(); h.UnknownKeys != 0 || h.Records != 3 {
		t.Errorf("health: %+v", h)
	}
}

// --- finding 3: publish and revoke are atomic across replicas ----------------------------------

func TestRevokeOnAnotherReplicaIsNotOverwrittenByARepublish(t *testing.T) {
	ctx := context.Background()
	hs, clk := newHookStore(), newTestClock()
	signer := testSigner(t, 1)
	d1 := &AdapterDirectory{Store: hs, Signer: signer, Now: clk.Now}
	d2 := &AdapterDirectory{Store: hs, Signer: signer, Now: clk.Now} // a second replica
	key := newRecovery(t)
	if err := d1.Publish(ctx, ana, key); err != nil {
		t.Fatal(err)
	}
	id := d1.directoryID("ana@x.io", key.Fingerprint)
	loads, fired := 0, false
	hs.afterLoad = func(lid string) {
		if lid != id || fired {
			return
		}
		if loads++; loads == 1 { // d1 has read the live record; now d2 revokes it
			fired = true
			if _, err := d2.Revoke(ctx, ana, key.Fingerprint, nil); err != nil {
				t.Errorf("revoke: %v", err)
			}
		}
	}
	err := d1.Publish(ctx, ana, key) // a republish that read the live record
	hs.afterLoad = nil
	if !fired {
		t.Fatal("the interleaving did not happen")
	}
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("the republish after a concurrent revoke = %v, want ErrRevoked", err)
	}
	if got, _ := d1.Lookup(ctx, "ana@x.io"); len(got) != 0 {
		t.Errorf("the revoked key is back: %+v", got)
	}
	// The log never claims the publication that lost: its entry is voided.
	ops := logOps(t, d1)
	var last LogEntry
	for _, e := range ops {
		last = e
	}
	if last.Op != OpVoid || ops[last.Ref-1].Op != OpPublish || last.Ref != 3 {
		t.Errorf("log: %+v", ops)
	}
}

func TestPublishOfANewKeyIsCreateIfAbsentAcrossReplicas(t *testing.T) {
	ctx := context.Background()
	hs, clk := newHookStore(), newTestClock()
	signer := testSigner(t, 1)
	d1 := &AdapterDirectory{Store: hs, Signer: signer, Now: clk.Now}
	d2 := &AdapterDirectory{Store: hs, Signer: signer, Now: clk.Now}
	key := newRecovery(t)
	listed, fired := 0, false
	hs.afterLoad = func(lid string) {
		// d1 listed the address and found nothing; before it writes, d2 publishes the same key
		// and revokes it.
		if !strings.HasPrefix(lid, docstore.DirectoryEnrollPrefix) { // the owner enrollment is read after the listing
			return
		}
		if listed++; listed == 1 && !fired {
			fired = true
			if err := d2.Publish(ctx, ana, key); err != nil {
				t.Errorf("d2 publish: %v", err)
			}
			if _, err := d2.Revoke(ctx, ana, key.Fingerprint, nil); err != nil {
				t.Errorf("d2 revoke: %v", err)
			}
		}
	}
	err := d1.Publish(ctx, ana, key)
	hs.afterLoad = nil
	if !fired {
		t.Fatal("the interleaving did not happen")
	}
	if !errors.Is(err, ErrRevoked) {
		t.Fatalf("d1 publish after d2's publish and revoke = %v, want ErrRevoked (it must not overwrite the tombstone)", err)
	}
	if got, _ := d1.Lookup(ctx, "ana@x.io"); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}

func TestPublishLeavesAVoidEntryWhenTheRecordCannotBeWritten(t *testing.T) {
	ctx := context.Background()
	hs, clk := newHookStore(), newTestClock()
	d := &AdapterDirectory{Store: hs, Signer: testSigner(t, 1), Now: clk.Now}
	hs.failSaveIf = func(id string) error {
		if strings.HasPrefix(id, directoryPrefix) && !strings.HasPrefix(id, popPrefix) {
			return errors.New("disk full")
		}
		return nil
	}
	key := newRecovery(t)
	if err := d.Publish(ctx, ana, key); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("publish = %v", err)
	}
	ops := logOps(t, d)
	if len(ops) != 2 || ops[0].Op != OpPublish || ops[1].Op != OpVoid || ops[1].Ref != 1 {
		t.Fatalf("log: %+v", ops)
	}
	audit := AuditAddress("ana@x.io", d.LogAddressesFor("ana@x.io"), ops, AuditOptions{})
	if len(audit.Entries) != 0 {
		t.Errorf("a voided entry took effect: %+v", audit.Entries)
	}
	keys := logKeys(t, d)
	if _, err := VerifyLog(ops, "", 0, keys); err != nil {
		t.Errorf("the voided log verifies: %v", err)
	}
	if got, _ := d.Lookup(ctx, "ana@x.io"); len(got) != 0 {
		t.Errorf("%+v", got)
	}
	// The same for a revoke.
	hs.failSaveIf = nil
	if err := d.Publish(ctx, ana, key); err != nil {
		t.Fatal(err)
	}
	hs.failSaveIf = func(id string) error {
		if strings.HasPrefix(id, directoryPrefix) {
			return errors.New("disk full")
		}
		return nil
	}
	if _, err := d.Revoke(ctx, ana, key.Fingerprint, nil); err == nil {
		t.Fatal("revoke succeeded")
	}
	ops = logOps(t, d)
	if n := len(ops); n != 5 || ops[3].Op != OpRevoke || ops[4].Op != OpVoid || ops[4].Ref != 4 {
		t.Fatalf("log: %+v", ops)
	}
	if got, _ := d.Lookup(ctx, "ana@x.io"); len(got) != 1 {
		t.Errorf("the key is still published: %+v", got)
	}
}

// --- finding 9: a busy log answers 503, after bounded retries ----------------------------------

func TestBusyLogIsBoundedAndAnswers503(t *testing.T) {
	old := logBackoffBase
	logBackoffBase = time.Microsecond
	defer func() { logBackoffBase = old }()
	hs := newHookStore()
	hs.failSaveIf = func(id string) error {
		if strings.HasPrefix(id, logEntryPrefix) {
			return docstore.ErrConflict // somebody always gets there first
		}
		return nil
	}
	e := newDirEnvWith(t, func(d *AdapterDirectory) { d.Store = hs }, nil)
	tok := e.token(ana)
	rec := e.publish(tok, map[string]string{"key": newRecovery(t).Text})
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("%d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	hs.mu.Lock()
	n := hs.saveIfs
	hs.mu.Unlock()
	if n > 20 { // 16 rounds lost, and the writes of the head and the claims around them
		t.Errorf("%d conditional writes: the retries are not bounded", n)
	}
	if got, _ := e.dir.Lookup(context.Background(), "ana@x.io"); len(got) != 0 {
		t.Errorf("nothing is published: %+v", got)
	}
}

// --- finding 8: challenge tokens are honoured only for the current key -------------------------

func TestChallengesVerifyAgainstTheCurrentKeyOnly(t *testing.T) {
	ctx := context.Background()
	store, clk := docstore.NewMemoryAdapter(), newTestClock()
	oldSigner, rotated := rotation(t)
	dOld := &AdapterDirectory{Store: store, Signer: oldSigner, Now: clk.Now}
	dNew := &AdapterDirectory{Store: store, Signer: rotated, Now: clk.Now}
	key, id := ageKey(t)
	// Control: a challenge of the current key works.
	c, err := dOld.Challenge(ctx, ana, key.Text)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := OpenChallenge(c, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dOld.PublishWith(ctx, ana, key, PublishOptions{Challenge: proof.Token, Nonce: proof.Nonce}); err != nil {
		t.Fatal(err)
	}
	// A challenge made under the old key is refused once it is retired.
	key2, id2 := ageKey(t)
	c2, _ := dOld.Challenge(ctx, ana, key2.Text)
	p2, err := OpenChallenge(c2, id2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dNew.PublishWith(ctx, ana, key2, PublishOptions{Challenge: p2.Token, Nonce: p2.Nonce}); !errors.Is(err, ErrBadProof) {
		t.Fatalf("a challenge of a retired key = %v, want ErrBadProof", err)
	}
	// And the new key's own work.
	c3, _ := dNew.Challenge(ctx, ana, key2.Text)
	p3, _ := OpenChallenge(c3, id2)
	if _, err := dNew.PublishWith(ctx, ana, key2, PublishOptions{Challenge: p3.Token, Nonce: p3.Nonce}); err != nil {
		t.Fatalf("a challenge of the current key: %v", err)
	}
}

// --- finding 10: a tombstone carries the revoke entry's number ----------------------------------

func TestTombstoneCarriesTheRevokeEntrysNumber(t *testing.T) {
	ctx := context.Background()
	d, _, clk := newTestDirectory(t)
	key := newRecovery(t)
	if err := d.Publish(ctx, ana, key); err != nil {
		t.Fatal(err)
	}
	clk.Add(time.Second)
	if _, err := d.Revoke(ctx, ana, key.Fingerprint, nil); err != nil {
		t.Fatal(err)
	}
	got, err := d.LookupWith(ctx, "ana@x.io", LookupOptions{IncludeRevoked: true})
	if err != nil || len(got) != 1 || !got[0].Revoked {
		t.Fatalf("%+v %v", got, err)
	}
	ops := logOps(t, d)
	if len(ops) != 2 || ops[1].Op != OpRevoke || got[0].Seq != ops[1].Seq || got[0].Seq == ops[0].Seq {
		t.Errorf("tombstone seq %d, log %+v", got[0].Seq, ops)
	}
	if !got[0].Verified {
		t.Errorf("a tombstone backed by its revoke entry verifies: %+v", got[0])
	}
}

// --- finding 11: the signed head and the truncation check ---------------------------------------

func TestLogHeadDetectsTruncationAndRewrite(t *testing.T) {
	ctx := context.Background()
	d, store, clk := newTestDirectory(t)
	keys := logKeys(t, d)
	h0, err := d.Head(ctx)
	if err != nil || h0.Seq != 0 || h0.Hash != zeroHash || VerifyHead(h0, keys) != nil {
		t.Fatalf("empty head: %+v %v", h0, err)
	}
	for range 4 {
		clk.Add(time.Second)
		if err := d.Publish(ctx, ana, newRecovery(t)); err != nil {
			t.Fatal(err)
		}
	}
	head, err := d.Head(ctx)
	if err != nil || head.Seq != 4 || head.Hash == "" || VerifyHead(head, keys) != nil {
		t.Fatalf("head: %+v %v", head, err)
	}
	tampered := head
	tampered.Seq = 3
	if VerifyHead(tampered, keys) == nil {
		t.Error("a head whose number was changed verifies")
	}
	// A client that saw entry 3 and fetches from entry 3 on.
	all, _ := d.ReadLog(ctx, 0, 100)
	seen := all.Entries[2]
	fromSeen, _ := d.ReadLog(ctx, seen.Seq-1, 100)
	if err := VerifyLogHead(seen.Seq, seen.Hash, fromSeen.Entries); err != nil {
		t.Fatalf("intact log: %v", err)
	}
	if err := CheckHead(seen.Seq, seen.Hash, head); err != nil {
		t.Fatalf("intact head: %v", err)
	}
	if err := CheckHead(0, "", head); err != nil {
		t.Fatal(err)
	}
	// Tail truncation: the server drops the last two entries and the head hint.
	for _, seq := range []int64{4, 3} {
		if err := store.Delete(ctx, logEntryID(seq)); err != nil {
			t.Fatal(err)
		}
	}
	_ = store.Delete(ctx, logHeadID)
	short, err := d.Head(ctx)
	if err != nil || short.Seq != 2 || VerifyHead(short, keys) != nil {
		t.Fatalf("truncated head: %+v %v", short, err)
	}
	if err := CheckHead(seen.Seq, seen.Hash, short); !errors.Is(err, ErrLogTruncated) {
		t.Errorf("truncated head: %v", err)
	}
	cut, _ := d.ReadLog(ctx, seen.Seq-1, 100)
	if len(cut.Entries) != 0 {
		t.Fatalf("%+v", cut.Entries)
	}
	if err := VerifyLogHead(seen.Seq, seen.Hash, cut.Entries); !errors.Is(err, ErrLogTruncated) {
		t.Errorf("truncated entries: %v", err)
	}
	// Rewrite: the server puts a different entry where the client saw one.
	other := seen
	other.Fingerprint = "SHA256:other"
	other.Hash = logHash(other)
	if err := VerifyLogHead(seen.Seq, seen.Hash, []LogEntry{other}); !errors.Is(err, ErrLogTruncated) {
		t.Errorf("rewritten entry: %v", err)
	}
	if err := CheckHead(seen.Seq, seen.Hash, docstore.DirectoryHead{Seq: seen.Seq, Hash: other.Hash}); !errors.Is(err, ErrLogTruncated) {
		t.Errorf("rewritten head: %v", err)
	}
}

// --- finding 12: exactly one "@" --------------------------------------------------------------

func TestMalformedAddressesAreRefused(t *testing.T) {
	ctx := context.Background()
	for addr, want := range map[string]bool{"ana@x.io": true, " ANA@X.io ": true, "a@b@c": false, "ana": false, "@x.io": false, "ana@": false, "an a@x.io": false, "": false, "a@@b": false} {
		if got := docstore.ValidAddress(addr); got != want {
			t.Errorf("ValidAddress(%q) = %v", addr, got)
		}
	}
	d, _, _ := newTestDirectory(t)
	if _, err := d.Lookup(ctx, "a@b@c"); !errors.Is(err, docstore.ErrDirectoryBadAddress) {
		t.Errorf("lookup a@b@c: %v", err)
	}
	bad := auth.User{ID: "google:9", Email: "a@b@x.io", VerifiedEmail: true}
	if err := d.Publish(ctx, bad, newRecovery(t)); !errors.Is(err, docstore.ErrDirectoryBadAddress) {
		t.Errorf("publish for a@b@x.io: %v", err)
	}
	if _, err := d.Revoke(ctx, bad, "SHA256:x", nil); !errors.Is(err, docstore.ErrDirectoryBadAddress) {
		t.Errorf("revoke for a@b@x.io: %v", err)
	}
	e := newDirEnv(t)
	tok := e.token(ana)
	if rec := e.do("GET", "/directory/lookup?email=a@b@c", tok, ""); rec.Code != 400 {
		t.Errorf("route lookup a@b@c = %d", rec.Code)
	}
	if rec := e.publish(e.token(bad), map[string]string{"key": newRecovery(t).Text}); rec.Code != 400 {
		t.Errorf("route publish for a@b@x.io = %d", rec.Code)
	}
}

// --- finding 4: the log routes are not a policy bypass -------------------------------------------

func TestLogRoutesNeedAProviderSessionAndFollowThePolicyAndBudgets(t *testing.T) {
	e := newDirEnv(t)
	anaTok := e.token(ana)
	e.publish(anaTok, map[string]string{"key": newRecovery(t).Text})
	passkeyTok := e.token(auth.User{ID: "passkey:z", Email: "z@x.io", VerifiedEmail: true})
	for _, path := range []string{"/directory/log", "/directory/log?since=0", "/directory/log/head", "/directory/log/address"} {
		if rec := e.do("GET", path, passkeyTok, ""); rec.Code != 403 {
			t.Errorf("passkey session %s = %d, want 403", path, rec.Code)
		}
		if rec := e.do("GET", path, "", ""); rec.Code != 401 {
			t.Errorf("signed out %s = %d", path, rec.Code)
		}
	}
	if rec := e.do("GET", "/directory/log", anaTok, ""); rec.Code != 200 {
		t.Errorf("provider session = %d", rec.Code)
	}

	// The policy applies: a policy that does not let this user read refuses the log too.
	denied := newDirEnvWith(t, nil, func(s *docstore.Server) {
		s.WithDirectoryPolicy(docstore.DirectoryPolicyFunc(func(auth.User, string) bool { return false }))
	})
	tok := denied.token(ana)
	for _, path := range []string{"/directory/log", "/directory/log/head"} {
		if rec := denied.do("GET", path, tok, ""); rec.Code != 403 {
			t.Errorf("policy %s = %d, want 403", path, rec.Code)
		}
	}

	// A per-user budget: a user over their own limit is refused.
	limited := newDirEnvWith(t, nil, func(s *docstore.Server) {
		s.WithDirectoryLimits(func(name string, perMinute float64, burst int) docstore.Limiter {
			if name == "dir-log" {
				return docstore.NewRateLimiter(1, 2)
			}
			return docstore.NewRateLimiter(perMinute, burst)
		})
	})
	tok = limited.token(ana)
	var codes []int
	for range 4 {
		codes = append(codes, limited.do("GET", "/directory/log", tok, "").Code)
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 || codes[3] != 429 {
		t.Errorf("per-user log budget: %v", codes)
	}
}

func TestLogAddressIsKeyedSoAGuessedAddressCannotBeTested(t *testing.T) {
	e := newDirEnv(t)
	anaTok := e.token(ana)
	e.publish(anaTok, map[string]string{"key": newRecovery(t).Text})
	unkeyed := sha256.Sum256([]byte("gojiberry-directory-log|ana@x.io"))
	log, _ := e.dir.ReadLog(context.Background(), 0, 10)
	if len(log.Entries) != 1 || log.Entries[0].Address == hex.EncodeToString(unkeyed[:]) {
		t.Fatalf("the address in the log is the unkeyed hash: %+v", log.Entries)
	}
	// Another directory key names the same address differently: it cannot be computed without the key.
	other := &AdapterDirectory{Store: docstore.NewMemoryAdapter(), Signer: testSigner(t, 9)}
	if other.LogAddressFor("ana@x.io") == e.dir.LogAddressFor("ana@x.io") {
		t.Error("the pseudonym does not depend on the key")
	}
	if e.dir.LogAddressFor(" ANA@x.io ") != e.dir.LogAddressFor("ana@x.io") || e.dir.LogAddressFor("bo@x.io") == e.dir.LogAddressFor("ana@x.io") {
		t.Error("normalised, and per address")
	}
	// The owner is told theirs, and finds their entries with it; nobody else gets it.
	rec := e.do("GET", "/directory/log/address", anaTok, "")
	var la docstore.DirectoryLogAddress
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &la) != nil || la.Address != log.Entries[0].Address {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	boRec := e.do("GET", "/directory/log/address", e.token(bo), "")
	var lb docstore.DirectoryLogAddress
	_ = json.Unmarshal(boRec.Body.Bytes(), &lb)
	if lb.Address == la.Address {
		t.Error("a user is told another's pseudonym")
	}
	// The chain still verifies without knowing any address.
	if _, err := VerifyLog(log.Entries, "", 0, logKeys(t, e.dir)); err != nil {
		t.Errorf("VerifyLog: %v", err)
	}
}

// --- finding 5: a user over their own limit does not spend the shared budgets ------------------

type countingLimiter struct {
	inner docstore.Limiter
	mu    *sync.Mutex
	calls map[string][]string
	name  string
}

func (c countingLimiter) Allow(ctx context.Context, key string) (bool, time.Duration) {
	c.mu.Lock()
	c.calls[c.name] = append(c.calls[c.name], key)
	c.mu.Unlock()
	return c.inner.Allow(ctx, key)
}

func TestLookupSpendsTheUsersOwnBudgetBeforeTheSharedOnes(t *testing.T) {
	var mu sync.Mutex
	calls := map[string][]string{}
	e := newDirEnvWith(t, nil, func(s *docstore.Server) {
		s.WithDirectoryLimits(func(name string, perMinute float64, burst int) docstore.Limiter {
			inner := docstore.NewRateLimiter(perMinute, burst)
			if name == "dir-lookup" {
				inner = docstore.NewRateLimiter(1, 1) // one lookup for each user
			}
			return countingLimiter{inner: inner, mu: &mu, calls: calls, name: name}
		})
	})
	tok := e.token(ana)
	if code, _ := e.lookup(tok, "bo@x.io"); code != 200 {
		t.Fatalf("first = %d", code)
	}
	for range 5 { // over their own limit: refused without touching the IP or global budget
		if code, _ := e.lookup(tok, "bo@x.io"); code != 429 {
			t.Fatalf("over the limit = %d", code)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if n := len(calls["dir-lookup-ip"]); n != 1 {
		t.Errorf("the shared IP budget was asked %d times, want 1 (only the lookup that was allowed)", n)
	}
	if n := len(calls["dir-lookup-all"]); n != 1 {
		t.Errorf("the global budget was asked %d times, want 1", n)
	}
	if n := len(calls["dir-lookup"]); n != 6 {
		t.Errorf("the user's budget was asked %d times", n)
	}
}

func TestGlobalLookupBudgetCanBePerPolicyDomain(t *testing.T) {
	mk := func(perDomain bool) *dirEnv {
		return newDirEnvWith(t, nil, func(s *docstore.Server) {
			s.WithDirectoryGlobalPerDomain(perDomain).WithDirectoryLimits(func(name string, perMinute float64, burst int) docstore.Limiter {
				if name == "dir-lookup-all" {
					return docstore.NewRateLimiter(0.001, 1) // one lookup for the whole server (or domain)
				}
				return docstore.NewRateLimiter(perMinute, burst)
			})
		})
	}
	x := auth.User{ID: "google:x", Email: "u@x.io", VerifiedEmail: true}
	y := auth.User{ID: "google:y", Email: "u@y.org", VerifiedEmail: true}
	shared := mk(false)
	if code, _ := shared.lookup(shared.token(x), "bo@x.io"); code != 200 {
		t.Fatal(code)
	}
	if code, _ := shared.lookup(shared.token(y), "bo@x.io"); code != 429 {
		t.Errorf("one shared budget: the other domain is refused too, got %d", code)
	}
	per := mk(true)
	if code, _ := per.lookup(per.token(x), "bo@x.io"); code != 200 {
		t.Fatal(code)
	}
	if code, _ := per.lookup(per.token(y), "bo@x.io"); code != 200 {
		t.Errorf("a budget per domain: another domain is not affected, got %d", code)
	}
	if code, _ := per.lookup(per.token(auth.User{ID: "google:x2", Email: "v@x.io", VerifiedEmail: true}), "bo@x.io"); code != 429 {
		t.Errorf("the same domain shares it: %d", code)
	}
}

// --- D: a store without conditional writes -----------------------------------------------------

// plainStore hides every optional capability of the store it wraps.
type plainStore struct{ docstore.Adapter }

func TestCheckStoreRefusesReplicasOnAStoreWithoutConditionalWrites(t *testing.T) {
	ctx := context.Background()
	plain := &AdapterDirectory{Store: plainStore{docstore.NewMemoryAdapter()}, Signer: testSigner(t, 1)}
	if err := plain.CheckStore(ctx, 2); err == nil || !strings.Contains(err.Error(), "conditional") {
		t.Errorf("two replicas on a plain store: %v", err)
	}
	var buf strings.Builder
	logTo(t, &buf)
	if err := plain.CheckStore(ctx, 1); err != nil {
		t.Errorf("one replica on a plain store: %v", err)
	}
	if !strings.Contains(buf.String(), "WARNING") {
		t.Errorf("no warning: %q", buf.String())
	}
	cond := &AdapterDirectory{Store: docstore.NewMemoryAdapter(), Signer: testSigner(t, 1)}
	if err := cond.CheckStore(ctx, 5); err != nil {
		t.Errorf("a conditional store: %v", err)
	}
	// A wrapper that has SaveIf but whose store does not honour it counts as plain.
	refusing := &AdapterDirectory{Store: refusesConditional{docstore.NewMemoryAdapter()}, Signer: testSigner(t, 1)}
	if err := refusing.CheckStore(ctx, 2); err == nil {
		t.Error("a store that answers ErrConditionalUnsupported is not conditional")
	}
}

type refusesConditional struct{ *docstore.MemoryAdapter }

func (refusesConditional) SaveIf(context.Context, string, []byte, string) error {
	return docstore.ErrConditionalUnsupported
}

func logTo(t *testing.T, w *strings.Builder) {
	t.Helper()
	logCapture(t, w)
}

// The directory's keyspaces stay readable on an encrypting store and out of the document API.
func TestDirectoryKeyspacesAreReservedAndLeftInTheClear(t *testing.T) {
	for _, id := range []string{"directory/ab/cd", "directory-log/e/000000000001", "directory-log/head", "directory-enroll/ab", "ratelimit/x/y"} {
		if !DefaultSkip(id) {
			t.Errorf("%s is encrypted", id)
		}
		if docstore.IsDocumentKey(id) {
			t.Errorf("%s counts as a document", id)
		}
	}
	e := newDirEnv(t)
	tok := e.token(ana)
	for _, id := range []string{"directory-enroll/ab", "directory-enroll/", "DIRECTORY-ENROLL/ab"} {
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			if rec := e.do(method, "/docs/"+id, tok, "x"); rec.Code != 400 && rec.Code != 404 {
				t.Errorf("%s %s via /docs = %d", method, id, rec.Code)
			}
		}
	}
}

// Two replicas revoking the same key: the second must not write a second tombstone over the first.
func TestRevokeRacingAnotherRevokeDoesNotOverwriteIt(t *testing.T) {
	ctx := context.Background()
	hs, clk := newHookStore(), newTestClock()
	signer := testSigner(t, 1)
	d1 := &AdapterDirectory{Store: hs, Signer: signer, Now: clk.Now}
	d2 := &AdapterDirectory{Store: hs, Signer: signer, Now: clk.Now}
	key := newRecovery(t)
	if err := d1.Publish(ctx, ana, key); err != nil {
		t.Fatal(err)
	}
	id := d1.directoryID("ana@x.io", key.Fingerprint)
	fired := false
	hs.afterLoad = func(lid string) {
		if lid == id && !fired {
			fired = true
			clk.Add(time.Second)
			if _, err := d2.Revoke(ctx, ana, key.Fingerprint, nil); err != nil {
				t.Errorf("d2: %v", err)
			}
		}
	}
	_, err := d1.Revoke(ctx, ana, key.Fingerprint, nil)
	hs.afterLoad = nil
	if !fired {
		t.Fatal("the interleaving did not happen")
	}
	if !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("the slower revoke = %v, want ErrKeyNotFound (it was revoked already)", err)
	}
	ops := logOps(t, d1)
	last := ops[len(ops)-1]
	if len(ops) != 4 || ops[1].Op != OpRevoke || ops[2].Op != OpRevoke || last.Op != OpVoid || last.Ref != 3 {
		t.Errorf("log: %+v", ops)
	}
	got, _ := d1.LookupWith(ctx, "ana@x.io", LookupOptions{IncludeRevoked: true})
	if len(got) != 1 || !got[0].Revoked || got[0].Seq != 2 {
		t.Errorf("the first tombstone stands: %+v", got)
	}
}
