// Package server is a small standalone speech proxy: text-to-
// speech via a local Piper (github.com/OHF-Voice/piper1-gpl, formerly
// rhasspy/piper) install, and speech-to-text via a local whisper.cpp-
// compatible install (github.com/mutablelogic/go-whisper embeds
// whisper.cpp itself, so its own CLI or plain whisper.cpp's own `main`/
// `whisper-cli` both work here) -- both genuinely local, offline tools
// with no API key or third-party network call involved, so (unlike
// translate/server's DeepL proxy) there's no secret to hide server-
// side. This package exists purely because neither tool speaks HTTP on
// its own the way Ollama does (task #160's own scripts/start-ollama.sh
// needed no server code at all, just a startup script, since Ollama
// already exposes a local HTTP API): it execs each one as a subprocess
// per request and adapts its stdin/output-file CLI plumbing into one
// small HTTP contract OnDesign's own browser-side editor/voice package
// talks to instead. Same environment-configured, CORS-open, single-
// binary shape as gojiberry/syncserver, gojiberry/fontserver, and
// gojiberry/translate/server -- see cmd/voiceserver for the entry
// point.
package server

import (
	"encoding/json"
	"github.com/elaous/gojiberry/observe"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/elaous/gojiberry/guard"
	"github.com/elaous/gojiberry/voice/listen"
	"github.com/elaous/gojiberry/voice/speak"
)

// Config is loaded entirely from the environment, the same posture
// syncserver.Config/fontserver.Config/server.Config take.
type Config struct {
	// Guard decides who may call the server: guard.Origins(...) for pages from
	// listed origins, guard.Token(...) for callers holding a token, both with
	// guard.Chain, or your own. Nil means guard.Open() (any origin, no check).
	// ConfigFromEnv reads GOJIBERRY_VOICESERVER_ALLOW_ORIGINS and GOJIBERRY_VOICESERVER_TOKEN.
	Guard guard.Guard
	Addr  string // listen address, e.g. ":8084"

	// PiperBinary/PiperModel gate /speak the same way
	// server.Config.DeepLAPIKey gates /translate: with
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
		Guard:         guard.FromEnv("GOJIBERRY_VOICESERVER", "/health"),
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
	// fetchGoogleFonts field and translate/server's own translator
	// interface document.
	speak      func(binary, model, text string) ([]byte, error)
	transcribe func(binary, model string, wav []byte) (string, error)
	// transcribeBias is used when the request carries a prompt or a language (the listener's own
	// words, to bias the recogniser; nothing is trained).
	transcribeBias func(binary, model string, wav []byte, o listen.Options) (string, error)

	// speakSlots and transcribeSlots bound the processes of each kind.
	speakSlots, transcribeSlots chan struct{}
}

// NewServer creates a voice server for the given config. With
// PiperBinary/WhisperBinary empty, the corresponding route stays wired
// but always reports 503 -- the same "still runs, just reports the
// feature is off" posture server.NewServer/fontserver's own
// handleGoogleFonts take with no key configured, rather than refusing
// to start at all.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg, speak: speak.Piper, transcribe: listen.Whisper, transcribeBias: listen.WhisperWith, speakSlots: make(chan struct{}, 2), transcribeSlots: make(chan struct{}, 2)}
}

// Handler returns the HTTP routes:
//
//	GET  /health       -- confirms the server is up
//	POST /speak        -- {"text"} -> audio/wav bytes
//	POST /transcribe   -- multipart form, field "audio" holding a WAV
//	                       file -> {"text"}; the optional fields "prompt" (words the
//	                       speaker is likely to say, up to listen.MaxPrompt bytes) and
//	                       "language" (a code such as "de") bias the recogniser
//
// Every response carries permissive CORS headers, the same reasoning
// as syncserver/fontserver/translate/server's own Handler: this
// server's whole point is being reachable from a page served from a
// different origin.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/speak", s.handleSpeak)
	mux.HandleFunc("/transcribe", s.handleTranscribe)
	return guard.Or(s.cfg.Guard).Wrap(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type speakRequest struct {
	Text string `json:"text"`
}

// maxSpeakBody/maxTranscribeBody cap request bodies at a generous but
// finite size, the same defensive posture translate/server's own
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
	done := observe.Default().Work("speak")
	audio, err := s.speak(s.cfg.PiperBinary, s.cfg.PiperModel, req.Text)
	if err != nil {
		done("error")
		log.Printf("voiceserver: speak: %v", err)
		http.Error(w, "speech synthesis failed", http.StatusBadGateway)
		return
	}
	done("ok")
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
	done := observe.Default().Work("transcribe")
	var text string
	if prompt, lang := listen.CleanPrompt(r.FormValue("prompt")), listen.CleanLanguage(r.FormValue("language")); prompt != "" || lang != "" {
		text, err = s.transcribeBias(s.cfg.WhisperBinary, s.cfg.WhisperModel, wav, listen.Options{Prompt: prompt, Language: lang})
	} else {
		text, err = s.transcribe(s.cfg.WhisperBinary, s.cfg.WhisperModel, wav)
	}
	if err != nil {
		done("error")
		log.Printf("voiceserver: transcribe: %v", err)
		http.Error(w, "transcription failed", http.StatusBadGateway)
		return
	}
	done("ok")
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
