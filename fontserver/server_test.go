package fontserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.FontsDir == "" {
		cfg.FontsDir = t.TempDir()
	}
	return NewServer(cfg)
}

func TestHealthEndpoint(t *testing.T) {
	srv := newTestServer(t, Config{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health = %d, want 200", rec.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	srv := newTestServer(t, Config{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/local/fonts", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS /local/fonts = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want \"*\"", got)
	}
}

func TestLocalFontsAndFileServing(t *testing.T) {
	dir := t.TempDir()
	writeTestFont(t, dir, "a.ttf", "Test Sans", "Regular")
	srv := newTestServer(t, Config{FontsDir: dir})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/local/fonts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /local/fonts = %d, want 200", rec.Code)
	}
	var body struct {
		Families []Family `json:"families"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Families) != 1 || body.Families[0].Name != "Test Sans" {
		t.Fatalf("families = %+v, want [Test Sans]", body.Families)
	}
	relPath := body.Families[0].Variants[0].Path

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/local/files/"+relPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /local/files/%s = %d, want 200", relPath, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "font/ttf" {
		t.Fatalf("Content-Type = %q, want font/ttf", ct)
	}
	if rec.Body.Len() == 0 {
		t.Fatal("served font file body is empty")
	}
}

func TestLocalFileServing404sUnknownPath(t *testing.T) {
	srv := newTestServer(t, Config{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/local/files/nope.ttf", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /local/files/nope.ttf = %d, want 404", rec.Code)
	}
}

func TestLocalRescanPicksUpNewFiles(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t, Config{FontsDir: dir})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/local/fonts", nil))
	var before struct {
		Families []Family `json:"families"`
	}
	json.Unmarshal(rec.Body.Bytes(), &before)
	if len(before.Families) != 0 {
		t.Fatalf("expected no families before any font exists, got %+v", before.Families)
	}

	writeTestFont(t, dir, "a.ttf", "Test Sans", "Regular")

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/local/rescan", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /local/rescan = %d, want 200", rec.Code)
	}
	var after struct {
		Families []Family `json:"families"`
	}
	json.Unmarshal(rec.Body.Bytes(), &after)
	if len(after.Families) != 1 {
		t.Fatalf("expected 1 family after rescan, got %+v", after.Families)
	}
}

func TestGoogleFontsDisabledWithoutKey(t *testing.T) {
	srv := newTestServer(t, Config{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/google/fonts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /google/fonts (no key) = %d, want 200", rec.Code)
	}
	var body struct {
		Enabled  bool         `json:"enabled"`
		Families []googleFont `json:"families"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Enabled {
		t.Fatal("enabled = true with no API key configured, want false")
	}
	if len(body.Families) != 0 {
		t.Fatalf("families = %+v, want empty with no API key", body.Families)
	}
}

func TestGoogleFontsFetchesAndCaches(t *testing.T) {
	srv := newTestServer(t, Config{GoogleAPIKey: "test-key", GoogleCacheTTL: time.Hour})
	calls := 0
	srv.fetchGoogleFonts = func(apiKey string) ([]googleFont, error) {
		calls++
		if apiKey != "test-key" {
			t.Errorf("fetchGoogleFonts called with key %q, want test-key", apiKey)
		}
		return []googleFont{{Family: "Roboto", Category: "sans-serif"}}, nil
	}

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/google/fonts", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /google/fonts = %d, want 200", rec.Code)
		}
		var body struct {
			Enabled  bool         `json:"enabled"`
			Families []googleFont `json:"families"`
		}
		json.Unmarshal(rec.Body.Bytes(), &body)
		if !body.Enabled || len(body.Families) != 1 || body.Families[0].Family != "Roboto" {
			t.Fatalf("iteration %d: families = %+v, want [Roboto]", i, body.Families)
		}
	}
	if calls != 1 {
		t.Fatalf("fetchGoogleFonts called %d times within the cache TTL, want 1", calls)
	}
}

func TestGoogleFontsRefetchesAfterTTLExpires(t *testing.T) {
	srv := newTestServer(t, Config{GoogleAPIKey: "test-key", GoogleCacheTTL: time.Millisecond})
	calls := 0
	srv.fetchGoogleFonts = func(apiKey string) ([]googleFont, error) {
		calls++
		return []googleFont{{Family: fmt.Sprintf("Family%d", calls)}}, nil
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/google/fonts", nil))
	time.Sleep(5 * time.Millisecond)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/google/fonts", nil))

	if calls < 2 {
		t.Fatalf("fetchGoogleFonts called %d times, want at least 2 after the TTL expired", calls)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv := newTestServer(t, Config{})
	cases := []struct {
		method, path string
	}{
		{http.MethodPost, "/local/fonts"},
		{http.MethodGet, "/local/rescan"},
		{http.MethodPost, "/local/files/a.ttf"},
		{http.MethodPost, "/google/fonts"},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", c.method, c.path, rec.Code)
		}
	}
}

func TestFontContentType(t *testing.T) {
	if got := fontContentType("Family/Regular.otf"); got != "font/otf" {
		t.Errorf("fontContentType(.otf) = %q, want font/otf", got)
	}
	if got := fontContentType("Family/Regular.OTF"); got != "font/otf" {
		t.Errorf("fontContentType(.OTF) = %q, want font/otf (case-insensitive)", got)
	}
	if got := fontContentType("Family/Regular.ttf"); got != "font/ttf" {
		t.Errorf("fontContentType(.ttf) = %q, want font/ttf", got)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("GOJIBERRY_FONTSERVER_ADDR", ":9999")
	t.Setenv("GOJIBERRY_FONTSERVER_DIR", "/tmp/some-fonts")
	t.Setenv("GOJIBERRY_FONTSERVER_GOOGLE_KEY", "my-key")
	t.Setenv("GOJIBERRY_FONTSERVER_GOOGLE_CACHE_TTL", "2h")

	cfg := ConfigFromEnv()
	if cfg.Addr != ":9999" || cfg.FontsDir != "/tmp/some-fonts" || cfg.GoogleAPIKey != "my-key" || cfg.GoogleCacheTTL != 2*time.Hour {
		t.Fatalf("ConfigFromEnv() = %+v, want overrides from environment", cfg)
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	t.Setenv("GOJIBERRY_FONTSERVER_ADDR", "")
	t.Setenv("GOJIBERRY_FONTSERVER_DIR", "")
	t.Setenv("GOJIBERRY_FONTSERVER_GOOGLE_KEY", "")
	t.Setenv("GOJIBERRY_FONTSERVER_GOOGLE_CACHE_TTL", "")

	cfg := ConfigFromEnv()
	if cfg.Addr != ":8082" || cfg.FontsDir != "./fonts" || cfg.GoogleAPIKey != "" || cfg.GoogleCacheTTL != 24*time.Hour {
		t.Fatalf("ConfigFromEnv() = %+v, want documented defaults", cfg)
	}
}

func TestGoogleFontsFetchFailureDegradesGracefully(t *testing.T) {
	srv := newTestServer(t, Config{GoogleAPIKey: "test-key", GoogleCacheTTL: time.Hour})
	srv.fetchGoogleFonts = func(apiKey string) ([]googleFont, error) {
		return nil, fmt.Errorf("boom: simulated network failure")
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/google/fonts", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /google/fonts (fetch failure) = %d, want 200 (graceful degrade, not a hard error)", rec.Code)
	}
	var body struct {
		Enabled  bool         `json:"enabled"`
		Families []googleFont `json:"families"`
	}
	json.Unmarshal(rec.Body.Bytes(), &body)
	if len(body.Families) != 0 {
		t.Fatalf("families = %+v, want empty on fetch failure with no prior cache", body.Families)
	}
}
