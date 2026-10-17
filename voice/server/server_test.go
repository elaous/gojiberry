package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/elaous/gojiberry/observe"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(speak func(binary, model, text string) ([]byte, error), transcribe func(binary, model string, wav []byte) (string, error)) *Server {
	s := NewServer(Config{PiperBinary: "piper", PiperModel: "voice.onnx", WhisperBinary: "whisper-cli", WhisperModel: "ggml.bin"})
	if speak != nil {
		s.speak = speak
	}
	if transcribe != nil {
		s.transcribe = transcribe
	}
	return s
}

func TestHealthReportsOK(t *testing.T) {
	s := newTestServer(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestSpeakWithNoBinaryConfiguredReports503(t *testing.T) {
	s := NewServer(Config{}) // no PiperBinary
	body, _ := json.Marshal(speakRequest{Text: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/speak", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with no Piper binary configured, got %d", w.Code)
	}
}

func TestSpeakRejectsEmptyText(t *testing.T) {
	s := newTestServer(nil, nil)
	body, _ := json.Marshal(speakRequest{Text: ""})
	req := httptest.NewRequest(http.MethodPost, "/speak", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty text, got %d", w.Code)
	}
}

func TestSpeakReturnsAudioBytesFromPiper(t *testing.T) {
	var gotBinary, gotModel, gotText string
	fakeWAV := []byte("RIFF....WAVEfmt ")
	s := newTestServer(func(binary, model, text string) ([]byte, error) {
		gotBinary, gotModel, gotText = binary, model, text
		return fakeWAV, nil
	}, nil)

	body, _ := json.Marshal(speakRequest{Text: "hello world"})
	req := httptest.NewRequest(http.MethodPost, "/speak", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "audio/wav" {
		t.Errorf("expected Content-Type audio/wav, got %q", ct)
	}
	if !bytes.Equal(w.Body.Bytes(), fakeWAV) {
		t.Errorf("expected the fake WAV bytes echoed back, got %v", w.Body.Bytes())
	}
	if gotBinary != "piper" || gotModel != "voice.onnx" || gotText != "hello world" {
		t.Errorf("expected speak called with (piper, voice.onnx, %q), got (%s, %s, %s)", "hello world", gotBinary, gotModel, gotText)
	}
}

func TestSpeakPropagatesPiperFailureAs502(t *testing.T) {
	s := newTestServer(func(binary, model, text string) ([]byte, error) {
		return nil, errors.New("piper crashed")
	}, nil)
	body, _ := json.Marshal(speakRequest{Text: "hello"})
	req := httptest.NewRequest(http.MethodPost, "/speak", bytes.NewReader(body))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when piper fails, got %d", w.Code)
	}
}

func TestTranscribeWithNoBinaryConfiguredReports503(t *testing.T) {
	s := NewServer(Config{}) // no WhisperBinary
	body, ct := multipartAudioBody(t, "fake wav bytes")
	req := httptest.NewRequest(http.MethodPost, "/transcribe", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with no whisper binary configured, got %d", w.Code)
	}
}

func TestTranscribeReturnsTextFromWhisper(t *testing.T) {
	var gotBinary, gotModel string
	var gotWAV []byte
	s := newTestServer(nil, func(binary, model string, wav []byte) (string, error) {
		gotBinary, gotModel, gotWAV = binary, model, wav
		return "hello world", nil
	})

	body, ct := multipartAudioBody(t, "fake wav bytes")
	req := httptest.NewRequest(http.MethodPost, "/transcribe", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if resp["text"] != "hello world" {
		t.Errorf(`expected {"text":"hello world"}, got %v`, resp)
	}
	if gotBinary != "whisper-cli" || gotModel != "ggml.bin" || string(gotWAV) != "fake wav bytes" {
		t.Errorf("expected transcribe called with (whisper-cli, ggml.bin, %q), got (%s, %s, %s)", "fake wav bytes", gotBinary, gotModel, gotWAV)
	}
}

func TestTranscribeRejectsMissingAudioField(t *testing.T) {
	s := newTestServer(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/transcribe", strings.NewReader("not multipart"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a malformed/missing audio field, got %d", w.Code)
	}
}

func TestTranscribePropagatesWhisperFailureAs502(t *testing.T) {
	s := newTestServer(nil, func(binary, model string, wav []byte) (string, error) {
		return "", errors.New("whisper crashed")
	})
	body, ct := multipartAudioBody(t, "fake wav bytes")
	req := httptest.NewRequest(http.MethodPost, "/transcribe", body)
	req.Header.Set("Content-Type", ct)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502 when whisper fails, got %d", w.Code)
	}
}

func TestCORSHeadersPresentOnEveryResponse(t *testing.T) {
	s := newTestServer(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Errorf("expected permissive CORS header, got %q", w.Header().Get("Access-Control-Allow-Origin"))
	}
}

func multipartAudioBody(t *testing.T, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("audio", "clip.wav")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf, w.FormDataContentType()
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("GOJIBERRY_VOICESERVER_ADDR", "")
	c := ConfigFromEnv()
	if c.Addr != ":8084" || c.PiperBinary != "" || c.WhisperBinary != "" {
		t.Fatalf("defaults: %+v", c)
	}
	t.Setenv("GOJIBERRY_VOICESERVER_ADDR", "127.0.0.1:9")
	t.Setenv("GOJIBERRY_VOICESERVER_PIPER_BINARY", "piper")
	t.Setenv("GOJIBERRY_VOICESERVER_PIPER_MODEL", "v.onnx")
	t.Setenv("GOJIBERRY_VOICESERVER_WHISPER_BINARY", "whisper-cli")
	t.Setenv("GOJIBERRY_VOICESERVER_WHISPER_MODEL", "m.bin")
	c = ConfigFromEnv()
	if c.Addr != "127.0.0.1:9" || c.PiperBinary != "piper" || c.PiperModel != "v.onnx" || c.WhisperBinary != "whisper-cli" || c.WhisperModel != "m.bin" || c.Guard == nil {
		t.Fatalf("from env: %+v", c)
	}
}

func TestAcquireAnswers429WhenFull(t *testing.T) {
	if !acquire(httptest.NewRecorder(), nil) {
		t.Fatal("no limit: always allowed")
	}
	sem := make(chan struct{}, 1)
	if !acquire(httptest.NewRecorder(), sem) {
		t.Fatal("first slot refused")
	}
	rec := httptest.NewRecorder()
	if acquire(rec, sem) || rec.Code != http.StatusTooManyRequests {
		t.Fatalf("full: %d", rec.Code)
	}
	release(sem)
	if !acquire(httptest.NewRecorder(), sem) {
		t.Fatal("a released slot is free again")
	}
	release(nil) // no limit: nothing to release
}

func TestSpeechIsReported(t *testing.T) {
	b := observe.NewBuiltin()
	old := observe.Default()
	observe.SetDefault(observe.New(observe.Options{Operator: b}))
	defer observe.SetDefault(old)
	s := newTestServer(func(string, string, string) ([]byte, error) { return []byte("RIFF"), nil }, nil)
	body, _ := json.Marshal(speakRequest{Text: "hello"})
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/speak", bytes.NewReader(body)))
	if got := b.Count(observe.WorkRuns, "work", "speak", "result", "ok"); got != 1 {
		t.Errorf("speak ok = %d, want 1", got)
	}
}

func TestVoicesListsWhatTheServerOffersWithoutItsModelPaths(t *testing.T) {
	s := NewServer(Config{PiperBinary: "piper", Voices: []Voice{{ID: "a", Name: "Ada", Lang: "en-GB", Licence: "https://example.org/terms", Model: "/secret/a.onnx"}}})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/voices", nil))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), `"lang":"en-GB"`) {
		t.Errorf("voices: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	NewServer(Config{}).Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/voices", nil))
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("no voices: %q", w.Body.String())
	}
}

func TestSpeakPicksTheVoiceAndRate(t *testing.T) {
	var gotModel string
	var gotRate float64
	s := NewServer(Config{PiperBinary: "piper", PiperModel: "default.onnx", Voices: []Voice{{ID: "a", Model: "a.onnx"}}})
	s.speak = func(_, model, _ string) ([]byte, error) { gotModel, gotRate = model, 1; return []byte("w"), nil }
	s.speakAt = func(_, model, _ string, rate float64) ([]byte, error) {
		gotModel, gotRate = model, rate
		return []byte("w"), nil
	}
	post := func(r speakRequest) int {
		b, _ := json.Marshal(r)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/speak", bytes.NewReader(b)))
		return w.Code
	}
	if c := post(speakRequest{Text: "x", Voice: "a", Rate: 1.5}); c != 200 || gotModel != "a.onnx" || gotRate != 1.5 {
		t.Errorf("voice a: %d %q %v", c, gotModel, gotRate)
	}
	if c := post(speakRequest{Text: "x"}); c != 200 || gotModel != "default.onnx" || gotRate != 1 {
		t.Errorf("default: %d %q %v", c, gotModel, gotRate)
	}
	if c := post(speakRequest{Text: "x", Voice: "../../etc/passwd"}); c != 400 {
		t.Errorf("an unknown voice must be refused, got %d", c)
	}
}
