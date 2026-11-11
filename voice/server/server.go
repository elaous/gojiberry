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

type Config struct {
	// nil means open: any origin, no check
	Guard guard.Guard
	Addr  string // listen address, e.g. ":8084"

	PiperBinary string // e.g. "piper", or a full path
	PiperModel  string

	// WhisperBinary/WhisperModel gate /transcribe the same way.
	WhisperBinary string // e.g. "whisper-cli", or a full path
	WhisperModel  string // path to a whisper.cpp .bin/.gguf model

	Voices []Voice
}

// Voice is one voice the server offers.
type Voice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Lang is the BCP 47 tag ("en-GB").
	Lang    string `json:"lang"`
	Licence string `json:"licence,omitempty"`
	Model   string `json:"-"`
}

func loadVoices(path string) ([]Voice, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Voice
		Model string `json:"model"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var out []Voice
	for _, r := range raw {
		v := r.Voice
		v.Model = r.Model
		if v.ID == "" || v.Model == "" {
			continue
		}
		if v.Name == "" {
			v.Name = v.ID
		}
		out = append(out, v)
	}
	return out, nil
}

func ConfigFromEnv() Config {
	voices, err := loadVoices(os.Getenv("GOJIBERRY_VOICESERVER_VOICES"))
	if err != nil {
		log.Printf("voiceserver: the voices file could not be read: %v", err)
	}
	return Config{
		Voices:        voices,
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

// never needs or exposes any API key
type Server struct {
	cfg Config

	speak          func(binary, model, text string) ([]byte, error)
	speakAt        func(binary, model, text string, rate float64) ([]byte, error)
	transcribe     func(binary, model string, wav []byte) (string, error)
	transcribeBias func(binary, model string, wav []byte, o listen.Options) (string, error)

	speakSlots, transcribeSlots chan struct{}
}

// NewServer creates a voice server for the given config.
func NewServer(cfg Config) *Server {
	return &Server{cfg: cfg, speak: speak.Piper, speakAt: speak.PiperAt, transcribe: listen.Whisper, transcribeBias: listen.WhisperWith, speakSlots: make(chan struct{}, 2), transcribeSlots: make(chan struct{}, 2)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/voices", s.handleVoices)
	mux.HandleFunc("/speak", s.handleSpeak)
	mux.HandleFunc("/transcribe", s.handleTranscribe)
	return guard.Or(s.cfg.Guard).Wrap(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type speakRequest struct {
	Text  string `json:"text"`
	Voice string `json:"voice,omitempty"`
	// Rate is the speed (1 normal), 0 for normal.
	Rate float64 `json:"rate,omitempty"`
}

func (s *Server) handleVoices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out := s.cfg.Voices
	if out == nil {
		out = []Voice{}
	}
	writeJSON(w, http.StatusOK, out)
}

const maxSpeakBody = 200 * 1024
const maxTranscribeBody = 25 * 1024 * 1024

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
	model := s.cfg.PiperModel
	if req.Voice != "" {
		model = ""
		for _, v := range s.cfg.Voices {
			if v.ID == req.Voice {
				model = v.Model
			}
		}
		if model == "" {
			done("error")
			http.Error(w, "no such voice", http.StatusBadRequest)
			return
		}
	}
	var audio []byte
	var err error
	if req.Rate > 0 && req.Rate != 1 {
		audio, err = s.speakAt(s.cfg.PiperBinary, model, req.Text, req.Rate)
	} else {
		audio, err = s.speak(s.cfg.PiperBinary, model, req.Text)
	}
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

// synthesis and transcription have separate slots
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
