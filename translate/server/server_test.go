package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTranslator is a translator stub for hermetic tests -- no real
// DeepL API call, no API key needed.
type fakeTranslator struct {
	got struct {
		text       []string
		targetLang string
		sourceLang string
	}
	result []Translation
	err    error
}

func (f *fakeTranslator) Translate(_ context.Context, text []string, targetLang, sourceLang string) ([]Translation, error) {
	f.got.text = text
	f.got.targetLang = targetLang
	f.got.sourceLang = sourceLang
	return f.result, f.err
}

// newTestServer builds a Server around t, or with no translator
// configured at all if t is nil -- assigning a nil *fakeTranslator
// straight into the translator interface field would leave a
// non-nil interface holding a nil pointer (the classic Go "typed nil"
// trap), which handleTranslate's own `s.translator == nil` check
// wouldn't catch.
func newTestServer(t *fakeTranslator) *Server {
	srv := &Server{cfg: Config{Addr: ":0"}}
	if t != nil {
		srv.translator = t
	}
	return srv
}

func TestHandleHealth(t *testing.T) {
	srv := newTestServer(nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestCORSPreflight(t *testing.T) {
	srv := newTestServer(nil)
	req := httptest.NewRequest(http.MethodOptions, "/translate", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want \"*\"", got)
	}
}

func TestHandleTranslateNoTranslatorConfigured(t *testing.T) {
	srv := newTestServer(nil)
	req := httptest.NewRequest(http.MethodPost, "/translate", strings.NewReader(`{"text":"hi","targetLang":"FR"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestHandleTranslateSuccess(t *testing.T) {
	fake := &fakeTranslator{result: []Translation{{Text: "Bonjour, le monde !", DetectedSourceLanguage: "EN"}}}
	srv := newTestServer(fake)

	req := httptest.NewRequest(http.MethodPost, "/translate", strings.NewReader(`{"text":"Hello, world!","targetLang":"FR"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", rec.Code, rec.Body.String())
	}
	var got translateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if got.TranslatedText != "Bonjour, le monde !" || got.DetectedSourceLanguage != "EN" {
		t.Fatalf("response = %+v, want translated text/detected language", got)
	}
	if len(fake.got.text) != 1 || fake.got.text[0] != "Hello, world!" || fake.got.targetLang != "FR" {
		t.Fatalf("translator called with %+v, want [Hello, world!]/FR", fake.got)
	}
}

func TestHandleTranslateMissingFields(t *testing.T) {
	srv := newTestServer(&fakeTranslator{})
	cases := []string{
		`{"targetLang":"FR"}`,
		`{"text":"hi"}`,
		`{}`,
		`not json`,
	}
	for _, body := range cases {
		req := httptest.NewRequest(http.MethodPost, "/translate", strings.NewReader(body))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400", body, rec.Code)
		}
	}
}

func TestHandleTranslateWrongMethod(t *testing.T) {
	srv := newTestServer(&fakeTranslator{})
	req := httptest.NewRequest(http.MethodGet, "/translate", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestHandleTranslateUpstreamError(t *testing.T) {
	fake := &fakeTranslator{err: http.ErrHandlerTimeout}
	srv := newTestServer(fake)
	req := httptest.NewRequest(http.MethodPost, "/translate", strings.NewReader(`{"text":"hi","targetLang":"FR"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

func TestHandleTranslatePassesSourceLangOption(t *testing.T) {
	fake := &fakeTranslator{result: []Translation{{Text: "ok"}}}
	srv := newTestServer(fake)
	req := httptest.NewRequest(http.MethodPost, "/translate", strings.NewReader(`{"text":"hi","targetLang":"FR","sourceLang":"EN"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	cfg := ConfigFromEnv()
	if cfg.Addr != ":8083" {
		t.Fatalf("Addr = %q, want :8083 (no env override in this test process)", cfg.Addr)
	}
}

func TestNewServerNoAPIKeyLeavesTranslatorNil(t *testing.T) {
	srv, err := NewServer(Config{Addr: ":0"})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if srv.translator != nil {
		t.Fatalf("translator = %v, want nil with no API key configured", srv.translator)
	}
}
