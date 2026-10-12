package public

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/elaous/gojiberry/docstore"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- 1. the in-flight bytes have a share per client and per network, and slow uploads cost quota

// holdUploadAs is holdUpload for a client behind the trusted proxy (X-Forwarded-For).
func holdUploadAs(t *testing.T, e *env, token string, n int, ip string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() }) // best-effort cleanup of a test connection
	if _, err := fmt.Fprintf(conn, "POST /public/doc HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nX-Forwarded-For: %s\r\nContent-Length: %d\r\n\r\nabc", token, ip, n); err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestSlowWritersFromSeveralAddressesCannotUseUpTheInflightCap(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		behindProxy(o)
		o.Config.MaxDocBytes = 1000
		o.Config.MaxInflightBytes = 8000 // defaults: 1000 per client (cap/8), 2000 per network (cap/4)
		o.Config.RequestTimeout = time.Minute
	})
	if c := e.svc.cfg; c.MaxInflightPerClient != 1000 || c.MaxInflightPerNet != 2000 {
		t.Fatalf("default shares %d and %d", c.MaxInflightPerClient, c.MaxInflightPerNet)
	}
	tokenFor := func(ip string) string {
		tok, _ := e.session(xff(ip)...)
		return tok
	}
	// six attackers, each in its own network, each slowly sending a declared 1000 bytes
	attackers := []string{"203.0.113.1", "198.51.100.1", "192.0.2.1", "100.64.0.1", "100.65.0.1", "2001:db8:a::1"}
	for _, ip := range attackers {
		holdUploadAs(t, e, tokenFor(ip), 1000, ip)
	}
	waitFor(t, "the six uploads to be admitted", func() bool { return e.svc.flight.held() == 6000 })
	// an attacker that opens a second connection finds its share used (429), not the global cap
	r := e.want(e.do("POST", "/public/doc", tokenFor("203.0.113.1"), bytes.Repeat([]byte("x"), 1000), xff("203.0.113.1")...), 429)
	if r.Header.Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
	// a second address in the same /24 still has the network's share (2000) ...
	holdUploadAs(t, e, tokenFor("203.0.113.2"), 1000, "203.0.113.2")
	waitFor(t, "the seventh upload", func() bool { return e.svc.flight.held() == 7000 })
	// ... a third does not (429): the network holds 2000
	e.want(e.do("POST", "/public/doc", tokenFor("203.0.113.3"), bytes.Repeat([]byte("x"), 1000), xff("203.0.113.3")...), 429)
	// and everybody else's writes still go through: the global cap was never used up
	e.want(e.do("POST", "/public/doc", tokenFor("100.99.0.1"), bytes.Repeat([]byte("y"), 1000), xff("100.99.0.1")...), 201)
	if got := e.svc.flight.held(); got != 7000 {
		t.Fatalf("%d bytes in flight, want the attackers' 7000", got)
	}
}

func TestInflightSharesAreBoundedByTheirDefaultsAndValidated(t *testing.T) {
	c := Config{}.withDefaults()
	if c.MaxInflightPerClient != DefaultMaxInflightBytes/8 || c.MaxInflightPerNet != DefaultMaxInflightBytes/4 {
		t.Fatalf("defaults %d %d", c.MaxInflightPerClient, c.MaxInflightPerNet)
	}
	// never below one document
	c = Config{MaxDocBytes: 100, MaxInflightBytes: 400}.withDefaults()
	if c.MaxInflightPerClient != 100 || c.MaxInflightPerNet != 100 {
		t.Fatalf("%d %d", c.MaxInflightPerClient, c.MaxInflightPerNet)
	}
	if err := (Config{MaxDocBytes: 100, MaxInflightBytes: 400, MaxInflightPerClient: 99}).withDefaults().Validate(); err == nil {
		t.Fatal("a per-client share below the document cap accepted")
	}
	get := func(env map[string]string) (Config, error) { return FromEnv(func(k string) string { return env[k] }) }
	got, err := get(map[string]string{
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_INFLIGHT_PER_CLIENT": "5000000",
		"GOJIBERRY_DOCSTORE_PUBLIC_MAX_INFLIGHT_PER_NET":    "9000000",
		"GOJIBERRY_DOCSTORE_PUBLIC_MIN_UPLOAD_RATE":         "50000",
		"GOJIBERRY_DOCSTORE_PUBLIC_UPLOAD_GRACE":            "2s",
	})
	if err != nil || got.MaxInflightPerClient != 5000000 || got.MaxInflightPerNet != 9000000 || got.MinUploadRate != 50000 || got.UploadGrace != 2*time.Second {
		t.Fatalf("%+v %v", got, err)
	}
	for _, k := range []string{"MAX_INFLIGHT_PER_CLIENT", "MAX_INFLIGHT_PER_NET", "MIN_UPLOAD_RATE", "UPLOAD_GRACE"} {
		if _, err := get(map[string]string{"GOJIBERRY_DOCSTORE_PUBLIC_" + k: "-1"}); err == nil || !strings.Contains(err.Error(), k) {
			t.Errorf("%s: %v", k, err)
		}
	}
	var f inflight
	f.max, f.perClient, f.perNet = 100, 30, 50
	if f.acquire("a", "n", 30) != flightOK || f.acquire("a", "n", 1) != flightClient {
		t.Fatal("per client")
	}
	if f.acquire("b", "n", 20) != flightOK || f.acquire("c", "n", 1) != flightNet {
		t.Fatal("per net")
	}
	if f.acquire("d", "m", 60) != flightGlobal || f.acquire("d", "m", 31) != flightClient || f.acquire("d", "m", 30) != flightOK || f.acquire("e", "k", 20) != flightOK || f.acquire("e", "k", 1) != flightGlobal {
		t.Fatal("mixed")
	}
	f.release("a", "n", 30)
	if f.heldBy("a") != 0 {
		t.Fatal("not released")
	}
}

func TestATimedOutOrAbandonedUploadStaysChargedButServerFailuresAreRefunded(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Config.IPDailyBytes = 10000
		o.Config.MaxDocBytes = 4000
		o.Config.RequestTimeout = 300 * time.Millisecond
	})
	tok, _ := e.session()
	// a body that stops arriving: 408, and the declared 1000 bytes were paid for
	st, answered := rawRequest(t, e, "POST", "/public/doc", tok, 1000, "abc", 5*time.Second)
	if !answered || !strings.Contains(st, "408") {
		t.Fatalf("slow create: %q (answered %v)", st, answered)
	}
	if used := quotaUsed(e, "ip:127.0.0.1"); used != 1000 {
		t.Fatalf("a timed-out upload left %d bytes charged, want the declared 1000", used)
	}
	waitFor(t, "the claim to end", func() bool { return e.svc.flight.held() == 0 })
	// a client that goes away in the middle pays too
	conn := holdUpload(t, e, tok, 2000)
	waitFor(t, "the upload to be admitted", func() bool { return e.svc.flight.held() == 2000 })
	_ = conn.Close() // the point of the test: the client goes away
	waitFor(t, "the claim to end", func() bool { return e.svc.flight.held() == 0 })
	waitFor(t, "the charge", func() bool { return quotaUsed(e, "ip:127.0.0.1") == 3000 })
	// a daily budget that is used up this way refuses the next upload up front
	for i := 0; i < 7; i++ {
		rawRequest(t, e, "POST", "/public/doc", tok, 1000, "abc", 5*time.Second)
	}
	e.want(e.do("POST", "/public/doc", tok, []byte("hello")), 429)
}

func TestSlowUploadsAreCutOffAtTheMinimumRate(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Config.MaxDocBytes = 1 << 20
		o.Config.RequestTimeout = 20 * time.Second
		o.Config.UploadGrace = 200 * time.Millisecond
		o.Config.MinUploadRate = 1000000
	})
	tok, _ := e.session()
	// a client that sends 3 bytes of a declared 100,000 and then nothing is answered 408 right
	// after the grace, not after the request timeout
	start := time.Now()
	st, answered := rawRequest(t, e, "POST", "/public/doc", tok, 100000, "abc", 10*time.Second)
	if !answered || !strings.Contains(st, "408") {
		t.Fatalf("stalled upload: %q (answered %v)", st, answered)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("the stalled upload held its claim for %v", d)
	}
	if used := quotaUsed(e, "ip:127.0.0.1"); used != 100000 {
		t.Fatalf("charged %d, want the declared 100000", used)
	}
	// a trickle (100 bytes every 50 ms) is far below 1 MB/s: cut off early too
	conn, err := net.Dial("tcp", strings.TrimPrefix(e.srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }() // best-effort cleanup of a test connection
	if _, err := fmt.Fprintf(conn, "POST /public/doc HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\nContent-Length: 100000\r\n\r\n", tok); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		done <- string(buf[:n])
	}()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
loop:
	for {
		select {
		case got := <-done:
			if !strings.Contains(got, "408") {
				t.Fatalf("trickle: %q", got)
			}
			break loop
		case <-tick.C:
			_, _ = conn.Write(bytes.Repeat([]byte("z"), 100))
			if time.Since(start) > 5*time.Second {
				t.Fatal("the trickle was not cut off")
			}
		}
	}
	// a client at speed is not affected, and small bodies are left to the request timeout
	e.want(e.do("POST", "/public/doc", tok, bytes.Repeat([]byte("a"), 100000)), 201)
	e.want(e.do("PUT", "/public/doc", tok, []byte("small")), 204)
	// a negative rate turns the minimum off
	e2 := newEnv(t, func(o *Options) { o.Config.MinUploadRate = -1 })
	if e2.svc.cfg.MinUploadRate >= 0 {
		t.Fatal("negative rate not kept")
	}
}

// ---- 2. admin: a minimum token length, failures per network and in all, a cooldown, a burst log

func TestAdminTokenMustBeLongEnoughUnlessExplicitlyAllowed(t *testing.T) {
	mk := func(token string, allow bool) error {
		_, err := New(Options{Store: docstore.NewExpiringAdapter(docstore.NewMemoryAdapter(), docstore.FixedTTL(24*hour)),
			Config: Config{AdminToken: token, NoProxy: true, AllowShortAdminToken: allow, ReportKey: testReportKey}})
		return err
	}
	if err := mk("fifteen-chars-xx", false); err != nil { // 16
		t.Fatalf("16 characters refused: %v", err)
	}
	if err := mk(strings.Repeat("a", 15), false); !errors.Is(err, ErrAdminTokenTooShort) {
		t.Fatalf("15 characters: %v", err)
	}
	if err := mk("short", true); err != nil {
		t.Fatalf("explicit override refused: %v", err)
	}
	if err := mk("", false); err != nil {
		t.Fatalf("no token, no route: %v", err)
	}
	get := func(env map[string]string) (Config, error) { return FromEnv(func(k string) string { return env[k] }) }
	if _, err := get(map[string]string{"GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN": "short"}); err == nil || !strings.Contains(err.Error(), "ADMIN_TOKEN") || !strings.Contains(err.Error(), "ALLOW_SHORT_ADMIN_TOKEN") {
		t.Fatalf("a short token from the environment: %v", err)
	}
	c, err := get(map[string]string{"GOJIBERRY_DOCSTORE_PUBLIC_ADMIN_TOKEN": "short", "GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_SHORT_ADMIN_TOKEN": "true"})
	if err != nil || !c.AllowShortAdminToken {
		t.Fatalf("override: %+v %v", c, err)
	}
	w := c.Warnings()
	found := false
	for _, x := range w {
		found = found || (strings.Contains(x, "ALLOW_SHORT_ADMIN_TOKEN") && strings.Contains(x, "GUESSED ONLINE"))
	}
	if !found {
		t.Fatalf("no loud warning: %v", w)
	}
	if _, err := get(map[string]string{"GOJIBERRY_DOCSTORE_PUBLIC_ALLOW_SHORT_ADMIN_TOKEN": "maybe"}); err == nil {
		t.Fatal("a bad boolean accepted")
	}
}

func adminGuess(e *env, ip, token string) int {
	e.t.Helper()
	return e.do("DELETE", "/admin/takedown/"+strings.Repeat("C", 22), "", nil, append(xff(ip), AdminHeader, token)...).StatusCode
}

func TestAdminFailuresAreAlsoCountedPerNetwork(t *testing.T) {
	e := newEnv(t, behindProxy)
	// one attacker holds a /48 (65,536 /64s) and guesses once from each of 200 of them: no /64 is
	// near its own 20, the /48 as a whole has 60
	got := map[int]int{}
	for i := 0; i < 200; i++ {
		got[adminGuess(e, fmt.Sprintf("2001:db8:aaaa:%x::1", i), fmt.Sprint("guess", i))]++
	}
	if got[403] != 60 || got[429] != 140 {
		t.Fatalf("the /48 got %v, want 60 failures and the rest limited", got)
	}
	// the operator is never held back, from that network or any other
	e.want(e.do("DELETE", "/admin/takedown/"+strings.Repeat("D", 22), "", nil, append(xff("2001:db8:aaaa:ffff::1"), AdminHeader, adminToken)...), 202)
	// another network has its own budget
	if st := adminGuess(e, "2001:db8:bbbb::1", "nope"); st != 403 {
		t.Fatalf("another network: %d", st)
	}
	// an IPv4 /24 is the same: 80 hosts, 60 failures
	got = map[int]int{}
	for i := 1; i <= 80; i++ {
		got[adminGuess(e, fmt.Sprintf("198.51.100.%d", i), "nope")]++
	}
	if got[403] != 60 || got[429] != 20 {
		t.Fatalf("the /24 got %v", got)
	}
}

func TestAdminGlobalFailureBudgetPutsWrongTokensIntoACooldown(t *testing.T) {
	logs := captureLog(t)
	e := newEnv(t, behindProxy)
	fresh := func(i int) string { return fmt.Sprintf("10.%d.%d.1", i/256, i%256) } // a network of its own each
	for i := 0; i < 300; i++ {
		if st := adminGuess(e, fresh(i), "guess"); st != 403 {
			t.Fatalf("failure %d: %d", i, st)
		}
	}
	// the 301st failure anywhere finds the global budget used up: cooldown
	r := e.do("DELETE", "/admin/takedown/"+strings.Repeat("C", 22), "", nil, append(xff(fresh(300)), AdminHeader, "guess")...)
	if r.StatusCode != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatalf("after the global budget: %d", r.StatusCode)
	}
	if !strings.Contains(logs.String(), "ALARM: failed admin authentications") {
		t.Fatalf("no alarm in the log: %s", logs.String())
	}
	// during the cooldown every wrong token is 429, even from a network that never failed ...
	if st := adminGuess(e, "172.16.5.5", "other guess"); st != 429 {
		t.Fatalf("new network during the cooldown: %d", st)
	}
	// ... but a matching token is never throttled
	e.want(e.do("DELETE", "/admin/takedown/"+strings.Repeat("E", 22), "", nil, append(xff("172.16.5.5"), AdminHeader, adminToken)...), 202)
	e.want(e.do("GET", "/admin/health", "", nil, append(xff(fresh(1)), AdminHeader, adminToken)...), 200)
	// the budget refills at 5 a second, but the cooldown holds for its 30 seconds
	e.clk.Add(10 * time.Second)
	if st := adminGuess(e, "172.16.7.7", "guess"); st != 429 {
		t.Fatalf("10 seconds into the cooldown: %d", st)
	}
	// 30 seconds after it began the route answers wrong tokens 403 again
	e.clk.Add(AdminCooldown)
	if st := adminGuess(e, "172.16.9.9", "guess"); st != 403 {
		t.Fatalf("after the cooldown: %d", st)
	}
}

func TestAdminFailureBurstIsLogged(t *testing.T) {
	logs := captureLog(t)
	e := newEnv(t, behindProxy)
	for i := 0; i < adminBurst-1; i++ {
		adminGuess(e, fmt.Sprintf("10.1.%d.1", i), "nope")
	}
	if strings.Contains(logs.String(), "failed admin authentications within a minute") {
		t.Fatal("logged before the burst")
	}
	adminGuess(e, "10.2.0.1", "nope")
	adminGuess(e, "10.2.1.1", "nope")
	if n := strings.Count(logs.String(), "failed admin authentications within a minute"); n != 1 {
		t.Fatalf("burst logged %d times: %s", n, logs.String())
	}
	e.clk.Add(2 * time.Minute)
	for i := 0; i < adminBurst; i++ {
		adminGuess(e, fmt.Sprintf("10.3.%d.1", i), "nope")
	}
	if n := strings.Count(logs.String(), "failed admin authentications within a minute"); n != 2 {
		t.Fatalf("second burst: logged %d times", n)
	}
}

// ---- 3. a client that goes away must not fail the first reconcile for everybody

func TestClientDisconnectDuringTheFirstReconcileDoesNotFailItForEveryone(t *testing.T) {
	logs := captureLog(t)
	e, hs := envWith(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	hs.listHook = func(ctx context.Context) error {
		once.Do(func() {
			close(entered)
			<-release
			// a store that honours its context notices the client's departure a moment later
			select {
			case <-ctx.Done():
			case <-time.After(300 * time.Millisecond):
			}
		})
		return ctx.Err()
	}
	a, _ := e.session()
	b, _ := e.session()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		req, _ := http.NewRequestWithContext(ctx, "POST", e.srv.URL+"/public/doc", strings.NewReader("hello"))
		req.Header.Set("Authorization", "Bearer "+a)
		if r, err := http.DefaultClient.Do(req); err == nil {
			r.Body.Close()
		}
	}()
	t.Cleanup(func() {
		once.Do(func() {})
		select {
		case <-release:
		default:
			close(release)
		}
	})
	<-entered // the first write is reconciling the counters
	cancel()  // and its client leaves
	time.Sleep(100 * time.Millisecond)
	close(release)
	// the reconcile was not the departing client's: another client's write goes through at once
	// (a reconcile that died with its client would have put everyone into a 15 second backoff)
	e.want(e.do("POST", "/public/doc", b, []byte("hello")), 201)
	if strings.Contains(logs.String(), "writes are refused") {
		t.Fatalf("the first reconcile failed: %s", logs.String())
	}
}

// The same through ensureCounters itself: the request that happens to start the reconcile is
// cancelled (an HTTP/2 stream reset, or a body already read and the connection gone).
func TestCancellingTheRequestThatStartedTheFirstReconcileDoesNotFailItForOthers(t *testing.T) {
	logs := captureLog(t)
	e, hs := envWith(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	hs.listHook = func(ctx context.Context) error {
		once.Do(func() {
			close(entered)
			<-release
		})
		return ctx.Err() // a store that honours its context
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan bool, 1)
	go func() { first <- e.svc.ensureCounters(ctx) }()
	<-entered
	cancel() // the starter's client leaves while the store is being listed
	close(release)
	if ok := <-first; !ok {
		t.Fatalf("the reconcile died with the request that started it: %s", logs.String())
	}
	if !e.svc.ensureCounters(context.Background()) {
		t.Fatal("everybody else is in the backoff")
	}
}

func TestWaitersOnTheFirstReconcileAreBoundedByTheirOwnContext(t *testing.T) {
	e, hs := envWith(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	hs.listHook = func(context.Context) error {
		once.Do(func() { close(entered); <-release })
		return nil
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	first := make(chan bool, 1)
	go func() { first <- e.svc.ensureCounters(context.Background()) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	waited := make(chan bool, 1)
	go func() { waited <- e.svc.ensureCounters(ctx) }()
	select {
	case ok := <-waited:
		if ok {
			t.Fatal("a waiter was told the counters are ready")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a waiter stayed behind the reconcile past its own deadline")
	}
	close(release)
	if ok := <-first; !ok {
		t.Fatal("the reconcile failed")
	}
	if !e.svc.ensureCounters(context.Background()) {
		t.Fatal("the counters are not ready after the reconcile")
	}
}

// ---- 4. a third prefix level and a reserve judged at the coarsest

func TestWideKey(t *testing.T) {
	for in, want := range map[string]string{
		"203.0.113.5":             "203.0.0.0/16",
		"::ffff:203.0.113.77":     "203.0.0.0/16",
		"2001:db8:1:2::1":         "2001:db8::/32",
		"2001:DB8:ffff:1:2:3:4:5": "2001:db8::/32",
		"Not An Address":          "not an address",
	} {
		if got := WideKey(in); got != want {
			t.Errorf("WideKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlash32FloodCannotFillTheGlobalCap(t *testing.T) {
	sessions := NewMemorySessionStore(200)
	e := newEnv(t, func(o *Options) {
		behindProxy(o)
		o.Sessions = sessions
		o.Config.MaxSessions = 200
		o.Config.MaxSessionsPerAddress = 2
		o.Config.MaxSessionsPerNet = 6
		o.Config.MaxSessionsPerWide = 20
	})
	// an attacker holding 2001:db8::/32 starts a session from each of 100 different /48s: no /48
	// is near its cap of 6, the /32 as a whole has 20
	got := map[int]int{}
	for i := 0; i < 100; i++ {
		got[sessionStatus(e, fmt.Sprintf("2001:db8:%x::1", 0x100+i))]++
	}
	if got[201] != 20 || got[429] != 80 {
		t.Fatalf("the /32 got %v, want 20 sessions and the rest 429", got)
	}
	// IPv4: one /16
	got = map[int]int{}
	for i := 0; i < 40; i++ {
		got[sessionStatus(e, fmt.Sprintf("198.51.%d.1", i))]++
	}
	if got[201] != 20 || got[429] != 20 {
		t.Fatalf("the /16 got %v", got)
	}
	// other wide networks are unaffected
	for _, ip := range []string{"2001:db9::1", "192.0.2.1", "203.0.113.9"} {
		if st := sessionStatus(e, ip); st != 201 {
			t.Fatalf("%s: %d", ip, st)
		}
	}
	if c := (Config{MaxSessions: 50, MaxSessionsPerNet: 20, MaxSessionsPerWide: 5}).withDefaults(); c.MaxSessionsPerWide != 20 {
		t.Fatalf("wide cap %d below the net cap", c.MaxSessionsPerWide)
	}
	if c := (Config{MaxSessions: 50, MaxSessionsPerWide: 500}).withDefaults(); c.MaxSessionsPerWide != 50 {
		t.Fatalf("wide cap %d above the global cap", c.MaxSessionsPerWide)
	}
	get2, err := FromEnv(func(k string) string {
		if k == "GOJIBERRY_DOCSTORE_PUBLIC_MAX_SESSIONS_PER_WIDE" {
			return "77"
		}
		return ""
	})
	if err != nil || get2.MaxSessionsPerWide != 77 {
		t.Fatalf("%+v %v", get2, err)
	}
}

func TestTheReserveIsJudgedAtTheCoarsestLevel(t *testing.T) {
	sessions := NewMemorySessionStore(50)
	e := newEnv(t, func(o *Options) {
		behindProxy(o)
		o.Sessions = sessions
		o.Config.MaxSessions = 50
		o.Config.MaxSessionsPerAddress = 2
		o.Config.MaxSessionsPerNet = 6
		o.Config.MaxSessionsPerWide = 50 // no third-level cap in the way: the reserve alone is under test
	})
	// the open 40 are used by an attacker in 2001:db8::/32 across seven /48s
	for p := 0; p < 7; p++ {
		for i := 0; i < 6 && sessions.Len() < 40; i++ {
			sessionStatus(e, fmt.Sprintf("2001:db8:%x:%x::1", 0x100+p, i))
		}
	}
	if sessions.Len() != 40 {
		t.Fatalf("%d sessions", sessions.Len())
	}
	// a /48 of its own, inside the same /32, is not a newcomer: the reserve is not for it
	if st := sessionStatus(e, "2001:db8:ffff::1"); st != 503 {
		t.Fatalf("a fresh /48 of a /32 that holds sessions got %d in the reserve, want 503", st)
	}
	// a visitor from another /32 (or another /16) is
	for _, ip := range []string{"2001:dc9::1", "198.51.100.1", "203.0.113.1"} {
		if st := sessionStatus(e, ip); st != 201 {
			t.Fatalf("a newcomer from %s got %d", ip, st)
		}
	}
	// the same at the store: a session with only a Net is judged by its Net
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st := NewMemorySessionStore(10)
	caps := SessionCaps{PerWide: 8, ReservePercent: 20}
	for i := 0; i < 8; i++ {
		if err := st.Create(ctx, fmt.Sprint("a", i), Session{Net: fmt.Sprint("N", i), Wide: "W1", Expires: now.Add(time.Hour)}, now, SessionCaps{ReservePercent: 20}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Create(ctx, "x", Session{Net: "N-new", Wide: "W1", Expires: now.Add(time.Hour)}, now, caps); err != ErrWideSessionsFull {
		t.Fatalf("the wide cap: %v", err)
	}
	if err := st.Create(ctx, "y", Session{Net: "N-new", Wide: "W1", Expires: now.Add(time.Hour)}, now, SessionCaps{ReservePercent: 20}); err != ErrSessionsFull {
		t.Fatalf("a new net in a known wide prefix inside the reserve: %v", err)
	}
	if err := st.Create(ctx, "z", Session{Net: "N-new", Wide: "W2", Expires: now.Add(time.Hour)}, now, SessionCaps{ReservePercent: 20}); err != nil {
		t.Fatalf("a new wide prefix inside the reserve: %v", err)
	}
}

// ---- 7. version snapshots are counted when they are written

func TestVersionSnapshotsAreCountedAtWriteTime(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.MaxTotalBytes = 100 })
	a, _ := e.session()
	d := e.create(a, strings.Repeat("a", 60))
	// a snapshot of the document is saved through the store (as the saver does): no sweep follows
	if err := e.store.Save(context.Background(), versionsKeyPrefix+d.ID+"/1.open", bytes.Repeat([]byte("v"), 40)); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Health().StorageBytes; got != 100 {
		t.Fatalf("StorageBytes = %d right after the snapshot, want 100", got)
	}
	b, _ := e.session()
	e.want(e.do("POST", "/public/doc", b, []byte("x")), 503) // the budget is used up, before any sweep
	// a sweep that lists it agrees, and discarding the document takes its snapshots off the count
	if _, err := e.svc.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Health().StorageBytes; got != 100 {
		t.Fatalf("after the sweep %d, want 100", got)
	}
	e.want(e.do("DELETE", "/public/doc", a, nil), 204)
	if got := e.svc.Health().StorageBytes; got != 0 {
		t.Fatalf("after the discard %d, want 0", got)
	}
	// keys that are not snapshots of a public document are ignored
	e.svc.NoteVersionWrite("versions/other/xx", 50)
	e.svc.NoteVersionWrite("pubdoc/"+d.ID, 50)
	e.svc.NoteVersionWrite(versionsKeyPrefix+"not-an-id/1", 50)
	if got := e.svc.Health().StorageBytes; got != 0 {
		t.Fatalf("%d counted for keys that are no snapshots", got)
	}
}

func TestSnapshotWrittenWhileAReconcileListsIsNotLost(t *testing.T) {
	var b budget
	r, _ := b.reserve("doc", 30, 30, 1000)
	r.commit()
	w := b.begin()
	b.addExtra("doc", 40) // written while the listing runs; the listing predates it
	b.addExtra("late", 5) // a snapshot of a document the ledger does not know
	b.set(w, map[string]budgetEntry{"doc": {doc: 30}})
	if used, _ := b.get(); used != 75 {
		t.Fatalf("used %d, want 30 + 40 + 5", used)
	}
	w = b.begin()
	b.release("doc") // a removal during the listing wins, snapshots included
	b.set(w, map[string]budgetEntry{"doc": {doc: 30, extra: 40}, "late": {extra: 5}})
	if used, _ := b.get(); used != 5 {
		t.Fatalf("used %d after the removal, want 5", used)
	}
	b.addExtra("doc", 0)
	b.addExtra("doc", -3)
	if used, _ := b.get(); used != 5 {
		t.Fatalf("used %d", used)
	}
}

// ---- 9. the session store does not scan under its lock on a refusal

func TestRefusedCreatesDoNotScanTheSessionStore(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const n = 20000
	st := NewMemorySessionStore(n)
	for i := 0; i < n; i++ {
		if err := st.Create(ctx, fmt.Sprint("k", i), Session{Client: fmt.Sprint("c", i), Net: fmt.Sprint("n", i), Wide: fmt.Sprint("w", i), Expires: now.Add(time.Hour)}, now, SessionCaps{}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	for i := 0; i < n; i++ { // a flood of refused creates at the cap
		if err := st.Create(ctx, "x", Session{Client: "x", Expires: now.Add(time.Hour)}, now, SessionCaps{}); err != ErrSessionsFull {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("%d refused creates over %d sessions took %v: each one walks the store", n, n, d)
	}
}

func TestMemorySessionStoreExpiresThroughItsHeap(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st := NewMemorySessionStore(10)
	mk := func(k string, d time.Duration) {
		t.Helper()
		if err := st.Create(ctx, k, Session{Client: "c", Net: "n", Wide: "w", Expires: now.Add(d)}, now, SessionCaps{PerClient: 5}); err != nil {
			t.Fatal(err)
		}
	}
	mk("a", time.Minute)
	mk("b", time.Hour)
	mk("c", 2*time.Hour)
	if err := st.Extend(ctx, "a", now.Add(3*time.Hour), now); err != nil { // a is the longest now
		t.Fatal(err)
	}
	later := now.Add(90 * time.Minute)
	if n, _ := st.Reap(ctx, later); n != 1 { // only b
		t.Fatalf("reaped %d, want 1", n)
	}
	if _, ok, _ := st.Get(ctx, "a", later); !ok {
		t.Fatal("the extended session was reaped")
	}
	if _, ok, _ := st.Get(ctx, "b", later); ok {
		t.Fatal("an expired session is alive")
	}
	if n := st.Len(); n != 2 {
		t.Fatalf("%d sessions", n)
	}
	// a create reaps what has expired, so expired sessions never count against the caps
	if err := st.Create(ctx, "d", Session{Client: "c", Net: "n", Wide: "w", Expires: later.Add(time.Hour)}, now.Add(4*time.Hour), SessionCaps{PerClient: 1}); err != nil {
		t.Fatal(err)
	}
	if n := st.Len(); n != 1 {
		t.Fatalf("%d sessions after everything else expired", n)
	}
	if n := func() int {
		st.mu.Lock()
		defer st.mu.Unlock()
		return len(st.byWide) + len(st.byNet) + len(st.byClient) + len(st.expiry)
	}(); n != 4 {
		t.Fatalf("counters left behind: %d", n)
	}
}

// A snapshot that would pass the byte budget is refused, one that fits is counted, and a deleted
// one comes off the count at once (no reconcile needed).
func TestVersionSnapshotIsRefusedOverTheBudgetAndSubtractedWhenDeleted(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Config.MaxTotalBytes = 100 })
	a, _ := e.session()
	d := e.create(a, strings.Repeat("a", 60))
	ctx := context.Background()
	key := func(n string) string { return versionsKeyPrefix + d.ID + "/" + n + ".open" }

	err := e.store.Save(ctx, key("big"), bytes.Repeat([]byte("v"), 50))
	if !errors.Is(err, docstore.ErrStorageFull) {
		t.Fatalf("a snapshot over the budget: %v, want ErrStorageFull", err)
	}
	if _, err := e.store.Load(ctx, key("big")); !errors.Is(err, docstore.ErrNotFound) {
		t.Fatalf("the refused snapshot was stored: %v", err)
	}
	if got := e.svc.Health().StorageBytes; got != 60 {
		t.Fatalf("StorageBytes = %d after a refused snapshot, want 60", got)
	}
	if err := e.store.Save(ctx, key("fits"), bytes.Repeat([]byte("v"), 40)); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Health().StorageBytes; got != 100 {
		t.Fatalf("StorageBytes = %d, want 100", got)
	}
	if err := e.store.Delete(ctx, key("fits")); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Health().StorageBytes; got != 60 {
		t.Fatalf("StorageBytes = %d right after the delete, want 60", got)
	}
	// the room is usable again
	if err := e.store.Save(ctx, key("again"), bytes.Repeat([]byte("v"), 40)); err != nil {
		t.Fatalf("after the delete: %v", err)
	}
}

// A store that wraps the ExpiringAdapter and names it (ExpiringHolder) gets the snapshot hooks too.
type holder struct{ *docstore.ExpiringAdapter }

func (h holder) Expiring() *docstore.ExpiringAdapter { return h.ExpiringAdapter }

func TestAWrappedExpiringAdapterGetsTheSnapshotHooksThroughExpiringHolder(t *testing.T) {
	e := newEnv(t, func(o *Options) {
		o.Config.MaxTotalBytes = 100
		o.Store = holder{o.Store.(*docstore.ExpiringAdapter)}
	})
	a, _ := e.session()
	d := e.create(a, strings.Repeat("a", 60))
	err := e.store.Save(context.Background(), versionsKeyPrefix+d.ID+"/1.open", bytes.Repeat([]byte("v"), 50))
	if !errors.Is(err, docstore.ErrStorageFull) {
		t.Fatalf("a snapshot over the budget through a wrapped store: %v", err)
	}
	if err := e.store.Save(context.Background(), versionsKeyPrefix+d.ID+"/2.open", bytes.Repeat([]byte("v"), 40)); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Health().StorageBytes; got != 100 {
		t.Fatalf("StorageBytes = %d, want 100", got)
	}
}
