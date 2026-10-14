package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/observe"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

func TestTranslateRejectsUnsafeTableNames(t *testing.T) {
	for _, table := range []string{"/etc/passwd", "--help", "../../x", "a b", "-x.ctb", "a;b"} {
		called := false
		s := newTestServer(func(binary, table, text string) (string, error) { called = true; return "", nil })
		body, _ := json.Marshal(translateRequest{Text: "hello", Table: table})
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/translate", bytes.NewReader(body)))
		if w.Code != http.StatusBadRequest || called {
			t.Errorf("table %q: status %d, translator called: %v", table, w.Code, called)
		}
	}
}

func TestExecLouTranslateRunsTheBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	dir := t.TempDir()
	// a stand-in for lou_translate: the table (if any) is the first argument, the text comes on stdin
	bin := filepath.Join(dir, "lou_translate")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '[%s]%s' \"$1\" \"$(cat)\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := execLouTranslate(bin, "en-ueb-g2.ctb", "hello"); err != nil || got != "[en-ueb-g2.ctb]hello" {
		t.Fatalf("with a table: %q, %v", got, err)
	}
	if got, err := execLouTranslate(bin, "", "hi"); err != nil || got != "[]hi" {
		t.Fatalf("no table: %q, %v", got, err)
	}
	failing := filepath.Join(dir, "failing")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\necho 'no such table' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := execLouTranslate(failing, "x", "hi"); err == nil || !strings.Contains(err.Error(), "no such table") {
		t.Fatalf("a failing lou_translate: %v", err)
	}
	if _, err := execLouTranslate(filepath.Join(dir, "absent"), "x", "hi"); err == nil {
		t.Fatal("a missing binary is an error")
	}
}

func TestBrailleIsReported(t *testing.T) {
	b := observe.NewBuiltin()
	old := observe.Default()
	observe.SetDefault(observe.New(observe.Options{Operator: b}))
	defer observe.SetDefault(old)
	s := newTestServer(func(string, string, string) (string, error) { return ",hello", nil })
	body, _ := json.Marshal(translateRequest{Text: "hello"})
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/translate", bytes.NewReader(body)))
	if got := b.Count(observe.WorkRuns, "work", "braille", "result", "ok"); got != 1 {
		t.Errorf("braille ok = %d, want 1", got)
	}
}
