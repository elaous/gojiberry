package passkey

import (
	"context"
	"errors"
	"strconv"
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
	// Put never sweeps: it stays busy until the sweep runs (the server runs it every minute).
	// Expired ones are only dropped once they are past the grace and Life.
	now = now.Add(10 * time.Minute)
	if _, err := sc.Put(ctx, Challenge{Expires: now.Add(time.Minute)}); !errors.Is(err, ErrBusy) {
		t.Fatalf("Put at the cap does not sweep inline: %v, want ErrBusy", err)
	}
	if n, err := sc.Sweep(ctx); err != nil || n != 3 {
		t.Fatalf("Sweep = %d, %v, want the three expired gone", n, err)
	}
	if _, err := sc.Put(ctx, Challenge{Expires: now.Add(time.Minute)}); err != nil {
		t.Fatalf("after the sweep there is room: %v", err)
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

// counting counts every call that reaches the store.
type counting struct {
	*docstore.MemoryAdapter
	loads, deletes, deleteIfs, lists, saveIfs atomic.Int64
}

func (c *counting) Load(ctx context.Context, id string) (docstore.Document, error) {
	c.loads.Add(1)
	return c.MemoryAdapter.Load(ctx, id)
}
func (c *counting) Delete(ctx context.Context, id string) error {
	c.deletes.Add(1)
	return c.MemoryAdapter.Delete(ctx, id)
}
func (c *counting) DeleteIf(ctx context.Context, id, prev string) error {
	c.deleteIfs.Add(1)
	return c.MemoryAdapter.DeleteIf(ctx, id, prev)
}
func (c *counting) SaveIf(ctx context.Context, id string, data []byte, prev string) error {
	c.saveIfs.Add(1)
	return c.MemoryAdapter.SaveIf(ctx, id, data, prev)
}
func (c *counting) List(ctx context.Context) ([]docstore.Meta, error) {
	c.lists.Add(1)
	return c.MemoryAdapter.List(ctx)
}
func (c *counting) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	c.lists.Add(1)
	return c.MemoryAdapter.ListPrefix(ctx, prefix)
}

func (c *counting) calls() int64 {
	return c.loads.Load() + c.deletes.Load() + c.deleteIfs.Load() + c.lists.Load() + c.saveIfs.Load()
}

func TestTokenCarriesTheBucketAndNamesTheRecord(t *testing.T) {
	mem, sc := newShared(t)
	now := time.Unix(1_700_000_000, 0)
	sc.Now = func() time.Time { return now }
	exp := now.Add(5 * time.Minute)
	tok, err := sc.Put(context.Background(), Challenge{Expires: exp})
	if err != nil {
		t.Fatal(err)
	}
	i := strings.IndexByte(tok, '.')
	if i < 0 || tok[i+1:] != strconv.FormatInt(bucketOf(exp), 10) {
		t.Fatalf("token %q does not end in the expiry minute %d", tok, bucketOf(exp))
	}
	metas, _ := mem.List(context.Background())
	if len(metas) != 1 || !strings.HasPrefix(metas[0].ID, ChallengePrefix+tok[i+1:]+"/") {
		t.Fatalf("record names = %+v, want %s<minute>/<hash>", metas, ChallengePrefix)
	}
	// The bucket end is never before the expiry.
	if b, ok := idBucket(metas[0].ID); !ok || bucketEnd(b).Before(exp) {
		t.Fatalf("bucket %d ends before %v", b, exp)
	}
}

// A token that is not exactly one of ours never reaches the store.
func TestMalformedTokensNeverReachTheStore(t *testing.T) {
	c := &counting{MemoryAdapter: docstore.NewMemoryAdapter()}
	sc, _ := NewStoreChallenges(c, 0)
	ctx := context.Background()
	bad := []string{
		"", ".", "a.", ".5", "a.-1", "a.01", "a.+5", "a.99999999999999999999", "a.1/../x", "a.1/x", "a.5.6",
		strings.Repeat("x", 127) + ".5", // 129 bytes
		"a.\u0663",                      // an Arabic-Indic digit
		"a.\uff15",                      // a full-width digit
		"a.5\u0663", "a. 5", "a.5 ", "a.0x5", "a.5e1", "a.1_0",
		"a.9223372036854775807", // fits an int64, beyond any real time
		"no-such-token", "abc",  // the old format: no bucket
	}
	for _, tok := range bad {
		if c, ok, err := sc.Take(ctx, tok); ok || err != nil || c.Email != "" {
			t.Errorf("Take(%q) = %+v %v %v", tok, c, ok, err)
		}
	}
	if n := c.calls(); n != 0 {
		t.Fatalf("malformed tokens made %d store calls", n)
	}
	// A well-formed token does reach the store (the control of the test).
	_, _, _ = sc.Take(ctx, "abc.5")
	if c.loads.Load() != 1 {
		t.Fatalf("a well-formed token must be looked up: %d loads", c.loads.Load())
	}
}

// lenientDelete is a store whose plain Delete of a missing record is not an error (as on S3), so
// only a conditional step can tell winner from loser.
type lenientDelete struct{ *docstore.MemoryAdapter }

func (l lenientDelete) Delete(ctx context.Context, id string) error {
	if err := l.MemoryAdapter.Delete(ctx, id); err != nil && !errors.Is(err, docstore.ErrNotFound) {
		return err
	}
	return nil
}

// lenientCAS exposes the ConditionalAdapter methods only (no DeleteIf), with the lenient Delete.
type lenientCAS struct {
	docstore.ConditionalAdapter
	l lenientDelete
}

func (c lenientCAS) Delete(ctx context.Context, id string) error { return c.l.Delete(ctx, id) }

func TestConcurrentTakeHasOneWinnerOnAStoreWhosePlainDeleteNeverFails(t *testing.T) {
	for _, mode := range []string{"DeleteIf", "used mark"} {
		t.Run(mode, func(t *testing.T) {
			l := lenientDelete{docstore.NewMemoryAdapter()}
			var store docstore.Adapter = l
			if mode == "used mark" {
				store = lenientCAS{ConditionalAdapter: l.MemoryAdapter, l: l}
			}
			ctx := context.Background()
			const replicas, perReplica, rounds = 4, 8, 30
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

// The sweep deletes by name, only past the bucket and the grace, and reads nothing.
func TestSweepDeletesOnlyExpiredByNameWithoutLoads(t *testing.T) {
	for name, wrap := range map[string]func(*counting) docstore.Adapter{
		"DeleteIf":  func(c *counting) docstore.Adapter { return c },
		"used mark": func(c *counting) docstore.Adapter { return conditionalOnly{c} },
	} {
		t.Run(name, func(t *testing.T) {
			c := &counting{MemoryAdapter: docstore.NewMemoryAdapter()}
			sc, _ := NewStoreChallenges(wrap(c), 0)
			now := time.Unix(1_700_000_000, 0)
			sc.Now = func() time.Time { return now }
			ctx := context.Background()
			var dead, live []string
			for i := 0; i < 5; i++ {
				tok, _ := sc.Put(ctx, Challenge{Expires: now.Add(time.Minute)})
				dead = append(dead, tok)
				tok, _ = sc.Put(ctx, Challenge{Expires: now.Add(20 * time.Minute)})
				live = append(live, tok)
			}
			// Records of the earlier layout and foreign names are left alone.
			old := ChallengePrefix + strings.Repeat("a", 64)
			_ = c.MemoryAdapter.Save(ctx, old, []byte(`{"expires":"2000-01-01T00:00:00Z"}`))
			_ = c.MemoryAdapter.Save(ctx, ChallengePrefix+"x/y", []byte(`junk`))
			now = now.Add(5 * time.Minute) // past the dead bucket and the grace, before the live ones
			c.loads.Store(0)
			c.saveIfs.Store(0)
			c.deletes.Store(0)
			n, err := sc.Sweep(ctx)
			if err != nil || n != len(dead) {
				t.Fatalf("Sweep = %d, %v, want %d", n, err, len(dead))
			}
			if c.loads.Load() != 0 || c.saveIfs.Load() != 0 || c.deleteIfs.Load() != 0 || c.deletes.Load() != int64(len(dead)) {
				t.Fatalf("sweep must only delete the %d dead by name: loads %d saveIfs %d deleteIfs %d deletes %d",
					len(dead), c.loads.Load(), c.saveIfs.Load(), c.deleteIfs.Load(), c.deletes.Load())
			}
			for _, tok := range live {
				if _, err := c.MemoryAdapter.Load(ctx, challengeID(tok)); err != nil {
					t.Fatalf("a live record was swept: %v", err)
				}
			}
			for _, tok := range dead {
				if _, err := c.MemoryAdapter.Load(ctx, challengeID(tok)); !errors.Is(err, docstore.ErrNotFound) {
					t.Fatalf("an expired record is still there: %v", err)
				}
			}
			if _, err := c.MemoryAdapter.Load(ctx, old); err != nil {
				t.Fatalf("an old-format record must be left alone: %v", err)
			}
			if got := sc.approx.Load(); got != int64(len(live)) {
				t.Fatalf("count = %d, want the %d live ones (foreign names are not counted)", got, len(live))
			}
			// Live ones are still good.
			if _, ok, _ := sc.Take(ctx, live[0]); !ok {
				t.Fatal("a live ceremony survives the sweep")
			}
		})
	}
}

// A used marker (a Take whose final delete failed) is swept by its own bucket, with no Load.
func TestSweepDeletesAUsedMarkerByItsBucket(t *testing.T) {
	c := &counting{MemoryAdapter: docstore.NewMemoryAdapter()}
	failing := &failDelete{counting: c, fail: true}
	sc, _ := NewStoreChallenges(conditionalOnly{failing}, 0)
	now := time.Unix(1_700_000_000, 0)
	sc.Now = func() time.Time { return now }
	ctx := context.Background()
	tok, _ := sc.Put(ctx, Challenge{Expires: now.Add(time.Minute)})
	if _, ok, err := sc.Take(ctx, tok); !ok || err != nil {
		t.Fatalf("Take = %v %v", ok, err)
	}
	if _, ok, _ := sc.Take(ctx, tok); ok {
		t.Fatal("the marker refuses a replay")
	}
	if got := countRecords(t, sc); got != 1 {
		t.Fatalf("the marker should be left behind, %d records", got)
	}
	now = now.Add(5 * time.Minute)
	failing.fail = false
	c.loads.Store(0)
	if n, err := sc.Sweep(ctx); err != nil || n != 1 || c.loads.Load() != 0 {
		t.Fatalf("Sweep = %d, %v with %d loads, want the marker gone with none", n, err, c.loads.Load())
	}
}

// failDelete makes plain Delete fail while fail is true (a Take's cleanup that did not happen).
type failDelete struct {
	*counting
	fail bool
}

func (f *failDelete) Delete(ctx context.Context, id string) error {
	if f.fail {
		return errors.New("down")
	}
	return f.counting.Delete(ctx, id)
}

// Put at the cap costs nothing and evicts nothing; the sweep is a listing and no Load.
func TestFullStoreCostsNoCallsInPutAndNoLoadsInSweep(t *testing.T) {
	c := &counting{MemoryAdapter: docstore.NewMemoryAdapter()}
	const total = 1000
	sc, _ := NewStoreChallenges(c, total)
	ctx := context.Background()
	var first string
	for i := 0; i < total; i++ {
		tok, err := sc.Put(ctx, Challenge{Expires: time.Now().Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = tok
		}
	}
	before := c.calls()
	for i := 0; i < 50; i++ {
		if _, err := sc.Put(ctx, Challenge{Expires: time.Now().Add(time.Minute)}); !errors.Is(err, ErrBusy) {
			t.Fatalf("Put at the cap = %v, want ErrBusy", err)
		}
	}
	if n := c.calls() - before; n != 0 {
		t.Fatalf("%d store calls for Puts at the cap, want 0 (no sweep, no Load)", n)
	}
	if n, err := sc.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("Sweep = %d, %v", n, err)
	}
	if c.loads.Load() != 0 {
		t.Fatalf("%d Loads in a sweep of %d live records, want none", c.loads.Load(), total)
	}
	if got := countRecords(t, sc); got != total {
		t.Fatalf("%d records, want %d: a live challenge is never evicted", got, total)
	}
	if _, ok, _ := sc.Take(ctx, first); !ok {
		t.Fatal("the first live ceremony is still good at the cap")
	}
	// A Take made room.
	if _, err := sc.Put(ctx, Challenge{Expires: time.Now().Add(time.Minute)}); err != nil {
		t.Fatalf("after a Take there is room: %v", err)
	}
}

// The cap across replicas: replicas that have just listed each accept up to the cap on their own
// count, so the store may exceed it, by no more than the writes of one refresh interval; the next
// listing stops the growth on every replica. With the per-client limiter in front, the overshoot
// is at most limiterWrites (a rate times clients times the interval).
func TestCapOvershootAcrossReplicasIsBoundedByTheWritesOfOneInterval(t *testing.T) {
	const cap, replicas, prefill, perInterval = 10, 3, 8, 4 // perInterval: what the limiters let one replica write
	mem := docstore.NewMemoryAdapter()
	now := time.Unix(1_700_000_000, 0)
	reps := make([]*StoreChallenges, replicas)
	for i := range reps {
		reps[i], _ = NewStoreChallenges(mem, cap)
		reps[i].Now = func() time.Time { return now }
	}
	ctx := context.Background()
	for i := 0; i < prefill; i++ {
		if _, err := reps[0].Put(ctx, Challenge{Expires: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range reps { // the sweep: every replica learns the store's count
		if _, err := r.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	// One interval: each replica writes until busy or until its limiters stop it.
	for _, r := range reps {
		for i := 0; i < perInterval; i++ {
			if _, err := r.Put(ctx, Challenge{Expires: now.Add(time.Hour)}); err != nil {
				if !errors.Is(err, ErrBusy) {
					t.Fatal(err)
				}
				break
			}
		}
	}
	got := countRecords(t, reps[0])
	if got <= cap {
		t.Fatalf("%d records: the test must show the overshoot (replicas do not see each other until they list)", got)
	}
	if got > cap+replicas*perInterval || got > replicas*cap {
		t.Fatalf("%d records, over the documented bound cap+replicas x writes = %d (and replicas x cap = %d)", got, cap+replicas*perInterval, replicas*cap)
	}
	// The next listing: everyone sees the true count and refuses, so growth stops.
	for _, r := range reps {
		if _, err := r.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Put(ctx, Challenge{Expires: now.Add(time.Hour)}); !errors.Is(err, ErrBusy) {
			t.Fatalf("after the listing: %v, want ErrBusy", err)
		}
	}
	if after := countRecords(t, reps[0]); after != got {
		t.Fatalf("grew from %d to %d after the listing", got, after)
	}
}

// The real bound is the per-client limiter on the Begin routes: a client flooding Begin on any
// replica is stopped at its budget, so the store gets at most budget records from it.
func TestPerClientLimiterBoundsWhatOneClientWritesAcrossReplicas(t *testing.T) {
	mem := docstore.NewMemoryAdapter()
	shared := NewRateLimiter(1, 5) // 5 at once, 1 a minute; one budget, as with GOJIBERRY_DOCSTORE_LIMITER=store
	mk := func() *rig {
		ch, _ := NewStoreChallenges(mem, 0)
		return newRigWith(t, &AdapterStore{Adapter: mem}, Config{Challenges: ch, Limiter: shared})
	}
	a, b := mk(), mk()
	ok := 0
	for i := 0; i < 20; i++ {
		r := a
		if i%2 == 1 {
			r = b
		}
		if code, _ := r.do("POST", "/auth/passkey/login/begin", "", nil); code == 200 {
			ok++
		}
	}
	if ok != 5 || countRecords(t, &StoreChallenges{Store: mem}) != 5 {
		t.Fatalf("%d begins accepted, %d records: want the burst of 5", ok, countRecords(t, &StoreChallenges{Store: mem}))
	}
}

// brokenChallenges fails Put like a store that is down.
type brokenChallenges struct{ ChallengeStore }

func (brokenChallenges) Put(context.Context, Challenge) (string, error) {
	return "", errors.New("connection refused to db.internal:5432")
}

func TestChallengeStoreFailureIs500NotBadRequest(t *testing.T) {
	r := newRigWith(t, &AdapterStore{Adapter: docstore.NewMemoryAdapter()}, Config{Challenges: brokenChallenges{NewMemoryChallenges(0)}})
	code, out := r.do("POST", "/auth/passkey/login/begin", "", nil)
	if code != 500 {
		t.Fatalf("login begin with a failing store = %d %v, want 500", code, out)
	}
	if msg, _ := out["error"].(string); strings.Contains(msg, "db.internal") {
		t.Fatalf("the store's error leaks to the client: %q", msg)
	}
}

// aged reports the modification times in times (zero for a name not in the map, when zeroOthers) in
// listings, and runs hook after each listing is taken (a Put during a sweep).
type aged struct {
	*docstore.MemoryAdapter
	times map[string]time.Time
	zero  bool
	hook  func()
}

func (a *aged) ListPrefix(ctx context.Context, prefix string) ([]docstore.Meta, error) {
	metas, err := a.MemoryAdapter.ListPrefix(ctx, prefix)
	for i := range metas {
		if a.zero {
			metas[i].UpdatedAt = time.Time{}
		} else if t, ok := a.times[metas[i].ID]; ok {
			metas[i].UpdatedAt = t
		}
	}
	if a.hook != nil {
		a.hook()
	}
	return metas, err
}

func TestSweepDeletesOldFormatRecordsAfterADayAndNothingForeign(t *testing.T) {
	ctx := context.Background()
	a := &aged{MemoryAdapter: docstore.NewMemoryAdapter(), times: map[string]time.Time{}}
	sc, _ := NewStoreChallenges(a, 0)
	now := time.Unix(1_700_000_000, 0)
	sc.Now = func() time.Time { return now }
	hash := func(c string) string { return strings.Repeat(c, 64) }
	put := func(name string, age time.Duration) string {
		_ = a.MemoryAdapter.Save(ctx, name, []byte(`{"expires":"2099-01-01T00:00:00Z"}`))
		a.times[name] = now.Add(-age)
		return name
	}
	oldDead := put(ChallengePrefix+hash("a"), 25*time.Hour)
	oldFresh := put(ChallengePrefix+hash("b"), time.Minute)
	foreign := []string{
		put(ChallengePrefix+strings.Repeat("a", 63), 48*time.Hour),         // wrong length
		put(ChallengePrefix+strings.Repeat("a", 65), 48*time.Hour),         // wrong length
		put(ChallengePrefix+strings.Repeat("A", 64), 48*time.Hour),         // uppercase hex
		put(ChallengePrefix+"x/"+hash("a"), 48*time.Hour),                  // extra segment
		put(ChallengePrefix+hash("a")+"/more", 48*time.Hour),               // extra segment
		put(ChallengePrefix+"1/2/"+hash("a"), 48*time.Hour),                // extra segment
		put(ChallengePrefix+"5/"+strings.Repeat("g", 64), 48*time.Hour),    // bucket 5 is long dead, hash is not hex
		put(ChallengePrefix+"5/"+strings.Repeat("a", 10), 48*time.Hour),    // bucket 5 is long dead, not a hash
		put(ChallengePrefix+"05/"+hash("a"), 48*time.Hour),                 // non-canonical bucket
		put(ChallengePrefix+"5/"+strings.ToUpper(hash("a")), 48*time.Hour), // uppercase hash
		put(ChallengePrefix, 48*time.Hour),                                 // the prefix alone
		put(ChallengePrefix+hash("c")+" ", 48*time.Hour),                   // trailing byte
	}
	n, err := sc.Sweep(ctx)
	if err != nil || n != 1 {
		t.Fatalf("Sweep = %d, %v, want only the 25h old record", n, err)
	}
	if _, err := a.MemoryAdapter.Load(ctx, oldDead); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("an old-format record over a day old is still there: %v", err)
	}
	for _, name := range append(foreign, oldFresh) {
		if _, err := a.MemoryAdapter.Load(ctx, name); err != nil {
			t.Fatalf("%q must be kept: %v", name, err)
		}
	}
	if got := sc.approx.Load(); got != 0 {
		t.Fatalf("count = %d, old-format and foreign names are not counted", got)
	}
	// A minute short of a day stays, a minute past goes.
	a.times[oldFresh] = now.Add(-oldChallengeAge)
	if n, _ := sc.Sweep(ctx); n != 0 {
		t.Fatalf("exactly a day old must stay, deleted %d", n)
	}
	a.times[oldFresh] = now.Add(-oldChallengeAge - time.Second)
	if n, _ := sc.Sweep(ctx); n != 1 {
		t.Fatalf("over a day old must go, deleted %d", n)
	}
}

func TestSweepOldFormatWithoutModificationTimeReadsOnce(t *testing.T) {
	ctx := context.Background()
	c := &counting{MemoryAdapter: docstore.NewMemoryAdapter()}
	a := &aged{MemoryAdapter: c.MemoryAdapter, zero: true}
	sc, _ := NewStoreChallenges(&agedCounting{aged: a, c: c}, 0)
	now := time.Unix(1_700_000_000, 0)
	sc.Now = func() time.Time { return now }
	save := func(c string, body string) string {
		name := ChallengePrefix + strings.Repeat(c, 64)
		_ = a.MemoryAdapter.Save(ctx, name, []byte(body))
		return name
	}
	expired := save("a", `{"expires":"`+now.Add(-2*time.Minute).UTC().Format(time.RFC3339)+`"}`)
	inGrace := save("b", `{"expires":"`+now.Add(-30*time.Second).UTC().Format(time.RFC3339)+`"}`)
	live := save("c", `{"expires":"`+now.Add(3*time.Minute).UTC().Format(time.RFC3339)+`"}`)
	garbage := save("d", `not json`)
	foreign := ChallengePrefix + "x/y"
	_ = a.MemoryAdapter.Save(ctx, foreign, []byte(`junk`))
	n, err := sc.Sweep(ctx)
	if err != nil || n != 2 {
		t.Fatalf("Sweep = %d, %v, want the expired and the garbage one", n, err)
	}
	if got := c.loads.Load(); got != 4 {
		t.Fatalf("loads = %d, want one per old-format record and none for the foreign name", got)
	}
	for _, gone := range []string{expired, garbage} {
		if _, err := a.MemoryAdapter.Load(ctx, gone); !errors.Is(err, docstore.ErrNotFound) {
			t.Fatalf("%q must be deleted: %v", gone, err)
		}
	}
	for _, kept := range []string{inGrace, live, foreign} {
		if _, err := a.MemoryAdapter.Load(ctx, kept); err != nil {
			t.Fatalf("%q must be kept: %v", kept, err)
		}
	}
	if sc.approx.Load() != 0 {
		t.Fatalf("count = %d, want 0", sc.approx.Load())
	}
}

// agedCounting lists through aged and counts Loads.
type agedCounting struct {
	*aged
	c *counting
}

func (a *agedCounting) Load(ctx context.Context, id string) (docstore.Document, error) {
	return a.c.Load(ctx, id)
}

func TestSweepKeepsARecordInItsGraceMinute(t *testing.T) {
	ctx := context.Background()
	mem, sc := newShared(t)
	now := time.Unix(1_700_000_040, 0)
	sc.Now = func() time.Time { return now }
	tok, err := sc.Put(ctx, Challenge{Expires: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	end := bucketEnd(bucketOf(now.Add(time.Minute)))
	for _, tc := range []struct {
		at   time.Duration
		gone bool
	}{{-time.Second, false}, {0, false}, {30 * time.Second, false}, {time.Minute - time.Second, false}, {time.Minute + time.Second, true}} {
		now = end.Add(tc.at)
		n, err := sc.Sweep(ctx)
		_, lerr := mem.Load(ctx, challengeID(tok))
		if gone := errors.Is(lerr, docstore.ErrNotFound); gone != tc.gone || (n == 1) != tc.gone || err != nil {
			t.Fatalf("at bucket end %+v: deleted %d, gone %v, err %v", tc.at, n, gone, err)
		}
		if want := int64(1); !tc.gone && sc.approx.Load() != want {
			t.Fatalf("at %+v the record is left and counted: count %d", tc.at, sc.approx.Load())
		}
	}
	if sc.approx.Load() != 0 {
		t.Fatalf("count after deletion = %d", sc.approx.Load())
	}
}

func TestSweepCountKeepsPutsMadeWhileListing(t *testing.T) {
	ctx := context.Background()
	a := &aged{MemoryAdapter: docstore.NewMemoryAdapter()}
	sc, _ := NewStoreChallenges(a, 0)
	now := time.Now()
	sc.Now = func() time.Time { return now }
	exp := Challenge{Expires: now.Add(time.Hour)}
	for i := 0; i < 3; i++ {
		if _, err := sc.Put(ctx, exp); err != nil {
			t.Fatal(err)
		}
	}
	a.hook = func() { // two ceremonies begin after the listing was taken
		a.hook = nil
		for i := 0; i < 2; i++ {
			if _, err := sc.Put(ctx, exp); err != nil {
				t.Error(err)
			}
		}
	}
	if _, err := sc.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sc.approx.Load(); got != 5 {
		t.Fatalf("count = %d, want 3 listed + 2 put during the listing", got)
	}
}
