package directoryclient

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// logRewriter passes the answers of /directory/log through f (a map of the JSON answer).
func logRewriter(f func(m map[string]any)) *http.Client {
	return pathClient("/directory/log", func(body []byte) []byte {
		var m map[string]any
		if json.Unmarshal(body, &m) != nil {
			return body
		}
		f(m)
		b, _ := json.Marshal(m)
		return b
	})
}

func publishN(t *testing.T, c *Client, n int) {
	t.Helper()
	for range n {
		p, _, id := ageKey(t)
		if _, err := c.Publish(context.Background(), PublishRequest{Email: "ana@x.io", Key: p, Identity: id}); err != nil {
			t.Fatal(err)
		}
	}
}

// S1: with nothing saved, the log must start where the read asked; a server that starts the answer
// later withheld the start of the log.
func TestWatchLogRefusesAnAnswerThatStartsLate(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	// An enrolled address is shown the whole log (anyone else gets only their own entries).
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	publishOwn := func() {
		p, _, id := ageKey(t)
		if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p, Identity: id, Owner: owner}); err != nil {
			t.Fatal(err)
		}
	}
	publishOwn()
	publishOwn()
	dropFirst := func(n int) *http.Client {
		return logRewriter(func(m map[string]any) {
			if es, ok := m["entries"].([]any); ok && len(es) >= n {
				m["entries"] = es[n:]
			}
		})
	}
	late := &Client{BaseURL: c.BaseURL, Token: c.Token, LogKeys: c.LogKeys, HTTP: dropFirst(2)}
	if _, err := late.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); !errors.Is(err, ErrLogCut) {
		t.Fatalf("a first run whose log starts at entry 3: %v", err)
	}
	// The same after a filtered read: the unfiltered one continues from the cursor, and must start
	// right after it.
	st := &WatchState{}
	if _, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{}); err != nil || st.Seq != 3 || st.Filtered {
		t.Fatalf("%v %+v", err, st)
	}
	sw := *st
	sw.Filtered, sw.Cursor = true, 1
	if _, err := late.WatchLog(ctx, "ana@x.io", &sw, WatchOptions{}); !errors.Is(err, ErrLogCut) {
		t.Fatalf("a read after a filtered one that skips entry 2: %v", err)
	}
	sw = *st
	sw.Filtered, sw.Cursor = true, 1
	if _, err := c.WatchLog(ctx, "ana@x.io", &sw, WatchOptions{}); err != nil {
		t.Fatalf("the honest continuation is accepted: %v", err)
	}
	if _, err := c.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); err != nil {
		t.Fatal(err)
	}
}

// S7: an address the audited state says is enrolled is shown the whole log; a filtered answer is
// the server withholding it.
func TestWatchLogRefusesAFilteredAnswerForAnEnrolledAddress(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{Owner: owner}); err != nil {
		t.Fatal(err)
	}
	st := *c.State
	st.Enrolled = true // settled (the audit keeps recent entries pending for a while)
	st.OwnerKeys = []docstore.DirectoryOwnerKey{{Pub: encrypt.FormatOwnerPub(owner.Public().(ed25519.PublicKey)), Seq: 1}}
	filt := &Client{BaseURL: c.BaseURL, Token: c.Token, LogKeys: c.LogKeys, HTTP: logRewriter(func(m map[string]any) { m["filtered"] = true })}
	if _, err := filt.WatchLog(ctx, "ana@x.io", &st, WatchOptions{Owner: owner}); !errors.Is(err, ErrFilteredEnrolled) {
		t.Fatalf("%v", err)
	}
	// Not enrolled as far as the state knows: a filtered answer is what the server gives anybody
	// who is not the owner of an enrolled address.
	if _, err := filt.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); errors.Is(err, ErrFilteredEnrolled) {
		t.Fatalf("%v", err)
	}
}

// S2: the number of an operation is the server's word. The entry there must be what the client did,
// or it is not the client's, and its pseudonym is not pinned.
func TestWatchLogHoldsMadeEntriesToWhatTheClientDid(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bo := e.client(bo)
	pb, _, _ := ageKey(t)
	if _, err := bo.Publish(ctx, PublishRequest{Email: "bo@x.io", Key: pb}); err != nil { // another address
		t.Fatal(err)
	}
	c := e.client(ana)
	owner, _ := GenerateOwnerKey()
	if _, err := e.client(ana).Enroll(ctx, "ana@x.io", owner); err != nil { // so that ana reads the whole log
		t.Fatal(err)
	}
	// The server answers ana's publication with the number of bo's entry, and reports bo's
	// pseudonym as hers.
	liar := &Client{BaseURL: c.BaseURL, Token: c.Token, LogKeys: c.LogKeys, State: c.State, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		res, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			return res, err
		}
		switch r.URL.Path {
		case "/directory/publish":
			return rewriteBody(res, func(b []byte) []byte {
				var m map[string]any
				_ = json.Unmarshal(b, &m)
				m["seq"] = 1 // bo's entry
				out, _ := json.Marshal(m)
				return out
			}), nil
		case "/directory/log/address":
			return rewriteBody(res, func([]byte) []byte {
				out, _ := json.Marshal(map[string]string{"address": e.dir.LogAddressFor("bo@x.io")})
				return out
			}), nil
		}
		return res, nil
	})}}
	p, _, id := ageKey(t)
	if _, err := liar.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p, Identity: id, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	if len(c.State.Made) != 1 || c.State.Made[0].Seq != 1 || c.State.Made[0].Op != encrypt.OpPublish || c.State.Made[0].Fingerprint == "" {
		t.Fatalf("what the client did is recorded with the server's number: %+v", c.State.Made)
	}
	rep, err := liar.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{})
	if err != nil {
		t.Fatalf("%v", err)
	}
	found := false
	for _, v := range rep.Violations {
		found = found || (v.Seq == 1 && strings.Contains(v.Why, "another key"))
	}
	if !found {
		t.Fatalf("an entry that is not what the client did is a violation: %+v", rep.Violations)
	}
	if len(c.State.Addresses) != 0 || !rep.AddressUnconfirmed {
		t.Fatalf("a server-chosen number pinned an address: %+v", c.State.Addresses)
	}
}

func TestMadeMismatch(t *testing.T) {
	e := docstore.DirectoryLogEntry{Op: encrypt.OpPublish, Fingerprint: "SHA256:ab+/c", OwnerSig: "sig", ProofSig: "proof"}
	for name, tc := range map[string]struct {
		m    MadeEntry
		diff bool
	}{
		"same":            {MadeEntry{Op: encrypt.OpPublish, Fingerprint: "SHA256:ab+/c", OwnerSig: "sig"}, false},
		"fingerprint seg": {MadeEntry{Op: encrypt.OpPublish, Fingerprint: docstore.DirectoryFingerprintSegment("SHA256:ab+/c")}, false},
		"other op":        {MadeEntry{Op: encrypt.OpRevoke, Fingerprint: "SHA256:ab+/c"}, true},
		"other key":       {MadeEntry{Op: encrypt.OpPublish, Fingerprint: "SHA256:zz"}, true},
		"other signature": {MadeEntry{Op: encrypt.OpPublish, Fingerprint: "SHA256:ab+/c", OwnerSig: "x"}, true},
		"reset proof":     {MadeEntry{Op: encrypt.OpReset, Fingerprint: "SHA256:ab+/c", OwnerSig: "proof"}, false},
		"reset not sig":   {MadeEntry{Op: encrypt.OpReset, Fingerprint: "SHA256:ab+/c", OwnerSig: "sig"}, true},
	} {
		ent := e
		if strings.HasPrefix(name, "reset") {
			ent.Op = encrypt.OpReset
		}
		if got := madeMismatch(tc.m, ent) != ""; got != tc.diff {
			t.Errorf("%s: mismatch %v, want %v", name, got, tc.diff)
		}
	}
}

// S4: what is pinned after an enrollment is the key the client made, not the one the server's
// answer names.
func TestEnrollPinsTheClientsOwnKeyNotTheServersAnswer(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	other, _ := GenerateOwnerKey()
	otherPub := encrypt.FormatOwnerPub(other.Public().(ed25519.PublicKey))
	c.HTTP = pathClient("/directory/enroll", func(b []byte) []byte {
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		m["keys"] = []any{map[string]any{"pub": otherPub, "seq": 1}}
		out, _ := json.Marshal(m)
		return out
	})
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Pins.Get("ana@x.io")
	if want := encrypt.FormatOwnerPub(owner.Public().(ed25519.PublicKey)); !ok || got != want {
		t.Fatalf("pinned %q, want the key the client made %q", got, want)
	}
	if m := c.State.Made; len(m) != 1 || m[0].Op != encrypt.OpEnroll || m[0].Fingerprint != encrypt.OwnerFingerprint(owner.Public().(ed25519.PublicKey)) || m[0].OwnerSig == "" {
		t.Errorf("%+v", m)
	}
	// Rotation and reset pin the new key the same way.
	next, _ := GenerateOwnerKey()
	if _, err := c.Rotate(ctx, "ana@x.io", owner, next); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Pins.Get("ana@x.io"); got != encrypt.FormatOwnerPub(next.Public().(ed25519.PublicKey)) {
		t.Errorf("after the rotation %q", got)
	}
}

// ownerAndRows enrolls ana's owner key, publishes a key and returns a viewer's first and second
// lookups with the given client set-up.
func enrolledAna(t *testing.T, e *env) (OwnerKey, string) {
	t.Helper()
	ctx := context.Background()
	c := e.client(ana)
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	p, _, id := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p, Identity: id, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	return owner, encrypt.FormatOwnerPub(owner.Public().(ed25519.PublicKey))
}

// S5: a key pinned only by trust on first use does not get the strongest mark.
func TestTrustOnFirstUseIsNotPromotedToPinned(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	_, pub := enrolledAna(t, e)
	v := e.client(bo)
	rows, err := v.Lookup(ctx, "ana@x.io")
	if err != nil || len(rows) != 1 || !rows[0].OwnerUnpinned || rows[0].OwnerSigned {
		t.Fatalf("%+v %v", rows, err)
	}
	for i := range 2 {
		rows, err = v.Lookup(ctx, "ana@x.io")
		r := rows[0]
		if err != nil || !r.OwnerFirstSeen || r.OwnerSigned || r.OwnerPinned || r.OwnerUnpinned || r.OwnerChanged || r.DirectoryKey.OwnerSigned {
			t.Fatalf("lookup %d: the same key as the first lookup showed is consistent, not confirmed: %+v %v", i+2, r, err)
		}
	}
	if _, ok := v.Pins.Get("ana@x.io"); ok {
		t.Error("a lookup wrote a pin")
	}
	// Confirmed by the host: the strongest mark.
	v.Pins.Set("ana@x.io", pub)
	if rows, _ = v.Lookup(ctx, "ana@x.io"); !rows[0].OwnerSigned || !rows[0].OwnerPinned || rows[0].OwnerFirstSeen {
		t.Errorf("%+v", rows[0])
	}
	// Without a State there is no memory: every lookup is a first sight.
	n := e.client(bo)
	n.State = nil
	for range 2 {
		if rows, _ = n.Lookup(ctx, "ana@x.io"); !rows[0].OwnerUnpinned || rows[0].OwnerFirstSeen {
			t.Errorf("%+v", rows[0])
		}
	}
}

// With two owner keys in one answer and nothing pinned, the second one is a change.
func TestSecondOwnerKeyInOneAnswerIsChanged(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	p1, _, id1 := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p1, Identity: id1, Owner: owner}); err != nil {
		t.Fatal(err)
	}
	next, _ := GenerateOwnerKey()
	if _, err := c.Rotate(ctx, "ana@x.io", owner, next); err != nil {
		t.Fatal(err)
	}
	p2, _, id2 := ageKey(t)
	if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p2, Identity: id2, Owner: next}); err != nil {
		t.Fatal(err)
	}
	rows, err := e.client(bo).Lookup(ctx, "ana@x.io")
	if err != nil || len(rows) != 2 {
		t.Fatalf("%+v %v", rows, err)
	}
	unpinned, changed := 0, 0
	for _, r := range rows {
		if r.OwnerSigned || r.OwnerPinned {
			t.Errorf("nothing is confirmed: %+v", r)
		}
		if r.OwnerUnpinned {
			unpinned++
		}
		if r.OwnerChanged {
			changed++
		}
	}
	if unpinned != 1 || changed != 1 {
		t.Errorf("unpinned %d, changed %d", unpinned, changed)
	}
}

// The chain WatchLog audited for the client's own address confirms a row without any pin.
func TestAuditedOwnerChainConfirmsARowWithoutPins(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	_, pub := enrolledAna(t, e)
	c := e.client(ana)
	c.Pins = nil
	c.State.Email = "ana@x.io"
	c.State.OwnerKeys = []docstore.DirectoryOwnerKey{{Pub: pub, Seq: 1}}
	rows, err := c.Lookup(ctx, "ana@x.io")
	if err != nil || len(rows) != 1 || !rows[0].OwnerSigned || !rows[0].OwnerPinned {
		t.Fatalf("%+v %v", rows, err)
	}
	c.State.OwnerKeys = nil
	if rows, _ = c.Lookup(ctx, "ana@x.io"); rows[0].OwnerSigned {
		t.Errorf("without the chain or a pin it is not confirmed: %+v", rows[0])
	}
}

// forgeEntry appends, as a holder of the directory key could, a signed entry for the address.
func forgeEntry(t *testing.T, e *env, in encrypt.LogEntry) int64 {
	t.Helper()
	ctx := context.Background()
	h, err := e.dir.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	in.Seq, in.Prev = h.Seq+1, h.Hash
	if in.At == 0 {
		in.At = time.Now().Unix()
	}
	in.Address = e.dir.LogAddressFor("ana@x.io")
	entry := encrypt.SignLogEntry(e.dir.Signer, in)
	data, _ := json.Marshal(entry)
	if err := e.store.Save(ctx, fmt.Sprintf("directory-log/e/%012d", entry.Seq), data); err != nil {
		t.Fatal(err)
	}
	return entry.Seq
}

// A void that comes in a later run than the entry it cancels is shown for the entry that was
// reported before, and once.
func TestWatchLogShowsAVoidThatCameAfterTheReport(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	publishN(t, c, 1)
	seq := forgeEntry(t, e, encrypt.LogEntry{Op: encrypt.OpPublish, Fingerprint: "SHA256:late"})
	rep, err := c.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{})
	if err != nil || len(rep.Foreign) != 1 || rep.Foreign[0].Entry.Seq != seq || rep.Foreign[0].Voided {
		t.Fatalf("%+v %v", rep, err)
	}
	v := forgeEntry(t, e, encrypt.LogEntry{Op: encrypt.OpVoid, Ref: seq})
	rep, err = c.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{})
	if err != nil || len(rep.LateVoided) != 1 || rep.LateVoided[seq] != v || len(rep.Violations) != 0 {
		t.Fatalf("%+v %v", rep, err)
	}
	if rep, err = c.WatchLog(ctx, "ana@x.io", c.State, WatchOptions{}); err != nil || len(rep.LateVoided) != 0 {
		t.Errorf("reported again: %+v %v", rep, err)
	}
}

// rewriteBody replaces the body of an answer.
func rewriteBody(res *http.Response, f func([]byte) []byte) *http.Response {
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	res.Body = io.NopCloser(bytes.NewReader(f(body)))
	res.ContentLength = -1
	return res
}

// A log that starts at entry 1 must start from the zero hash: an answer whose first entry follows
// something else is a log with its start withheld.
func TestWatchLogRefusesAFirstEntryThatFollowsSomething(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	forged := &Client{BaseURL: c.BaseURL, Token: c.Token, LogKeys: c.LogKeys, HTTP: pathClient("/directory/log", func(b []byte) []byte {
		var l docstore.DirectoryLog
		_ = json.Unmarshal(b, &l)
		l.Entries[0].Prev = strings.Repeat("ab", 32)
		l.Entries[0] = encrypt.SignLogEntry(e.dir.Signer, l.Entries[0])
		out, _ := json.Marshal(l)
		return out
	})}
	_, err := forged.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{})
	if !errors.Is(err, ErrLogCut) || !strings.Contains(err.Error(), "start of the log") {
		t.Fatalf("%v", err)
	}
}

// A saved filtered state's signed head must be compared with the chain when the read turns full:
// whether the full read starts right after it, overlaps it, or starts past it.
func TestWatchLogAnchorsAFilteredToFullSwitchToTheSavedHead(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	owner, _ := GenerateOwnerKey() // an enrolled address reads the whole log
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	publishOwn := func(n int) {
		for range n {
			p, _, id := ageKey(t)
			if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p, Identity: id, Owner: owner}); err != nil {
				t.Fatal(err)
			}
		}
	}
	publishOwn(2)
	st := &WatchState{}
	if _, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{}); err != nil || st.Filtered {
		t.Fatalf("%v %+v", err, st)
	}
	saved := *st // honest: the head the watcher saw (entry 3)
	publishOwn(2)
	head, err := c.Head(ctx)
	if err != nil || head.Seq != saved.Seq+2 {
		t.Fatalf("%v %+v %+v", err, head, saved)
	}

	// Legitimate switches: the saved hash is the chain's, whatever the cursor.
	for _, cursor := range []int64{saved.Seq - 1, saved.Seq, saved.Seq + 1} {
		sw := saved
		sw.Filtered, sw.Cursor = true, cursor
		if _, err := c.WatchLog(ctx, "ana@x.io", &sw, WatchOptions{}); err != nil || sw.Filtered || sw.Seq != head.Seq {
			t.Errorf("honest switch with cursor %d: %v %+v", cursor, err, sw)
		}
	}

	// A saved head that is not on the chain: the full read starts after it (cursor = Seq) or overlaps
	// it (cursor < Seq), or starts past it (cursor > Seq).
	for _, tc := range []struct{ seq, cursor int64 }{
		{head.Seq - 1, head.Seq - 1},
		{head.Seq - 1, head.Seq - 2},
		{head.Seq - 2, head.Seq - 1},
	} {
		sw := WatchState{Seq: tc.seq, Hash: "00ff", Cursor: tc.cursor, Filtered: true}
		before := sw
		if _, err := c.WatchLog(ctx, "ana@x.io", &sw, WatchOptions{}); !errors.Is(err, ErrLogCut) {
			t.Errorf("saved head %d with a wrong hash, cursor %d: %v", tc.seq, tc.cursor, err)
		}
		if sw.Seq != before.Seq || sw.Hash != before.Hash || !sw.Filtered {
			t.Errorf("the state changed on an error: %+v", sw)
		}
	}
}

// storeEntry writes a signed entry straight into the server's store (a rogue server's rewrite).
func storeEntry(t *testing.T, e *env, in encrypt.LogEntry) encrypt.LogEntry {
	t.Helper()
	in.At = time.Now().Unix()
	in.Address = e.dir.LogAddressFor("ana@x.io")
	entry := encrypt.SignLogEntry(e.dir.Signer, in)
	data, _ := json.Marshal(entry)
	if err := e.store.Save(context.Background(), fmt.Sprintf("directory-log/e/%012d", entry.Seq), data); err != nil {
		t.Fatal(err)
	}
	return entry
}

// A filtered state whose cursor is past the saved head (older runs saved it so) must not let a
// server rewrite the entries between the head and the cursor when the read turns full: the entries
// after the cursor chain among themselves, and nothing else links them to the saved head.
func TestWatchLogDetectsARewriteBetweenTheSavedHeadAndAFilteredCursor(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	owner, _ := GenerateOwnerKey() // an enrolled address reads the whole log
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		p, _, id := ageKey(t)
		if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p, Identity: id, Owner: owner}); err != nil {
			t.Fatal(err)
		}
	}
	saved := &WatchState{}
	if _, err := c.WatchLog(ctx, "ana@x.io", saved, WatchOptions{}); err != nil || saved.Filtered {
		t.Fatalf("%v %+v", err, saved)
	}
	// The server's history after the saved head is a fork: entry head+1 does not follow the head.
	f1 := storeEntry(t, e, encrypt.LogEntry{Seq: saved.Seq + 1, Prev: strings.Repeat("ab", 32), Op: encrypt.OpPublish, Fingerprint: "SHA256:fork1"})
	f2 := storeEntry(t, e, encrypt.LogEntry{Seq: saved.Seq + 2, Prev: f1.Hash, Op: encrypt.OpPublish, Fingerprint: "SHA256:fork2"})
	forgeEntry(t, e, encrypt.LogEntry{Op: encrypt.OpPublish, Fingerprint: "SHA256:fork3"})
	_ = f2

	// A state an older run saved: filtered, the cursor two past the head.
	sw := *saved
	sw.Filtered, sw.Cursor = true, saved.Seq+2
	before := sw
	if _, err := c.WatchLog(ctx, "ana@x.io", &sw, WatchOptions{}); !errors.Is(err, ErrLogCut) {
		t.Fatalf("a fork between the saved head and the cursor went unnoticed: %v", err)
	}
	if sw.Seq != before.Seq || sw.Cursor != before.Cursor || !sw.Filtered {
		t.Errorf("the state changed on an error: %+v", sw)
	}
}

// A filtered run whose scan passes the head (the log grew after the head was read) saves a cursor
// that never passes the saved head, and the next run, filtered or full, goes on from there.
func TestWatchLogFilteredCursorStaysWithinTheSavedHead(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	publishN(t, c, 2)
	grown := false
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		res, err := http.DefaultTransport.RoundTrip(r)
		if err == nil && r.URL.Path == "/directory/log/head" && !grown {
			grown = true
			publishN(t, e.client(ana), 2) // the log grows between the head and the scan
		}
		return res, err
	})}
	st := &WatchState{}
	rep, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{})
	if err != nil || !st.Filtered || !rep.Filtered {
		t.Fatalf("%v %+v", err, st)
	}
	if st.Cursor > st.Seq {
		t.Fatalf("the cursor %d is past the saved head %d", st.Cursor, st.Seq)
	}
	if len(rep.Entries) != 4 {
		t.Fatalf("the entries past the head are reported with the run that read them: %+v", rep.Entries)
	}
	// The entries past the head are read again next time (not reported twice), and the head moves on.
	rep, err = c.WatchLog(ctx, "ana@x.io", st, WatchOptions{})
	if err != nil || len(rep.Entries) != 0 || st.Seq != 4 || st.Cursor != 4 {
		t.Fatalf("%v %+v %+v", err, rep.Entries, st)
	}
	if len(st.Pending) != 4 {
		t.Fatalf("an entry read again is pending once: %d pending, want 4", len(st.Pending))
	}
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	if _, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{Owner: owner}); err != nil || st.Filtered {
		t.Fatalf("filtered to full: %v %+v", err, st)
	}
}

// growingFilteredRun saves a filtered state whose pending entries (1 to 4) include two past the
// signed head, the way the test above does.
func growingFilteredRun(t *testing.T) (*env, *Client, *WatchState) {
	t.Helper()
	e := newEnv(t)
	c := e.client(ana)
	publishN(t, c, 2)
	grown := false
	hc := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		res, err := http.DefaultTransport.RoundTrip(r)
		if err == nil && r.URL.Path == "/directory/log/head" && !grown {
			grown = true
			publishN(t, e.client(ana), 2)
		}
		return res, err
	})}
	c.HTTP = hc
	st := &WatchState{}
	if _, err := c.WatchLog(context.Background(), "ana@x.io", st, WatchOptions{}); err != nil || !st.Filtered || len(st.Pending) != 4 {
		t.Fatalf("%v %+v", err, st)
	}
	return e, c, st
}

// A re-read entry whose number is pending but whose hash is not the one remembered is the server
// telling two stories: ErrLogCut, state unchanged (the same entry again is simply deduplicated).
func TestWatchLogRefusesAPendingEntryRewrittenBetweenRuns(t *testing.T) {
	ctx := context.Background()
	e, c, st := growingFilteredRun(t)
	if st.Pending[3].Seq != 4 || st.Pending[2].Seq != 3 {
		t.Fatalf("%+v", st.Pending)
	}
	before, _ := json.Marshal(st)
	storeEntry(t, e, encrypt.LogEntry{Seq: 4, Prev: st.Pending[2].Hash, Op: encrypt.OpPublish, Fingerprint: "SHA256:rewritten"})
	if _, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{}); !errors.Is(err, ErrLogCut) {
		t.Fatalf("a pending entry rewritten: %v", err)
	}
	after, _ := json.Marshal(st)
	if string(before) != string(after) {
		t.Fatalf("the state changed on an error:\n%s\n%s", before, after)
	}
}

// The audit sees each entry once, whether it is pending or read again.
func TestWatchLogAuditsAReReadEntryOnce(t *testing.T) {
	ctx := context.Background()
	_, c, st := growingFilteredRun(t)
	st.Reported = 0 // so that the audit reports whatever it sees
	rep, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]int{}
	for _, en := range rep.Entries {
		seen[en.Seq]++
	}
	if len(rep.Entries) != 4 {
		t.Fatalf("want entries 1 to 4 once each: %+v", rep.Entries)
	}
	for seq, n := range seen {
		if n != 1 {
			t.Fatalf("entry %d audited %d times", seq, n)
		}
	}
}

// A filtered state at the head, then a full read with nothing past the head: not a cut.
func TestWatchLogFilteredToFullSwitchWithNoNewEntries(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	owner, _ := GenerateOwnerKey() // an enrolled address reads the whole log
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		p, _, id := ageKey(t)
		if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p, Identity: id, Owner: owner}); err != nil {
			t.Fatal(err)
		}
	}
	st := &WatchState{}
	if _, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{}); err != nil || st.Filtered || st.Seq != 3 {
		t.Fatalf("%v %+v", err, st)
	}
	st.Filtered, st.Cursor = true, st.Seq // cursor == Seq == the head, nothing past it
	if _, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{}); err != nil || st.Filtered || st.Seq != 3 {
		t.Fatalf("filtered to full with no new entries: %v %+v", err, st)
	}
}
