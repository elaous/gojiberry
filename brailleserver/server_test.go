package brailleserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(translate func(binary, table, text string) (string, error)) *Server {
	s := NewServer(Config{LouTranslateBinary: "lou_translate", Table: "en-us-g2.ctb"})
	if translate != nil {
		s.translate = translate
	}
	return s
}

func TestHealthReportsOK(t *testing.T) {
	s := newTestServer(nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestTranslateWithNoBinaryConfiguredReports503(t *testing.T) {
	s := NewServer(Config{}) // no LouTranslateBinary
	body, _ := json.Marshal(translateRequest{Text: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/translate", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with no lou_translate binary configured, got %d", w.Code)
	}
}

func TestTranslateRejectsEmptyText(t *testing.T) {
	s := newTestServer(nil)
	body, _ := json.Marshal(translateRequest{Text: ""})
	req := httptest.NewRequest(http.MethodPost, "/translate", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty text, got %d", w.Code)
	}
}

func TestTranslateReturnsBrailleFromLouTranslate(t *testing.T) {
	var gotBinary, gotTable, gotText string
	s := newTestServer(func(binary, table, text string) (string, error) {
		gotBinary, gotTable, gotText = binary, table, text
		return ",hello world", nil // liblouis's own ASCII braille for "hello world"-shaped output (illustrative, not asserted verbatim)
	})

	body, _ := json.Marshal(translateRequest{Text: "hello world"})
	req := httptest.NewRequest(http.MethodPost, "/translate", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp translateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if resp.Braille != ",hello world" {
		t.Errorf("expected the fake braille echoed back, got %q", resp.Braille)
	}
	if gotBinary != "lou_translate" || gotTable != "en-us-g2.ctb" || gotText != "hello world" {
		t.Errorf("expected translate called with (lou_translate, en-us-g2.ctb, %q), got (%s, %s, %s)", "hello world", gotBinary, gotTable, gotText)
	}
}

func TestTranslateRequestTableOverridesConfigDefault(t *testing.T) {
	var gotTable string
	s := newTestServer(func(binary, table, text string) (string, error) {
		gotTable = table
		return "", nil
	})

	body, _ := json.Marshal(translateRequest{Text: "hello", Table: "en-us-g1.ctb"})
	req := httptest.NewRequest(http.MethodPost, "/translate", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotTable != "en-us-g1.ctb" {
		t.Errorf("expected the request's own table (en-us-g1.ctb) to override the server's default, got %q", gotTable)
	}
}

func TestTranslatePropagatesLouTranslateFailureAs502(t *testing.T) {
	s := newTestServer(func(binary, table, text string) (string, error) {
		return "", errors.New("lou_translate crashed")
	})
	body, _ := json.Marshal(translateRequest{Text: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/translate", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when lou_translate fails, got %d", w.Code)
	}
}

func TestCORSHeadersPresentOnEveryResponse(t *testing.T) {
	s := newTestServer(nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected permissive CORS header, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestConfigFromEnvDefaultsTableToGrade2(t *testing.T) {
	cfg := ConfigFromEnv()
	if cfg.Table != defaultTable {
		t.Fatalf("ConfigFromEnv().Table = %q, want defaultTable (%q) with nothing set in the environment", cfg.Table, defaultTable)
	}
}
