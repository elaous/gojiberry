// Package voiceserver is a small standalone speech proxy: text-to-
// speech via a local Piper (github.com/OHF-Voice/piper1-gpl, formerly
// rhasspy/piper) install, and speech-to-text via a local whisper.cpp-
// compatible install (github.com/mutablelogic/go-whisper embeds
// whisper.cpp itself, so its own CLI or plain whisper.cpp's own `main`/
// `whisper-cli` both work here) -- both genuinely local, offline tools
// with no API key or third-party network call involved, so (unlike
// translateserver's DeepL proxy) there's no secret to hide server-
// side. This package exists purely because neither tool speaks HTTP on
// its own the way Ollama does (task #160's own scripts/start-ollama.sh
// needed no server code at all, just a startup script, since Ollama
// already exposes a local HTTP API): it execs each one as a subprocess
// per request and adapts its stdin/output-file CLI plumbing into one
// small HTTP contract OnDesign's own browser-side editor/voice package
// talks to instead. Same environment-configured, CORS-open, single-
// binary shape as gojiberry/syncserver, gojiberry/fontserver, and
// gojiberry/translateserver -- see cmd/voiceserver for the entry
// point.
package voiceserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Config is loaded entirely from the environment, the same posture
// syncserver.Config/fontserver.Config/translateserver.Config take.
type Config struct {
	Addr string // listen address, e.g. ":8084"

	// PiperBinary/PiperModel gate /speak the same way
	// translateserver.Config.DeepLAPIKey gates /translate: with
	// PiperBinary empty, /speak always reports 503.
	PiperBinary string // e.g. "piper", or a full path
	PiperModel  string // path to a Piper .onnx voice model (its .onnx.json sits alongside it)

	// WhisperBinary/WhisperModel gate /transcribe the same way.
	WhisperBinary string // e.g. "whisper-cli", or a full path
	WhisperModel  string // path to a whisper.cpp .bin/.gguf model
}

// ConfigFromEnv reads GOJIBERRY_VOICESERVER_ADDR (default ":8084"),
// GOJIBERRY_VOICESERVER_PIPER_BINARY/_PIPER_MODEL, and
// GOJIBERRY_VOICESERVER_WHISPER_BINARY/_WHISPER_MODEL (all empty by
// default -- see NewServer's own doc comment for what an unset pair
// means for each route).
func ConfigFromEnv() Config {
	return Config{
		Addr:          envOr("GOJIBERRY_VOICESERVER_ADDR", ":8084"),
		PiperBinary:   os.Getenv("GOJIBERRY_VOICESERVER_PIPER_BINARY"),
		PiperModel:    os.Getenv("GOJIBERRY_VOICESERVER_PIPER_MODEL"),
		WhisperBinary: os.Getenv("GOJIBERRY_VOICESERVER_WHISPER_BINARY"),
		WhisperModel:  os.Getenv("GOJIBERRY_VOICESERVER_WHISPER_MODEL"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// Server proxies TTS/STT requests to local Piper/whisper installs,
// never needing (or exposing) any API key.
type Server struct {
	cfg Config

	// speak/transcribe are swappable so tests can inject a fake
	// instead of shelling out to a real piper/whisper-cli binary --
	// the same "swappable network call" reasoning fontserver's own
	// fetchGoogleFonts field and translateserver's own translator
	// interface document.
	speak      func(binary, model, text string) ([]byte, error)
	transcribe func(binary, model string, wav []byte) (string, error)

	// speakSlots and transcribeSlots bound the processes of each kind.
	speakSlots, transcribeSlots chan struct{}
}

// NewServer creates a voice server for the given config. With
// PiperBinary/WhisperBinary empty, the corresponding route stays wired
// but always reports 503 -- the same "still runs, just reports the
// feature is off" posture translateserver.NewServer/fontserver's own
// handleGoogleFonts take with no key configured, rather than refusing
// to start at all.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg, speak: execPiper, transcribe: execWhisper, speakSlots: make(chan struct{}, 2), transcribeSlots: make(chan struct{}, 2)}
}

// Handler returns the HTTP routes:
//
//	GET  /health       -- confirms the server is up
//	POST /speak        -- {"text"} -> audio/wav bytes
//	POST /transcribe   -- multipart form, field "audio" holding a WAV
//	                       file -> {"text"}
//
// Every response carries permissive CORS headers, the same reasoning
// as syncserver/fontserver/translateserver's own Handler: this
// server's whole point is being reachable from a page served from a
// different origin.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/speak", s.handleSpeak)
	mux.HandleFunc("/transcribe", s.handleTranscribe)
	return corsMiddleware(mux)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type speakRequest struct {
	Text string `json:"text"`
}

// maxSpeakBody/maxTranscribeBody cap request bodies at a generous but
// finite size, the same defensive posture translateserver's own
// maxTranslateBody takes against a client-supplied Content-Length that
// can't be trusted.
const maxSpeakBody = 200 * 1024            // ~200KB of text, plenty for a document read aloud in one request
const maxTranscribeBody = 25 * 1024 * 1024 // ~25MB of 16kHz mono WAV is a long dictation clip already

func (s *Server) handleSpeak(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.PiperBinary == "" {
		http.Error(w, "text-to-speech is not configured on this server (no Piper binary)", http.StatusServiceUnavailable)
		return
	}

	var req speakRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxSpeakBody)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}

	if !acquire(w, s.speakSlots) {
		return
	}
	defer release(s.speakSlots)
	audio, err := s.speak(s.cfg.PiperBinary, s.cfg.PiperModel, req.Text)
	if err != nil {
		log.Printf("voiceserver: speak: %v", err)
		http.Error(w, "speech synthesis failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.cfg.WhisperBinary == "" {
		http.Error(w, "speech-to-text is not configured on this server (no whisper binary)", http.StatusServiceUnavailable)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxTranscribeBody)
	file, _, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "expected a multipart \"audio\" file field", http.StatusBadRequest)
		return
	}
	defer file.Close()
	wav, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "failed to read audio field", http.StatusBadRequest)
		return
	}

	if !acquire(w, s.transcribeSlots) {
		return
	}
	defer release(s.transcribeSlots)
	text, err := s.transcribe(s.cfg.WhisperBinary, s.cfg.WhisperModel, wav)
	if err != nil {
		log.Printf("voiceserver: transcribe: %v", err)
		http.Error(w, "transcription failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// execPiper is speak's real implementation: pipes text into piper's
// own stdin and reads the WAV it writes to a temp --output_file, the
// well-established Piper CLI invocation shape (unchanged across the
// rhasspy/piper -> OHF-Voice/piper1-gpl rename) -- `echo "text" | piper
// --model voice.onnx --output_file out.wav`.
const (
	speakTimeout      = time.Minute
	transcribeTimeout = 5 * time.Minute
)

// acquire takes a slot from sem (nil: no limit) or answers 429. Synthesis and
// transcription have their own, so a long transcription does not turn every
// /speak request away.
func acquire(w http.ResponseWriter, sem chan struct{}) bool {
	if sem == nil {
		return true
	}
	select {
	case sem <- struct{}{}:
		return true
	default:
		http.Error(w, "too many requests in progress", http.StatusTooManyRequests)
		return false
	}
}

func release(sem chan struct{}) {
	if sem != nil {
		<-sem
	}
}

func execPiper(binary, model, text string) ([]byte, error) {
	out, err := os.CreateTemp("", "voiceserver-speak-*.wav")
	if err != nil {
		return nil, err
	}
	outPath := out.Name()
	_ = out.Close()
	defer func() { _ = os.Remove(outPath) }()

	args := []string{"--output_file", outPath}
	if model != "" {
		args = append(args, "--model", model)
	}
	ctx, cancel := context.WithTimeout(context.Background(), speakTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(text)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("piper: %w: %s", err, combined)
	}

	return os.ReadFile(outPath)
}

// execWhisper is transcribe's real implementation: writes the given
// WAV to a temp file and runs it through whisper.cpp's own well-
// established CLI shape (`whisper-cli -m model.bin -f in.wav -otxt -of
// outbase -nt` -- plain text output, no timestamps, predates and
// underlies go-whisper's own CLI too), then reads back the .txt file
// it writes.
func execWhisper(binary, model string, wav []byte) (string, error) {
	in, err := os.CreateTemp("", "voiceserver-transcribe-*.wav")
	if err != nil {
		return "", err
	}
	inPath := in.Name()
	defer func() { _ = os.Remove(inPath) }()
	if _, err := in.Write(wav); err != nil {
		_ = in.Close()
		return "", err
	}
	_ = in.Close()

	outBase := inPath[:len(inPath)-len(filepath.Ext(inPath))]
	outPath := outBase + ".txt"
	defer func() { _ = os.Remove(outPath) }()

	args := []string{"-f", inPath, "-otxt", "-of", outBase, "-nt"}
	if model != "" {
		args = append(args, "-m", model)
	}
	ctx, cancel := context.WithTimeout(context.Background(), transcribeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("whisper: %w: %s", err, combined)
	}

	text, err := os.ReadFile(outPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(text)), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
