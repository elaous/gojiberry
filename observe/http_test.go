package observe

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func okHandler(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte("body"))
	})
}

func serve(o *Observer, h http.Handler, method, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, nil)
	Middleware(o, h).ServeHTTP(w, req)
	return w
}

func TestMiddlewareRecordsRouteTemplateMethodAndStatusClass(t *testing.T) {
	o, r := newObs()
	serve(o, okHandler(404), "GET", "/docs/3f2a-secret-document-id")
	serve(o, okHandler(204), "PUT", "/docs/another-id")
	serve(o, okHandler(429), "POST", "/auth/passkey/login/finish")
	serve(o, okHandler(500), "GET", "/ready")
	serve(o, okHandler(200), "PROPFIND", "/nowhere/at/all")
	pts, spans := r.snapshot()
	got := map[string]float64{}
	for _, p := range pts {
		k := string(p.Metric)
		for _, l := range p.Labels {
			k += "|" + l.Name + "=" + l.Value
		}
		if p.Kind == Gauge {
			got[k] = p.Value
		} else {
			got[k] += p.Value
		}
	}
	for k, want := range map[string]float64{
		"http_requests_total|route=/docs/{id}|method=GET|status_class=4xx":       1,
		"http_requests_total|route=/docs/{id}|method=PUT|status_class=2xx":       1,
		"http_requests_total|route=/auth/passkey/*|method=POST|status_class=4xx": 1,
		"http_requests_total|route=/ready|method=GET|status_class=5xx":           1,
		"http_requests_total|route=other|method=other|status_class=2xx":          1,
		"auth_outcomes_total|flow=passkey|outcome=limited":                       1,
		"errors_total|code=server_error":                                         1,
		"http_inflight_requests":                                                 0,
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v\n%v", k, got[k], want, got)
		}
	}
	if len(spans) != 5 {
		t.Fatalf("spans = %d", len(spans))
	}
	for _, s := range spans {
		for _, a := range s.Attrs {
			if a.Key == "route" && strings.Contains(a.Str, "secret") {
				t.Fatal("an id reached a span")
			}
		}
	}
}

func TestMiddlewareCountsAPanicAndRethrowsIt(t *testing.T) {
	o, r := newObs()
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	func() {
		defer func() {
			if recover() == nil {
				t.Error("the panic was swallowed")
			}
		}()
		Middleware(o, h).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/docs/x", nil))
	}()
	if r.count(Errors) != 1 || r.count(HTTPRequests) != 1 {
		t.Fatal("the panic was not counted")
	}
}

func TestMiddlewareIsTheHandlerItselfWhenOff(t *testing.T) {
	h := okHandler(200)
	if fmt.Sprintf("%p", Middleware(nil, h)) != fmt.Sprintf("%p", h) {
		t.Fatal("a nil observer must not wrap the handler")
	}
	if fmt.Sprintf("%p", Middleware(New(Options{}), h)) != fmt.Sprintf("%p", h) {
		t.Fatal("an observer without sinks must not wrap the handler")
	}
}

func TestMiddlewarePassesFlushThrough(t *testing.T) {
	o, _ := newObs()
	flushed := false
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.(http.Flusher).Flush()
		flushed = true
	})
	Middleware(o, h).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/health", nil))
	if !flushed {
		t.Fatal("Flush is not available through the wrapper")
	}
}

func TestRouteOfIsAlwaysAnAllowedTemplate(t *testing.T) {
	for path, want := range map[string]string{
		"/docs": "/docs", "/docs/": "/docs", "/DOCS/abc": "/docs/{id}", "/docs/a/b/c": "/docs/{id}", "/docs/x/acl": "/docs/{id}/acl",
		"/docs/x/renew": "/docs/{id}/renew", "/auth/google/login": "/auth/{provider}/login", "/auth/evil-name/callback": "/auth/{provider}/callback",
		"/auth/me": "/auth/me", "/auth/passkey/register/begin": "/auth/passkey/*", "/auth/zzz": "/auth/other",
		"/directory/lookup": "/directory/*", "/admin/usage": "/admin/*", "/telemetry": "/telemetry", "/me/config": "/me/config",
		"/public/view/abc": "/public/*", "/": "other", "": "other", "/unknown/ip/10.0.0.1": "other",
	} {
		if got := RouteOf(path); got != want {
			t.Errorf("RouteOf(%q) = %q, want %q", path, got, want)
		}
	}
}

func FuzzRouteOf(f *testing.F) {
	for _, s := range []string{"/docs/x", "/auth/a/login", "/%00", "/docs/\xff\xfe", strings.Repeat("/a", 500), "/ADMIN/x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, path string) {
		route := RouteOf(path)
		if !labelAllowed["route"][route] {
			t.Fatalf("RouteOf(%q) = %q, not on the allowlist", path, route)
		}
	})
}

// fuzzBytes returns n random printable-ish bytes, including separators and non-ASCII.
func fuzzString(rng *rand.Rand, n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789-_./:@%?=&+ \té中"
	rs := []rune(alphabet)
	var b strings.Builder
	for range n {
		b.WriteRune(rs[rng.IntN(len(rs))])
	}
	return b.String()
}

// TestNothingRequestDerivedReachesAnySink is the no-PII test: requests with random paths, methods,
// headers, queries and bodies, carrying markers where an address, an id, a token or a user agent
// would be, go through the middleware into a sink that records everything. Every label and attribute
// that arrives must be an enumerated value, and no marker may appear anywhere in what the sinks got.
func TestNothingRequestDerivedReachesAnySink(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	b := NewBuiltin()
	r := &rec{}
	o := New(Options{Operator: Fanout(b, r), Usage: r, TraceSample: 1})
	o.SetUsageAllowlist(map[string][]string{"feature": {"find"}})
	markers := []string{"203.0.113.77", "2001:db8::dead:beef", "MARKER-DOC-ID-9f8e7d", "Bearer MARKER-TOKEN-abcdef", "MARKER-SESSION-COOKIE",
		"Mozilla/5.0 MARKER-UA", "alice@example.com", "MARKER-QUERY-VALUE", "MARKER-BODY-TEXT", "MARKER-SHARE-KEY"}
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// a handler that touches everything a real one could
		_ = req.RemoteAddr
		_ = req.Header.Get("Authorization")
		code := []int{200, 204, 301, 400, 401, 403, 404, 429, 500, 503}[rng.IntN(10)]
		w.Header().Set("X-Echo", req.Header.Get("User-Agent"))
		w.WriteHeader(code)
	})
	prefixes := []string{"/docs/", "/docs", "/auth/", "/auth/passkey/", "/directory/", "/admin/", "/public/", "/me/config/", "/telemetry", "/x/", "/", "/ready", "/health", "/quota"}
	methods := []string{"GET", "PUT", "POST", "DELETE", "HEAD", "OPTIONS", "PATCH", "TRACE", "BREW"}
	for i := range 3000 {
		m := markers[rng.IntN(len(markers))]
		path := prefixes[rng.IntN(len(prefixes))] + m + fuzzString(rng, rng.IntN(30)) + []string{"", "/acl", "/renew", "/login", "/callback"}[rng.IntN(5)]
		target := (&url.URL{Path: path, RawQuery: "q=" + url.QueryEscape(markers[rng.IntN(len(markers))]+fuzzString(rng, 8))}).String()
		req := httptest.NewRequest(methods[rng.IntN(len(methods))], target, strings.NewReader(markers[rng.IntN(len(markers))]))
		req.RemoteAddr = markers[rng.IntN(2)] + ":4242"
		req.Header.Set("Authorization", markers[3])
		req.Header.Set("Cookie", "s="+markers[4])
		req.Header.Set("User-Agent", markers[5])
		req.Header.Set("X-Forwarded-For", markers[rng.IntN(2)])
		req.Header.Set("X-Gojiberry-Token", markers[9])
		req.Header.Set("Referer", "https://example.com/"+markers[2])
		Middleware(o, h).ServeHTTP(httptest.NewRecorder(), req)
		if i%500 == 0 {
			o.Usage("feature", "find", 1)
		}
	}
	pts, spans := r.snapshot()
	if len(pts) == 0 || len(spans) == 0 {
		t.Fatal("nothing was recorded")
	}
	for _, p := range pts {
		d := defByName[p.Metric]
		if d == nil || d.Kind != p.Kind || len(p.Labels) != len(d.Labels) {
			t.Fatalf("a point is not of the schema: %+v", p)
		}
		for i, l := range p.Labels {
			if usageLabels[l.Name] {
				if l.Name == "fact" && l.Value != "feature" || l.Name == "choice" && l.Value != "find" {
					t.Fatalf("usage label %+v is not what was registered", l)
				}
				continue
			}
			if l.Name != d.Labels[i] || !labelAllowed[l.Name][l.Value] {
				t.Fatalf("label %+v of %s is not an allowed value", l, p.Metric)
			}
		}
	}
	for _, s := range spans {
		sd := spanByName[s.Name]
		if sd == nil {
			t.Fatalf("unknown span %s", s.Name)
		}
		for _, a := range s.Attrs {
			ad := attrDefs[a.Key]
			if ad.numeric != a.IsNum {
				t.Fatalf("attribute %+v has the wrong type", a)
			}
			if ad.numeric && (a.Num < ad.lo || a.Num > ad.hi) {
				t.Fatalf("attribute %+v is out of range", a)
			}
			if !ad.numeric && !labelAllowed[ad.enumFrom][a.Str] {
				t.Fatalf("attribute %+v is not an allowed value", a)
			}
		}
	}
	// and no marker anywhere: not in the points, the spans, or the built-in sink's totals
	dump, _ := json.Marshal([]any{pts, spans, b.Describe(), o.Describe()})
	for _, m := range markers {
		if strings.Contains(string(dump), m) {
			t.Fatalf("%q reached a sink", m)
		}
	}
	for _, frag := range []string{"203.0.113", "MARKER", "alice", "Mozilla", "dead:beef", "example.com"} {
		if strings.Contains(string(dump), frag) {
			t.Fatalf("a fragment %q of request data reached a sink", frag)
		}
	}
	if len(o.Dropped()) != 0 {
		t.Fatalf("the middleware itself tried to send something outside the allowlist: %v", o.Dropped())
	}
}
