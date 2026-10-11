package voiceserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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

// fakeTool writes a shell script that stands in for piper or whisper-cli.
func fakeTool(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	p := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExecPiperRunsTheBinary(t *testing.T) {
	// piper --output_file OUT [--model M]: text on stdin, a WAV in OUT
	bin := fakeTool(t, `out=$2; model=""; [ "$3" = "--model" ] && model=$4
printf 'RIFF[%s][%s]' "$(cat)" "$model" > "$out"`)
	got, err := execPiper(bin, "voice.onnx", "hello")
	if err != nil || string(got) != "RIFF[hello][voice.onnx]" {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := execPiper(bin, "", "hi"); err != nil || string(got) != "RIFF[hi][]" {
		t.Fatalf("no model: %q, %v", got, err)
	}
}

func TestExecPiperReportsFailures(t *testing.T) {
	bin := fakeTool(t, `echo "no such voice" >&2; exit 3`)
	if _, err := execPiper(bin, "x", "hi"); err == nil || !strings.Contains(err.Error(), "no such voice") {
		t.Fatalf("a failing piper: %v", err)
	}
	if _, err := execPiper(filepath.Join(t.TempDir(), "absent"), "x", "hi"); err == nil {
		t.Fatal("a missing binary is an error")
	}
}

func TestExecWhisperRunsTheBinary(t *testing.T) {
	// whisper-cli -f IN -otxt -of BASE -nt [-m M]: BASE.txt holds the text
	bin := fakeTool(t, `in=$2; base=$5; model=""; [ "$7" = "-m" ] && model=$8
printf '  heard %s with %s \n' "$(wc -c < "$in" | tr -d ' ')" "$model" > "$base.txt"`)
	got, err := execWhisper(bin, "m.bin", []byte("12345"))
	if err != nil || got != "heard 5 with m.bin" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestExecWhisperReportsFailures(t *testing.T) {
	if _, err := execWhisper(fakeTool(t, `echo "bad model" >&2; exit 2`), "m", []byte("x")); err == nil || !strings.Contains(err.Error(), "bad model") {
		t.Fatalf("a failing whisper: %v", err)
	}
	if _, err := execWhisper(fakeTool(t, `exit 0`), "m", []byte("x")); err == nil {
		t.Fatal("no text file written is an error")
	}
	if _, err := execWhisper(filepath.Join(t.TempDir(), "absent"), "m", []byte("x")); err == nil {
		t.Fatal("a missing binary is an error")
	}
}
