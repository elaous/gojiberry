// Package translateserver is a small standalone translation proxy: it
// holds a DeepL API key server-side (a client-side WASM binary can't
// keep a secret safe -- anyone could extract it from the downloaded
// binary) and exposes one HTTP endpoint a browser can call with plain
// text and a target language, returning the translated result. Same
// environment-configured, CORS-open, single-binary shape as
// gojiberry/syncserver and gojiberry/fontserver -- see
// cmd/translateserver for the entry point.
//
// DeepL only for now (a small client of its HTTP API in deepl.go), matching
// OnDesign's own roadmap recommendation for task #106; a second
// provider (Google Translate, say) would slot in behind the same
// translator interface this package already isolates the DeepL client
// behind, the same way fontserver's own fetchGoogleFonts field keeps
// its network call swappable for tests without needing a second
// provider yet either.
package translateserver

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/elaous/gojiberry/guard"
)

// Config is loaded entirely from the environment, the same posture
// syncserver.Config/fontserver.Config take: turning real translation
// on is always an environment variable, never a code change.
type Config struct {
	// Guard decides who may call the server: guard.Origins(...) for pages from
	// listed origins, guard.Token(...) for callers holding a token, both with
	// guard.Chain, or your own. Nil means guard.Open() (any origin, no check).
	// ConfigFromEnv reads GOJIBERRY_TRANSLATESERVER_ALLOW_ORIGINS and GOJIBERRY_TRANSLATESERVER_TOKEN.
	Guard       guard.Guard
	Addr        string // listen address, e.g. ":8083"
	DeepLAPIKey string // DeepL API authentication key; empty disables translation
}

// ConfigFromEnv reads GOJIBERRY_TRANSLATESERVER_ADDR (default ":8083")
// and GOJIBERRY_TRANSLATESERVER_DEEPL_KEY (default "", meaning
// /translate always reports 503 -- see handleTranslate).
func ConfigFromEnv() Config {
	return Config{
		Guard:       guard.FromEnv("GOJIBERRY_TRANSLATESERVER", "/health"),
		Addr:        envOr("GOJIBERRY_TRANSLATESERVER_ADDR", ":8083"),
		DeepLAPIKey: os.Getenv("GOJIBERRY_TRANSLATESERVER_DEEPL_KEY"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// translator is the one call this package makes to DeepL, so tests can
// inject a fake instead of hitting the real API (the same "swappable
// network call" reasoning fontserver's own fetchGoogleFonts field
// documents). sourceLang may be "".
type translator interface {
	Translate(ctx context.Context, text []string, targetLang, sourceLang string) ([]Translation, error)
}

// Server proxies translation requests to DeepL, never exposing the
// API key to the browser.
type Server struct {
	cfg        Config
	translator translator // nil if cfg.DeepLAPIKey is empty
}

// NewServer creates a translate server for the given config. With no
// API key configured, translator stays nil and every /translate
// request reports 503 -- the same "still runs, just reports the
// feature is off" posture fontserver's own handleGoogleFonts takes
// with no Google API key, rather than refusing to start at all.
func NewServer(cfg Config) (*Server, error) {
	s := &Server{cfg: cfg}
	if cfg.DeepLAPIKey != "" {
		t, err := newDeeplClient(cfg.DeepLAPIKey)
		if err != nil {
			return nil, err
		}
		s.translator = t
	}
	return s, nil
}

// Handler returns the HTTP routes:
//
//	GET  /health     -- confirms the server is up
//	POST /translate   -- {"text","targetLang","sourceLang"} -> {"translatedText","detectedSourceLanguage"}
//
// Every response carries permissive CORS headers, the same reasoning
// as syncserver.Handler/fontserver.Handler: this server's whole point
// is being reachable from a page served from a different origin.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/translate", s.handleTranslate)
	return guard.Or(s.cfg.Guard).Wrap(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type translateRequest struct {
	Text       string `json:"text"`
	TargetLang string `json:"targetLang"`
	SourceLang string `json:"sourceLang"` // optional; DeepL auto-detects when empty
}

type translateResponse struct {
	TranslatedText         string `json:"translatedText"`
	DetectedSourceLanguage string `json:"detectedSourceLanguage"`
}

// maxTranslateBody caps the request body DeepL itself would reject
// past 128KiB (DeepL's documented limit) -- read that much
// plus a little slack rather than trusting a client-supplied
// Content-Length.
const maxTranslateBody = 200 * 1024

func (s *Server) handleTranslate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.translator == nil {
		http.Error(w, "translation is not configured on this server (no DeepL API key)", http.StatusServiceUnavailable)
		return
	}

	var req translateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxTranslateBody)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Text == "" || req.TargetLang == "" {
		http.Error(w, "text and targetLang are required", http.StatusBadRequest)
		return
	}

	results, err := s.translator.Translate(r.Context(), []string{req.Text}, req.TargetLang, req.SourceLang)
	if err != nil {
		log.Printf("translateserver: %v", err)
		http.Error(w, "the translation service failed", http.StatusBadGateway)
		return
	}
	if len(results) == 0 {
		http.Error(w, "translation returned no results", http.StatusBadGateway)
		return
	}

	writeJSON(w, http.StatusOK, translateResponse{
		TranslatedText:         results[0].Text,
		DetectedSourceLanguage: results[0].DetectedSourceLanguage,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
