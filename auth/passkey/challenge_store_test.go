package passkey

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

// conditionalOnly hides DeleteIf, so Take must consume with the SaveIf used marker.
type conditionalOnly struct{ docstore.ConditionalAdapter }

func newShared(t *testing.T) (*docstore.MemoryAdapter, *StoreChallenges) {
	t.Helper()
	mem := docstore.NewMemoryAdapter()
	sc, err := NewStoreChallenges(mem, 0)
	if err != nil {
		t.Fatal(err)
	}
	return mem, sc
}

func TestStoreChallengesNeedConditionalWrites(t *testing.T) {
	if _, err := NewStoreChallenges(plainAdapter{docstore.NewMemoryAdapter()}, 0); !errors.Is(err, ErrNotConditional) {
		t.Fatalf("err = %v, want ErrNotConditional", err)
	}
}

// plainAdapter exposes only the Adapter methods.
type plainAdapter struct{ a docstore.Adapter }

func (p plainAdapter) Save(c context.Context, id string, d []byte) error { return p.a.Save(c, id, d) }
func (p plainAdapter) Load(c context.Context, id string) (docstore.Document, error) {
	return p.a.Load(c, id)
}
func (p plainAdapter) List(c context.Context) ([]docstore.Meta, error) { return p.a.List(c) }
func (p plainAdapter) Delete(c context.Context, id string) error       { return p.a.Delete(c, id) }

func TestStoreChallengesPutTakeIsSingleUseAndKeepsTheCeremony(t *testing.T) {
	for name, wrap := range map[string]func(*docstore.MemoryAdapter) docstore.Adapter{
		"DeleteIf":  func(m *docstore.MemoryAdapter) docstore.Adapter { return m },
		"used mark": func(m *docstore.MemoryAdapter) docstore.Adapter { return conditionalOnly{m} },
	} {
		t.Run(name, func(t *testing.T) {
			mem := docstore.NewMemoryAdapter()
			sc, _ := NewStoreChallenges(wrap(mem), 0)
			ctx := context.Background()
			tok, err := sc.Put(ctx, Challenge{Account: "acc", Email: "a@b.c", Create: true, Expires: time.Now().Add(time.Minute)})
			if err != nil || len(tok) < 32 {
				t.Fatalf("Put = %q, %v", tok, err)
			}
			metas, _ := mem.List(ctx)
			if len(metas) != 1 || !strings.HasPrefix(metas[0].ID, ChallengePrefix) || strings.Contains(metas[0].ID, tok) {
				t.Fatalf("records = %+v: under the prefix, and the token is not in the name", metas)
			}
			if docstore.IsDocumentKey(metas[0].ID) {
				t.Fatal("a challenge record must not be a document key (expiry, encryption and the API leave it alone)")
			}
			c, ok, err := sc.Take(ctx, tok)
			if err != nil || !ok || c.Account != "acc" || c.Email != "a@b.c" || !c.Create {
				t.Fatalf("Take = %+v, %v, %v", c, ok, err)
			}
			if _, ok, _ := sc.Take(ctx, tok); ok {
				t.Fatal("a challenge is good once")
			}
			if metas, _ := mem.List(ctx); len(metas) != 0 {
				t.Fatalf("the record is gone after Take: %+v", metas)
			}
			if _, ok, err := sc.Take(ctx, "no-such-token"); ok || err != nil {
				t.Fatalf("unknown token: %v %v", ok, err)
			}
		})
	}
}

func TestStoreChallengesExpire(t *testing.T) {
	_, sc := newShared(t)
	now := time.Now()
	sc.Now = func() time.Time { return now }
	ctx := context.Background()
	tok, _ := sc.Put(ctx, Challenge{Expires: now.Add(time.Minute)})
	now = now.Add(time.Minute + time.Second)
	if _, ok, err := sc.Take(ctx, tok); ok || err != nil {
		t.Fatalf("an expired challenge is refused: %v %v", ok, err)
	}
	if n := countRecords(t, sc); n != 0 {
		t.Fatalf("an expired record taken is deleted, %d left", n)
	}
}

func countRecords(t *testing.T, sc *StoreChallenges) int {
	t.Helper()
	m, err := docstore.ListPrefix(context.Background(), sc.Store, ChallengePrefix)
	if err != nil {
		t.Fatal(err)
	}
	return len(m)
}

func TestStoreChallengesConcurrentTakeHasExactlyOneWinner(t *testing.T) {
	for name, wrap := range map[string]func(*docstore.MemoryAdapter) docstore.Adapter{
		"DeleteIf":  func(m *docstore.MemoryAdapter) docstore.Adapter { return m },
		"used mark": func(m *docstore.MemoryAdapter) docstore.Adapter { return conditionalOnly{m} },
	} {
		t.Run(name, func(t *testing.T) {
			mem := docstore.NewMemoryAdapter()
			store := wrap(mem)
			ctx := context.Background()
			// Several "replicas": separate StoreChallenges over one store.
			const replicas, perReplica, rounds = 4, 16, 25
			reps := make([]*StoreChallenges, replicas)
			for i := range reps {
				reps[i], _ = NewStoreChallenges(store, 0)
			}
			for r := 0; r < rounds; r++ {
				tok, err := reps[0].Put(ctx, Challenge{Expires: time.Now().Add(time.Minute)})
				if err != nil {
					t.Fatal(err)
				}
				var wins atomic.Int32
				var wg sync.WaitGroup
				start := make(chan struct{})
				for i := 0; i < replicas*perReplica; i++ {
					wg.Add(1)
					go func(sc *StoreChallenges) {
						defer wg.Done()
						<-start
						if _, ok, err := sc.Take(ctx, tok); err == nil && ok {
							wins.Add(1)
						}
					}(reps[i%replicas])
				}
				close(start)
				wg.Wait()
				if w := wins.Load(); w != 1 {
					t.Fatalf("round %d: %d winners, want exactly 1", r, w)
				}
			}
		})
	}
}

func TestStoreChallengesAreCappedAndSwept(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	sc, _ := NewStoreChallenges(mem, 3)
	now := time.Now()
	sc.Now = func() time.Time { return now }
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := sc.Put(ctx, Challenge{Expires: now.Add(time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sc.Put(ctx, Challenge{Expires: now.Add(time.Minute)}); !errors.Is(err, ErrBusy) {
		t.Fatalf("full of live challenges: %v, want ErrBusy (a live one is never evicted)", err)
	}
	// Expired (past the grace) ones are dropped to make room, at most once a second.
	now = now.Add(3 * time.Minute)
	if _, err := sc.Put(ctx, Challenge{Expires: now.Add(time.Minute)}); err != nil {
		t.Fatalf("after expiry there is room: %v", err)
	}
	if n := countRecords(t, sc); n != 1 {
		t.Fatalf("%d records, the expired three are swept", n)
	}
	// A second replica that has not listed yet trusts its own count (the documented overshoot),
	// and learns the store's at its next sweep.
	other, _ := NewStoreChallenges(mem, 1)
	other.Now = sc.Now
	if _, err := other.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Put(ctx, Challenge{Expires: now.Add(time.Minute)}); !errors.Is(err, ErrBusy) {
		t.Fatalf("a replica at the cap by the store's count: %v, want ErrBusy", err)
	}
}

func TestSweepDeletesUsedMarkersAndUnreadableOldRecords(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	sc, _ := NewStoreChallenges(conditionalOnly{mem}, 0)
	ctx := context.Background()
	_ = mem.Save(ctx, ChallengePrefix+"used", []byte(`{"used":true,"expires":"2999-01-01T00:00:00Z"}`))
	_ = mem.Save(ctx, ChallengePrefix+"junk", []byte(`not json`))
	_ = mem.Save(ctx, ChallengePrefix+"live", []byte(`{"expires":"2999-01-01T00:00:00Z"}`))
	n, err := sc.Sweep(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v: the used marker goes; fresh junk waits an hour; the live one stays", n, err)
	}
	sc.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if n, _ := sc.Sweep(ctx); n != 1 {
		t.Fatalf("old junk is swept: %d", n)
	}
	if got := countRecords(t, sc); got != 1 {
		t.Fatalf("%d records left, want the live one", got)
	}
}

func TestParseChallengeMode(t *testing.T) {
	for in, want := range map[string]string{"": "memory", "memory": "memory", " Store ": "store"} {
		if got, err := ParseChallengeMode(in); err != nil || got != want {
			t.Errorf("%q = %q, %v", in, got, err)
		}
	}
	if _, err := ParseChallengeMode("redis"); err == nil {
		t.Error("an unknown mode is an error")
	}
}

// Two Services (two replicas) sharing one store of accounts and one of ceremonies.
func twoReplicas(t *testing.T, ttl time.Duration) (a, b *rig) {
	t.Helper()
	mem := docstore.NewMemoryAdapter()
	accounts := &AdapterStore{Adapter: mem}
	mk := func() *rig {
		ch, err := NewStoreChallenges(mem, 0)
		if err != nil {
			t.Fatal(err)
		}
		return newRigWith(t, accounts, Config{Challenges: ch, ChallengeTTL: ttl})
	}
	a, b = mk(), mk()
	b.user = a.user // one person, one authenticator
	return a, b
}

func (r *rig) begin(path string) (string, map[string]any) {
	r.t.Helper()
	code, begin := r.do("POST", path, "", nil)
	if code != 200 {
		r.t.Fatalf("begin: %d %v", code, begin)
	}
	return begin["token"].(string), begin
}

func TestReplicasShareCeremoniesBeginOnAFinishOnB(t *testing.T) {
	a, b := twoReplicas(t, 0)
	a.register("ana@opendocs.io", "") // the account lives in the shared store
	// Login: begin on A, finish on B.
	tok, begin := a.begin("/auth/passkey/login/begin")
	answer, err := a.user.Get(asJSON(t, map[string]any{"publicKey": begin["options"].(map[string]any)["publicKey"]}), origin)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := b.do("POST", "/auth/passkey/login/finish?token="+tok, "", answer); code != 200 || out["auth_token"] == "" {
		t.Fatalf("finish on the other replica: %d %v", code, out)
	}
	// Replay: the same answer on A after B consumed the challenge.
	if code, _ := a.do("POST", "/auth/passkey/login/finish?token="+tok, "", answer); code != 410 {
		t.Fatalf("replay on A = %d, want 410", code)
	}
	if code, _ := b.do("POST", "/auth/passkey/login/finish?token="+tok, "", answer); code != 410 {
		t.Fatalf("replay on B = %d, want 410", code)
	}

	// Registration too: begin on B, finish on A.
	code, rb := b.do("POST", "/auth/passkey/register/begin", "", asJSON(t, map[string]string{"email": "bo@opendocs.io"}))
	if code != 200 {
		t.Fatalf("register begin: %d %v", code, rb)
	}
	reg, err := b.user.Create(asJSON(t, map[string]any{"publicKey": rb["options"].(map[string]any)["publicKey"]}), origin)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := a.do("POST", "/auth/passkey/register/finish?token="+rb["token"].(string), "", reg); code != 200 {
		t.Fatalf("register finish on the other replica: %d %v", code, out)
	}
}

func TestReplicasRefuseAnExpiredCeremony(t *testing.T) {
	a, b := twoReplicas(t, 30*time.Millisecond)
	a.register("ana@opendocs.io", "")
	tok, begin := a.begin("/auth/passkey/login/begin")
	answer, _ := a.user.Get(asJSON(t, map[string]any{"publicKey": begin["options"].(map[string]any)["publicKey"]}), origin)
	time.Sleep(80 * time.Millisecond)
	if code, _ := b.do("POST", "/auth/passkey/login/finish?token="+tok, "", answer); code != 410 {
		t.Fatalf("expired = %d, want 410", code)
	}
}

func TestWithoutASharedStoreAnotherReplicaDoesNotKnowTheCeremony(t *testing.T) {
	// The failure the shared store removes: per-process challenges.
	mem := docstore.NewMemoryAdapter()
	accounts := &AdapterStore{Adapter: mem}
	a, b := newRig(t, accounts), newRig(t, accounts)
	b.user = a.user
	a.register("ana@opendocs.io", "")
	tok, begin := a.begin("/auth/passkey/login/begin")
	answer, _ := a.user.Get(asJSON(t, map[string]any{"publicKey": begin["options"].(map[string]any)["publicKey"]}), origin)
	if code, _ := b.do("POST", "/auth/passkey/login/finish?token="+tok, "", answer); code != 410 {
		t.Fatalf("per-process challenges: other replica = %d, want 410", code)
	}
}
