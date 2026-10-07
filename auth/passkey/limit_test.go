package passkey

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/elaous/gojiberry/docstore"
)

func TestRateLimiterBurstRefillAndForgetting(t *testing.T) {
	now := time.Unix(1000, 0)
	l := NewRateLimiter(60, 3) // one a second, 3 at once
	l.Now = func() time.Time { return now }
	ctx := context.Background()
	for i := range 3 {
		if ok, _ := l.Allow(ctx, "a"); !ok {
			t.Fatalf("burst %d refused", i)
		}
	}
	ok, wait := l.Allow(ctx, "a")
	if ok || wait <= 0 || wait > 2*time.Second {
		t.Fatalf("over the burst: %v %v", ok, wait)
	}
	if ok, _ := l.Allow(ctx, "b"); !ok {
		t.Error("another key has its own budget")
	}
	now = now.Add(1100 * time.Millisecond)
	if ok, _ := l.Allow(ctx, "a"); !ok {
		t.Error("a token came back after a second")
	}
	if ok, _ := l.Allow(ctx, "a"); ok {
		t.Error("only one token came back")
	}
	// (Forgetting idle keys is tested with the limiter itself, in docstore.)
	if ok, wait := (&RateLimiter{}).Allow(ctx, "z"); ok || wait <= 0 {
		t.Error("a zero budget refuses")
	}
}

func TestTrustedProxiesReadForwardedForOnlyFromThem(t *testing.T) {
	ip := TrustedProxies("10.0.0.0/8", "127.0.0.1")
	req := func(remote, xff string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	cases := []struct{ remote, xff, want string }{
		{"203.0.113.9:1000", "1.2.3.4", "203.0.113.9"},                     // not from a proxy: the header is not believed
		{"10.1.1.1:80", "198.51.100.7", "198.51.100.7"},                    // from a proxy
		{"10.1.1.1:80", "6.6.6.6, 198.51.100.7, 10.2.2.2", "198.51.100.7"}, // rightmost that is not a proxy
		{"10.1.1.1:80", "", "10.1.1.1"},                                    // nothing forwarded
		{"[::1]:80", "9.9.9.9", "::1"},                                     // not in the list
		{"127.0.0.1:80", "not-an-ip", "not-an-ip"},                         // garbage is just a key
	}
	for _, c := range cases {
		if got := ip(req(c.remote, c.xff)); got != c.want {
			t.Errorf("%s %q: %q, want %q", c.remote, c.xff, got, c.want)
		}
	}
	if ClientIP(req("192.0.2.5:99", "1.1.1.1")) != "192.0.2.5" {
		t.Error("the default is the remote address")
	}
}

type countLimiter struct {
	mu   sync.Mutex
	keys []string
	deny bool
}

func (c *countLimiter) Allow(_ context.Context, key string) (bool, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = append(c.keys, key)
	return !c.deny, 7 * time.Second
}

func TestAnInjectedLimiterAndClientIPDecideTheRoutes(t *testing.T) {
	r := newRig(t, NewMemoryStore())
	cl := &countLimiter{}
	r.svc.limiter = cl
	r.svc.clientIP = func(*http.Request) string { return "tenant-7" }
	if code, _ := r.do("POST", "/auth/passkey/login/begin", "", nil); code != 200 {
		t.Fatal(code)
	}
	if len(cl.keys) != 1 || cl.keys[0] != "login|tenant-7" {
		t.Errorf("the key is group|client: %v", cl.keys)
	}
	cl.deny = true
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/auth/passkey/register/begin", nil)
	r.svc.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "8" {
		t.Errorf("%d %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if cl.keys[len(cl.keys)-1] != "register|tenant-7" {
		t.Errorf("%v", cl.keys)
	}
	// Unlimited turns it off.
	r.svc.limiter = Unlimited{}
	for range 50 {
		if code, _ := r.do("POST", "/auth/passkey/login/begin", "", nil); code != 200 {
			t.Fatal(code)
		}
	}
}

func TestTheDefaultLimiterStopsABurstOfBegins(t *testing.T) {
	r := newRig(t, NewMemoryStore()) // New makes the default limiter
	var got429 bool
	for range defaultBurst + 5 {
		if code, _ := r.do("POST", "/auth/passkey/login/begin", "", nil); code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Error("the default limiter refuses a burst past its budget")
	}
}

type memChallenges struct {
	mu sync.Mutex
	m  map[string]Challenge
	n  int
}

func (c *memChallenges) Put(_ context.Context, ch Challenge) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	tok := string(rune('a' + c.n))
	c.m[tok] = ch
	return tok, nil
}

func (c *memChallenges) Take(_ context.Context, tok string) (Challenge, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.m[tok]
	delete(c.m, tok)
	return ch, ok, nil
}

func TestAnInjectedChallengeStoreHoldsTheCeremonies(t *testing.T) {
	r := newRig(t, NewMemoryStore())
	cs := &memChallenges{m: map[string]Challenge{}}
	r.svc.challenges = cs
	_, tok, err := r.svc.LoginStart(context.Background())
	if err != nil || len(cs.m) != 1 || cs.m[tok].Expires.IsZero() {
		t.Fatalf("%v %v", err, cs.m)
	}
	if _, err := r.svc.LoginFinish(context.Background(), tok, []byte("{}")); err == nil {
		t.Error("a bad answer is refused")
	}
	if len(cs.m) != 0 {
		t.Error("the challenge is consumed by the finish, good or bad")
	}
	if _, err := r.svc.LoginFinish(context.Background(), tok, []byte("{}")); err != ErrBadChallenge {
		t.Errorf("used once: %v", err)
	}
}

// A store that is only a Store (no Updater, no Creator) still works: the Service serializes.
type plainStore struct{ Store }

func TestUpdaterAndCreatorOverAConditionalAdapterAreCompareAndSet(t *testing.T) {
	ctx := context.Background()
	st := &AdapterStore{Adapter: docstore.NewMemoryAdapter()}
	a := &Account{ID: "passkey:1", Handle: []byte("h1"), Name: "n"}
	if err := st.Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := st.Create(ctx, a); err != ErrExists {
		t.Errorf("a second Create: %v", err)
	}
	if err := st.Update(ctx, "passkey:none", func(*Account) error { return nil }); err != ErrNotFound {
		t.Errorf("%v", err)
	}
	// Many writers each add a label: nobody's change is lost.
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := st.Update(ctx, "passkey:1", func(a *Account) error {
				a.Credentials = append(a.Credentials, Credential{Label: string(rune('A' + i))})
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, _ := st.Get(ctx, "passkey:1")
	if len(got.Credentials) != 20 {
		t.Errorf("lost updates: %d of 20", len(got.Credentials))
	}
	// fn failing writes nothing.
	boom := context.Canceled
	if err := st.Update(ctx, "passkey:1", func(a *Account) error { a.Name = "changed"; return boom }); err != boom {
		t.Error(err)
	}
	if got, _ := st.Get(ctx, "passkey:1"); got.Name != "n" {
		t.Error("an error from fn aborts the write")
	}
	// The handle record names the account.
	if by, err := st.ByHandle(ctx, []byte("h1")); err != nil || by.ID != "passkey:1" {
		t.Errorf("%v", err)
	}
}

func TestServiceFallsBackToItsOwnLockForAPlainStore(t *testing.T) {
	r := newRig(t, plainStore{NewMemoryStore()})
	ctx := context.Background()
	if err := r.svc.create(ctx, &Account{ID: "x", Handle: []byte("h")}); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.create(ctx, &Account{ID: "x", Handle: []byte("h")}); err != ErrExists {
		t.Errorf("%v", err)
	}
	if err := r.svc.update(ctx, "x", func(a *Account) error { a.Name = "y"; return nil }); err != nil {
		t.Fatal(err)
	}
	if a, _ := r.svc.store.Get(ctx, "x"); a.Name != "y" {
		t.Error("the change was stored")
	}
}

// A shared (store) limiter is a passkey Limiter: two services over one store share the budget.
func TestStoreLimiterCanBeInjectedAsTheServiceLimiter(t *testing.T) {
	store := docstore.NewMemoryAdapter()
	var a, b Limiter = docstore.NewStoreLimiter(store, "passkey", DefaultPerMinute, 2), docstore.NewStoreLimiter(store, "passkey", DefaultPerMinute, 2)
	ctx := context.Background()
	got := 0
	for i := range 4 {
		l := a
		if i%2 == 1 {
			l = b
		}
		if ok, _ := l.Allow(ctx, "register|203.0.113.9"); ok {
			got++
		}
	}
	if got != 2 {
		t.Errorf("two replicas allowed %d, want the shared 2", got)
	}
	if DefaultBurst != 20 || DefaultPerMinute != 30 {
		t.Error("the exported defaults are the documented ones")
	}
}
