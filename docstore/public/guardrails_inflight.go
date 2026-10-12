package public

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ---- the cap on request bodies in flight

// inflight bounds the bytes of request bodies being read at once: in all, per client (an IPv4
// address or an IPv6 /64) and per network (a /48 or a /24). The shares are what keep a few slow
// writers from holding the whole cap and turning every write of everyone else into a 503.
type inflight struct {
	mu        sync.Mutex
	used      int64
	max       int64
	perClient int64 // 0: no per-client share
	perNet    int64 // 0: no per-network share
	byClient  map[string]int64
	byNet     map[string]int64
}

// flightResult says why acquire refused.
type flightResult int

const (
	flightOK flightResult = iota
	flightClient
	flightNet
	flightGlobal
)

func (f *inflight) acquire(client, net string, n int64) flightResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case f.used+n > f.max:
		return flightGlobal
	case f.perClient > 0 && f.byClient[client]+n > f.perClient:
		return flightClient
	case f.perNet > 0 && f.byNet[net]+n > f.perNet:
		return flightNet
	}
	f.used += n
	if f.byClient == nil {
		f.byClient, f.byNet = map[string]int64{}, map[string]int64{}
	}
	f.byClient[client] += n
	f.byNet[net] += n
	return flightOK
}

func (f *inflight) release(client, net string, n int64) {
	f.mu.Lock()
	f.used -= n
	if f.used < 0 {
		log.Printf("public docstore: in-flight counter went to %d; clamped to 0", f.used)
		f.used = 0
	}
	sub := func(m map[string]int64, k string) {
		if m == nil {
			return
		}
		if m[k] -= n; m[k] <= 0 {
			delete(m, k)
		}
	}
	sub(f.byClient, client)
	sub(f.byNet, net)
	f.mu.Unlock()
}

func (f *inflight) held() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.used
}

// heldBy is what the client holds.
func (f *inflight) heldBy(client string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byClient[client]
}

// ---- charging before the body is read

// ticket is what a write holds while it is in flight: the bytes charged to the address's daily
// quota (in the quota window it was charged in) and the bytes reserved in the in-flight caps.
// release gives back what the request did not end up writing, for a failure of the server's own
// (a store error, a refusal that is not the client's doing); a charge the client earned, a body
// that arrived too slowly or was refused for its content, is made final with keep.
type ticket struct {
	s      *Service
	key    string
	client string
	net    string
	held   int64
	win    int64 // the quota window the charge went into (WindowQuota)
	hasWin bool
	flight int64
}

// admit runs the checks that need no body, in front of reading one: a declared size over the
// cap (413), a full store (503), too many bytes of bodies in flight for the client or its network
// (429) or in all (503), the address's daily bytes (429, the declared size, or the cap for a body
// of unknown length, is charged and settle gives the difference back). The caller must
// `defer t.release()` and call t.keep() once the write is stored (or the charge is the client's to
// pay). It answers 503 while the counters are unknown (a failed first reconcile).
func (s *Service) admit(w http.ResponseWriter, r *http.Request) (*ticket, bool) {
	if r.ContentLength > s.cfg.MaxDocBytes {
		http.Error(w, "document too large", http.StatusRequestEntityTooLarge)
		return nil, false
	}
	if !s.ensureCounters(r.Context()) {
		w.Header().Set("Retry-After", strconv.Itoa(int(reconcileBackoff/time.Second)))
		http.Error(w, "the public offer is starting up; try again shortly", http.StatusServiceUnavailable)
		return nil, false
	}
	if s.bytes.full(s.cfg.MaxTotalBytes) {
		storeFull(w)
		return nil, false
	}
	n := r.ContentLength
	if n < 0 {
		n = s.cfg.MaxDocBytes
	}
	addr := s.clientIP(r)
	client, net := ClientKey(addr), NetKey(addr)
	switch s.flight.acquire(client, net, n) {
	case flightClient, flightNet:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many of your uploads in progress; try again shortly", http.StatusTooManyRequests)
		return nil, false
	case flightGlobal:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "too many uploads in progress; try again shortly", http.StatusServiceUnavailable)
		return nil, false
	}
	t := &ticket{s: s, key: "ip:" + client, client: client, net: net, held: n, flight: n}
	if n > 0 {
		var ok bool
		var retry time.Duration
		var err error
		if wq, isWin := s.quota.(WindowQuota); isWin {
			ok, t.win, retry, err = wq.TakeIn(r.Context(), t.key, n)
			t.hasWin = true
		} else {
			ok, retry, err = s.quota.Take(r.Context(), t.key, n)
		}
		if err != nil {
			t.held = 0
			t.release()
			s.fail(w, err)
			return nil, false
		}
		if !ok {
			t.held = 0
			t.release()
			tooMany(w, retry)
			return nil, false
		}
	} else {
		t.held = 0
	}
	return t, true
}

func (t *ticket) refund(n int64) {
	if n <= 0 {
		return
	}
	if q, ok := t.s.quota.(WindowQuota); ok && t.hasWin {
		q.RefundIn(context.Background(), t.key, n, t.win)
	} else if q, ok := t.s.quota.(QuotaRefunder); ok {
		q.Refund(context.Background(), t.key, n)
	}
}

// settle trims the charge, and the in-flight claim, to the bytes actually read.
func (t *ticket) settle(actual int64) {
	if t.held > actual {
		t.refund(t.held - actual)
		t.held = actual
	}
	if t.flight > actual {
		t.s.flight.release(t.client, t.net, t.flight-actual)
		t.flight = actual
	}
}

// release gives back what is still held (a write that failed or was refused) and the in-flight
// claim. A nil ticket is fine.
func (t *ticket) release() {
	if t == nil {
		return
	}
	t.refund(t.held)
	t.held = 0
	t.s.flight.release(t.client, t.net, t.flight)
	t.flight = 0
}

// keep makes the quota charge final (the in-flight claim ends with release).
func (t *ticket) keep() { t.held = 0 }

func storeFull(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "300")
	http.Error(w, "the public offer's storage is full right now; try again later", http.StatusServiceUnavailable)
}
