package sessionstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/elaous/gojiberry/auth"
	"github.com/elaous/gojiberry/docstore"
	"github.com/elaous/gojiberry/docstore/encrypt"
)

// conditionalOnly hides DeleteIf, so TakeState must consume with the SaveIf used marker.
type conditionalOnly struct{ docstore.ConditionalAdapter }

// plainAdapter exposes only the Adapter methods.
type plainAdapter struct{ a docstore.Adapter }

func (p plainAdapter) Save(c context.Context, id string, d []byte) error { return p.a.Save(c, id, d) }
func (p plainAdapter) Load(c context.Context, id string) (docstore.Document, error) {
	return p.a.Load(c, id)
}
func (p plainAdapter) List(c context.Context) ([]docstore.Meta, error) { return p.a.List(c) }
func (p plainAdapter) Delete(c context.Context, id string) error       { return p.a.Delete(c, id) }

// both runs a test on a store with DeleteIf and on one with only SaveIf.
func both(t *testing.T, f func(t *testing.T, wrap func(*docstore.MemoryAdapter) docstore.Adapter)) {
	t.Helper()
	for name, wrap := range map[string]func(*docstore.MemoryAdapter) docstore.Adapter{
		"DeleteIf":  func(m *docstore.MemoryAdapter) docstore.Adapter { return m },
		"used mark": func(m *docstore.MemoryAdapter) docstore.Adapter { return conditionalOnly{m} },
	} {
		wrap := wrap
		t.Run(name, func(t *testing.T) { f(t, wrap) })
	}
}

var ana = auth.User{ID: "google:1", Email: "ana@gojiberry.io", VerifiedEmail: true, Name: "Ana", Username: "ana", Picture: "https://gojiberry.io/a.png"}

func TestNeedsConditionalWrites(t *testing.T) {
	if _, err := New(plainAdapter{docstore.NewMemoryAdapter()}); !errors.Is(err, ErrNotConditional) {
		t.Fatalf("err = %v, want ErrNotConditional", err)
	}
}

func TestSessionIsSharedByReplicasAndNeverStoresTheToken(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	a, _ := New(mem)
	b, _ := New(mem)
	ctx := context.Background()
	tok, err := a.PutSession(ctx, ana, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	u, ok, err := b.GetSession(ctx, tok)
	if err != nil || !ok || u != ana {
		t.Fatalf("replica B: %+v %v %v", u, ok, err)
	}
	metas, _ := mem.List(ctx)
	if len(metas) != 1 || !strings.HasPrefix(metas[0].ID, SessionPrefix) {
		t.Fatalf("records = %+v", metas)
	}
	secret, _, _ := strings.Cut(tok, ".")
	doc, _ := mem.Load(ctx, metas[0].ID)
	if strings.Contains(metas[0].ID, secret) || strings.Contains(string(doc.Data), secret) {
		t.Fatalf("the token's secret is in the store: %s %s", metas[0].ID, doc.Data)
	}
	if docstore.IsDocumentKey(metas[0].ID) {
		t.Fatal("a session record must not be a document key")
	}
	// What a reader of the store holds (the name, the body) is not a token.
	for _, forged := range []string{metas[0].ID, strings.TrimPrefix(metas[0].ID, SessionPrefix), "", ".", "x.", ".5", secret, secret + ".-1", secret + ".01", secret + ".9999999999"} {
		if _, ok, err := b.GetSession(ctx, forged); ok || err != nil {
			t.Fatalf("forged %q: %v %v", forged, ok, err)
		}
	}
	// A real secret with another bucket is another name.
	i := strings.LastIndexByte(tok, '.')
	if _, ok, _ := b.GetSession(ctx, tok[:i]+".1"); ok {
		t.Fatal("a token with a changed bucket must not match")
	}
	var rec sessionRecord
	if err := jsonUnmarshal(doc.Data, &rec); err != nil || rec.IssuedAt.IsZero() || !rec.Expires.After(rec.IssuedAt) || rec.User != ana {
		t.Fatalf("record = %+v, %v", rec, err)
	}
}

func TestLogoutOnOneReplicaEndsTheSessionOnTheOther(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	a, _ := New(mem)
	b, _ := New(mem)
	ctx := context.Background()
	tok, _ := a.PutSession(ctx, ana, time.Hour)
	if _, ok, _ := b.GetSession(ctx, tok); !ok {
		t.Fatal("B should know the session")
	}
	if err := a.DeleteSession(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.GetSession(ctx, tok); ok {
		t.Fatal("logout on A must end the session on B at once (no cache)")
	}
	if err := b.DeleteSession(ctx, tok); err != nil {
		t.Fatalf("deleting a deleted session: %v", err)
	}
	if err := b.DeleteSession(ctx, "garbage"); err != nil {
		t.Fatalf("deleting garbage: %v", err)
	}
	if metas, _ := mem.List(ctx); len(metas) != 0 {
		t.Fatalf("left: %+v", metas)
	}
}

func TestSessionExpiresAndDoesNotSlide(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	nowFn := func() time.Time { return time.Unix(0, clock.Load()) }
	a, _ := New(mem)
	a.Now = nowFn
	b, _ := New(mem)
	b.Now = nowFn
	ctx := context.Background()
	tok, _ := a.PutSession(ctx, ana, 30*time.Minute)
	clock.Store(now.Add(29 * time.Minute).UnixNano())
	if _, ok, _ := b.GetSession(ctx, tok); !ok {
		t.Fatal("alive at 29 minutes")
	}
	// A read at 29 minutes did not extend it.
	clock.Store(now.Add(30*time.Minute + time.Second).UnixNano())
	if _, ok, _ := b.GetSession(ctx, tok); ok {
		t.Fatal("expired at 30 minutes: the stored Expires is checked on read, even before the sweep")
	}
	if _, ok, _ := a.GetSession(ctx, tok); ok {
		t.Fatal("expired on A too")
	}
}

func TestStateIsSingleUseAcrossReplicas(t *testing.T) {
	both(t, func(t *testing.T, wrap func(*docstore.MemoryAdapter) docstore.Adapter) {
		mem := docstore.NewMemoryAdapter()
		a, _ := New(wrap(mem))
		b, _ := New(wrap(mem))
		ctx := context.Background()
		st := auth.LoginState{Provider: "google", ReturnTo: "https://app.gojiberry.io/x", Verifier: "v", Nonce: "n"}
		tok, err := a.PutState(ctx, st, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		got, ok, err := b.TakeState(ctx, tok)
		if err != nil || !ok || got != st {
			t.Fatalf("replica B: %+v %v %v", got, ok, err)
		}
		for _, r := range []*Store{a, b} {
			if _, ok, err := r.TakeState(ctx, tok); ok || err != nil {
				t.Fatalf("replay: %v %v", ok, err)
			}
		}
		if metas, _ := mem.List(ctx); len(metas) != 0 {
			t.Fatalf("the record is gone after the take: %+v", metas)
		}
		if _, ok, err := a.TakeState(ctx, "no-such.1"); ok || err != nil {
			t.Fatalf("unknown: %v %v", ok, err)
		}
	})
}

func TestStateExpiredIsRefusedAndConsumed(t *testing.T) {
	both(t, func(t *testing.T, wrap func(*docstore.MemoryAdapter) docstore.Adapter) {
		mem := docstore.NewMemoryAdapter()
		now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
		var clock atomic.Int64
		clock.Store(now.UnixNano())
		a, _ := New(wrap(mem))
		a.Now = func() time.Time { return time.Unix(0, clock.Load()) }
		ctx := context.Background()
		tok, _ := a.PutState(ctx, auth.LoginState{Provider: "p"}, time.Minute)
		clock.Store(now.Add(time.Minute + time.Second).UnixNano())
		if _, ok, err := a.TakeState(ctx, tok); ok || err != nil {
			t.Fatalf("expired state taken: %v %v", ok, err)
		}
		if metas, _ := mem.List(ctx); len(metas) != 0 {
			t.Fatalf("an expired state is consumed (deleted) at once: %+v", metas)
		}
	})
}

// Of many concurrent takes, on many replicas, exactly one wins.
func TestConcurrentTakeHasExactlyOneWinner(t *testing.T) {
	both(t, func(t *testing.T, wrap func(*docstore.MemoryAdapter) docstore.Adapter) {
		mem := docstore.NewMemoryAdapter()
		var replicas []*Store
		for i := 0; i < 4; i++ {
			r, _ := New(wrap(mem))
			replicas = append(replicas, r)
		}
		ctx := context.Background()
		for round := 0; round < 25; round++ {
			tok, err := replicas[0].PutState(ctx, auth.LoginState{Provider: "p", Nonce: "n"}, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			var wins atomic.Int32
			var wg sync.WaitGroup
			start := make(chan struct{})
			for g := 0; g < 64; g++ {
				wg.Add(1)
				go func(g int) {
					defer wg.Done()
					<-start
					if _, ok, err := replicas[g%len(replicas)].TakeState(ctx, tok); err != nil {
						t.Errorf("take: %v", err)
					} else if ok {
						wins.Add(1)
					}
				}(g)
			}
			close(start)
			wg.Wait()
			if wins.Load() != 1 {
				t.Fatalf("round %d: %d winners, want exactly 1", round, wins.Load())
			}
		}
		if metas, _ := mem.List(ctx); len(metas) != 0 {
			t.Fatalf("left: %+v", metas)
		}
	})
}

func TestFloodIsBoundedAndNeverEvictsLiveRecords(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	s, _ := New(mem)
	s.MaxSessions, s.MaxStates = 5, 3
	ctx := context.Background()
	var toks []string
	for i := 0; i < 5; i++ {
		tok, err := s.PutSession(ctx, ana, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		toks = append(toks, tok)
	}
	if _, err := s.PutSession(ctx, ana, time.Hour); !errors.Is(err, auth.ErrStoreFull) {
		t.Fatalf("over the bound: %v", err)
	}
	// A flood of logins does not take the sessions' room, nor the other way round.
	var states []string
	for i := 0; i < 3; i++ {
		tok, err := s.PutState(ctx, auth.LoginState{}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		states = append(states, tok)
	}
	if _, err := s.PutState(ctx, auth.LoginState{}, time.Minute); !errors.Is(err, auth.ErrStoreFull) {
		t.Fatalf("states over the bound: %v", err)
	}
	for _, tok := range toks {
		if _, ok, _ := s.GetSession(ctx, tok); !ok {
			t.Fatal("a live session was evicted")
		}
	}
	// Room comes back with a logout and with a take.
	_ = s.DeleteSession(ctx, toks[0])
	if _, err := s.PutSession(ctx, ana, time.Hour); err != nil {
		t.Fatalf("after a logout: %v", err)
	}
	if _, ok, _ := s.TakeState(ctx, states[0]); !ok {
		t.Fatal("take")
	}
	if _, err := s.PutState(ctx, auth.LoginState{}, time.Minute); err != nil {
		t.Fatalf("after a take: %v", err)
	}
	// A failing Put (a full store) wrote nothing.
	if metas, _ := mem.List(ctx); len(metas) != 5+3 {
		t.Fatalf("%d records, want 8", len(metas))
	}
}

// A restarted replica learns the count from its first sweep.
func TestSweepSetsTheCountFromTheStore(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	a, _ := New(mem)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		_, _ = a.PutSession(ctx, ana, time.Hour)
	}
	b, _ := New(mem)
	b.MaxSessions = 4
	if _, err := b.PutSession(ctx, ana, time.Hour); err != nil {
		t.Fatalf("a fresh replica has not counted yet: %v", err)
	}
	if n, err := b.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	if _, err := b.PutSession(ctx, ana, time.Hour); !errors.Is(err, auth.ErrStoreFull) {
		t.Fatalf("after the sweep the 5 records count: %v", err)
	}
}

func TestSweepDeletesOnlyTheExpired(t *testing.T) {
	both(t, func(t *testing.T, wrap func(*docstore.MemoryAdapter) docstore.Adapter) {
		mem := docstore.NewMemoryAdapter()
		now := time.Date(2030, 1, 1, 12, 30, 0, 0, time.UTC)
		var clock atomic.Int64
		clock.Store(now.UnixNano())
		s, _ := New(wrap(mem))
		s.Now = func() time.Time { return time.Unix(0, clock.Load()) }
		ctx := context.Background()
		live, _ := s.PutSession(ctx, ana, 25*time.Hour)
		expiring, _ := s.PutSession(ctx, ana, 10*time.Minute) // dead at 12:40, its hour ends at 13:00
		dead, _ := s.PutSession(ctx, ana, time.Second)
		deadHour := sessionKind.bucketOf(now.Add(time.Second))
		liveState, _ := s.PutState(ctx, auth.LoginState{Provider: "p"}, 9*time.Minute)
		deadState, _ := s.PutState(ctx, auth.LoginState{Provider: "p"}, time.Second)
		// A used marker whose delete failed, and records that are not ours.
		marker, _ := jsonMarshal(stateRecord{V: 1, Used: true, Expires: now})
		_ = mem.Save(ctx, LoginStatePrefix+"1/abc", marker)
		_ = mem.Save(ctx, SessionPrefix+"junk", []byte("x"))
		_ = mem.Save(ctx, "doc1", []byte("a document"))

		// 12:45: the states (dead at 12:30:01 and 12:39) are past their minute and go, with the old
		// marker; the sessions have expired (12:30:01, 12:40) but their hour has not ended: kept.
		clock.Store(now.Add(15 * time.Minute).UnixNano())
		if n, err := s.Sweep(ctx); err != nil || n != 3 {
			t.Fatalf("sweep at 12:45 = %d, %v (want the old marker and two states)", n, err)
		}
		if _, ok, _ := s.GetSession(ctx, dead); ok {
			t.Fatal("the expired session is refused on read even though the sweep has not come to it")
		}
		// 13:01:30: the hour of 12:40 and 12:30:01 is over (plus the grace).
		clock.Store(time.Date(2030, 1, 1, 13, 1, 30, 0, time.UTC).UnixNano())
		if deadHour == 0 {
			t.Fatal("test setup")
		}
		n, err := s.Sweep(ctx)
		if err != nil || n != 2 { // dead, expiring
			t.Fatalf("sweep at 13:01 = %d, %v (want the two dead sessions)", n, err)
		}
		if _, ok, _ := s.GetSession(ctx, live); !ok {
			t.Fatal("the live session must survive the sweep")
		}
		_, _, _ = expiring, liveState, deadState
		left, _ := mem.List(ctx)
		ids := map[string]bool{}
		for _, m := range left {
			ids[m.ID] = true
		}
		if len(ids) != 3 || !ids["doc1"] || !ids[SessionPrefix+"junk"] {
			t.Fatalf("left = %v: the live session, a document and a foreign name must stay", ids)
		}
	})
}

// A sweep that runs while a session lives in the same hour as dead ones keeps it.
func TestSweepKeepsALiveSessionInTheBucketOfADeadOne(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	s, _ := New(mem)
	s.Now = func() time.Time { return time.Unix(0, clock.Load()) }
	ctx := context.Background()
	dead, _ := s.PutSession(ctx, ana, time.Minute)
	live, _ := s.PutSession(ctx, ana, 59*time.Minute) // same hour bucket (13:00)
	clock.Store(now.Add(30 * time.Minute).UnixNano())
	if n, _ := s.Sweep(ctx); n != 0 {
		t.Fatalf("deleted %d before the bucket ended", n)
	}
	if _, ok, _ := s.GetSession(ctx, dead); ok {
		t.Fatal("dead")
	}
	if _, ok, _ := s.GetSession(ctx, live); !ok {
		t.Fatal("live")
	}
}

func TestEncryptedStoreKeepsRecordsSecretAndWorks(t *testing.T) {
	id, _ := age.GenerateX25519Identity()
	both(t, func(t *testing.T, wrap func(*docstore.MemoryAdapter) docstore.Adapter) {
		mem := docstore.NewMemoryAdapter()
		mk := func() *Store {
			enc := encrypt.New(wrap(mem), encrypt.Static{To: []age.Recipient{id.Recipient()}, Keys: []age.Identity{id}})
			s, err := New(enc)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
		a, b := mk(), mk()
		ctx := context.Background()
		tok, err := a.PutSession(ctx, ana, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if u, ok, err := b.GetSession(ctx, tok); err != nil || !ok || u != ana {
			t.Fatalf("B: %+v %v %v", u, ok, err)
		}
		st, err := a.PutState(ctx, auth.LoginState{Provider: "google", Verifier: "pkce-secret", Nonce: "n"}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		metas, _ := mem.List(ctx)
		for _, m := range metas {
			d, _ := mem.Load(ctx, m.ID)
			if !encrypt.IsEncrypted(d.Data) || strings.Contains(string(d.Data), "ana@gojiberry.io") || strings.Contains(string(d.Data), "pkce-secret") {
				t.Fatalf("%s is readable in the store: %q", m.ID, d.Data)
			}
		}
		// Single use through the encrypting adapter (it has no DeleteIf for an encrypted id).
		var wins atomic.Int32
		var wg sync.WaitGroup
		for g := 0; g < 16; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				r := []*Store{a, b}[g%2]
				if _, ok, err := r.TakeState(ctx, st); err == nil && ok {
					wins.Add(1)
				}
			}(g)
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("%d winners", wins.Load())
		}
		if err := a.DeleteSession(ctx, tok); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := b.GetSession(ctx, tok); ok {
			t.Fatal("logout through the encrypting adapter")
		}
		if n, err := a.Sweep(ctx); err != nil || n != 0 {
			t.Fatalf("sweep = %d, %v", n, err)
		}
		// The records are not in DefaultSkip: they are encrypted, and a repair pass does not choke.
		for _, k := range []string{SessionPrefix + "5/x", LoginStatePrefix + "5/x"} {
			if encrypt.DefaultSkip(k) {
				t.Fatalf("%s must not be in DefaultSkip: it holds names and e-mail addresses", k)
			}
		}
	})
}

// failing makes every read fail.
type failing struct {
	docstore.ConditionalAdapter
	fail atomic.Bool
}

func (f *failing) Load(ctx context.Context, id string) (docstore.Document, error) {
	if f.fail.Load() {
		return docstore.Document{}, errors.New("store down")
	}
	return f.ConditionalAdapter.Load(ctx, id)
}
func (f *failing) Delete(ctx context.Context, id string) error {
	if f.fail.Load() {
		return errors.New("store down")
	}
	return f.ConditionalAdapter.Delete(ctx, id)
}

func TestStoreErrorsAreErrorsNotAnswers(t *testing.T) {
	f := &failing{ConditionalAdapter: docstore.NewMemoryAdapter()}
	s, _ := New(f)
	ctx := context.Background()
	tok, _ := s.PutSession(ctx, ana, time.Hour)
	st, _ := s.PutState(ctx, auth.LoginState{}, time.Minute)
	f.fail.Store(true)
	if _, ok, err := s.GetSession(ctx, tok); ok || err == nil {
		t.Fatalf("GetSession: %v %v", ok, err)
	}
	if err := s.DeleteSession(ctx, tok); err == nil {
		t.Fatal("a failed delete must be reported: the session is still valid")
	}
	if _, ok, err := s.TakeState(ctx, st); ok || err == nil {
		t.Fatalf("TakeState: %v %v", ok, err)
	}
	f.fail.Store(false)
	if _, ok, _ := s.TakeState(ctx, st); !ok {
		t.Fatal("a failed read must not consume the state")
	}
}

func TestParseMode(t *testing.T) {
	for in, want := range map[string]string{"": "memory", " Memory ": "memory", "store": "store", "STORE-REQUIRED": "store-required"} {
		if got, err := ParseMode(in); err != nil || got != want {
			t.Errorf("ParseMode(%q) = %q, %v", in, got, err)
		}
	}
	if _, err := ParseMode("redis"); err == nil {
		t.Error("redis is not a mode")
	}
}
