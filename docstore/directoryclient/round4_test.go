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

func TestWatchLogRefusesAnAnswerThatStartsLate(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
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
	if _, err := filt.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{}); errors.Is(err, ErrFilteredEnrolled) {
		t.Fatalf("%v", err)
	}
}

func TestWatchLogRefusesAFilteredAnswerWhenTheAuditFindsTheEnrollment(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	filt := &Client{BaseURL: c.BaseURL, Token: c.Token, LogKeys: c.LogKeys, HTTP: logRewriter(func(m map[string]any) { m["filtered"] = true })}
	var st WatchState
	if _, err := filt.WatchLog(ctx, "ana@x.io", &st, WatchOptions{Owner: owner}); !errors.Is(err, ErrFilteredEnrolled) {
		t.Fatalf("a first filtered run over an enrollment: %v", err)
	}
	if st.Seq != 0 || st.Enrolled || len(st.Pending) != 0 {
		t.Fatalf("the refused run remembered something: %+v", st)
	}
	if _, err := c.WatchLog(ctx, "ana@x.io", &WatchState{}, WatchOptions{Owner: owner}); err != nil {
		t.Fatalf("the whole log: %v", err)
	}
}

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
	next, _ := GenerateOwnerKey()
	if _, err := c.Rotate(ctx, "ana@x.io", owner, next); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Pins.Get("ana@x.io"); got != encrypt.FormatOwnerPub(next.Public().(ed25519.PublicKey)) {
		t.Errorf("after the rotation %q", got)
	}
}

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
	v.Pins.Set("ana@x.io", pub)
	if rows, _ = v.Lookup(ctx, "ana@x.io"); !rows[0].OwnerSigned || !rows[0].OwnerPinned || rows[0].OwnerFirstSeen {
		t.Errorf("%+v", rows[0])
	}
	n := e.client(bo)
	n.State = nil
	for range 2 {
		if rows, _ = n.Lookup(ctx, "ana@x.io"); !rows[0].OwnerUnpinned || rows[0].OwnerFirstSeen {
			t.Errorf("%+v", rows[0])
		}
	}
}

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

	for _, cursor := range []int64{saved.Seq - 1, saved.Seq, saved.Seq + 1} {
		sw := saved
		sw.Filtered, sw.Cursor = true, cursor
		if _, err := c.WatchLog(ctx, "ana@x.io", &sw, WatchOptions{}); err != nil || sw.Filtered || sw.Seq != head.Seq {
			t.Errorf("honest switch with cursor %d: %v %+v", cursor, err, sw)
		}
	}

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
	f1 := storeEntry(t, e, encrypt.LogEntry{Seq: saved.Seq + 1, Prev: strings.Repeat("ab", 32), Op: encrypt.OpPublish, Fingerprint: "SHA256:fork1"})
	f2 := storeEntry(t, e, encrypt.LogEntry{Seq: saved.Seq + 2, Prev: f1.Hash, Op: encrypt.OpPublish, Fingerprint: "SHA256:fork2"})
	forgeEntry(t, e, encrypt.LogEntry{Op: encrypt.OpPublish, Fingerprint: "SHA256:fork3"})
	_ = f2

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

func ownedAna(t *testing.T, c *Client) (OwnerKey, func(n int)) {
	t.Helper()
	ctx := context.Background()
	owner, _ := GenerateOwnerKey()
	if _, err := c.Enroll(ctx, "ana@x.io", owner); err != nil {
		t.Fatal(err)
	}
	return owner, func(n int) {
		for range n {
			p, _, id := ageKey(t)
			if _, err := c.Publish(ctx, PublishRequest{Email: "ana@x.io", Key: p, Identity: id, Owner: owner}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestWatchLogAuditsPendingAndNewEntriesInLogOrder(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	c := e.client(ana)
	owner, publishOwn := ownedAna(t, c)
	publishOwn(3) // entries 1 (enroll), 2, 3, 4
	first := &WatchState{}
	if _, err := c.WatchLog(ctx, "ana@x.io", first, WatchOptions{Owner: owner}); err != nil || first.Filtered || len(first.Pending) != 4 {
		t.Fatalf("%v %+v", err, first)
	}
	p := first.Pending
	st := &WatchState{
		Email: first.Email, Addresses: first.Addresses, Filtered: true,
		Seq: p[3].Seq, Hash: p[3].Hash, Cursor: 2,
		Pending: []docstore.DirectoryLogEntry{p[0], p[3]},
	}
	rep, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{Owner: owner})
	if err != nil || st.Filtered || len(rep.Violations) != 0 {
		t.Fatalf("%v %+v %+v", err, rep, st)
	}
	var got []int64
	for _, en := range rep.Entries {
		got = append(got, en.Seq)
	}
	if fmt.Sprint(got) != "[1 3 4]" {
		t.Fatalf("the entries are audited out of the log's order: %v", got)
	}
}

func TestWatchLogGrowthPastTheHeadAtTheCursor(t *testing.T) {
	ctx := context.Background()
	run := func(t *testing.T, grow func(e *env, publishOwn func(int), st *WatchState), serve func(e *env, since string, m map[string]any)) (*WatchState, error) {
		e := newEnv(t)
		c := e.client(ana)
		owner, publishOwn := ownedAna(t, c)
		publishOwn(2) // entries 1 to 3: the head is entry 3
		st := &WatchState{}
		if _, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{Owner: owner}); err != nil || st.Filtered || st.Seq != 3 {
			t.Fatalf("%v %+v", err, st)
		}
		st.Filtered, st.Cursor = true, st.Seq // the cursor sits at the head
		grown := false
		c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			res, err := http.DefaultTransport.RoundTrip(r)
			if err != nil {
				return res, err
			}
			switch {
			case r.URL.Path == "/directory/log/head" && !grown:
				grown = true
				grow(e, publishOwn, st) // the log grows after the head was signed
			case r.URL.Path == "/directory/log" && serve != nil:
				return rewriteBody(res, func(b []byte) []byte {
					var m map[string]any
					if json.Unmarshal(b, &m) != nil {
						return b
					}
					serve(e, r.URL.Query().Get("since"), m)
					out, _ := json.Marshal(m)
					return out
				}), nil
			}
			return res, err
		})}
		_, err := c.WatchLog(ctx, "ana@x.io", st, WatchOptions{Owner: owner})
		return st, err
	}
	honest := func(e *env, publishOwn func(int), st *WatchState) { publishOwn(1) }

	t.Run("honest", func(t *testing.T) {
		st, err := run(t, honest, nil)
		if err != nil || st.Filtered || st.Seq != 4 {
			t.Fatalf("%v %+v", err, st)
		}
	})
	t.Run("another entry at the head", func(t *testing.T) {
		st, err := run(t, honest, func(e *env, since string, m map[string]any) {
			if since != "2" { // the fetch of the entry at the head, not the read from it
				return
			}
			es, _ := m["entries"].([]any)
			for i, x := range es {
				raw, _ := json.Marshal(x)
				var le encrypt.LogEntry
				if json.Unmarshal(raw, &le) != nil || le.Seq != 3 {
					continue
				}
				forged := encrypt.SignLogEntry(e.dir.Signer, encrypt.LogEntry{Seq: 3, Prev: le.Prev, At: le.At, Address: le.Address, Op: encrypt.OpPublish, Fingerprint: "SHA256:forged"})
				fb, _ := json.Marshal(forged)
				var fm any
				_ = json.Unmarshal(fb, &fm)
				es[i] = fm
			}
		})
		if !errors.Is(err, ErrLogCut) || !st.Filtered || st.Seq != 3 {
			t.Fatalf("an entry at the head that is not the signed one: %v %+v", err, st)
		}
	})
	t.Run("the first entry read does not follow the saved head", func(t *testing.T) {
		fork := func(e *env, publishOwn func(int), st *WatchState) {
			storeEntry(t, e, encrypt.LogEntry{Seq: 4, Prev: strings.Repeat("ab", 32), Op: encrypt.OpPublish, Fingerprint: "SHA256:fork"})
		}
		st, err := run(t, fork, nil)
		if !errors.Is(err, ErrLogCut) || !st.Filtered || st.Seq != 3 {
			t.Fatalf("an entry that does not follow the saved head: %v %+v", err, st)
		}
	})
}

func TestParseWatchState(t *testing.T) {
	// First run: nothing saved.
	if st, err := ParseWatchState(""); err != nil || st.Seq != 0 || len(st.Pending) != 0 {
		t.Fatalf("empty: %+v %v", st, err)
	}
	// A state this version wrote round-trips.
	want := WatchState{Seq: 7, Hash: "abc", Cursor: 5, Reported: 6}
	b, _ := json.Marshal(want)
	got, err := ParseWatchState(string(b))
	if err != nil || got.Seq != 7 || got.Hash != "abc" || got.Cursor != 5 || got.Reported != 6 {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	for name, raw := range map[string]string{
		"old made format": `{"seq":7,"hash":"abc","cursor":5,"made":[5]}`,
		"truncated":       `{"seq":7,"hash":"ab`,
		"wrong type":      `{"seq":"seven"}`,
		"not an object":   `[1,2,3]`,
	} {
		st, err := ParseWatchState(raw)
		if !errors.Is(err, ErrBadWatchState) {
			t.Errorf("%s: err %v, want ErrBadWatchState", name, err)
		}
		if st.Seq != 0 || st.Hash != "" || st.Cursor != 0 {
			t.Errorf("%s: kept half a state: %+v", name, st)
		}
	}
}

func TestAcceptOwnerClearsTheChangedWarningForThatKey(t *testing.T) {
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
	// Bo sees the first owner key.
	b := e.client(bo)
	if _, err := b.Lookup(ctx, "ana@x.io"); err != nil {
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
	changed := func() (n int, pub string) {
		rows, err := b.Lookup(ctx, "ana@x.io")
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.OwnerChanged {
				n++
				pub = r.Owner.Pub
			}
		}
		return n, pub
	}
	n, pub := changed()
	if n == 0 || pub == "" {
		t.Fatalf("the rotated key is not flagged: %d", n)
	}
	if err := b.AcceptOwner("ana@x.io", pub); err != nil {
		t.Fatal(err)
	}
	if n, _ := changed(); n != 0 {
		t.Errorf("still flagged after the user accepted it: %d", n)
	}
	if err := b.AcceptOwner("nobody@x.io", pub); err == nil {
		t.Error("an address never looked up was accepted")
	}
	b.State = nil
	if err := b.AcceptOwner("ana@x.io", pub); err == nil {
		t.Error("accepted with nowhere to remember it")
	}
}

func TestParseWatchStateRefusesNullAndNegativePositions(t *testing.T) {
	for _, raw := range []string{"null", " null ", `{"seq":-1}`, `{"cursor":-5}`, `{"reported":-2}`} {
		if st, err := ParseWatchState(raw); !errors.Is(err, ErrBadWatchState) || st.Seq != 0 {
			t.Errorf("%q: %+v %v", raw, st, err)
		}
	}
	if _, err := ParseWatchState(`{"seq":3}`); err != nil {
		t.Error(err)
	}
}
