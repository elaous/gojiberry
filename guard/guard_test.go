package guard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

func do(g Guard, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	g.Wrap(ok).ServeHTTP(rec, req)
	return rec
}

func TestOpenIsWildcard(t *testing.T) {
	rec := do(Open(), "POST", "/x", map[string]string{"Origin": "https://evil.example"})
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if rec := do(Open(), "OPTIONS", "/x", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("preflight %d", rec.Code)
	}
}

func TestOriginsAllowsListedAndRefusesOthers(t *testing.T) {
	g := Origins("https://app.example.com/", "http://localhost:3000")
	rec := do(g, "POST", "/x", map[string]string{"Origin": "https://app.example.com"})
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" || rec.Header().Get("Vary") != "Origin" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	for _, origin := range []string{"https://evil.example", "https://app.example.com.evil.example", "null", "http://app.example.com"} {
		for _, method := range []string{"GET", "POST", "OPTIONS"} {
			if rec := do(g, method, "/x", map[string]string{"Origin": origin}); rec.Code != http.StatusForbidden || rec.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Errorf("%s from %q: %d %v", method, origin, rec.Code, rec.Header())
			}
		}
	}
	if rec := do(g, "POST", "/x", nil); rec.Code != 200 {
		t.Fatalf("a request with no Origin (a program, not a page): %d", rec.Code)
	}
	if rec := do(g, "OPTIONS", "/x", map[string]string{"Origin": "http://localhost:3000"}); rec.Code != http.StatusNoContent {
		t.Fatalf("preflight from an allowed origin: %d", rec.Code)
	}
}

func TestOriginsStar(t *testing.T) {
	rec := do(Origins("*"), "GET", "/x", map[string]string{"Origin": "https://anything"})
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
}

func TestTokenRequiresTheSecret(t *testing.T) {
	g := Token("s3cret", Options{Public: []string{"/health"}})
	for name, hdr := range map[string]map[string]string{
		"none": nil, "wrong": {"Authorization": "Bearer nope"}, "scheme": {"Authorization": "Basic s3cret"}, "prefix": {"Authorization": "Bearer s3cret2"},
	} {
		if rec := do(g, "POST", "/x", hdr); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	rec := do(g, "POST", "/x", map[string]string{"Authorization": "Bearer s3cret", "Origin": "https://any.example"})
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if rec := do(g, "GET", "/health", nil); rec.Code != 200 {
		t.Fatalf("public path: %d", rec.Code)
	}
	if rec := do(g, "OPTIONS", "/x", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("preflight: %d", rec.Code)
	}
	if rec := do(Token(""), "GET", "/x", map[string]string{"Authorization": "Bearer "}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("an empty secret must refuse everything: %d", rec.Code)
	}
}

func TestChainNeedsBoth(t *testing.T) {
	g := Chain(Origins("https://app.example.com"), Token("t"))
	good := map[string]string{"Origin": "https://app.example.com", "Authorization": "Bearer t"}
	rec := do(g, "POST", "/x", good)
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	if rec := do(g, "POST", "/x", map[string]string{"Origin": "https://app.example.com"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := do(g, "POST", "/x", map[string]string{"Origin": "https://evil.example", "Authorization": "Bearer t"}); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong origin: %d", rec.Code)
	}
}

func TestTokenFuncAndOr(t *testing.T) {
	g := TokenFunc(func(r *http.Request) bool { return r.Header.Get("X-Key") == "k" })
	if do(g, "GET", "/x", map[string]string{"X-Key": "k"}).Code != 200 || do(g, "GET", "/x", nil).Code != 401 {
		t.Fatal("custom check")
	}
	if Or(nil) == nil {
		t.Fatal("Or(nil) must give the default")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("T_ALLOW_ORIGINS", "")
	t.Setenv("T_TOKEN", "")
	if rec := do(FromEnv("T"), "POST", "/x", map[string]string{"Origin": "https://x"}); rec.Code != 200 {
		t.Fatalf("neither set: open, got %d", rec.Code)
	}
	t.Setenv("T_ALLOW_ORIGINS", "https://a.example, https://b.example")
	if do(FromEnv("T"), "GET", "/x", map[string]string{"Origin": "https://b.example"}).Code != 200 || do(FromEnv("T"), "GET", "/x", map[string]string{"Origin": "https://c.example"}).Code != 403 {
		t.Fatal("origins from the environment")
	}
	t.Setenv("T_TOKEN", "tok")
	g := FromEnv("T", "/health")
	if do(g, "GET", "/health", nil).Code != 200 || do(g, "GET", "/x", map[string]string{"Origin": "https://a.example"}).Code != 401 {
		t.Fatal("token from the environment, health public")
	}
}

func TestTokenHeader(t *testing.T) {
	g := Token("tok", Options{TokenHeader: "X-Gojiberry-Token"})
	if do(g, "GET", "/x", map[string]string{"X-Gojiberry-Token": "tok"}).Code != 200 {
		t.Fatal("custom header")
	}
	if do(g, "GET", "/x", map[string]string{"Authorization": "Bearer tok"}).Code != 401 {
		t.Fatal("Authorization must not satisfy a custom-header guard")
	}
	rec := do(g, "OPTIONS", "/x", nil)
	if h := rec.Header().Get("Access-Control-Allow-Headers"); h != "Content-Type, Authorization, X-Gojiberry-Token" {
		t.Fatalf("allow headers %q", h)
	}
}

func TestTokenQueryForBrowserSockets(t *testing.T) {
	g := Token("tok", Options{TokenHeader: "X-Gojiberry-Token", TokenQuery: "guard_token"})
	if do(g, "GET", "/docs/d/ws?guard_token=tok", nil).Code != 200 || do(g, "GET", "/docs/d/ws?guard_token=bad", nil).Code != 401 || do(g, "GET", "/docs/d/ws", nil).Code != 401 {
		t.Fatal("the token as a query parameter")
	}
	if do(Token("tok"), "GET", "/x?guard_token=tok", nil).Code != 401 {
		t.Fatal("without TokenQuery a query parameter is not a token")
	}
}
